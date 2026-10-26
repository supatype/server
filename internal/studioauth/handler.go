package studioauth

import (
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
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
	Mode       string
	// Resources carries the process connections, for the endpoints that read the
	// schema snapshot straight from the database.
	Resources *data.Resources
	// Members reads and writes Studio membership. It carries the process
	// resources, so a deployment with no database still yields a usable value
	// whose every call reports that and denies.
	Members studiomembers.Store
	// PublicURLs are the addresses this deployment answers on. DevBypass refuses
	// to open Studio unless every one of them is local, so convenience cannot
	// follow a copied config into production.
	PublicURLs []string
	// OpenDev opens Studio without authentication. It is only honoured in dev
	// mode on a locally addressed deployment; see Config.DevBypass.
	OpenDev bool
	// StudioRole resolves a verified user id to their Studio role from
	// `_supatype.studio_members`. Studio capability deliberately does not come
	// from a JWT claim: `app_metadata` is the developer's namespace for their own
	// app roles, so reading it here means assigning an app role can hand out admin
	// UI access by accident. Nil keeps the legacy claim-based path, so a
	// deployment that has not been migrated still works.
	StudioRole StudioRoleLookup
	// AdminConfigPath is the generated `admin-config.json`, relative to the
	// working directory. `AdminRolesFromConfigFile` already reads it; the preview
	// endpoints read the publishing block from the same file, so settings the
	// engine writes reach this process by one route rather than two.
	AdminConfigPath string
}

// StudioRoleLookup returns the Studio role recorded for a user id. The second
// result is false when the user has no membership row.
type StudioRoleLookup func(userID string) (string, bool)

// ConfigFromServer builds handler config from ServerConfig and admin-config path.
func ConfigFromServer(cfg *config.Config) Config {
	return Config{
		JWTSecret:       cfg.JWTSecret,
		ServiceRoleKey:  cfg.ServiceRoleKey,
		AnonKey:         cfg.AnonKey,
		AdminRoles:      AdminRolesFromConfigFile(cfg.AdminConfigPath, cfg.StudioAdminRoles),
		AdminConfigPath: cfg.AdminConfigPath,
		Mode:            cfg.Mode,
		OpenDev:         cfg.StudioOpenDev.Bool(),
		PublicURLs:      cfg.PublicURLs,
	}
}

// VerifyHandler serves GET /studio/auth/verify.
func VerifyHandler(c Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			utilities.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		if c.DevBypass() {
			utilities.WriteJSON(w, http.StatusOK, verifyOKResponse(Result{
				Role: "dev-bypass",
				Sub:  "dev-bypass",
			}, c))
			return
		}

		result := ResolveAccess(req, c)
		if !result.Allowed {
			status := http.StatusForbidden
			if result.Sub == "" && result.Message == "Authentication required" {
				status = http.StatusUnauthorized
			}
			utilities.WriteJSON(w, status, map[string]interface{}{
				"error":   "forbidden",
				"message": result.Message,
				"allowed": false,
			})
			return
		}

		utilities.WriteJSON(w, http.StatusOK, verifyOKResponse(result, c))
	}
}

// verifyOKResponse reports the capability set Studio renders from.
//
// It used to hardcode every permission to true, so an `editor` membership row was
// handed a full-access UI while the control plane restricted the same role, so
// the two hosts disagreed about what a role means.
func verifyOKResponse(result Result, c Config) map[string]interface{} {
	perms := result.Permissions
	if perms == nil {
		legacy := legacyAdminPermissions()
		perms = &legacy
	}

	// `mode` is the identity data requests will act as, and `canElevate` whether
	// the caller may change it. Studio shows the elevated banner from these rather
	// than inferring privilege from the role name.
	mode := ModeSelf
	if perms.ElevatedSQL {
		mode = ModeElevated
	}

	return map[string]interface{}{
		"allowed":     true,
		"role":        result.Role,
		"sub":         result.Sub,
		"permissions": perms,
		"mode":        mode,
		"canElevate":  perms.ElevatedSQL,
		"seesDrafts":  roleSeesDrafts(result.Role, c),
	}
}

// roleSeesDrafts answers whether this caller sees other people's unpublished work.
//
// **Studio needs telling, because row level security cannot tell it.** Studio's
// data plane goes through the proxy with the service role, which bypasses
// policies entirely. That is deliberate: Studio is an admin tool gated by its
// own capability system rather than by each project's access rules. But it means
// the draft-visibility setting, which the generated policies compile in, binds
// the API and not the UI. A project that narrowed it would still see every draft
// here.
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
	// The dev bypass has no membership row and no role, and it exists to open
	// Studio completely on a locally addressed deployment. Withholding drafts
	// from it would be a strange half-measure.
	if c.DevBypass() {
		return true
	}
	return settings.RoleSeesDrafts(role)
}

