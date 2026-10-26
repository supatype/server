package studioauth

import (
	"net/http"
	"strconv"
)

// The headers that tell the control plane which Studio user a request acts
// for. Set only by this proxy, from the verified session, and removed from
// every inbound request first: a header a browser could set itself would tell
// the control plane nothing.
//
// The control plane trusts them only behind its own check that the request came
// through the gateway, and anyone able to forge that already holds the service
// role.
const (
	StudioActorHeader    = "X-Supatype-Studio-Actor"
	StudioRoleHeader     = "X-Supatype-Studio-Role"
	StudioElevatedHeader = "X-Supatype-Studio-Elevated"
	// ViewerAuthorizationHeader carries a mediated caller's own bearer token,
	// so the realtime inspector can judge changes as the person looking.
	ViewerAuthorizationHeader = "X-Supatype-Viewer-Authorization"
)

var studioIdentityHeaders = []string{
	StudioActorHeader,
	StudioRoleHeader,
	StudioElevatedHeader,
	ViewerAuthorizationHeader,
}

// stripStudioIdentity removes every inbound copy of the identity headers.
func stripStudioIdentity(h http.Header) {
	for _, name := range studioIdentityHeaders {
		h.Del(name)
	}
}

// setStudioIdentity names the admitted caller to the control plane. Elevated
// says whether they hold ElevatedSQL, which is what decides whether they may
// see other users' data as it was delivered.
func setStudioIdentity(h http.Header, adm admission) {
	if adm.actor != "" {
		h.Set(StudioActorHeader, adm.actor)
	}
	h.Set(StudioRoleHeader, adm.role)
	h.Set(StudioElevatedHeader, strconv.FormatBool(adm.perms.ElevatedSQL))
}
