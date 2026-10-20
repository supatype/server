package studioauth

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
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
//	Capability                                   admin  developer  editor  viewer
//	Read content rows                             yes      yes       yes     yes
//	Edit content rows                             yes      yes       yes     no
//	Schema and migration history view             yes      yes       no      yes
//	SQL editor (elevated: RLS does not apply)     yes      yes       no      no
//	Elevated SQL, DDL, destructive                yes      no        no      no
//	Studio members and keys                       yes      no        no      no
//	Realtime Inspector (channels, stream, test)   yes      yes       no      no
//
// Unknown roles get nothing. A typo in a `studio_members` row must fail closed
// rather than inherit whatever the nearest known role allows.

// StudioPermissions is the capability set Studio renders from. Its JSON names
// are the shared document's permission names, one field each.
type StudioPermissions struct {
	// Read and Write cover content rows, which is to say the data plane.
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
	// ElevatedSQL covers DDL and anything that bypasses RLS.
	ElevatedSQL bool `json:"elevatedSql"`
	// RealtimeInspect covers the Realtime Inspector: channels, connections,
	// the live message stream and test broadcasts. Not realtime settings.
	RealtimeInspect bool `json:"realtimeInspect"`
	ViewLogs        bool `json:"viewLogs"`
	ManageKeys      bool `json:"manageKeys"`
	ManageDomains   bool `json:"manageDomains"`
	// ManageMembers covers Studio membership and user administration.
	ManageMembers bool `json:"manageMembers"`
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
// `/functions/v1/admin` and is not content.
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
// service-role administration and needs `ElevatedSQL`, the capability that
// already means "may act as the service role": it is what resolveActingMode
// requires to elevate. A new route therefore starts admin-only rather than
// inheriting "may write content rows".
func AllowsRequest(perms StudioPermissions, method, path string) bool {
	readOnly := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions

	switch {
	// The SQL runner connects with the project's admin DSN, so every query
	// through it is elevated regardless of who asked: row level security does not
	// apply to it. The owner chose to open it to every role holding SQLEditor,
	// developers included (RX1), knowing it lets them read and change every row.
	case under(path, "/sql"):
		return perms.SQLEditor

	// The allow-listed admin operations that need a superuser connection (SQ2):
	// enabling an extension, and RLS or policy changes on the auth and storage
	// tables. The same roles that hold the SQL editor reach them, and each is
	// recorded as elevated.
	case under(path, "/studio-admin"):
		return perms.SQLEditor

	// The catalogue's introspect query alone: the tables and columns behind Data
	// Explorer and the schema screens, for every role that may read rows. Editors
	// use those screens without holding schema view.
	case under(path, "/metadata/introspect"):
		return perms.Read

	// Studio's named, read-only schema queries (RQ3): catalogs and the ledger,
	// never rows, for every role that may see the schema.
	case under(path, "/metadata"):
		return perms.SchemaView

	// Studio membership and project keys.
	case under(path, "/admin/studio-members"):
		if readOnly {
			return perms.ManageMembers || perms.SchemaView
		}
		return perms.ManageMembers

	// User administration is a separate capability from writing content rows.
	case under(path, "/auth/v1"):
		return perms.ManageMembers

	case isDataPlane(path):
		if readOnly {
			return perms.Read
		}
		return perms.Write

	// The one named exception to "the control plane is admin only".
	case IsRealtimeInspectorRoute(method, path):
		return perms.RealtimeInspect

	default:
		return perms.ElevatedSQL
	}
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
	if rctx := chi.RouteContext(req.Context()); rctx != nil && rctx.RoutePath != "" {
		return rctx.RoutePath
	}
	if req.URL.RawPath != "" {
		return req.URL.RawPath
	}
	return req.URL.Path
}

// legacyAdminPermissions is what a deployment still on the claim-based path
// reports. Those deployments have no membership rows to carry a finer role, and
// the claim only ever meant "is an admin".
func legacyAdminPermissions() StudioPermissions {
	perms, _ := PermissionsForRole(RoleAdmin)
	return perms
}
