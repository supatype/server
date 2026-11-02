package studioauth

import (
	"net/http"
	"strings"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
	"github.com/supatype/server/internal/functions"
	"github.com/supatype/server/internal/studioctx"
	"github.com/supatype/server/internal/studiomembers"
	"github.com/supatype/server/internal/utilities"
)

// Config holds studio auth handler dependencies.
type Config struct {
	JWTSecret      string
	ServiceRoleKey string
	// AnonKey is sent as `apikey` when a request acts as the caller rather than
	// elevated, so gateways that require a key still see one that carries no
	// privilege of its own.
	AnonKey    string
	AdminRoles []string
	// Resources carries the process connections, for the endpoints that read the
	// schema snapshot straight from the database.
	Resources *data.Resources
	// Members reads and writes Studio membership. It carries the process
	// resources, so a deployment with no database still yields a usable value
	// whose every call reports that and denies.
	Members studiomembers.Store
	// Audit records requests that skip the app's row rules. Nil records them
	// through Members.
	Audit ElevatedAuditor
	// StudioRole resolves a verified user id to their Studio role from
	// `_supatype.studio_members`. Studio capability deliberately does not come
	// from a JWT claim: `app_metadata` is the developer's namespace for their own
	// app roles, so reading it here means assigning an app role can hand out admin
	// UI access by accident. Nil keeps the legacy claim-based path, so a
	// deployment that has not been migrated still works.
	StudioRole StudioRoleLookup
	// TargetRoles finds the Studio roles of the user a user administration
	// request acts on (admin_target.go). Nil looks them up through Members.
	TargetRoles TargetRoleLookup
	// AdminConfigPath is the generated `admin-config.json`, relative to the
	// working directory. `AdminRolesFromConfigFile` already reads it; the preview
	// endpoints read the publishing block from the same file, so settings the
	// engine writes reach this process by one route rather than two.
	AdminConfigPath string
	// FunctionsAdminMounted is whether the gateway serves the functions admin API
	// (functions.Mounted). Without it, the API's old address /functions/v1/admin
	// is only ever a call to a project function named admin.
	FunctionsAdminMounted bool
}

// StudioRoleLookup returns the Studio role recorded for a user id. The second
// result is false when the user has no membership row.
type StudioRoleLookup func(userID string) (string, bool)

// ConfigFromServer builds handler config from ServerConfig and admin-config path.
func ConfigFromServer(cfg *config.Config) Config {
	return Config{
		JWTSecret:             cfg.JWTSecret,
		ServiceRoleKey:        cfg.ServiceRoleKey,
		AnonKey:               cfg.AnonKey,
		AdminRoles:            AdminRolesFromConfigFile(cfg.AdminConfigPath, cfg.StudioAdminRoles),
		AdminConfigPath:       cfg.AdminConfigPath,
		FunctionsAdminMounted: functions.Mounted(cfg),
	}
}

// VerifyHandler serves GET /studio/auth/verify.
func VerifyHandler(c Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			WriteErrorBody(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		result := ResolveAccess(req, c)
		if !result.Allowed {
			status := refusalStatus(result)
			body := errorBody(status, result.Message)
			body["allowed"] = false
			utilities.WriteJSON(w, status, body)
			return
		}

		utilities.WriteJSON(w, http.StatusOK, verifyOKResponse(result, c, req))
	}
}

// verifyOKResponse reports the capability set Studio renders from.
//
// It used to hardcode every permission to true, so an `editor` membership row was
// handed a full-access UI while the control plane restricted the same role, so
// the two hosts disagreed about what a role means.
func verifyOKResponse(result Result, c Config, req *http.Request) map[string]interface{} {
	perms := result.Permissions
	if perms == nil {
		legacy := legacyAdminPermissions()
		perms = &legacy
	}

	// `mode` is the identity data requests will act as, and `canElevate` whether
	// the caller may change it. Studio shows the elevated banner and its mode
	// switch from these rather than inferring privilege from the role name.
	return map[string]interface{}{
		"allowed":     true,
		"role":        result.Role,
		"sub":         result.Sub,
		"permissions": perms,
		"mode":        reportedMode(req, *perms),
		"canElevate":  perms.ElevatedSQL,
		"seesDrafts":  roleSeesDrafts(result.Role, c),
	}
}

