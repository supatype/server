package studioauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
	"github.com/supatype/server/internal/studiobootstrap"
)

// The two endpoints Studio loads before it renders anything. Both answer from
// the database on every request: a capability baked into a session payload
// would let a demoted user keep the interface they had.

// bootstrapConfig builds a config over a project database holding this AST.
//
// Run the DB-backed packages with -p 1: they share one database and the same
// `_supatype` table names.
func bootstrapConfig(t *testing.T, ast, role string) Config {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the bootstrap tests against Postgres")
	}
	resources, err := data.Open(context.Background(), &config.Config{SQLDatabaseURL: dsn})
	if err != nil {
		t.Fatalf("open resources: %v", err)
	}
	t.Cleanup(func() { _ = resources.Close() })

	ctx := context.Background()
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}

	stmts := []string{
		`CREATE SCHEMA IF NOT EXISTS _supatype`,
		`DROP TABLE IF EXISTS _supatype.schema_state`,
		`CREATE TABLE _supatype.schema_state (
			id INT PRIMARY KEY, ast_snapshot JSONB, admin_config JSONB)`,
	}
	if ast != "" {
		stmts = append(stmts,
			`INSERT INTO _supatype.schema_state (id, ast_snapshot, admin_config)
			 VALUES (1, '`+ast+`'::jsonb, '{"theme":"dark"}'::jsonb)`)
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS _supatype.schema_state`)
	})

	c := membershipConfig(role)
	c.Resources = resources
	return c
}

const schemaWithAMaskedColumn = `{"models":[
	{"name":"Post","annotations":{"db":{"tableName":"posts"},
	 "platform":{"access":{"read":{"type":"public"},
	   "fields":{"secret":{"read":{"type":"private"}}}}}}}
]}`

// signedIn is a token for a verified application user.
func signedIn(t *testing.T) string {
	t.Helper()
	return bearer(t, jwt.MapClaims{
		"sub":          "user-1",
		"role":         "authenticated",
		"app_metadata": map[string]any{"role": "editor"},
	})
}

// ─── Method ───────────────────────────────────────────────────────────────────

func TestTheBootstrapEndpointsTakeOnlyGET(t *testing.T) {
	c := bootstrapConfig(t, schemaWithAMaskedColumn, "admin")

	for name, handler := range map[string]http.HandlerFunc{
		"schema":  SchemaHandler(c),
		"session": SessionHandler(c),
	} {
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			rec := httptest.NewRecorder()
			handler(rec, studioRequest(method, "/studio/schema", ""))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status = %d, want 405", method, name, rec.Code)
			}
		}
	}
}

// ─── Who may ask ──────────────────────────────────────────────────────────────

func TestTheBootstrapEndpointsRefuseWhoTheyMust(t *testing.T) {
	admitted := bootstrapConfig(t, schemaWithAMaskedColumn, "admin")
	refused := bootstrapConfig(t, schemaWithAMaskedColumn, "")

	for name, tc := range map[string]struct {
		config Config
		token  string
		want   int
	}{
		"no token":     {admitted, "", http.StatusUnauthorized},
		"not a member": {refused, signedIn(t), http.StatusForbidden},
	} {
		for endpoint, handler := range map[string]http.HandlerFunc{
			"schema":  SchemaHandler(tc.config),
			"session": SessionHandler(tc.config),
		} {
			rec := httptest.NewRecorder()
			handler(rec, studioRequest(http.MethodGet, "/studio/"+endpoint, tc.token))
			if rec.Code != tc.want {
				t.Errorf("%s, %s: status = %d, want %d (%s)", endpoint, name, rec.Code, tc.want, rec.Body.String())
			}
		}
	}
}

// ─── Schema ───────────────────────────────────────────────────────────────────

func TestSchemaHandler(t *testing.T) {
	c := bootstrapConfig(t, schemaWithAMaskedColumn, "admin")

	rec := httptest.NewRecorder()
	SchemaHandler(c)(rec, studioRequest(http.MethodGet, "/studio/schema", signedIn(t)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	var body struct {
		SchemaHash  string            `json:"schemaHash"`
		Models      []json.RawMessage `json:"models"`
		AdminConfig json.RawMessage   `json:"adminConfig"`
		Role        string            `json:"role"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaHash == "" || len(body.Models) != 1 || body.Role != "admin" {
		t.Errorf("body = %+v", body)
	}
	if !strings.Contains(string(body.AdminConfig), "dark") {
		t.Errorf("admin config = %s", body.AdminConfig)
	}

	// Revalidated every time, so a schema push or a role change takes effect at
	// once, and the tag makes the common case a 304.
	if got := rec.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("cache-control = %q", got)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	for name, header := range map[string]string{
		"the exact tag": etag,
		"a weak tag":    "W/" + etag,
		"one of a list": `"other", ` + etag,
		"the wildcard":  "*",
	} {
		conditional := studioRequest(http.MethodGet, "/studio/schema", signedIn(t))
		conditional.Header.Set("If-None-Match", header)
		rec := httptest.NewRecorder()
		SchemaHandler(c)(rec, conditional)
		if rec.Code != http.StatusNotModified {
			t.Errorf("%s: status = %d, want 304", name, rec.Code)
		}
	}

	// A tag that does not match is a full answer, not a 304.
	stale := studioRequest(http.MethodGet, "/studio/schema", signedIn(t))
	stale.Header.Set("If-None-Match", `"something-else"`)
	rec = httptest.NewRecorder()
	SchemaHandler(c)(rec, stale)
	if rec.Code != http.StatusOK {
		t.Errorf("a stale tag: status = %d, want 200", rec.Code)
	}
}

