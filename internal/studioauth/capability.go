package studioauth

import (
	"net/http"
	"strings"
)

// Studio roles and what each may do.
//
// This must agree with `STUDIO_ROLE_PERMISSIONS` in the control plane
// (supatype-cloud, src/middleware/studio-capability.ts) and `STUDIO_ROLES` in the
// CLI. The same membership row has to mean the same thing whether the project
// runs self-hosted or managed — two hosts quietly disagreeing about what
// "developer" allows is how a role becomes meaningless.
//
//	Capability                                   admin  developer  editor
//	Edit content rows                              ✓        ✓        ✓
//	Schema / migrations view                       ✓        ✓        —
//	SQL editor (as the acting user's role)         ✓        ✓        —
//	Elevated SQL / DDL / destructive                ✓        —        —
//	Studio members + keys                          ✓        —        —
//
// Unknown roles get nothing. A typo in a `studio_members` row must fail closed
// rather than inherit whatever the nearest known role allows.

// StudioPermissions is the capability set Studio renders from.
type StudioPermissions struct {
	// Read and Write cover content rows — the data plane.
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

// AllowsRequest reports whether a capability set covers one proxied request.
//
// Mirrors the control plane's `resolvePermission` mapping so a role means the
// same thing in both hosts. Until Studio stops proxying through the service role,
// this is the only thing standing between an `editor` membership and a schema
// change.
func AllowsRequest(perms StudioPermissions, method, path string) bool {
	readOnly := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions

	switch {
	// The SQL runner still connects with the project's admin DSN, so every query
	// through it is elevated regardless of who asked. It moves to SQLEditor once
	// the runner drops to the acting user's role and RLS applies (P1.6/P1.7);
	// until then, granting a developer access here would hand them DDL.
	case path == "/sql" || strings.HasPrefix(path, "/sql/"):
		return perms.ElevatedSQL

	// Studio membership and project keys.
	case strings.HasPrefix(path, "/admin/studio-members"):
		if readOnly {
			return perms.ManageMembers || perms.SchemaView
		}
		return perms.ManageMembers

	// User administration is a separate capability from writing content rows.
	case strings.HasPrefix(path, "/auth/v1"):
		return perms.ManageMembers

	default:
		if readOnly {
			return perms.Read
		}
		return perms.Write
	}
}

// legacyAdminPermissions is what a deployment still on the claim-based path
// reports. Those deployments have no membership rows to carry a finer role, and
// the claim only ever meant "is an admin".
func legacyAdminPermissions() StudioPermissions {
	perms, _ := PermissionsForRole(RoleAdmin)
	return perms
}
