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
//   - ModeSelf forwards the caller's own token. PostgREST assumes their role and
//     their own RLS policies apply, so Studio shows them exactly what their
//     application would.
//   - ModeElevated injects the service role key. It is the escape hatch for
//     administration that RLS would otherwise block, gated on the `elevatedSql`
//     capability and recorded.
//
// A caller may always ask to drop to ModeSelf. Only a role holding `elevatedSql`
// may act elevated, and asking for it without that capability is refused rather
// than silently downgraded — a request that would return a misleadingly empty
// result is worse than an error.
const (
	ModeSelf     = "self"
	ModeElevated = "elevated"
)

// ActingModeHeader lets the client state which identity it wants, and carries the
// resolved mode back on the response so the UI can show it. Studio's elevated
// banner reads this rather than assuming.
const ActingModeHeader = "X-Supatype-Acting-Mode"

// resolveActingMode decides how one proxied request should act.
//
// The default is elevated for a role that may elevate, and self for everyone
// else. That keeps an administrator's panel working as it always has while the
// roles a project would hand to a contractor are confined to their own RLS
// policies. The default becomes self for everyone once Studio ships the toggle
// that makes elevation visible and reversible in the UI.
func resolveActingMode(req *http.Request, perms StudioPermissions) (mode string, ok bool) {
	requested := strings.ToLower(strings.TrimSpace(req.Header.Get(ActingModeHeader)))

	switch requested {
	case ModeSelf:
		// Dropping privilege needs no permission.
		return ModeSelf, true
	case ModeElevated:
		if !perms.ElevatedSQL {
			return "", false
		}
		return ModeElevated, true
	case "":
		if perms.ElevatedSQL {
			return ModeElevated, true
		}
		return ModeSelf, true
	default:
		return "", false
	}
}