// Two callers with different application roles see different tables, so they
// must not share a cache entry.
func TestTheSchemaTagDistinguishesCallers(t *testing.T) {
	c := bootstrapConfig(t, schemaWithAMaskedColumn, "admin")

	tagFor := func(token string) string {
		rec := httptest.NewRecorder()
		SchemaHandler(c)(rec, studioRequest(http.MethodGet, "/studio/schema", token))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
		}
		return rec.Header().Get("ETag")
	}

	editorTag := tagFor(signedIn(t))
	otherTag := tagFor(bearer(t, jwt.MapClaims{
		"sub": "user-2", "role": "authenticated",
		"app_metadata": map[string]any{"role": "viewer"},
	}))
	if editorTag == otherTag {
		t.Errorf("two roles share the tag %q", editorTag)
	}
}

// A project that has never been pushed is a 404 — there is nothing to render —
// and a database that cannot be reached is a 503, which is a different problem
// with a different fix.
func TestSchemaWithNothingToRead(t *testing.T) {
	empty := bootstrapConfig(t, "", "admin")
	rec := httptest.NewRecorder()
	SchemaHandler(empty)(rec, studioRequest(http.MethodGet, "/studio/schema", signedIn(t)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("no schema state: status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}

	unreachable := bootstrapConfig(t, schemaWithAMaskedColumn, "admin")
	pool, err := unreachable.Resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `DROP TABLE _supatype.schema_state`); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	SchemaHandler(unreachable)(rec, studioRequest(http.MethodGet, "/studio/schema", signedIn(t)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unreachable: status = %d, want 503 (%s)", rec.Code, rec.Body.String())
	}
}

// An AST that will not parse is reported rather than rendered as a project with
// no tables.
func TestSchemaWithAnASTThatWillNotParse(t *testing.T) {
	c := bootstrapConfig(t, `{"models":"not a list"}`, "admin")

	rec := httptest.NewRecorder()
	SchemaHandler(c)(rec, studioRequest(http.MethodGet, "/studio/schema", signedIn(t)))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// ─── Session ──────────────────────────────────────────────────────────────────

func TestSessionHandler(t *testing.T) {
	c := bootstrapConfig(t, schemaWithAMaskedColumn, "admin")

	rec := httptest.NewRecorder()
	SessionHandler(c)(rec, studioRequest(http.MethodGet, "/studio/session", signedIn(t)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Sub         string            `json:"sub"`
		Role        string            `json:"role"`
		AppRole     string            `json:"appRole"`
		Mode        string            `json:"mode"`
		CanElevate  bool              `json:"canElevate"`
		Permissions StudioPermissions `json:"permissions"`
		Access      map[string]any    `json:"access"`
		Fields      map[string]any    `json:"fields"`
		SchemaHash  string            `json:"schemaHash"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Sub != "user-1" || body.Role != "admin" {
		t.Errorf("identity = %+v", body)
	}
	// The application role, not the Studio role: the policies never look at the
	// Studio role, so reporting it would describe access the database will not
	// grant.
	if body.AppRole != "editor" {
		t.Errorf("app role = %q, want the token's application role", body.AppRole)
	}
	if body.Access["posts"] == nil {
		t.Errorf("access = %v, want the per-model verdicts", body.Access)
	}
	if body.Fields["posts"] == nil {
		t.Errorf("fields = %v, want the per-column verdicts", body.Fields)
	}
	if body.SchemaHash == "" {
		t.Error("no schema hash")
	}

	// Capability is exactly what must not be cached.
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("cache-control = %q", got)
	}
}

// A role that cannot elevate acts as itself, and is told so rather than shown
// an elevated banner it cannot use.
func TestSessionReportsWhetherTheCallerMayElevate(t *testing.T) {
	for role, wantElevated := range map[string]bool{"admin": true, "editor": false} {
		c := bootstrapConfig(t, schemaWithAMaskedColumn, role)

		rec := httptest.NewRecorder()
		SessionHandler(c)(rec, studioRequest(http.MethodGet, "/studio/session", signedIn(t)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d (%s)", role, rec.Code, rec.Body.String())
		}

		var body struct {
			Mode       string `json:"mode"`
			CanElevate bool   `json:"canElevate"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.CanElevate != wantElevated {
			t.Errorf("%s: canElevate = %v, want %v", role, body.CanElevate, wantElevated)
		}
		wantMode := ModeSelf
		if wantElevated {
			wantMode = ModeElevated
		}
		if body.Mode != wantMode {
			t.Errorf("%s: mode = %q, want %q", role, body.Mode, wantMode)
		}
	}
}

// A session with no schema to describe still answers: the caller's capability
// does not depend on the schema being there.
func TestSessionWithNoSchema(t *testing.T) {
	c := bootstrapConfig(t, "", "admin")

	rec := httptest.NewRecorder()
	SessionHandler(c)(rec, studioRequest(http.MethodGet, "/studio/session", signedIn(t)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"access"`) {
		t.Error("access was reported with no schema to derive it from")
	}
}

// ─── Dev bypass ───────────────────────────────────────────────────────────────

// Under the bypass both endpoints answer with no token, as the service role.
func TestTheBootstrapEndpointsUnderDevBypass(t *testing.T) {
	c := bootstrapConfig(t, schemaWithAMaskedColumn, "admin")
	c.Mode = "dev"
	c.OpenDev = true

	for name, handler := range map[string]http.HandlerFunc{
		"schema":  SchemaHandler(c),
		"session": SessionHandler(c),
	} {
		rec := httptest.NewRecorder()
		handler(rec, studioRequest(http.MethodGet, "/studio/"+name, ""))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d (%s)", name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "dev-bypass") {
			t.Errorf("%s: the bypass identity was not reported: %s", name, rec.Body.String())
		}
	}
}

// ─── The application identity ─────────────────────────────────────────────────

// app_metadata is the developer's namespace and wins; a top-level role is the
// fallback; anything else is anon, because an unverified claim must never widen
// what a caller is told they may do.
func TestTheApplicationRoleComesFromTheToken(t *testing.T) {
	for name, tc := range map[string]struct {
		claims jwt.MapClaims
		want   string
	}{
		"app_metadata wins": {
			jwt.MapClaims{"sub": "u", "role": "authenticated",
				"app_metadata": map[string]any{"role": "editor"}},
			"editor",
		},
		"a top-level role": {
			jwt.MapClaims{"sub": "u", "role": "authenticated"}, "authenticated",
		},
		"app_metadata with no role": {
			jwt.MapClaims{"sub": "u", "role": "authenticated", "app_metadata": map[string]any{}},
			"authenticated",
		},
		"app_metadata with an empty role": {
			jwt.MapClaims{"sub": "u", "role": "authenticated",
				"app_metadata": map[string]any{"role": ""}},
			"authenticated",
		},
		"no role at all": {jwt.MapClaims{"sub": "u"}, "anon"},
	} {
		req := studioRequest(http.MethodGet, "/studio/session", bearer(t, tc.claims))
		caller := callerFromRequest(req, Result{Sub: "u"})
		if caller.AppRole != tc.want {
			t.Errorf("%s: app role = %q, want %q", name, caller.AppRole, tc.want)
		}
	}
}

// With no readable token the caller is anon with no claims, which is what the
// dev bypass and a malformed header both look like.
func TestTheApplicationIdentityWithNoReadableToken(t *testing.T) {
	for name, header := range map[string]string{
		"no header":    "",
		"not a bearer": "Basic abc",
		"not a token":  "Bearer nonsense",
	} {
		req := httptest.NewRequest(http.MethodGet, "/studio/session", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		caller := callerFromRequest(req, Result{Sub: "u"})
		if caller.AppRole != "anon" || caller.Claims != nil {
			t.Errorf("%s: caller = %+v, want anon with no claims", name, caller)
		}
	}
}

// The cache key separates an anonymous caller from a signed-in one with the
// same role, because they do not see the same thing.
func TestCallerCacheKey(t *testing.T) {
	for name, tc := range map[string]struct {
		caller studiobootstrap.Caller
		want   string
	}{
		"a signed-in editor": {studiobootstrap.Caller{UserID: "u", AppRole: "editor"}, "editor"},
		"an anonymous one":   {studiobootstrap.Caller{AppRole: "editor"}, "editor-unauth"},
		"no role named":      {studiobootstrap.Caller{UserID: "u"}, "anon"},
		"nothing at all":     {studiobootstrap.Caller{}, "anon-unauth"},
	} {
		if got := callerCacheKey(tc.caller); got != tc.want {
			t.Errorf("%s: key = %q, want %q", name, got, tc.want)
		}
	}
}

func TestRawOrNull(t *testing.T) {
	if rawOrNull(nil) != nil {
		t.Error("nothing should render as null")
	}
	if rawOrNull(json.RawMessage("")) != nil {
		t.Error("an empty message should render as null")
	}
	if rawOrNull(json.RawMessage(`{"a":1}`)) == nil {
		t.Error("a real message was dropped")
	}
}
