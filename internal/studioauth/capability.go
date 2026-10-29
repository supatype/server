package studioauth

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/supatype/server/internal/utilities"
)

// Studio roles and what each may do.
//
// One role set serves both hosts: owner, admin, developer, editor, billing and
// viewer, with what each may do written once in role_matrix.json. The control
// plane (supatype-cloud) and the CLI carry the same document and pin the same
// hash, so the same membership row means the same thing whether the project runs
// self-hosted or managed. Self-host grants admin, developer, editor and viewer;
// owner and billing are Cloud org roles and grant nothing here.
//
// Studio roles decide what a Studio user may do in Studio; the app's row rules
// do not limit Studio editing, its field rules (masking) still apply (owner
// decision 2026-10-21, acting_mode.go).
//
//	Capability                                   admin  developer  editor  viewer
//	Read content rows and files                   yes      yes       yes     yes
//	Edit content rows and files                   yes      yes       yes     no
//	Schema and migration history view             yes      yes       no      yes
//	Storage policies and RLS status, read-only    yes      yes       yes     yes
//	SQL editor, elevated SQL                      yes      yes       no      no
//	Storage bucket management                     yes      yes       no      no
//	Auth users administration                     yes      yes       no      no
//	Project keys                                  yes      yes       no      no
//	Studio members (granting Studio roles)        yes      no        no      no
//	Realtime Inspector (channels, stream, test)   yes      yes       no      no
//	Administration (admin API, control plane)     yes      yes       no      no
//
// Unknown roles get nothing. A typo in a `studio_members` row must fail closed
// rather than inherit whatever the nearest known role allows.

// StudioPermissions is the capability set Studio renders from. Its JSON names
// are the shared document's permission names, one field each.
type StudioPermissions struct {
	// Read and Write cover content rows and files, which is to say the data plane.
	Read  bool `json:"read"`
	Write bool `json:"write"`
	// SchemaView covers the schema and migration history views.
	SchemaView bool `json:"schemaView"`
	// PushSchema and Deploy cover schema pushes and functions, hooks and secrets.
	PushSchema bool `json:"pushSchema"`
	Deploy     bool `json:"deploy"`
	// SQLEditor opens the SQL runner, which connects as the project's owning
	// role: row level security does not apply, so its holder can read and change
	// every row. The owner granted it to developers knowingly (RX1).
	SQLEditor bool `json:"sqlEditor"`
	// ElevatedSQL is acting as the service role for data requests: the elevated
	// mode, where no rule applies, field masking included.
	ElevatedSQL bool `json:"elevatedSql"`
	// RealtimeInspect covers the Realtime Inspector: channels, connections,
	// the live message stream and test broadcasts. Not realtime settings.
	RealtimeInspect bool `json:"realtimeInspect"`
	ViewLogs        bool `json:"viewLogs"`
	ManageKeys      bool `json:"manageKeys"`
	ManageDomains   bool `json:"manageDomains"`
	// ManageUsers covers the project's own users, through its auth admin API.
	ManageUsers bool `json:"manageUsers"`
	// ManageBuckets covers creating, changing and deleting storage buckets.
	// Listing them is Read, since browsing files starts there.
	ManageBuckets bool `json:"manageBuckets"`
	// ManageMembers covers Studio membership: granting and revoking Studio roles.
	ManageMembers bool `json:"manageMembers"`
	// Administer covers service-role administration: the admin API, the
	// self-host control plane, function administration, and any route no other
	// permission names, so a route added later starts with the admins and
	// developers who administer the project.
	Administer bool `json:"administer"`
	// The rest are Cloud organisation powers. Self-host has no organisation, so
	// nothing here acts on them; they are carried so the document maps whole.
	Billing       bool `json:"billing"`
	OwnOrg        bool `json:"ownOrg"`
	OrgSettings   bool `json:"orgSettings"`
	CreateProject bool `json:"createProject"`
	DeleteProject bool `json:"deleteProject"`
	ViewUsage     bool `json:"viewUsage"`
}

// RoleAdmin is the only role that can grant or revoke Studio access.
const RoleAdmin = "admin"

// PermissionsForRole returns the capability set for a Studio role, and whether
// the role is one this deployment understands.
func PermissionsForRole(role string) (StudioPermissions, bool) {
	perms, ok := studioRolePermissions[role]
	return perms, ok
}

