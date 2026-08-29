package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/valkey"
	"github.com/supatype/server/internal/data/valkey/valkeytest"
	"github.com/supatype/server/internal/restcache"
)

// The admin API changes what the data plane does and hands out a database
// password. What it refuses matters more than what it returns.

const serviceKey = "service-role-key"

// memStore is an apiconfig.Store held in memory, so a test can watch what was
// written rather than only what came back.
type memStore struct {
	cfg    apiconfig.ApiConfig
	getErr error
	setErr error
	sets   int
}

func newMemStore() *memStore { return &memStore{cfg: apiconfig.DefaultApiConfig()} }

func (s *memStore) Get(context.Context) (apiconfig.ApiConfig, error) {
	if s.getErr != nil {
		return apiconfig.ApiConfig{}, s.getErr
	}
	return s.cfg, nil
}

func (s *memStore) Set(_ context.Context, cfg apiconfig.ApiConfig) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.sets++
	s.cfg = cfg
	return nil
}

// api returns the handler and its store.
//
// Outside dev mode the service-role key is filled in unless the test is about
// its absence, so a test that forgets it gets a 403 it did not mean to assert.
func api(t *testing.T, cfg *config.Config, cache valkey.Client) (http.Handler, *memStore) {
	t.Helper()
	if cfg.Mode != "dev" && cfg.ServiceRoleKey == "" {
		cfg.ServiceRoleKey = serviceKey
	}
	store := newMemStore()
	return Handler(store, cfg, cache), store
}

// devConfig is the mode in which the admin API needs no token.
func devConfig() *config.Config { return &config.Config{Mode: "dev"} }

// call runs one request.
func call(t *testing.T, handler http.Handler, method, path, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not the error shape: %s", rec.Body.String())
	}
	return body.Error
}

// ─── The gate ─────────────────────────────────────────────────────────────────

// Everything under /admin/v1 changes the data plane or reveals a secret, so
// nothing reaches it without the service role.
func TestEveryRouteNeedsTheServiceRole(t *testing.T) {
	cfg := &config.Config{Mode: "standalone", ServiceRoleKey: serviceKey}
	handler, _ := api(t, cfg, valkeytest.New())

	routes := []struct{ method, path string }{
		{http.MethodGet, "/config/rest"},
		{http.MethodPatch, "/config/rest"},
		{http.MethodGet, "/config/graphql"},
		{http.MethodPatch, "/config/graphql"},
		{http.MethodGet, "/database/credentials/status"},
		{http.MethodPost, "/database/credentials/first-view"},
		{http.MethodPost, "/database/credentials/rotate"},
		{http.MethodGet, "/cache"},
		{http.MethodDelete, "/cache"},
		{http.MethodGet, "/cache/entries/abc"},
	}

	for _, route := range routes {
		for name, bearer := range map[string]string{
			"no token":    "",
			"a bad token": "nope",
		} {
			rec := call(t, handler, route.method, route.path, bearer, "{}")
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s with %s: status = %d, want 403", route.method, route.path, name, rec.Code)
			}
		}
		// The gate and some handlers both answer 403, so the message is what
		// distinguishes "you may not be here" from "that is disabled".
		rec := call(t, handler, route.method, route.path, serviceKey, "{}")
		if strings.Contains(errorOf(t, rec), "service role key") {
			t.Errorf("%s %s with the service role was refused by the gate", route.method, route.path)
		}
	}
}

