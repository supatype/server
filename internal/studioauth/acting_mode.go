package studioauth

import (
	"net/http"
	"strings"
)

// Who a Studio data request acts as.
//
// Studio used to forward every request with the service role key, which bypasses
// RLS entirely: being admitted to the panel was the same thing as having
// unrestricted access to the database. Identity and capability are separate, so
// the acting identity is now separate too.
//
//   - ModeStudio is the default for editors and viewers: Studio's roles decide
//     what they may do with content, not the app's rules (owner decision
//     2026-10-21). Admins and developers may ask for it.
//     Rows go to PostgREST with the caller's own identity plus the Studio claim
//     (studio_token.go), which the engine's Studio policies admit, so the app's
//     row rules are skipped while its field rules, masking included, still apply
//     to the caller. Files go to storage as the service role, since storage has no
//     field rules. Every such request is recorded.
//   - ModeSelf forwards the caller's own token. PostgREST assumes their role and
//     their own RLS policies apply, so Studio shows them exactly what their
//     application would: a preview of the app's rules, for any role that asks.
//   - ModeElevated injects the service role key, which no rule applies to, masks
//     included. Gated on the `elevatedSql` capability, the default for the roles
//     that hold it (admins and developers), and recorded.
//   - ModeMediated is the Realtime Inspector's, for a caller acting as
//     themselves: the service role key reaches the control plane, which is told
//     who the caller is (identity.go) and shows them only what that allows.
//
// A caller may always ask to drop to ModeSelf. Asking for a mode the role does
// not hold is refused rather than silently downgraded, because a request that
// returns a misleadingly empty result is worse than an error.
const (
	ModeSelf     = "self"
	ModeStudio   = "studio"
	ModeElevated = "elevated"
	ModeMediated = "mediated"
)

// ActingModeHeader lets the client state which identity it wants, and carries the
// resolved mode back on the response so the UI can show it. Studio's mode notice
// reads this rather than assuming.
const ActingModeHeader = "X-Supatype-Acting-Mode"

// resolveActingMode decides how one proxied request should act: the mode asked
// for when the caller's role holds it, else the role's default (defaultMode).
func resolveActingMode(req *http.Request, perms StudioPermissions) (mode string, ok bool) {
	requested := strings.ToLower(strings.TrimSpace(req.Header.Get(ActingModeHeader)))
	switch requested {
	case ModeSelf:
		// Dropping privilege needs no permission.
		return ModeSelf, true
	case ModeElevated:
		return ModeElevated, perms.ElevatedSQL
	case ModeStudio:
		return ModeStudio, perms.Read
	case "":
		return defaultMode(perms), true
	default:
		return "", false
	}
}

// defaultMode is the mode a request that asks for none acts in: elevated, with
// full access as before, for a role that may elevate; Studio for any other role
// that reads content.
func defaultMode(perms StudioPermissions) string {
	if perms.ElevatedSQL {
		return ModeElevated
	}
	if perms.Read {
		return ModeStudio
	}
	return ModeSelf
}

// reportedMode is the mode a request carrying this request's header acts in:
// the one asked for when the caller may have it, else the default. Verify and
// session report it, so Studio's mode switch shows what its requests will do.
func reportedMode(req *http.Request, perms StudioPermissions) string {
	if mode, ok := resolveActingMode(req, perms); ok {
		return mode
	}
	return defaultMode(perms)
}