// KnownStudioRoles lists the roles a membership row may hold, most privileged
// first, so the API response and CLI help are stably ordered.
func KnownStudioRoles() []string {
	return append([]string(nil), selfHostRoles...)
}

// dataPlanePrefixes are the routes that carry content: rows, files, realtime and
// function invocation. Every role works with content, so these are the only
// routes that `Read` and `Write` open. Function administration lives under
// `/admin/v1/functions` (and, for a transition, its old `/functions/v1/admin`)
// and bucket management under `/storage/v1/bucket`, and neither is content.
var dataPlanePrefixes = []string{"/rest/v1", "/graphql/v1", "/storage/v1", "/realtime/v1", "/functions/v1"}

// AllowsRequest reports whether a capability set covers one proxied request.
//
// `path` must be the path the gateway's router will route on (see
// routedPath), since a rule that judges one path while the router serves
// another decides nothing.
//
// Content routes are an allowlist, as in the control plane's
// `resolvePermission`, which refuses any path it does not name. Everything else
// behind the gateway (the self-host control plane at `/platform/v1`, the admin
// API at `/admin/v1`, function administration, and any route added later) is
// service-role administration and needs `Administer`, so a new route starts
// with admins and developers rather than inheriting "may write content rows".
func AllowsRequest(perms StudioPermissions, method, path string) bool {
	if allowed, named := allowsNamedRoute(perms, method, path); named {
		return allowed
	}
	switch {
	case isDataPlane(path):
		return allowsContent(perms, method, path)
	// The one named exception to "the control plane is admin only".
	case IsRealtimeInspectorRoute(method, path):
		return perms.RealtimeInspect
	default:
		return perms.Administer
	}
}

// allowsNamedRoute answers for the routes with a rule of their own, and reports
// whether path was one of them.
func allowsNamedRoute(perms StudioPermissions, method, path string) (allowed, named bool) {
	switch {
	// The SQL runner connects with the project's admin DSN, so every query
	// through it is elevated regardless of who asked (RX1). The allow-listed
	// admin operations that need a superuser connection (SQ2) go with it.
	case under(path, "/sql"), under(path, "/studio-admin"):
		return perms.SQLEditor, true
	// Studio's named, read-only schema queries (RQ3): catalogs and the ledger.
	case under(path, "/metadata"):
		return allowsMetadata(perms, method, path), true
	case under(path, "/admin/studio-members"):
		return perms.ManageMembers || (isReadOnly(method) && perms.SchemaView), true
	// The project's own users are a separate capability from content rows.
	case under(path, "/auth/v1"):
		return perms.ManageUsers, true
	// Listing buckets is where browsing files starts; changing them is not content.
	case under(path, "/storage/v1/bucket"):
		return perms.ManageBuckets || (isReadOnly(method) && perms.Read), true
	}
	return false, false
}

// allowsMetadata judges a request to the read-only catalogue. The gateway
// answers it only on a POST to two exact paths: the introspect query, the
// tables and columns behind Data Explorer and the schema screens, for every
// role that may read rows; the rest of the catalogue for schema view. Any other
// request under /metadata falls through to the application carrying the
// service role's key (forwardIdentity), so it is administration.
func allowsMetadata(perms StudioPermissions, method, path string) bool {
	switch {
	case method != http.MethodPost:
		return perms.Administer
	case path == "/metadata/introspect":
		return perms.Read
	case path == "/metadata":
		return perms.SchemaView
	default:
		return perms.Administer
	}
}

// allowsContent judges one data-plane request: a read needs Read, anything else
// Write. Listing a bucket's objects and signing a download URL are POSTs that
// only read, so a viewer may browse files.
func allowsContent(perms StudioPermissions, method, path string) bool {
	if isReadOnly(method) || isStorageReadPost(method, path) {
		return perms.Read
	}
	return perms.Write
}

// The storage POSTs that only read: whole-path patterns whose segments are
// neither dot segments nor percent-encoded, so nothing a URL parser would later
// resolve differently can pass for one.
var (
	storageListObjects = regexp.MustCompile(`^/storage/v1/object/list/[^/.%][^/%]*$`)
	storageSignObject  = regexp.MustCompile(`^/storage/v1/object/sign/[^/.%][^/%]*(/[^/.%][^/%]*)+$`)
)

