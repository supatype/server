package modes

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/proxy"
	"golang.org/x/crypto/acme/autocert"
)

// The gaps the mode middlewares had left: what happens when a caller supplies a
// token this service did not issue, when an origin is offered that nobody
// allowed, and when the tenant signature is present but wrong.

// downstreamHandler answers with a body, so a test can tell "the request was
// passed on" from "the middleware answered it".
func downstreamHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("downstream"))
	})
}

// ─── API keys ─────────────────────────────────────────────────────────────────

// signed builds a key this project would accept, with the given claims.
func signed(t *testing.T, secret string, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)

	var key any = []byte(secret)
	signedString, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signedString
}

// A key is proof that this project issued it. Anything that is not, including
// one signed with a different algorithm, proves nothing.
func TestAPIKeyRoleFromToken(t *testing.T) {
	const secret = "project-secret"

	valid := signed(t, secret, jwt.SigningMethodHS256, jwt.MapClaims{"role": "anon"})
	wrongSecret := signed(t, "someone-elses-secret", jwt.SigningMethodHS256, jwt.MapClaims{"role": "anon"})
	noRole := signed(t, secret, jwt.SigningMethodHS256, jwt.MapClaims{"sub": "x"})
	numericRole := signed(t, secret, jwt.SigningMethodHS256, jwt.MapClaims{"role": 7})
	unknownRole := signed(t, secret, jwt.SigningMethodHS256, jwt.MapClaims{"role": "root"})

	// HS512 verifies against the same key bytes, so only the explicit method
	// check refuses it. Without that check an attacker could pick the algorithm.
	wrongAlgorithm := signed(t, secret, jwt.SigningMethodHS512, jwt.MapClaims{"role": "anon"})

	for name, tc := range map[string]struct {
		token, secret, want string
	}{
		"a key this project issued":       {valid, secret, "anon"},
		"no token":                        {"", secret, ""},
		"no secret configured":            {valid, "", ""},
		"a secret of only spaces":         {valid, "   ", ""},
		"not a JWT at all":                {"not-a-jwt", secret, ""},
		"signed by someone else":          {wrongSecret, secret, ""},
		"signed with another algorithm":   {wrongAlgorithm, secret, ""},
		"no role claim":                   {noRole, secret, ""},
		"a role claim that is not a name": {numericRole, secret, ""},
		"a role PostgREST does not know":  {unknownRole, secret, ""},
	} {
		if got := APIKeyRoleFromToken(tc.token, tc.secret); got != tc.want {
			t.Errorf("%s: role = %q, want %q", name, got, tc.want)
		}
	}
}

// Every PostgREST role a project issues keys for has to be accepted, or the
// data plane refuses its own keys.
func TestAPIKeyRoleFromTokenAcceptsEveryProjectRole(t *testing.T) {
	const secret = "project-secret"
	for _, role := range []string{"anon", "authenticated", "service_role"} {
		token := signed(t, secret, jwt.SigningMethodHS256, jwt.MapClaims{"role": role})
		if got := APIKeyRoleFromToken(token, secret); got != role {
			t.Errorf("role %q came back as %q", role, got)
		}
	}
}

// ─── CORS ─────────────────────────────────────────────────────────────────────

