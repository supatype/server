package studioauth

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Studio roles and what each may do.
//
// This must agree with `STUDIO_ROLE_PERMISSIONS` in the control plane
// (supatype-cloud, src/middleware/studio-capability.ts) and `STUDIO_ROLES` in the
// CLI. The same membership row has to mean the same thing whether the project
// runs self-hosted or managed. Two hosts quietly disagreeing about what
// "developer" allows is how a role becomes meaningless.
//
//	Capability                                   admin  developer  editor
//	Edit content rows                             yes      yes       yes
//	Schema / migrations view                      yes      yes       no
//	SQL editor (as the acting user's role)        yes      yes       no
//	Elevated SQL / DDL / destructive              yes      no        no
//	Studio members + keys                         yes      no        no
//
// Unknown roles get nothing. A typo in a `studio_members` row must fail closed
// rather than inherit whatever the nearest known role allows.

// StudioPermissions is the capability set Studio renders from.
type StudioPermissions struct {
	// Read and Write cover content rows, which is to say the data plane.
	Read  bool `json:"read"`
	Write bool `json:"write"`
	// SchemaView covers the schema and migration history views.
	SchemaView bool `json:"schemaView"`
	// SQLEditor runs queries as the acting user's own role, so RLS applies to
	// them. This is what makes the editor safe to hand to a contractor: they can
	// explore and debug without being able to dump the database.
	SQLEditor bool `json:"sqlEditor"`
	// ElevatedSQL covers DDL and anything that bypasses RLS.
	ElevatedSQL bool `json:"elevatedSql"`
	// ManageMembers covers Studio membership and project API keys.
	ManageMembers bool `json:"manageMembers"`
}

// RoleAdmin is the only role that can grant or revoke Studio access.
const RoleAdmin = "admin"

var studioRolePermissions = map[string]StudioPermissions{
	RoleAdmin: {
		Read: true, Write: true, SchemaView: true,
		SQLEditor: true, ElevatedSQL: true, ManageMembers: true,
	},
	"developer": {
		Read: true, Write: true, SchemaView: true, SQLEditor: true,
	},
	"editor": {
		Read: true, Write: true,
	},
}

// PermissionsForRole returns the capability set for a Studio role, and whether
// the role is one this deployment understands.
func PermissionsForRole(role string) (StudioPermissions, bool) {
	perms, ok := studioRolePermissions[role]
	return perms, ok
}

// KnownStudioRoles lists the roles a membership row may hold, most privileged
// first, so the API response and CLI help are stably ordered.
func KnownStudioRoles() []string {
	return []string{RoleAdmin, "developer", "editor"}
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
	// The SQL runner still connects with the project's admin DSN, so every query
	// through it is elevated regardless of who asked. It moves to SQLEditor once
	// the runner drops to the acting user's role and RLS applies (P1.6/P1.7);
	// until then, granting a developer access here would hand them DDL.
	case under(path, "/sql"):
		return perms.ElevatedSQL

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

	default:
		return perms.ElevatedSQL
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