func isStorageReadPost(method, path string) bool {
	return method == http.MethodPost &&
		(storageListObjects.MatchString(path) || storageSignObject.MatchString(path))
}

func isReadOnly(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// The Realtime Inspector's control-plane routes. Whole-path patterns, so a
// tail such as `/channels/../../schema/push` is not one of them. The ref is
// one segment that is neither a dot segment nor percent-encoded, so nothing a
// URL parser would later resolve differently can pass for one.
var (
	realtimeInspectorReads     = regexp.MustCompile(`^/platform/v1/projects/[^/.%][^/%]*/realtime/(channels|connections|stream)$`)
	realtimeInspectorBroadcast = regexp.MustCompile(`^/platform/v1/projects/[^/.%][^/%]*/realtime/broadcast$`)
)

// realtimeInspectorProject is where self-host Studio learns its project ref.
const realtimeInspectorProject = "/platform/v1/project"

// IsRealtimeInspectorRoute reports whether one request is a Realtime Inspector
// call: a GET of the project, channels, connections or the stream, or a POST of
// a test broadcast. Realtime settings, when they exist, are not among them.
func IsRealtimeInspectorRoute(method, path string) bool {
	switch method {
	case http.MethodGet:
		return path == realtimeInspectorProject || realtimeInspectorReads.MatchString(path)
	case http.MethodPost:
		return realtimeInspectorBroadcast.MatchString(path)
	default:
		return false
	}
}

func isDataPlane(path string) bool {
	if under(path, "/functions/v1/admin") {
		return false
	}
	for _, prefix := range dataPlanePrefixes {
		if under(path, prefix) {
			return true
		}
	}
	return false
}

// serviceRoleOnlyPrefixes are the upstreams behind the proxy that admit nothing
// but the service role: the admin API, GoTrue's user administration, storage
// bucket management and function administration. A caller's own token is
// refused by each of them, whatever their Studio role.
var serviceRoleOnlyPrefixes = []string{"/admin/v1", "/auth/v1/admin", "/storage/v1/bucket", "/functions/v1/admin"}

// isServiceRoleOnly reports whether a proxied request goes to an upstream that
// admits only the service role. The self-host control plane is one, except for
// the Realtime Inspector's routes, which mediate a caller acting as themselves.
//
// Acting as yourself decides whose policies filter project rows. It was also
// sending the caller's own token to these routes, so an admin who switched had
// every administration screen refused ("User not allowed", "Forbidden:
// service_role required"), administration their role still held.
func isServiceRoleOnly(method, path string) bool {
	for _, prefix := range serviceRoleOnlyPrefixes {
		if under(path, prefix) {
			return true
		}
	}
	return under(path, "/platform/v1") && !IsRealtimeInspectorRoute(method, path)
}

// under reports whether path is prefix or lies beneath it, on a segment
// boundary, the way the router mounts a sub-tree. `/rest/v1x` is not under
// `/rest/v1`. Paths are not cleaned, because the router does not clean them
// either: `/platform/v1/../rest/v1` is routed to the control plane.
func under(path, prefix string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := path[len(prefix):]
	return rest == "" || rest[0] == '/'
}

// routedPath is the path chi routes a request on: the remainder a mount left in
// the route context, else the escaped form when the request has one, else the
// decoded path. Judging the decoded path alone would let `/%72est/v1` read as
// content rows while the router sends it to the catch-all.
func routedPath(req *http.Request) string {
	unrouted := req.URL.Path
	if req.URL.RawPath != "" {
		unrouted = req.URL.RawPath
	}
	return utilities.RoutePath(req, unrouted)
}

// legacyAdminPermissions is what a deployment still on the claim-based path
// reports. Those deployments have no membership rows to carry a finer role, and
// the claim only ever meant "is an admin".
func legacyAdminPermissions() StudioPermissions {
	perms, _ := PermissionsForRole(RoleAdmin)
	return perms
}

// permissionsOf is the capability set an admitted caller acts with: their
// membership role's, or on the legacy claim path an admin's.
func permissionsOf(result Result) StudioPermissions {
	if result.Permissions != nil {
		return *result.Permissions
	}
	return legacyAdminPermissions()
}
