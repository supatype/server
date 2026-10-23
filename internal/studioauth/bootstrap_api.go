package studioauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/studiobootstrap"
	"github.com/supatype/server/internal/utilities"
)

// Studio's two bootstrap endpoints:
//
//	GET /studio/schema    the pushed schema, filtered to what the caller may reach
//	GET /studio/session   what the caller may do, resolved as far as possible
//
// Both are answered from the database on every request. A capability baked into a
// session payload or a cookie would let a demoted user keep the interface they had
// until it expired, and capability is precisely the thing that must not be
// cacheable. What *is* cacheable is the schema, which is the same for everyone
// with the same access, hence the ETag.

// SchemaHandler serves GET /studio/schema.
func SchemaHandler(c Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			utilities.WriteJSON(w, http.StatusMethodNotAllowed, errorBody("method not allowed"))
			return
		}

		caller, result, ok := bootstrapCaller(w, req, c)
		if !ok {
			return
		}

		snapshot, ok := schemaSnapshot(w, req, c)
		if !ok || notModified(w, req, snapshot, caller) {
			return
		}

		models, err := studiobootstrap.FilterForCaller(snapshot, caller)
		if err != nil {
			utilities.WriteJSON(w, http.StatusInternalServerError, errorBody("could not read the schema AST"))
			return
		}

		utilities.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"schemaHash":  snapshot.Hash,
			"models":      models,
			"adminConfig": rawOrNull(snapshot.AdminConfig),
			"role":        result.Role,
		})
	}
}

// schemaSnapshot loads the schema state for the schema endpoint, answering the
// request itself when it cannot: 404 before the first push, 503 when the state
// is there but could not be read.
func schemaSnapshot(w http.ResponseWriter, req *http.Request, c Config) (*studiobootstrap.Snapshot, bool) {
	snapshot, err := studiobootstrap.LoadSnapshot(req.Context(), c.Resources)
	if errors.Is(err, studiobootstrap.ErrNoSchemaState) {
		utilities.WriteJSON(w, http.StatusNotFound, errorBody(err.Error()))
		return nil, false
	}
	if err != nil {
		logSchemaReadFailure(req, err)
		utilities.WriteJSON(w, http.StatusServiceUnavailable, errorBody("could not read schema state"))
		return nil, false
	}
	return snapshot, true
}

// notModified sets the schema's cache headers and answers 304 when the client's
// copy is current.
func notModified(w http.ResponseWriter, req *http.Request, snapshot *studiobootstrap.Snapshot, caller studiobootstrap.Caller) bool {
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
		return true
	}
	return false
}

// The session's `fieldAccess` marker. Without it a session whose schema could
// not be read looked the same as one whose schema has no access rules, and Studio
// read the missing keys as "nothing is restricted".
const (
	fieldAccessAvailable   = "available"
	fieldAccessUnavailable = "unavailable"
)

// SessionHandler serves GET /studio/session.
//
// Deliberately has no `userId` parameter: it answers for the caller the token
// names and nobody else. A parameter would be an invitation to ask about another
// user, and the answer would then have to be trusted by whoever asked.
func SessionHandler(c Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			utilities.WriteJSON(w, http.StatusMethodNotAllowed, errorBody("method not allowed"))
			return
		}

		caller, result, ok := bootstrapCaller(w, req, c)
		if !ok {
			return
		}

		perms := permissionsOf(result)

		body := map[string]interface{}{
			"sub":         result.Sub,
			"role":        result.Role,
			"appRole":     caller.AppRole,
			"permissions": perms,
			"mode":        reportedMode(req, perms),
			"canElevate":  perms.ElevatedSQL,
		}

		markFieldAccess(body, req, loadSessionAccess(body, req, c, caller, perms.Write))

		w.Header().Set("Cache-Control", "private, no-store")
		utilities.WriteJSON(w, http.StatusOK, body)
	}
}

// loadSessionAccess reads the schema state and puts the caller's access on a
// session body, or says why it could not.
func loadSessionAccess(body map[string]interface{}, req *http.Request, c Config, caller studiobootstrap.Caller, canWrite bool) error {
	snapshot, err := studiobootstrap.LoadSnapshot(req.Context(), c.Resources)
	if err != nil {
		return err
	}
	return addSessionAccess(body, snapshot, caller, canWrite)
}