// roleSeesDrafts answers whether this caller sees other people's unpublished work.
//
// **Studio needs telling, because row level security cannot tell it.** Studio's
// data plane goes through the proxy as Studio or as the service role, and either
// skips the app's row policies (acting_mode.go). That is deliberate: Studio is
// an admin tool gated by its own capability system rather than by each
// project's access rules. But it means the draft-visibility setting, which the
// generated policies compile in, binds the API and not the UI. A project that
// narrowed it would still see every draft here.
//
// So the setting is enforced in both places from **one stored answer**: the
// policies read `_supatype.publishing_settings`, and this reads the same list out
// of the config the engine writes from it. Two enforcement points, one source of
// truth, and neither side owns a copy of the rule.
//
// A project with no versioned models has no publishing config, and nothing to
// show, so the answer is false and Studio renders no draft affordances at all.
func roleSeesDrafts(role string, c Config) bool {
	settings, err := PublishingSettingsFromConfigFile(c.AdminConfigPath)
	if err != nil {
		return false
	}
	return settings.RoleSeesDrafts(role)
}

// RequireAdmin wraps a handler with studio admin JWT checks.
func RequireAdmin(c Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		result := ResolveAccess(req, c)
		if !result.Allowed {
			writeRefusal(w, result)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// admission is who one proxied request comes from and how it will act.
type admission struct {
	mode  string
	actor string
	role  string
	perms StudioPermissions
	// claims is the caller's verified token, which a request acting as Studio
	// forwards with the Studio claim added.
	claims jwt.MapClaims
	// readsOnly says the request only reads, judged as AllowsRequest judged
	// it: a GraphQL query sent as a POST reads (permissionMethod).
	readsOnly bool
}

// ProxyHandler forwards privileged API calls after admin JWT verification.
// inner must be the main service mux (without /studio routes).
func ProxyHandler(inner http.Handler, c Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := routedPath(req)
		if isTokenRefresh(req, path) {
			inner.ServeHTTP(w, forwardedCopy(req))
			return
		}
		adm, ok := admitProxied(w, req, c, path)
		if !ok {
			return
		}

		req2 := forwardedCopy(req)
		mode, ok := forwardIdentity(w, req, req2, c, adm, path)
		if !ok || !clampSignedURLExpiry(w, req2, path) {
			return
		}
		audit, ok := auditForward(w, req2, c, adm, mode, path)
		if !ok {
			return
		}

		w.Header().Set(ActingModeHeader, mode)
		serveAudited(inner, w, req2, audit)
	})
}

// forwardedCopy is the request the proxy sends upstream.
//
// Clone already deep-copies the URL. An empty path is not a valid
// request-target, so it becomes the root. The Studio identity headers go before
// anything else, so no path through the proxy carries a browser's own claim
// about who it is. Its context says it came through the proxy, which is what
// lets the data plane accept a Studio claim on it and keeps it out of the
// response cache (studioctx).
func forwardedCopy(req *http.Request) *http.Request {
	req2 := req.Clone(studioctx.WithProxied(req.Context()))
	if req2.URL.Path == "" {
		req2.URL.Path = "/"
	}
	stripStudioIdentity(req2.Header)
	return req2
}

// isTokenRefresh reports whether a request is GoTrue's refresh grant.
//
// Studio's client sends every auth call through the proxy, its token refresh
// included. A refresh carries no access token, only the refresh token, so
// admission refused it, and the client then cleared the session it shares with
// the sign-in client: the next reload showed the sign-in screen. The grant is
// the same public endpoint the gateway serves at /auth/v1/token, its credential
// is the refresh token itself, and it is forwarded with the caller's own
// headers, so passing it through grants nothing the caller did not already have.
func isTokenRefresh(req *http.Request, path string) bool {
	return req.Method == http.MethodPost &&
		path == "/auth/v1/token" &&
		req.URL.Query().Get("grant_type") == "refresh_token"
}

