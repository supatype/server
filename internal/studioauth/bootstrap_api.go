package studioauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/studiobootstrap"
)

// Studio's two bootstrap endpoints:
//
//	GET /studio/schema   — the pushed schema, filtered to what the caller may reach
//	GET /studio/session  — what the caller may do, resolved as far as possible
//
// Both are answered from the database on every request. A capability baked into a
// session payload or a cookie would let a demoted user keep the interface they had
// until it expired, and capability is precisely the thing that must not be
// cacheable. What *is* cacheable is the schema, which is the same for everyone
// with the same access — hence the ETag.

// SchemaHandler serves GET /studio/schema.
func SchemaHandler(c Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, errorBody("method not allowed"))
			return
		}

		caller, result, ok := bootstrapCaller(w, req, c)
		if !ok {
			return
		}

		snapshot, err := studiobootstrap.LoadSnapshot(req.Context(), c.Resources)
		if err != nil {
			if errors.Is(err, studiobootstrap.ErrNoSchemaState) {
				writeJSON(w, http.StatusNotFound, errorBody(err.Error()))
				return
			}
			writeJSON(w, http.StatusServiceUnavailable, errorBody("could not read schema state"))
			return
		}

		// The filtered payload depends on the caller as well as the schema, so the
		// tag covers both. Without the role in it, two callers with different
		// access would share a cache entry.
		etag := "\"" + snapshot.Hash + "-" + callerCacheKey(caller) + "\""
		w.Header().Set("ETag", etag)
		// Revalidate every time: a schema push or a role change must take effect
		// immediately, and the ETag makes the common case a 304 anyway.
		w.Header().Set("Cache-Control", "private, no-cache")

		if match := req.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		models, err := studiobootstrap.FilterForCaller(snapshot, caller)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorBody("could not read the schema AST"))
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"schemaHash":  snapshot.Hash,
			"models":      models,
			"adminConfig": rawOrNull(snapshot.AdminConfig),
			"role":        result.Role,
		})
	}
}

// SessionHandler serves GET /studio/session.
//
// Deliberately has no `userId` parameter: it answers for the caller the token
// names and nobody else. A parameter would be an invitation to ask about another
// user, and the answer would then have to be trusted by whoever asked.
func SessionHandler(c Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, errorBody("method not allowed"))
			return
		}

		caller, result, ok := bootstrapCaller(w, req, c)
		if !ok {
			return
		}

		perms := legacyAdminPermissions()
		if result.Permissions != nil {
			perms = *result.Permissions
		}

		mode := ModeSelf
		if perms.ElevatedSQL {
			mode = ModeElevated
		}

		body := map[string]interface{}{
			"sub":         result.Sub,
			"role":        result.Role,
			"appRole":     caller.AppRole,
			"permissions": perms,
			"mode":        mode,
			"canElevate":  perms.ElevatedSQL,
		}

		// Row-independent access per model, so Studio can grey out what is settled
		// and only ask per row where the answer genuinely depends on one.
		if snapshot, err := studiobootstrap.LoadSnapshot(req.Context(), c.Resources); err == nil {
			if models, err := studiobootstrap.FilterForCaller(snapshot, caller); err == nil {
				access := make(map[string]map[string]studiobootstrap.Verdict, len(models))
				for _, m := range models {
					access[m.Table] = m.Access
				}
				body["access"] = access
				body["schemaHash"] = snapshot.Hash
			}

			// Per-column verdicts, so a masked cell can show a lock instead of an empty
			// string, a readable-but-not-writable input can be disabled rather than
			// rejected on save, and a column nobody can create is absent from a create
			// form rather than blocking it.
			if fields, err := studiobootstrap.FieldVerdictsForCaller(snapshot, caller); err == nil {
				body["fields"] = fields
			}
		}

		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, body)
	}
}

// bootstrapCaller admits the request and describes who is asking.
func bootstrapCaller(
	w http.ResponseWriter,
	req *http.Request,
	c Config,
) (studiobootstrap.Caller, Result, bool) {
	if c.DevBypass() {
		return studiobootstrap.Caller{AppRole: "service_role"},
			Result{Allowed: true, Role: "dev-bypass", Sub: "dev-bypass"}, true
	}

	result := ResolveAccess(req, c)
	if !result.Allowed {
		status := http.StatusForbidden
		if result.Message == "Authentication required" {
			status = http.StatusUnauthorized
		}
		writeJSON(w, status, errorBody(result.Message))
		return studiobootstrap.Caller{}, result, false
	}

	return callerFromRequest(req, result), result, true
}

// callerFromRequest reads the *application* identity from the token.
//
// The app role is what `auth.role()` returns and what a `Role<>` rule tests — the
// developer's namespace, not the Studio role. Using the Studio role here would
// report access the database will not grant, because the policies never look at it.
func callerFromRequest(req *http.Request, result Result) studiobootstrap.Caller {
	caller := studiobootstrap.Caller{UserID: result.Sub, AppRole: "anon"}

	claims := unverifiedClaims(extractBearerToken(req))
	if claims == nil {
		return caller
	}
	caller.Claims = claims

	if appMeta, ok := claims["app_metadata"].(map[string]interface{}); ok {
		if role, ok := appMeta["role"].(string); ok && role != "" {
			caller.AppRole = role
			return caller
		}
	}
	if role, ok := claims["role"].(string); ok && role != "" {
		caller.AppRole = role
	}
	return caller
}

// unverifiedClaims decodes claims without checking the signature.
//
// Safe only because `ResolveAccess` has already verified this exact token; these
// claims decorate an answer about an identity that is established, and are never
// themselves an authorization input.
func unverifiedClaims(token string) map[string]interface{} {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(token, claims); err != nil {
		return nil
	}
	return claims
}

// callerCacheKey distinguishes callers whose filtered schema may differ. The app
// role is the only input to filtering beyond the schema itself; the user id is not
// included because two users with the same role see the same table list.
func callerCacheKey(caller studiobootstrap.Caller) string {
	role := strings.TrimSpace(caller.AppRole)
	if role == "" {
		role = "anon"
	}
	if caller.UserID == "" {
		return role + "-unauth"
	}
	return role
}

// etagMatches handles the comma-separated list and the `W/` weak prefix that a
// conditional request may carry.
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

func rawOrNull(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