func TestUnionCORSOrigins(t *testing.T) {
	for name, tc := range map[string]struct {
		env, manifest, want []string
	}{
		"nothing anywhere":            {nil, nil, []string{}},
		"env only":                    {[]string{"a"}, nil, []string{"a"}},
		"manifest only":               {nil, []string{"b"}, []string{"b"}},
		"env first, then extra":       {[]string{"a"}, []string{"b"}, []string{"a", "b"}},
		"the same origin twice":       {[]string{"a"}, []string{"a"}, []string{"a"}},
		"a duplicate within one list": {[]string{"a", "a", "b"}, nil, []string{"a", "b"}},
		"padding is trimmed":          {[]string{"  a  "}, []string{"b  "}, []string{"a", "b"}},
		"empty entries are dropped":   {[]string{"", "   "}, []string{"b"}, []string{"b"}},
	} {
		got := unionCORSOrigins(tc.env, tc.manifest)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// A request whose Origin nobody allowed is served, and served without CORS
// headers. That is what makes the browser refuse it while a server-to-server
// caller, which sends no Origin, is unaffected.
func TestAnOriginNobodyAllowedGetsNoHeaders(t *testing.T) {
	for name, tc := range map[string]struct {
		allowed []string
		origin  string
	}{
		"the allowlist is empty":   {nil, "https://app.example"},
		"the origin is not on it":  {[]string{"https://other.example"}, "https://app.example"},
		"no origin was sent":       {[]string{"https://app.example"}, ""},
		"the origin is whitespace": {[]string{"https://app.example"}, "   "},
	} {
		req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		rec := httptest.NewRecorder()
		AllowlistCORSMiddleware(func(*http.Request) []string { return tc.allowed }, downstreamHandler()).
			ServeHTTP(rec, req)

		if rec.Code != http.StatusOK || rec.Body.String() != "downstream" {
			t.Errorf("%s: the request should still be served: %d %q", name, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s: allow-origin = %q, want none", name, got)
		}
	}
}

// An OPTIONS that is not a preflight is an ordinary request. Answering 204 with
// no body would swallow a genuine OPTIONS the upstream wanted to handle.
func TestOptionsWithoutAPreflightHeaderIsPassedOn(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/rest/v1/posts", nil)
	req.Header.Set("Origin", "https://app.example")

	rec := httptest.NewRecorder()
	AllowlistCORSMiddleware(
		func(*http.Request) []string { return []string{"https://app.example"} },
		downstreamHandler(),
	).ServeHTTP(rec, req)

	if rec.Body.String() != "downstream" {
		t.Errorf("body = %q, want the upstream's answer", rec.Body.String())
	}
}

// The preflight answers with exactly the headers the browser asked for when it
// names them, and the standing list when it does not.
func TestPreflightEchoesTheRequestedHeaders(t *testing.T) {
	for name, tc := range map[string]struct {
		requested, want string
	}{
		"the browser named its headers": {"X-Custom, Authorization", "X-Custom, Authorization"},
		"the browser named none":        {"", defaultCORSHeaders},
	} {
		req := httptest.NewRequest(http.MethodOptions, "/rest/v1/posts", nil)
		req.Header.Set("Origin", "https://app.example")
		req.Header.Set("Access-Control-Request-Method", "POST")
		if tc.requested != "" {
			req.Header.Set("Access-Control-Request-Headers", tc.requested)
		}

		rec := httptest.NewRecorder()
		AllowlistCORSMiddleware(
			func(*http.Request) []string { return []string{"https://app.example"} },
			downstreamHandler(),
		).ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("%s: status = %d, want 204", name, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Headers"); got != tc.want {
			t.Errorf("%s: allow-headers = %q, want %q", name, got, tc.want)
		}
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got != defaultCORSMethods {
			t.Errorf("%s: allow-methods = %q", name, got)
		}
		if got := rec.Header().Get("Access-Control-Max-Age"); got != "600" {
			t.Errorf("%s: max-age = %q", name, got)
		}
	}
}

// A handler that writes a body without setting a status still gets the header:
// Write goes through the same gate as WriteHeader, because net/http will send
// an implicit 200 and it is too late afterwards.
func TestTheHeaderIsAddedWhateverTheHandlerCallsFirst(t *testing.T) {
	for name, handler := range map[string]http.Handler{
		"the handler sets a status": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("body"))
		}),
		"the handler only writes": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("body"))
		}),
		"the handler writes twice": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("one"))
			_, _ = w.Write([]byte("two"))
		}),
	} {
		req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
		req.Header.Set("Origin", "https://app.example")

		rec := httptest.NewRecorder()
		AllowlistCORSMiddleware(
			func(*http.Request) []string { return []string{"https://app.example"} },
			handler,
		).ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
			t.Errorf("%s: allow-origin = %q", name, got)
		}
		// Added once, however many times the handler wrote.
		if got := rec.Header().Values("Vary"); len(got) != 1 || got[0] != "Origin" {
			t.Errorf("%s: Vary = %v, want exactly one Origin", name, got)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: the body was swallowed", name)
		}
	}
}

// The manifest's origins are per request, so two tenants on one process do not
// see each other's allowlist.
func TestManagedCORSTakesTheManifestPerRequest(t *testing.T) {
	manifestFor := func(r *http.Request) *proxy.RouteManifest {
		switch r.Header.Get("X-Supatype-Tenant") {
		case "one":
			return &proxy.RouteManifest{CorsAllowedOrigins: []string{"https://one.example"}}
		case "none":
			return nil
		default:
			return &proxy.RouteManifest{}
		}
	}

	for name, tc := range map[string]struct {
		tenant, origin string
		wantAllowed    bool
	}{
		"the tenant's own origin":     {"one", "https://one.example", true},
		"another tenant's origin":     {"two", "https://one.example", false},
		"the environment's origin":    {"two", "https://env.example", true},
		"a tenant with no manifest":   {"none", "https://env.example", true},
		"an origin nobody configured": {"one", "https://nowhere.example", false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("X-Supatype-Tenant", tc.tenant)

		rec := httptest.NewRecorder()
		ManagedCORSMiddleware("https://env.example", manifestFor, downstreamHandler()).ServeHTTP(rec, req)

		allowed := rec.Header().Get("Access-Control-Allow-Origin") == tc.origin
		if allowed != tc.wantAllowed {
			t.Errorf("%s: allowed = %v, want %v", name, allowed, tc.wantAllowed)
		}
	}
}

// With no manifest source at all the environment's list still applies, which is
// the standalone case.
func TestManagedCORSWithoutAManifestSource(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	req.Header.Set("Origin", "https://env.example")

	rec := httptest.NewRecorder()
	ManagedCORSMiddleware("https://env.example", nil, downstreamHandler()).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://env.example" {
		t.Errorf("allow-origin = %q", got)
	}
}