// forwardIdentity puts the credentials and identity headers for one admitted
// request on its forwarded copy, and returns the mode it acts in. It reports
// false, having written the response, when the credentials cannot be made.
func forwardIdentity(w http.ResponseWriter, req, req2 *http.Request, c Config, adm admission, path string) (string, bool) {
	// The metadata catalogue reads catalogs through the runner's connection, read-only:
	// the service role reaches it, and there is nothing elevated to record.
	if under(path, "/metadata") {
		return ModeElevated, setServiceRole(w, req2, c)
	}
	switch {
	case adm.mode == ModeSelf && IsRealtimeInspectorRoute(req.Method, path):
		return forwardMediated(w, req, req2, c, adm)
	case adm.mode == ModeStudio:
		if !setStudioCredentials(w, req2, c, adm, path) {
			return "", false
		}
		return ModeStudio, true
	case adm.mode == ModeAnon:
		return ModeAnon, setAnonIdentity(w, req2, c)
	}
	if !setActingIdentity(w, req, req2, c, adm) {
		return "", false
	}
	if adm.mode == ModeElevated && under(path, "/platform/v1") {
		setStudioIdentity(req2.Header, adm)
	}
	return adm.mode, true
}

// forwardMediated sends a caller acting as themselves to a Realtime Inspector
// route. The platform proxy admits only the service role, so their own token
// could never reach the control plane: the control plane is told who they are
// instead, and shown their own token to judge changes as them.
func forwardMediated(w http.ResponseWriter, req, req2 *http.Request, c Config, adm admission) (string, bool) {
	if !setServiceRole(w, req2, c) {
		return "", false
	}
	setStudioIdentity(req2.Header, adm)
	req2.Header.Set(ViewerAuthorizationHeader, req.Header.Get("Authorization"))
	return ModeMediated, true
}

// setServiceRole forwards a request as the service role. It reports false,
// having written the response, when no service role key is configured.
func setServiceRole(w http.ResponseWriter, req2 *http.Request, c Config) bool {
	sr := strings.TrimSpace(c.ServiceRoleKey)
	if sr == "" {
		WriteErrorBody(w, http.StatusServiceUnavailable, "service role key not configured")
		return false
	}
	req2.Header.Set("Authorization", "Bearer "+sr)
	req2.Header.Set("apikey", sr)
	return true
}

// setAnonKey puts the anon key in `apikey`, for gateways that require one, on a
// request that does not act as the service role: the service role key there
// would re-elevate it.
func setAnonKey(req2 *http.Request, c Config) {
	if anon := strings.TrimSpace(c.AnonKey); anon != "" {
		req2.Header.Set("apikey", anon)
		return
	}
	req2.Header.Del("apikey")
}

// setAnonIdentity sends a request as an anonymous client: the anon key as both
// the apikey and the bearer, the caller's Studio session dropped. It reports
// false, having written the response, when no anon key is configured.
func setAnonIdentity(w http.ResponseWriter, req2 *http.Request, c Config) bool {
	anon := strings.TrimSpace(c.AnonKey)
	if anon == "" {
		WriteErrorBody(w, http.StatusServiceUnavailable, "anon key not configured")
		return false
	}
	req2.Header.Set("Authorization", "Bearer "+anon)
	req2.Header.Set("apikey", anon)
	return true
}

// setActingIdentity puts the credentials for an elevated or self request on its
// forwarded copy. It reports false, having written the response, when
// elevation is asked for and no service role key is configured.
func setActingIdentity(w http.ResponseWriter, req, req2 *http.Request, c Config, adm admission) bool {
	if adm.mode != ModeElevated {
		// Leave the caller's own Authorization in place: PostgREST assumes their
		// role and their own RLS policies apply.
		setAnonKey(req2, c)
		return true
	}
	if !setServiceRole(w, req2, c) {
		return false
	}
	return true
}

