package studioauth

import (
	"net/http"
	"strings"
)

// Studio roles and what each may do.
//
// This must agree with the control plane's `STUDIO_ROLE_PERMISSIONS`
// (supatype-cloud, src/middleware/studio-capability.ts). The same membership row
// has to mean the same thing whether the project runs self-hosted or managed —
// two hosts quietly disagreeing about what "viewer" allows is how a role becomes
// meaningless.
//
// Unknown roles get nothing. A typo in a `studio_members` row must fail closed
// rather than inherit whatever the nearest known role allows.

// StudioPermissions is the capability set Studio renders from.
type StudioPermissions struct {
	Read           bool `json:"read"`
	Write          bool `json:"write"`
	ManageUsers    bool `json:"manageUsers"`
	ManageSettings bool `json:"manageSettings"`
}

var studioRolePermissions = map[string]StudioPermissions{
	"admin":  {Read: true, Write: true, ManageUsers: true, ManageSettings: true},
	"editor": {Read: true, Write: true, ManageUsers: false, ManageSettings: false},
	"viewer": {Read: true, Write: false, ManageUsers: false, ManageSettings: false},
}

// PermissionsForRole returns the capability set for a Studio role, and whether
// the role is one this deployment understands.
func PermissionsForRole(role string) (StudioPermissions, bool) {
	perms, ok := studioRolePermissions[role]
	return perms, ok
}

// KnownStudioRoles lists the roles a membership row may hold.
func KnownStudioRoles() []string {
	// Fixed order so the API response is stable, most privileged first.
	return []string{"admin", "editor", "viewer"}
}

// AllowsRequest reports whether a capability set covers one proxied request.
//
// Mirrors the control plane's `resolvePermission` mapping so a role means the
// same thing in both hosts. Until Studio stops proxying through the service role,
// this is the only thing standing between a `viewer` membership and a write.
func AllowsRequest(perms StudioPermissions, method, path string) bool {
	readOnly := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions

	switch {
	// The SQL runner is unrestricted access to the database by definition, so it
	// tracks the most privileged capability rather than plain write.
	case path == "/sql" || strings.HasPrefix(path, "/sql"):
		return perms.ManageSettings

	// User administration, not row data.
	case strings.HasPrefix(path, "/auth/v1"):
		return perms.ManageUsers

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
	return StudioPermissions{Read: true, Write: true, ManageUsers: true, ManageSettings: true}
}
