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
// handed a full-access UI while the control plane restricted the same role — the
// two hosts disagreeing about what a role means.
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
// policies entirely — that is deliberate, Studio is an admin tool gated by its
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

// ProxyHandler forwards privileged API calls after admin JWT verification.
// inner must be the main service mux (without /studio routes).
func ProxyHandler(inner http.Handler, c Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mode := ModeElevated
		actor := ""

		if !c.DevBypass() {
			result := ResolveAccess(req, c)
			if !result.Allowed {
				status := http.StatusForbidden
				if result.Message == "Authentication required" {
					status = http.StatusUnauthorized
				}
				utilities.WriteJSON(w, status, map[string]string{"error": result.Message})
				return
			}
			actor = result.Sub

			// Admission alone is not enough: an `editor` must not reach the SQL
			// runner or user administration through here.
			perms := legacyAdminPermissions()
			if result.Permissions != nil {
				perms = *result.Permissions
			}
			if !AllowsRequest(perms, req.Method, req.URL.Path) {
				utilities.WriteJSON(w, http.StatusForbidden, map[string]string{
					"error": "Studio role \"" + result.Role + "\" cannot perform this request",
				})
				return
			}

			var ok bool
			mode, ok = resolveActingMode(req, perms)
			if !ok {
				utilities.WriteJSON(w, http.StatusForbidden, map[string]string{
					"error": "Studio role \"" + result.Role + "\" cannot act with elevated access",
				})
				return
			}
		}

		// Clone already deep-copies the URL, so the three branches that copied it
		// again — two of which could not run, because net/http never hands a
		// server handler a request without one — are gone. An empty path is not
		// a valid request-target, so that default stays.
		req2 := req.Clone(req.Context())
		if req2.URL.Path == "" {
			req2.URL.Path = "/"
		}

		if mode == ModeElevated {
			sr := strings.TrimSpace(c.ServiceRoleKey)
			if sr == "" {
				utilities.WriteJSON(w, http.StatusServiceUnavailable,
					map[string]string{"error": "service role key not configured"})
				return
			}
			req2.Header.Set("Authorization", "Bearer "+sr)
			req2.Header.Set("apikey", sr)
			recordElevatedRequest(c.Members, req, actor, req2.URL.Path)
		} else {
			// Leave the caller's own Authorization in place: PostgREST assumes
			// their role and their own RLS policies apply. `apikey` still has to
			// be present for gateways that require one, but it must not be the
			// service role key — that would re-elevate the request.
			if anon := strings.TrimSpace(c.AnonKey); anon != "" {
				req2.Header.Set("apikey", anon)
			} else {
				req2.Header.Del("apikey")
			}
		}

		w.Header().Set(ActingModeHeader, mode)
		inner.ServeHTTP(w, req2)
	})
}

// recordElevatedRequest notes that a request bypassed RLS.
//
// Reads are logged only: Studio polls, and an audit row per read would bury the
// membership trail in noise. Anything that could change data gets a durable row,
// because "who edited this outside their own policies" must survive log rotation.
func recordElevatedRequest(members studiomembers.Store, req *http.Request, actor, path string) {
	readOnly := req.Method == http.MethodGet ||
		req.Method == http.MethodHead ||
		req.Method == http.MethodOptions

	logrus.WithFields(logrus.Fields{
		"actor":  actor,
		"method": req.Method,
		"path":   path,
		"mode":   ModeElevated,
	}).Info("studio: elevated request")

	if readOnly {
		return
	}
	members.AuditElevated(req.Context(), actor, req.Method, path)
}