// admitProxied decides whether one request may pass the Studio proxy, and as
// whom. On a refusal it has already written the response.
//
// Admission alone is not enough: the request must also be one the caller's role
// covers, judged on the path the router will actually route.
func admitProxied(w http.ResponseWriter, req *http.Request, c Config, path string) (admission, bool) {
	result := ResolveAccess(req, c)
	if !result.Allowed {
		writeRefusal(w, result)
		return admission{}, false
	}

	perms := permissionsOf(result)
	method := permissionMethod(req, path)
	functionsAdmin := servesFunctionsAdmin(req, c, perms, path)
	if !allowsProxied(perms, method, path, functionsAdmin) && !allowsStoragePolicyRead(req, perms, path) {
		WriteErrorBody(w, http.StatusForbidden, "Studio role \""+result.Role+"\" cannot perform this request")
		return admission{}, false
	}
	if !guardOwnAccount(w, req, result, path) {
		return admission{}, false
	}
	if !guardAdminAccounts(w, req, c, perms, path) {
		return admission{}, false
	}
	adm, ok := admitActingMode(w, req, result, perms, path, functionsAdmin)
	adm.readsOnly = isReadOnly(method)
	return adm, ok
}

// admitActingMode decides how an admitted request acts. The SQL runner is
// elevated by definition (RX1): it acts as the service role for everyone
// AllowsRequest admits to it, whatever mode their data requests act in, and is
// recorded as elevated. So is the read-only metadata catalogue (RQ3), which needs
// the service role to reach the runner's connection; being read-only, it is not
// recorded as elevated.
//
// An upstream that admits only the service role (isServiceRoleOnly, or the
// functions admin API at its old address: functionsAdmin) acts
// elevated for a caller who may elevate, whichever mode they chose: acting as
// yourself applies to project rows, not to administration the role holds. A
// role that cannot elevate is not elevated by these routes either.
//
// A mode the caller cannot act in, one the proxy does not know included, is
// refused first, on every route: no route elevates a request whose mode was
// not understood.
func admitActingMode(w http.ResponseWriter, req *http.Request, result Result, perms StudioPermissions, path string, functionsAdmin bool) (admission, bool) {
	mode, ok := resolveActingMode(req, perms)
	if !ok {
		WriteErrorBody(w, http.StatusForbidden, "Studio role \""+result.Role+"\" cannot act in the mode it asked for")
		return admission{}, false
	}
	adm := admission{mode: ModeElevated, actor: result.Sub, role: result.Role, perms: perms, claims: result.Claims}
	if under(path, "/sql") || under(path, "/metadata") || under(path, "/studio-admin") {
		return adm, true
	}
	// Anonymous is asked for by name and means it, on every route: an
	// administration route refuses it upstream, as it refuses any anon client.
	if mode == ModeAnon {
		adm.mode = ModeAnon
		return adm, true
	}
	if perms.ElevatedSQL && (functionsAdmin || isServiceRoleOnly(req.Method, path)) {
		return adm, true
	}
	adm.mode = mode
	if mode == ModeStudio {
		return admitAsStudio(w, req, adm, path)
	}
	return adm, true
}

// admitAsStudio settles a request acting as Studio. A route with no Studio rule
// to apply (realtime, function invocation, the Realtime Inspector) acts as the
// caller themselves, whatever their role: a caller who asked for Studio mode
// did not ask to be elevated, and is not elevated silently. Any other route must
// name its resource unambiguously, since it carries more than the caller's own
// token (isUnambiguousPath).
func admitAsStudio(w http.ResponseWriter, req *http.Request, adm admission, path string) (admission, bool) {
	if studioRouteFor(req.Method, path) == studioSelf {
		adm.mode = ModeSelf
		return adm, true
	}
	if !isUnambiguousPath(path) {
		WriteErrorBody(w, http.StatusForbidden, "Studio cannot act on a path with dot segments or escapes")
		return admission{}, false
	}
	return adm, true
}