// markFieldAccess says on a session body whether its access answers are there.
//
// A session still answers when they are not, because the caller's capability
// does not depend on the schema. Leaving the keys out was indistinguishable from
// a schema with no rules, so Studio drew every column as unrestricted; the
// marker lets it say "unavailable" instead.
func markFieldAccess(body map[string]interface{}, req *http.Request, err error) {
	if err == nil {
		body["fieldAccess"] = fieldAccessAvailable
		return
	}
	body["fieldAccess"] = fieldAccessUnavailable
	if errors.Is(err, studiobootstrap.ErrNoSchemaState) {
		body["fieldAccessReason"] = err.Error()
		return
	}
	logSchemaReadFailure(req, err)
	body["fieldAccessReason"] = "could not read the schema state; the server log has the cause"
}

// loggedSchemaFailures holds each schema read failure already logged, so a
// project that cannot be read says why once rather than on every Studio load.
var loggedSchemaFailures sync.Map

// logSchemaReadFailure logs a schema state read that failed, once per distinct
// error.
func logSchemaReadFailure(req *http.Request, err error) {
	if _, seen := loggedSchemaFailures.LoadOrStore(err.Error(), struct{}{}); seen {
		return
	}
	logrus.WithError(err).WithField("path", req.URL.Path).
		Error("studio: could not read _supatype.schema_state, so Studio has no schema or access answers")
}

// addSessionAccess puts the caller's settled access on a session body.
//
// Row-independent access per model, so Studio can grey out what is settled and
// only ask per row where the answer genuinely depends on one. Per-column
// verdicts, so a masked cell can show a lock instead of an empty string, a
// readable-but-not-writable input can be disabled rather than rejected on save,
// and a column nobody can create is absent from a create form rather than
// blocking it. A Studio role that writes nothing (viewer) is offered no write,
// whatever the app's own rules would let its token do: the proxy refuses those
// requests, so offering them would show controls that cannot work.
//
// Both halves or neither: an AST that cannot be read is an error, so the session
// is marked unavailable rather than answered with one half missing.
func addSessionAccess(body map[string]interface{}, snapshot *studiobootstrap.Snapshot, caller studiobootstrap.Caller, canWrite bool) error {
	models, err := studiobootstrap.FilterForCaller(snapshot, caller)
	if err != nil {
		return fmt.Errorf("model access from the schema AST: %w", err)
	}
	fields, err := studiobootstrap.FieldVerdictsForCaller(snapshot, caller)
	if err != nil {
		return fmt.Errorf("field access from the schema AST: %w", err)
	}

	access := make(map[string]map[string]studiobootstrap.Verdict, len(models))
	for _, m := range models {
		access[m.Table] = withoutWrites(m.Access, canWrite)
	}
	if !canWrite {
		for _, columns := range fields {
			for name, verdict := range columns {
				columns[name] = studiobootstrap.FieldVerdict{Read: verdict.Read, Write: studiobootstrap.VerdictDeny, Create: studiobootstrap.VerdictDeny}
			}
		}
	}
	body["access"] = access
	body["fields"] = fields
	body["schemaHash"] = snapshot.Hash
	return nil
}

// withoutWrites is a model's access with create, update and delete denied when
// the caller's Studio role writes nothing.
func withoutWrites(access map[string]studiobootstrap.Verdict, canWrite bool) map[string]studiobootstrap.Verdict {
	if canWrite {
		return access
	}
	out := make(map[string]studiobootstrap.Verdict, len(access))
	for op, verdict := range access {
		if op != "read" {
			verdict = studiobootstrap.VerdictDeny
		}
		out[op] = verdict
	}
	return out
}

// bootstrapCaller admits the request and describes who is asking.
func bootstrapCaller(
	w http.ResponseWriter,
	req *http.Request,
	c Config,
) (studiobootstrap.Caller, Result, bool) {
	result := ResolveAccess(req, c)
	if !result.Allowed {
		status := http.StatusForbidden
		if result.Message == "Authentication required" {
			status = http.StatusUnauthorized
		}
		utilities.WriteJSON(w, status, errorBody(result.Message))
		return studiobootstrap.Caller{}, result, false
	}

	caller := callerFromRequest(req, result)
	// Acting as Studio, the app's row rules do not limit them (acting_mode.go).
	caller.AsStudio = reportedMode(req, permissionsOf(result)) == ModeStudio
	return caller, result, true
}

// callerFromRequest reads the *application* identity from the token.
//
// The app role is what `auth.role()` returns and what a `Role<>` rule tests: the
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
	if caller.AsStudio {
		// A caller acting as Studio is answered differently from the same caller
		// acting as themselves, so the two must not share a tag.
		role += "+studio"
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