// RequireAdmin wraps a handler with studio admin JWT checks (skipped when DevBypass).
func RequireAdmin(c Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if c.DevBypass() {
			next.ServeHTTP(w, req)
			return
		}
		result := ResolveAccess(req, c)
		if !result.Allowed {
			status := http.StatusForbidden
			if result.Message == "Authentication required" {
				status = http.StatusUnauthorized
			}
			utilities.WriteJSON(w, status, map[string]string{"error": result.Message})
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
}

// devBypassAdmission is the dev bypass's: elevated, with no signed-in actor.
func devBypassAdmission() admission {
	return admission{mode: ModeElevated, role: "dev-bypass", perms: legacyAdminPermissions()}
}

// ProxyHandler forwards privileged API calls after admin JWT verification.
// inner must be the main service mux (without /studio routes).
func ProxyHandler(inner http.Handler, c Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := routedPath(req)
		adm := devBypassAdmission()
		if !c.DevBypass() {
			var ok bool
			if adm, ok = admitProxied(w, req, c, path); !ok {
				return
			}
		}

		// Clone already deep-copies the URL, so the three branches that copied it
		// again are gone: two of them could not run, because net/http never hands
		// a server handler a request without one. An empty path is not a valid
		// request-target, so that default stays.
		req2 := req.Clone(req.Context())
		if req2.URL.Path == "" {
			req2.URL.Path = "/"
		}

		// Before any branch, so no path through the proxy carries a browser's
		// own claim about who it is.
		stripStudioIdentity(req2.Header)

		mode, ok := forwardIdentity(w, req, req2, c, adm, path)
		if !ok {
			return
		}

		w.Header().Set(ActingModeHeader, mode)
		inner.ServeHTTP(w, req2)
	})
}

// forwardIdentity puts the credentials and identity headers for one admitted
// request on its forwarded copy, and returns the mode it acts in. It reports
// false, having written the response, when the service role is needed and no
// key is configured.
//
// A caller acting as themselves on a Realtime Inspector route is mediated: the
// platform proxy admits only the service role, so their own token could never
// reach the control plane, and the control plane is told who they are instead.
func forwardIdentity(w http.ResponseWriter, req, req2 *http.Request, c Config, adm admission, path string) (string, bool) {
	if adm.mode == ModeSelf && IsRealtimeInspectorRoute(req.Method, path) {
		if !setServiceRole(w, req2, c) {
			return "", false
		}
		setStudioIdentity(req2.Header, adm)
		req2.Header.Set(ViewerAuthorizationHeader, req.Header.Get("Authorization"))
		recordElevatedRequest(c.Members, req, adm.actor, req2.URL.Path, ModeMediated)
		return ModeMediated, true
	}
	if !setActingIdentity(w, req, req2, c, adm.mode, adm.actor) {
		return "", false
	}
	if adm.mode == ModeElevated && under(path, "/platform/v1") {
		setStudioIdentity(req2.Header, adm)
	}
	return adm.mode, true
}

// setServiceRole forwards a request as the service role. It reports false,
// having written the response, when no service role key is configured.
func setServiceRole(w http.ResponseWriter, req2 *http.Request, c Config) bool {
	sr := strings.TrimSpace(c.ServiceRoleKey)
	if sr == "" {
		utilities.WriteJSON(w, http.StatusServiceUnavailable,
			map[string]string{"error": "service role key not configured"})
		return false
	}
	req2.Header.Set("Authorization", "Bearer "+sr)
	req2.Header.Set("apikey", sr)
	return true
}

// setActingIdentity puts the credentials for mode on the forwarded request.
// It reports false, having written the response, when elevation is asked for
// and no service role key is configured.
func setActingIdentity(w http.ResponseWriter, req, req2 *http.Request, c Config, mode, actor string) bool {
	if mode != ModeElevated {
		// Leave the caller's own Authorization in place: PostgREST assumes their
		// role and their own RLS policies apply. `apikey` still has to be present
		// for gateways that require one, but it must not be the service role key,
		// because that would re-elevate the request.
		if anon := strings.TrimSpace(c.AnonKey); anon != "" {
			req2.Header.Set("apikey", anon)
		} else {
			req2.Header.Del("apikey")
		}
		return true
	}

	if !setServiceRole(w, req2, c) {
		return false
	}
	recordElevatedRequest(c.Members, req, actor, req2.URL.Path, ModeElevated)
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
		status := http.StatusForbidden
		if result.Message == "Authentication required" {
			status = http.StatusUnauthorized
		}
		utilities.WriteJSON(w, status, map[string]string{"error": result.Message})
		return admission{}, false
	}

	perms := legacyAdminPermissions()
	if result.Permissions != nil {
		perms = *result.Permissions
	}
	if !AllowsRequest(perms, req.Method, path) {
		utilities.WriteJSON(w, http.StatusForbidden, map[string]string{
			"error": "Studio role \"" + result.Role + "\" cannot perform this request",
		})
		return admission{}, false
	}

	mode, ok := resolveActingMode(req, perms)
	if !ok {
		utilities.WriteJSON(w, http.StatusForbidden, map[string]string{
			"error": "Studio role \"" + result.Role + "\" cannot act with elevated access",
		})
		return admission{}, false
	}
	return admission{mode: mode, actor: result.Sub, role: result.Role, perms: perms}, true
}

// recordElevatedRequest notes that a request bypassed RLS.
//
// Reads are logged only: Studio polls, and an audit row per read would bury the
// membership trail in noise. Anything that could change data gets a durable row,
// because "who edited this outside their own policies" must survive log rotation.
func recordElevatedRequest(members studiomembers.Store, req *http.Request, actor, path, mode string) {
	readOnly := req.Method == http.MethodGet ||
		req.Method == http.MethodHead ||
		req.Method == http.MethodOptions

	logrus.WithFields(logrus.Fields{
		"actor":  actor,
		"method": req.Method,
		"path":   path,
		"mode":   mode,
	}).Info("studio: elevated request")

	if readOnly {
		return
	}
	members.AuditElevated(req.Context(), actor, req.Method, path)
}