// A deployment with no key configured fails closed, rather than accepting
// anything or an empty bearer.
func TestNoKeyConfiguredFailsClosed(t *testing.T) {
	// Built directly, because the helper fills the key in.
	handler := Handler(newMemStore(), &config.Config{Mode: "standalone"}, valkeytest.New())

	rec := call(t, handler, http.MethodGet, "/config/rest", serviceKey, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := errorOf(t, rec); got != "service role key not configured" {
		t.Errorf("error = %q", got)
	}
}

// Dev mode is a local machine, where there is no key to have.
func TestDevModeNeedsNoToken(t *testing.T) {
	handler, _ := api(t, devConfig(), valkeytest.New())

	if rec := call(t, handler, http.MethodGet, "/config/rest", "", ""); rec.Code != http.StatusOK {
		t.Errorf("status = %d (%s)", rec.Code, rec.Body.String())
	}
}

// ─── REST configuration ───────────────────────────────────────────────────────

func TestGetAndPatchRestConfig(t *testing.T) {
	handler, store := api(t, devConfig(), valkeytest.New())

	rec := call(t, handler, http.MethodGet, "/config/rest", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	var got apiconfig.RestConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "public" || got.MaxRows != 1000 {
		t.Errorf("config = %+v", got)
	}

	rec = call(t, handler, http.MethodPatch, "/config/rest", "",
		`{"schema":"app","max_rows":50,"cache_max_ttl":60,"cache_tables":{"posts":{"enabled":true}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	if store.cfg.Rest.Schema != "app" || store.cfg.Rest.MaxRows != 50 || store.cfg.Rest.CacheMaxTTL != 60 {
		t.Errorf("stored = %+v", store.cfg.Rest)
	}
	if !store.cfg.Rest.CacheTables["posts"].Enabled {
		t.Errorf("cache tables = %v", store.cfg.Rest.CacheTables)
	}
}

// A patch naming nothing changes nothing but still writes, which is how a
// caller confirms the current state.
func TestAnEmptyPatchLeavesTheConfigAlone(t *testing.T) {
	handler, store := api(t, devConfig(), valkeytest.New())
	store.cfg.Rest.Schema = "app"

	if rec := call(t, handler, http.MethodPatch, "/config/rest", "", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if store.cfg.Rest.Schema != "app" {
		t.Errorf("schema = %q", store.cfg.Rest.Schema)
	}
}

// The schema is interpolated into a header PostgREST acts on, and the bounds
// are what the API documents. Anything outside either is the caller's mistake.
func TestRestConfigRefusals(t *testing.T) {
	handler, store := api(t, devConfig(), valkeytest.New())

	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"not JSON":               {"{", "invalid JSON"},
		"a schema with a space":  {`{"schema":"my schema"}`, "invalid schema name"},
		"a schema with a quote":  {`{"schema":"a\"b"}`, "invalid schema name"},
		"a schema starting 1":    {`{"schema":"1st"}`, "invalid schema name"},
		"an empty schema":        {`{"schema":""}`, "invalid schema name"},
		"a schema too long":      {`{"schema":"` + strings.Repeat("a", 64) + `"}`, "invalid schema name"},
		"max_rows of zero":       {`{"max_rows":0}`, "max_rows must be 1–100000"},
		"max_rows negative":      {`{"max_rows":-1}`, "max_rows must be 1–100000"},
		"max_rows too large":     {`{"max_rows":100001}`, "max_rows must be 1–100000"},
		"a negative cache ttl":   {`{"cache_max_ttl":-1}`, "cache_max_ttl must be 0–86400"},
		"a cache ttl over a day": {`{"cache_max_ttl":86401}`, "cache_max_ttl must be 0–86400"},
	} {
		rec := call(t, handler, http.MethodPatch, "/config/rest", "", tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
		if got := errorOf(t, rec); got != tc.want {
			t.Errorf("%s: error = %q, want %q", name, got, tc.want)
		}
	}
	if store.sets != 0 {
		t.Errorf("a refused patch was written %d times", store.sets)
	}
}

// The names PostgREST accepts, and this must not refuse.
func TestSchemaNamesThatAreFine(t *testing.T) {
	handler, _ := api(t, devConfig(), valkeytest.New())

	for _, schema := range []string{"public", "_private", "app_v2", "a$b", strings.Repeat("a", 63)} {
		body := `{"schema":"` + schema + `"}`
		if rec := call(t, handler, http.MethodPatch, "/config/rest", "", body); rec.Code != http.StatusOK {
			t.Errorf("%q was refused: %s", schema, rec.Body.String())
		}
	}
}

// ─── GraphQL configuration ────────────────────────────────────────────────────

func TestGetAndPatchGraphQLConfig(t *testing.T) {
	handler, store := api(t, devConfig(), valkeytest.New())

	if rec := call(t, handler, http.MethodGet, "/config/graphql", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}

	rec := call(t, handler, http.MethodPatch, "/config/graphql", "",
		`{"introspection":false,"max_query_depth":5,"max_rows":10}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	if store.cfg.GraphQL.Introspection || store.cfg.GraphQL.MaxQueryDepth != 5 || store.cfg.GraphQL.MaxRows != 10 {
		t.Errorf("stored = %+v", store.cfg.GraphQL)
	}
}

// Query depth is what stops a nested query costing the database everything, so
// the bound is enforced rather than documented.
func TestGraphQLConfigRefusals(t *testing.T) {
	handler, _ := api(t, devConfig(), valkeytest.New())

	for name, tc := range map[string]struct{ body, want string }{
		"not JSON":           {"{", "invalid JSON"},
		"a depth of zero":    {`{"max_query_depth":0}`, "max_query_depth must be 1–50"},
		"a depth over fifty": {`{"max_query_depth":51}`, "max_query_depth must be 1–50"},
		"max_rows of zero":   {`{"max_rows":0}`, "max_rows must be 1–100000"},
		"max_rows too large": {`{"max_rows":100001}`, "max_rows must be 1–100000"},
	} {
		rec := call(t, handler, http.MethodPatch, "/config/graphql", "", tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
		if got := errorOf(t, rec); got != tc.want {
			t.Errorf("%s: error = %q, want %q", name, got, tc.want)
		}
	}
}

// ─── The store ────────────────────────────────────────────────────────────────

// A configuration that cannot be read or written is reported rather than
// answered as though it worked.
func TestAStoreThatWillNotWork(t *testing.T) {
	for name, path := range map[string]string{"rest": "/config/rest", "graphql": "/config/graphql"} {
		unreadable, _ := api(t, devConfig(), valkeytest.New())
		_ = unreadable

		store := newMemStore()
		store.getErr = valkeytest.ErrFailed
		handler := Handler(store, devConfig(), valkeytest.New())

		for _, method := range []string{http.MethodGet, http.MethodPatch} {
			rec := call(t, handler, method, path, "", `{}`)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("%s %s unreadable: status = %d, want 500", method, name, rec.Code)
			}
		}

		store = newMemStore()
		store.setErr = valkeytest.ErrFailed
		handler = Handler(store, devConfig(), valkeytest.New())
		if rec := call(t, handler, http.MethodPatch, path, "", `{}`); rec.Code != http.StatusInternalServerError {
			t.Errorf("%s unwritable: status = %d, want 500", name, rec.Code)
		}
	}
}

// ─── Methods ──────────────────────────────────────────────────────────────────

func TestMethodsThatAreNotAllowed(t *testing.T) {
	handler, _ := api(t, devConfig(), valkeytest.New())

	for name, tc := range map[string]struct{ method, path string }{
		"POST to rest config":       {http.MethodPost, "/config/rest"},
		"DELETE the rest config":    {http.MethodDelete, "/config/rest"},
		"POST to graphql config":    {http.MethodPost, "/config/graphql"},
		"POST to credential status": {http.MethodPost, "/database/credentials/status"},
		"GET the first view":        {http.MethodGet, "/database/credentials/first-view"},
		"GET a rotation":            {http.MethodGet, "/database/credentials/rotate"},
		"PATCH the cache":           {http.MethodPatch, "/cache"},
	} {
		rec := call(t, handler, tc.method, tc.path, "", "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", name, rec.Code)
		}
	}
}

// ─── Database credentials ─────────────────────────────────────────────────────

// kek returns a base64 32-byte key-encryption key.
func kek(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// Each mode describes the password differently, because in each it is owned by
// someone else: the platform, the operator, or the developer's own machine.
func TestCredentialStatusPerMode(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg      *config.Config
		wantMode string
		reveal   bool
	}{
		"managed":                    {&config.Config{Mode: "managed"}, "cloud", false},
		"self-host, readback off":    {&config.Config{Mode: "standalone", PostgresPassword: "pw"}, "self_host", false},
		"self-host, readback on":     {&config.Config{Mode: "standalone", PostgresPassword: "pw", AllowSecretReadback: true}, "self_host", true},
		"self-host with no password": {&config.Config{Mode: "standalone", AllowSecretReadback: true}, "self_host", false},
		"local":                      {&config.Config{Mode: "dev"}, "local", true},
	} {
		handler, _ := api(t, tc.cfg, valkeytest.New())
		rec := call(t, handler, http.MethodGet, "/database/credentials/status", serviceKey, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}

		var got statusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Mode != tc.wantMode {
			t.Errorf("%s: mode = %q, want %q", name, got.Mode, tc.wantMode)
		}
		if got.CanReveal != tc.reveal {
			t.Errorf("%s: can reveal = %v, want %v", name, got.CanReveal, tc.reveal)
		}
	}
}

// The whole point of the managed flow: the password is shown once and then it
// is gone.
func TestTheManagedPasswordIsShownOnce(t *testing.T) {
	cfg := &config.Config{Mode: "managed", DBCredentialsKEK: kek(t)}
	handler, _ := api(t, cfg, valkeytest.New())

	// Nothing has been generated yet.
	rec := call(t, handler, http.MethodPost, "/database/credentials/first-view", serviceKey, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("before rotation: status = %d, want 409", rec.Code)
	}

	rec = call(t, handler, http.MethodPost, "/database/credentials/rotate", serviceKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}

	rec = call(t, handler, http.MethodGet, "/database/credentials/status", serviceKey, "")
	var status statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.CanReveal || status.PasswordStatus != "available_once" || status.Generation != 2 {
		t.Fatalf("after rotation: %+v", status)
	}

	rec = call(t, handler, http.MethodPost, "/database/credentials/first-view", serviceKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("first view: %d %s", rec.Code, rec.Body.String())
	}
	var revealed struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &revealed); err != nil {
		t.Fatal(err)
	}
	if len(revealed.Password) != 32 {
		t.Errorf("password = %q, want 32 characters", revealed.Password)
	}

	// And not again.
	if rec := call(t, handler, http.MethodPost, "/database/credentials/first-view", serviceKey, ""); rec.Code != http.StatusConflict {
		t.Errorf("second view: status = %d, want 409", rec.Code)
	}
	rec = call(t, handler, http.MethodGet, "/database/credentials/status", serviceKey, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.CanReveal || status.PasswordStatus != "hidden" || status.FirstViewConsumed == "" {
		t.Errorf("after the view: %+v", status)
	}
}

// Rotating twice makes a new password, and the old generation's ciphertext is
// not what the new first-view returns.
func TestRotatingTwice(t *testing.T) {
	cfg := &config.Config{Mode: "managed", DBCredentialsKEK: kek(t)}
	handler, _ := api(t, cfg, valkeytest.New())

	reveal := func() string {
		t.Helper()
		if rec := call(t, handler, http.MethodPost, "/database/credentials/rotate", serviceKey, ""); rec.Code != http.StatusOK {
			t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
		}
		rec := call(t, handler, http.MethodPost, "/database/credentials/first-view", serviceKey, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("first view: %d %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Password
	}

	if first, second := reveal(), reveal(); first == second {
		t.Error("rotating produced the same password")
	}
}

// Rotation is the platform's job, so anywhere the platform does not own the
// password it is refused rather than half done.
func TestRotationIsManagedModeOnly(t *testing.T) {
	for _, mode := range []string{"dev", "standalone"} {
		handler, _ := api(t, &config.Config{Mode: mode, ServiceRoleKey: serviceKey}, valkeytest.New())
		rec := call(t, handler, http.MethodPost, "/database/credentials/rotate", serviceKey, "")
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", mode, rec.Code)
		}
	}
}

// Self-host reveals the operator's own secret only when the deployment has
// said it may.
func TestSelfHostFirstView(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  *config.Config
		want int
	}{
		"readback allowed":   {&config.Config{Mode: "standalone", AllowSecretReadback: true, PostgresPassword: "pw"}, http.StatusOK},
		"readback disabled":  {&config.Config{Mode: "standalone", PostgresPassword: "pw"}, http.StatusForbidden},
		"nothing configured": {&config.Config{Mode: "standalone", AllowSecretReadback: true}, http.StatusNotFound},
	} {
		handler, _ := api(t, tc.cfg, valkeytest.New())
		rec := call(t, handler, http.MethodPost, "/database/credentials/first-view", serviceKey, "")
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// On a developer's own machine the password is whatever the local stack uses,
// and the default is the one the local stack ships with.
func TestLocalFirstView(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  *config.Config
		want string
	}{
		"a configured password": {&config.Config{Mode: "dev", PostgresPassword: "mine"}, "mine"},
		"the local default":     {&config.Config{Mode: "dev"}, "postgres"},
	} {
		handler, _ := api(t, tc.cfg, valkeytest.New())
		rec := call(t, handler, http.MethodPost, "/database/credentials/first-view", serviceKey, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Password != tc.want {
			t.Errorf("%s: password = %q, want %q", name, body.Password, tc.want)
		}
	}
}

// A cache that will not answer is not a tenant whose password has never been
// set. Answering "pending" invites an operator to rotate a password that is
// already in use.
func TestACacheThatWillNotAnswerIsNotPending(t *testing.T) {
	cache := valkeytest.New()
	cache.GetErr = valkeytest.ErrFailed
	cfg := &config.Config{Mode: "managed", DBCredentialsKEK: kek(t)}
	handler, _ := api(t, cfg, cache)

	for name, tc := range map[string]struct{ method, path string }{
		"status":     {http.MethodGet, "/database/credentials/status"},
		"first view": {http.MethodPost, "/database/credentials/first-view"},
		"rotate":     {http.MethodPost, "/database/credentials/rotate"},
	} {
		rec := call(t, handler, tc.method, tc.path, serviceKey, "")
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// With no cache at all there is nowhere to keep a managed password, which is a
// misconfiguration rather than a tenant with none.
func TestManagedModeWithNoCache(t *testing.T) {
	handler, _ := api(t, &config.Config{Mode: "managed"}, valkey.Unavailable())

	rec := call(t, handler, http.MethodGet, "/database/credentials/status", serviceKey, "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// Metadata that will not parse is reported, not treated as a fresh tenant.
func TestCredentialMetadataThatWillNotParse(t *testing.T) {
	cache := valkeytest.New()
	cache.Put("tenant:default:dbcred:meta", []byte("{not json"), 0)
	handler, _ := api(t, &config.Config{Mode: "managed"}, cache)

	rec := call(t, handler, http.MethodGet, "/database/credentials/status", serviceKey, "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// A write that fails must not leave the caller thinking a rotation happened.
func TestARotationThatCannotBeStored(t *testing.T) {
	cache := valkeytest.New()
	cache.SetErr = valkeytest.ErrFailed
	handler, _ := api(t, &config.Config{Mode: "managed", DBCredentialsKEK: kek(t), ServiceRoleKey: serviceKey}, cache)

	rec := call(t, handler, http.MethodPost, "/database/credentials/rotate", serviceKey, "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// ─── Encryption ───────────────────────────────────────────────────────────────

func TestManagedSecretRoundTrip(t *testing.T) {
	key := kek(t)

	secret, err := encryptManagedSecret(key, "proj-1", 3, "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if secret.Algorithm != "aes-256-gcm" || secret.KeyVersion != 1 {
		t.Errorf("secret = %+v", secret)
	}
	if strings.Contains(secret.Ciphertext, "hunter2") {
		t.Error("the password is in the ciphertext")
	}

	got, err := decryptManagedSecret(key, "proj-1", 3, secret)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hunter2" {
		t.Errorf("password = %q", got)
	}
}

// The tenant and generation are bound into the ciphertext, so a secret cannot
// be moved between tenants or replayed from an earlier rotation.
func TestASecretIsBoundToItsTenantAndGeneration(t *testing.T) {
	key := kek(t)
	secret, err := encryptManagedSecret(key, "proj-1", 3, "hunter2")
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		ref        string
		generation int
	}{
		"another tenant":     {"proj-2", 3},
		"another generation": {"proj-1", 4},
	} {
		if _, err := decryptManagedSecret(key, tc.ref, tc.generation, secret); err == nil {
			t.Errorf("%s: the secret decrypted", name)
		}
	}

	if _, err := decryptManagedSecret(kek(t), "proj-1", 3, secret); err == nil {
		t.Error("another key decrypted the secret")
	}
}

// A key that is not a 32-byte base64 value is a misconfiguration, and the
// message names the variable to fix.
func TestAKeyThatCannotBeUsed(t *testing.T) {
	for name, bad := range map[string]string{
		"not base64": "!!!not base64!!!",
		"too short":  base64.StdEncoding.EncodeToString([]byte("short")),
		"too long":   base64.StdEncoding.EncodeToString(make([]byte, 64)),
		"nothing":    "",
	} {
		if _, err := encryptManagedSecret(bad, "proj-1", 1, "pw"); err == nil {
			t.Errorf("encrypt with %s: want an error", name)
		} else if !strings.Contains(err.Error(), "SUPATYPE_DB_CREDENTIALS_KEK") {
			t.Errorf("encrypt with %s: the message should name the variable: %v", name, err)
		}
		if _, err := decryptManagedSecret(bad, "proj-1", 1, encryptedSecret{}); err == nil {
			t.Errorf("decrypt with %s: want an error", name)
		}
	}
}

// A stored secret whose fields are not base64 cannot be decrypted, and saying
// so beats panicking on the decode.
func TestASecretThatWillNotDecode(t *testing.T) {
	key := kek(t)
	good, err := encryptManagedSecret(key, "proj-1", 1, "pw")
	if err != nil {
		t.Fatal(err)
	}

	for name, secret := range map[string]encryptedSecret{
		"a nonce that is not base64":      {Nonce: "!!!", Ciphertext: good.Ciphertext},
		"a ciphertext that is not base64": {Nonce: good.Nonce, Ciphertext: "!!!"},
		"a nonce of the wrong length":     {Nonce: base64.StdEncoding.EncodeToString([]byte("x")), Ciphertext: good.Ciphertext},
		"nothing at all":                  {},
	} {
		if _, err := decryptManagedSecret(key, "proj-1", 1, secret); err == nil {
			t.Errorf("%s decrypted", name)
		}
	}
}

// A stored secret that is not JSON is reported rather than read as an empty
// one, which would decrypt to nothing and be handed out as the password.
func TestAStoredSecretThatIsNotJSON(t *testing.T) {
	cache := valkeytest.New()
	cache.Put("tenant:default:dbcred:secret:v1", []byte("{not json"), 0)

	if _, err := loadManagedSecret(context.Background(), cache, kek(t), "default", 1); err == nil {
		t.Error("want an error")
	}
}

func TestRandomPassword(t *testing.T) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"

	first := randomPassword(32)
	if len(first) != 32 {
		t.Fatalf("length = %d", len(first))
	}
	for _, c := range first {
		if !strings.ContainsRune(alphabet, c) {
			t.Errorf("password contains %q, which is not in the alphabet", c)
		}
	}
	if second := randomPassword(32); first == second {
		t.Error("two passwords were the same")
	}
}

// The tenant header namespaces every key, so one tenant's rotation cannot touch
// another's password.
func TestTenantRef(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := tenantRef(req); got != "default" {
		t.Errorf("with no header: %q", got)
	}
	req.Header.Set("X-Supatype-Tenant", "proj-1")
	if got := tenantRef(req); got != "proj-1" {
		t.Errorf("with a header: %q", got)
	}

	if metaKey("proj-1") != "tenant:proj-1:dbcred:meta" {
		t.Errorf("meta key = %q", metaKey("proj-1"))
	}
	if secretKey("proj-1", 2) != "tenant:proj-1:dbcred:secret:v2" {
		t.Errorf("secret key = %q", secretKey("proj-1", 2))
	}
}

// Two tenants rotate independently.
func TestTwoTenantsDoNotShareAPassword(t *testing.T) {
	cfg := &config.Config{Mode: "managed", DBCredentialsKEK: kek(t)}
	handler, _ := api(t, cfg, valkeytest.New())

	reveal := func(tenant string) string {
		t.Helper()
		for _, step := range []string{"/database/credentials/rotate", "/database/credentials/first-view"} {
			req := httptest.NewRequest(http.MethodPost, step, nil)
			req.Header.Set("Authorization", "Bearer "+serviceKey)
			req.Header.Set("X-Supatype-Tenant", tenant)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s for %s: %d %s", step, tenant, rec.Code, rec.Body.String())
			}
			if strings.HasSuffix(step, "first-view") {
				var body struct {
					Password string `json:"password"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				return body.Password
			}
		}
		return ""
	}

	if reveal("proj-1") == reveal("proj-2") {
		t.Error("two tenants got the same password")
	}
}

// ─── The cache API ────────────────────────────────────────────────────────────

// storedEntry writes a cache entry the admin API will list.
func storedEntry(t *testing.T, cache *valkeytest.Client, key, table string) {
	t.Helper()
	raw, err := json.Marshal(restcache.Entry{
		StatusCode: http.StatusOK,
		Body:       []byte(`[{"id":1}]`),
		CachedAt:   time.Now().UTC(),
		Table:      table,
		Scope:      "user",
		Method:     http.MethodGet,
		Path:       "/" + table,
	})
	if err != nil {
		t.Fatal(err)
	}
	cache.Put(key, raw, 60)
}

func TestListingAndFlushingTheCache(t *testing.T) {
	cache := valkeytest.New()
	prefix := restcache.RestKeyPrefix("local")
	storedEntry(t, cache, prefix+"one", "posts")
	storedEntry(t, cache, prefix+"two", "comments")
	handler, _ := api(t, devConfig(), cache)

	rec := call(t, handler, http.MethodGet, "/cache", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var listing struct {
		Entries []cacheEntrySummary `json:"entries"`
		Cursor  string              `json:"cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 2 {
		t.Fatalf("entries = %+v", listing.Entries)
	}
	for _, entry := range listing.Entries {
		if entry.TTLSeconds <= 0 || entry.SizeBytes == 0 || entry.CachedAt == "" {
			t.Errorf("entry = %+v", entry)
		}
	}

	// Narrowed to one table.
	rec = call(t, handler, http.MethodGet, "/cache?table=posts", "", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Table != "posts" {
		t.Errorf("filtered = %+v", listing.Entries)
	}

	// Flushing one table leaves the other.
	if rec := call(t, handler, http.MethodDelete, "/cache?table=posts", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("flush: %d %s", rec.Code, rec.Body.String())
	}
	if keys := cache.Keys(); len(keys) != 1 || keys[0] != prefix+"two" {
		t.Errorf("after the flush: %v", keys)
	}

	// And flushing everything leaves nothing.
	if rec := call(t, handler, http.MethodDelete, "/cache", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("flush all: %d", rec.Code)
	}
	if keys := cache.Keys(); len(keys) != 0 {
		t.Errorf("after flushing everything: %v", keys)
	}
}

func TestGettingAndDeletingOneEntry(t *testing.T) {
	cache := valkeytest.New()
	prefix := restcache.RestKeyPrefix("local")
	key := prefix + "one"
	storedEntry(t, cache, key, "posts")
	handler, _ := api(t, devConfig(), cache)

	path := "/cache/entries/" + CacheKeyParam(key)
	rec := call(t, handler, http.MethodGet, path, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	var detail cacheEntryDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.StatusCode != http.StatusOK || detail.Table != "posts" {
		t.Errorf("detail = %+v", detail)
	}
	if detail.BodyPreview == "" || len(detail.BodyJSON) == 0 {
		t.Errorf("body was not shown: %+v", detail)
	}

	if rec := call(t, handler, http.MethodDelete, path, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if len(cache.Keys()) != 0 {
		t.Errorf("the entry survived: %v", cache.Keys())
	}
}

// A body that is not JSON is still previewable, and a long one is truncated
// rather than returned whole.
func TestTheBodyPreview(t *testing.T) {
	cache := valkeytest.New()
	key := restcache.RestKeyPrefix("local") + "big"
	raw, err := json.Marshal(restcache.Entry{
		StatusCode: http.StatusOK,
		Body:       []byte(strings.Repeat("x", cacheBodyPreviewMax*2)),
		CachedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	cache.Put(key, raw, 60)
	handler, _ := api(t, devConfig(), cache)

	rec := call(t, handler, http.MethodGet, "/cache/entries/"+CacheKeyParam(key), "", "")
	var detail cacheEntryDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.BodyPreview) != cacheBodyPreviewMax {
		t.Errorf("preview is %d bytes, want it capped at %d", len(detail.BodyPreview), cacheBodyPreviewMax)
	}
	if len(detail.BodyJSON) != 0 {
		t.Error("a body that is not JSON was offered as JSON")
	}
}

// A key outside the tenant's prefix is refused, or one tenant's admin API
// reads another's cached rows.
func TestAKeyOutsideTheTenantIsRefused(t *testing.T) {
	cache := valkeytest.New()
	other := restcache.RestKeyPrefix("someone-else") + "one"
	storedEntry(t, cache, other, "posts")
	handler, _ := api(t, devConfig(), cache)

	rec := call(t, handler, http.MethodGet, "/cache/entries/"+CacheKeyParam(other), "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := errorOf(t, rec); got != "key out of tenant scope" {
		t.Errorf("error = %q", got)
	}
}

func TestCacheEntryKeyRefusals(t *testing.T) {
	handler, _ := api(t, devConfig(), valkeytest.New())

	for name, tc := range map[string]struct {
		path string
		want string
	}{
		"no key":          {"/cache/entries/", "key required"},
		"only whitespace": {"/cache/entries/%20", "key required"},
		"not base64":      {"/cache/entries/!!!", "invalid key encoding"},
	} {
		rec := call(t, handler, http.MethodGet, tc.path, "", "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
		if got := errorOf(t, rec); got != tc.want {
			t.Errorf("%s: error = %q, want %q", name, got, tc.want)
		}
	}
}

func TestAnEntryThatIsNotThere(t *testing.T) {
	handler, _ := api(t, devConfig(), valkeytest.New())
	key := restcache.RestKeyPrefix("local") + "ghost"

	rec := call(t, handler, http.MethodGet, "/cache/entries/"+CacheKeyParam(key), "", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// An entry that will not decode is skipped in a listing — one bad entry must
// not hide the rest — but reported when asked for directly.
func TestACorruptEntry(t *testing.T) {
	cache := valkeytest.New()
	prefix := restcache.RestKeyPrefix("local")
	storedEntry(t, cache, prefix+"good", "posts")
	cache.Put(prefix+"bad", []byte("{not json"), 60)
	handler, _ := api(t, devConfig(), cache)

	rec := call(t, handler, http.MethodGet, "/cache", "", "")
	var listing struct {
		Entries []cacheEntrySummary `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 1 {
		t.Errorf("entries = %+v, want the corrupt one skipped", listing.Entries)
	}

	rec = call(t, handler, http.MethodGet, "/cache/entries/"+CacheKeyParam(prefix+"bad"), "", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("asked for directly: status = %d, want 500", rec.Code)
	}
}

// The listing is bounded and hands back a cursor, so a tenant with more entries
// than one page is paged rather than truncated silently.
func TestTheListingIsPagedByCursor(t *testing.T) {
	cache := valkeytest.New()
	prefix := restcache.RestKeyPrefix("local")
	for i := 0; i < 5; i++ {
		storedEntry(t, cache, prefix+string(rune('a'+i)), "posts")
	}
	handler, _ := api(t, devConfig(), cache)

	rec := call(t, handler, http.MethodGet, "/cache?limit=2", "", "")
	var listing struct {
		Entries []cacheEntrySummary `json:"entries"`
		Cursor  string              `json:"cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 2 {
		t.Errorf("entries = %d, want the limit honoured", len(listing.Entries))
	}
}

// A limit outside the bounds is a mistake, not a request to return everything
// or nothing.
func TestTheListingLimitIsBounded(t *testing.T) {
	cache := valkeytest.New()
	prefix := restcache.RestKeyPrefix("local")
	for i := 0; i < 3; i++ {
		storedEntry(t, cache, prefix+string(rune('a'+i)), "posts")
	}
	handler, _ := api(t, devConfig(), cache)

	for _, query := range []string{"?limit=0", "?limit=-1", "?limit=201", "?limit=nonsense", ""} {
		rec := call(t, handler, http.MethodGet, "/cache"+query, "", "")
		var listing struct {
			Entries []cacheEntrySummary `json:"entries"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
			t.Fatal(err)
		}
		if len(listing.Entries) != 3 {
			t.Errorf("%q: entries = %d, want the default limit", query, len(listing.Entries))
		}
	}
}

// A cache that will not answer is a bad gateway: the admin API is reporting on
// something it could not reach, not on an empty cache.
func TestTheCacheAPIReportsAFailingCache(t *testing.T) {
	prefix := restcache.RestKeyPrefix("local")

	scanFails := valkeytest.New()
	scanFails.ScanErr = valkeytest.ErrFailed

	getFails := valkeytest.New()
	storedEntry(t, getFails, prefix+"one", "posts")
	getFails.GetErr = valkeytest.ErrFailed

	ttlFails := valkeytest.New()
	storedEntry(t, ttlFails, prefix+"one", "posts")
	ttlFails.TTLErr = valkeytest.ErrFailed

	delFails := valkeytest.New()
	storedEntry(t, delFails, prefix+"one", "posts")
	delFails.DelErr = valkeytest.ErrFailed

	for name, tc := range map[string]struct {
		cache  *valkeytest.Client
		method string
		path   string
	}{
		"the scan fails on a list":   {scanFails, http.MethodGet, "/cache"},
		"the scan fails on a flush":  {scanFails, http.MethodDelete, "/cache"},
		"a read fails while listing": {getFails, http.MethodGet, "/cache"},
		"a ttl fails while listing":  {ttlFails, http.MethodGet, "/cache"},
		"a read fails on one entry":  {getFails, http.MethodGet, "/cache/entries/" + CacheKeyParam(prefix+"one")},
		"a delete fails":             {delFails, http.MethodDelete, "/cache/entries/" + CacheKeyParam(prefix+"one")},
		"a flush cannot delete":      {delFails, http.MethodDelete, "/cache"},
	} {
		handler, _ := api(t, devConfig(), tc.cache)
		rec := call(t, handler, tc.method, tc.path, "", "")
		if rec.Code != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// A read that fails while narrowing a flush to one table skips that key rather
// than deleting it: removing an entry without knowing which table it belongs to
// would flush more than was asked for.
func TestAFlushByTableSkipsWhatItCannotRead(t *testing.T) {
	cache := valkeytest.New()
	prefix := restcache.RestKeyPrefix("local")
	storedEntry(t, cache, prefix+"one", "posts")
	cache.Put(prefix+"unreadable", []byte("{not json"), 60)
	handler, _ := api(t, devConfig(), cache)

	if rec := call(t, handler, http.MethodDelete, "/cache?table=posts", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if keys := cache.Keys(); len(keys) != 1 || keys[0] != prefix+"unreadable" {
		t.Errorf("keys = %v, want the undecodable entry left alone", keys)
	}
}

// With no cache configured the routes exist and say so, rather than 404ing as
// though the admin API did not have them.
func TestTheCacheAPIWithNoCacheConfigured(t *testing.T) {
	handler, _ := api(t, devConfig(), valkey.Unavailable())

	for _, path := range []string{"/cache", "/cache/entries/abc"} {
		rec := call(t, handler, http.MethodGet, path, "", "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, rec.Code)
		}
		if got := errorOf(t, rec); got != "valkey not configured" {
			t.Errorf("%s: error = %q", path, got)
		}
	}
}

// In managed mode a tenant without the cache is told so, on every cache route
// and on the config fields that would enable it.
func TestATenantWithoutTheCache(t *testing.T) {
	disabled := false
	cache := valkeytest.New().WithTenant("proj-1", &valkey.TenantConfig{RestCacheEnabled: &disabled})
	cfg := &config.Config{Mode: "managed", ManagedProjectRef: "proj-1", ServiceRoleKey: serviceKey}
	handler, _ := api(t, cfg, cache)

	for name, tc := range map[string]struct{ method, path, body string }{
		"listing":            {http.MethodGet, "/cache", ""},
		"flushing":           {http.MethodDelete, "/cache", ""},
		"one entry":          {http.MethodGet, "/cache/entries/abc", ""},
		"configuring a ttl":  {http.MethodPatch, "/config/rest", `{"cache_max_ttl":60}`},
		"configuring tables": {http.MethodPatch, "/config/rest", `{"cache_tables":{}}`},
	} {
		rec := call(t, handler, tc.method, tc.path, serviceKey, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 (%s)", name, rec.Code, rec.Body.String())
		}
		if got := errorOf(t, rec); got != "rest_cache_not_available" {
			t.Errorf("%s: error = %q", name, got)
		}
	}

	// The fields that do not configure the cache still work.
	if rec := call(t, handler, http.MethodPatch, "/config/rest", serviceKey, `{"max_rows":10}`); rec.Code != http.StatusOK {
		t.Errorf("a patch that does not touch the cache was refused: %d %s", rec.Code, rec.Body.String())
	}
}

// The prefix is what scopes every cache operation to one tenant.
func TestTenantCachePrefix(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/cache", nil)

	if got := tenantCachePrefix(&config.Config{}, req); got != "tenant:local:rest:" {
		t.Errorf("with nothing configured: %q", got)
	}
	if got := tenantCachePrefix(&config.Config{ManagedProjectRef: "proj-1"}, req); got != "tenant:proj-1:rest:" {
		t.Errorf("with a configured project: %q", got)
	}
	req.Header.Set("X-Supatype-Tenant", "routed")
	if got := tenantCachePrefix(&config.Config{ManagedProjectRef: "proj-1"}, req); got != "tenant:routed:rest:" {
		t.Errorf("with a routed tenant: %q", got)
	}
}

// The key a caller sends back has to be the one the listing gave them.
func TestCacheKeyParamRoundTrips(t *testing.T) {
	cache := valkeytest.New()
	key := restcache.RestKeyPrefix("local") + "one"
	storedEntry(t, cache, key, "posts")
	handler, _ := api(t, devConfig(), cache)

	rec := call(t, handler, http.MethodGet, "/cache", "", "")
	var listing struct {
		Entries []cacheEntrySummary `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 1 {
		t.Fatalf("entries = %+v", listing.Entries)
	}

	rec = call(t, handler, http.MethodGet, "/cache/entries/"+CacheKeyParam(listing.Entries[0].Key), "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("the listed key could not be fetched: %d %s", rec.Code, rec.Body.String())
	}
}

// An entry route takes only a read and a delete.
func TestAnUnsupportedMethodOnOneEntry(t *testing.T) {
	handler, _ := api(t, devConfig(), valkeytest.New())
	key := restcache.RestKeyPrefix("local") + "one"

	rec := call(t, handler, http.MethodPatch, "/cache/entries/"+CacheKeyParam(key), "", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

// An entry that expires between the scan and the read is skipped rather than
// failing the listing, and left alone by a flush narrowed to one table: it is
// already gone, and nothing can say which table it was.
func TestAnEntryThatVanishesMidListing(t *testing.T) {
	cache := valkeytest.New()
	prefix := restcache.RestKeyPrefix("local")
	storedEntry(t, cache, prefix+"live", "posts")
	// The scan sees the key and the read does not, which is what expiring
	// between the two looks like.
	cache.PutVanishing(prefix + "gone")
	handler, _ := api(t, devConfig(), cache)

	rec := call(t, handler, http.MethodGet, "/cache", "", "")
	var listing struct {
		Entries []cacheEntrySummary `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Key != prefix+"live" {
		t.Errorf("entries = %+v", listing.Entries)
	}

	if rec := call(t, handler, http.MethodDelete, "/cache?table=posts", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("flush: %d", rec.Code)
	}
	if keys := cache.Keys(); len(keys) != 1 || keys[0] != prefix+"gone" {
		t.Errorf("keys = %v, want the expired one left and the live one flushed", keys)
	}
}

// A flush walks every page, or a tenant with more entries than one scan returns
// keeps some of them.
func TestAFlushWalksEveryPage(t *testing.T) {
	cache := valkeytest.New()
	cache.ScanPageSize = 2
	prefix := restcache.RestKeyPrefix("local")
	for i := 0; i < 7; i++ {
		storedEntry(t, cache, prefix+string(rune('a'+i)), "posts")
	}
	handler, _ := api(t, devConfig(), cache)

	if rec := call(t, handler, http.MethodDelete, "/cache", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if keys := cache.Keys(); len(keys) != 0 {
		t.Errorf("keys = %v, want everything flushed across pages", keys)
	}
}

// And a listing pages too, handing back a cursor that continues where it left
// off.
func TestAListingPagesWithACursor(t *testing.T) {
	cache := valkeytest.New()
	cache.ScanPageSize = 2
	prefix := restcache.RestKeyPrefix("local")
	for i := 0; i < 5; i++ {
		storedEntry(t, cache, prefix+string(rune('a'+i)), "posts")
	}
	handler, _ := api(t, devConfig(), cache)

	seen := map[string]bool{}
	cursor := "0"
	for page := 0; page < 10; page++ {
		rec := call(t, handler, http.MethodGet, "/cache?limit=2&cursor="+cursor, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: %d %s", page, rec.Code, rec.Body.String())
		}
		var listing struct {
			Entries []cacheEntrySummary `json:"entries"`
			Cursor  string              `json:"cursor"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
			t.Fatal(err)
		}
		for _, entry := range listing.Entries {
			seen[entry.Key] = true
		}
		cursor = listing.Cursor
		if cursor == "0" {
			break
		}
	}
	if len(seen) != 5 {
		t.Errorf("saw %d of 5 entries across pages", len(seen))
	}
}

// Metadata a previous version wrote without these fields still reads, rather
// than presenting a tenant with no status and generation zero.
func TestCredentialMetadataIsNormalised(t *testing.T) {
	cache := valkeytest.New()
	cache.Put("tenant:default:dbcred:meta", []byte(`{"status":"","generation":0}`), 0)
	handler, _ := api(t, &config.Config{Mode: "managed"}, cache)

	rec := call(t, handler, http.MethodGet, "/database/credentials/status", serviceKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var got statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.PasswordStatus != "pending" || got.Generation != 1 {
		t.Errorf("status = %+v, want it normalised", got)
	}
}

// A first view that reveals the password and then cannot record that it did
// must report, or the next caller is shown it again.
func TestAFirstViewThatCannotRecordItself(t *testing.T) {
	cache := valkeytest.New()
	handler, _ := api(t, &config.Config{Mode: "managed", DBCredentialsKEK: kek(t)}, cache)

	if rec := call(t, handler, http.MethodPost, "/database/credentials/rotate", serviceKey, ""); rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}

	// The next write is the one that records the view as consumed.
	cache.SetErr = valkeytest.ErrFailed
	rec := call(t, handler, http.MethodPost, "/database/credentials/first-view", serviceKey, "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Error("the password was returned despite the failure to record the view")
	}
}

// A rotation that stores the new secret and then cannot record it must report:
// the caller would otherwise believe the old password still works.
func TestARotationThatCannotRecordItself(t *testing.T) {
	cache := valkeytest.New()
	// The secret write succeeds; the metadata write after it does not.
	cache.SetErrAfter = 1
	handler, _ := api(t, &config.Config{Mode: "managed", DBCredentialsKEK: kek(t)}, cache)

	rec := call(t, handler, http.MethodPost, "/database/credentials/rotate", serviceKey, "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// The metadata says the password is there and the ciphertext is not, which is
// a store someone has edited. Reported rather than answered with nothing.
func TestAFirstViewWhoseSecretIsGone(t *testing.T) {
	cache := valkeytest.New()
	handler, _ := api(t, &config.Config{Mode: "managed", DBCredentialsKEK: kek(t)}, cache)

	if rec := call(t, handler, http.MethodPost, "/database/credentials/rotate", serviceKey, ""); rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}
	if err := cache.Del(context.Background(), secretKey("default", 2)); err != nil {
		t.Fatal(err)
	}

	rec := call(t, handler, http.MethodPost, "/database/credentials/first-view", serviceKey, "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// A read that fails is not a secret that is not there, and the difference is
// what stops an operator rotating over a password still in use.
func TestASecretThatCannotBeRead(t *testing.T) {
	cache := valkeytest.New()
	cache.GetErr = valkeytest.ErrFailed

	if _, err := loadManagedSecret(context.Background(), cache, kek(t), "default", 1); err == nil {
		t.Error("want an error")
	} else if !strings.Contains(err.Error(), "read managed password") {
		t.Errorf("the error should distinguish a failed read: %v", err)
	}
}

// A key the deployment configured badly stops a rotation rather than storing
// something nothing can decrypt.
func TestARotationWithAnUnusableKey(t *testing.T) {
	handler, _ := api(t, &config.Config{Mode: "managed", DBCredentialsKEK: "not-a-key"}, valkeytest.New())

	rec := call(t, handler, http.MethodPost, "/database/credentials/rotate", serviceKey, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := errorOf(t, rec); !strings.Contains(got, "SUPATYPE_DB_CREDENTIALS_KEK") {
		t.Errorf("the error should name the variable to fix: %q", got)
	}
}

// The branches are unreachable with real records, so the seam proves they
// report rather than writing a truncated one.
func TestARecordThatCannotBeEncoded(t *testing.T) {
	original := marshalJSON
	t.Cleanup(func() { marshalJSON = original })
	marshalJSON = func(any) ([]byte, error) { return nil, valkeytest.ErrFailed }

	ctx := context.Background()
	if err := saveMeta(ctx, valkeytest.New(), "default", dbCredMeta{}); err == nil {
		t.Error("saveMeta: want an error")
	}
	if err := saveManagedSecret(ctx, valkeytest.New(), kek(t), "default", 1, "pw"); err == nil {
		t.Error("saveManagedSecret: want an error")
	}
}