// ─── Tenant signature ─────────────────────────────────────────────────────────

// The signature is what proves Kong routed the request; a tenant id on its own
// is a header anyone can set.
func TestTenantMiddlewareRefusesAnythingButAValidSignature(t *testing.T) {
	const secret = "shared-secret"
	valid := computeHMAC("tenant-1", secret)

	for name, tc := range map[string]struct {
		tenant, sig string
		want        int
	}{
		"a valid signature":             {"tenant-1", valid, http.StatusOK},
		"the signature in upper case":   {"tenant-1", strings.ToUpper(valid), http.StatusOK},
		"a signature for another id":    {"tenant-1", computeHMAC("tenant-2", secret), http.StatusUnauthorized},
		"a signature under another key": {"tenant-1", computeHMAC("tenant-1", "other"), http.StatusUnauthorized},
		"nonsense":                      {"tenant-1", "deadbeef", http.StatusUnauthorized},
		"no signature":                  {"tenant-1", "", http.StatusUnauthorized},
		"no tenant":                     {"", valid, http.StatusUnauthorized},
		"neither":                       {"", "", http.StatusUnauthorized},
	} {
		req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
		if tc.tenant != "" {
			req.Header.Set("X-Supatype-Tenant", tc.tenant)
		}
		if tc.sig != "" {
			req.Header.Set("X-Supatype-Tenant-Sig", tc.sig)
		}

		rec := httptest.NewRecorder()
		TenantMiddleware(secret, downstreamHandler()).ServeHTTP(rec, req)

		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, tc.want)
		}
	}
}

// The signature is over the tenant id under the shared key, and every input
// has to change it or the verification is not verifying.
func TestComputeHMAC(t *testing.T) {
	base := computeHMAC("tenant-1", "secret")

	if base == "" || base != strings.ToLower(base) {
		t.Errorf("signature = %q, want lowercase hex", base)
	}
	if len(base) != 64 {
		t.Errorf("length = %d, want 64 hex characters of SHA-256", len(base))
	}
	if computeHMAC("tenant-2", "secret") == base {
		t.Error("the tenant id does not affect the signature")
	}
	if computeHMAC("tenant-1", "other") == base {
		t.Error("the key does not affect the signature")
	}
	if computeHMAC("tenant-1", "secret") != base {
		t.Error("the same inputs produced a different signature")
	}
}

// ─── ACME ─────────────────────────────────────────────────────────────────────

func TestNewACMEManagerExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}

	manager, err := NewACMEManager("example.com", "~/certs")
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil {
		t.Fatal("no manager")
	}

	expanded := filepath.Join(home, "certs")
	if _, err := os.Stat(expanded); err != nil {
		t.Errorf("the cache directory was not created at the expanded path: %v", err)
	}
	if got := autocert.DirCache(expanded); manager.Cache != got {
		t.Errorf("the cache points at %v, want %v", manager.Cache, got)
	}
}

// A cache directory that cannot be created is a reason to refuse, not to run
// without one and fail at the first renewal.
func TestNewACMEManagerReportsAnUnusableCacheDir(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewACMEManager("example.com", filepath.Join(blocker, "certs")); err == nil {
		t.Error("want an error when the cache path is inside a file")
	}
}

// A path that merely starts with a tilde is not a home reference.
func TestNewACMEManagerOnlyExpandsALeadingTildeSlash(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"~weird", "~"} {
		path := filepath.Join(dir, name)
		if _, err := NewACMEManager("example.com", path); err != nil {
			t.Errorf("%q: %v", name, err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%q: the directory was not created literally: %v", name, err)
		}
	}
}

func TestStandaloneTLSConfigFloorsTheVersion(t *testing.T) {
	manager, err := NewACMEManager("example.com", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := StandaloneTLSConfig(manager)

	if cfg.MinVersion < tls.VersionTLS12 {
		t.Errorf("min version = %x, want at least TLS 1.2", cfg.MinVersion)
	}
	if cfg.GetCertificate == nil {
		t.Error("the manager's certificate callback was lost")
	}
}

// A tilde path with no home to expand it against is a configuration this cannot
// honour, and guessing a directory for someone's TLS certificates is worse than
// saying so.
func TestNewACMEManagerReportsAnUnresolvableHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	// os.UserHomeDir consults these on Windows before USERPROFILE is decisive.
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	if _, err := NewACMEManager("example.com", "~/certs"); err == nil {
		t.Error("want an error when there is no home directory to expand against")
	}
}
