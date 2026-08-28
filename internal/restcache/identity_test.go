package restcache

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/valkey"
	"github.com/supatype/server/internal/data/valkey/valkeytest"
)

// The cache key's auth component decides who shares an entry with whom, so the
// cases that matter are the ones where a token is not what it claims: expired,
// unsigned, signed by someone else, or not a token at all.

const jwtSecret = "cache-secret"

// token builds a JWT signed with secret. An empty secret leaves the signature
// wrong, which is how an unverifiable token is arranged.
func token(t *testing.T, secret string, claims jwtClaims) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	body := header + "." + base64.RawURLEncoding.EncodeToString(payload)

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func bearer(value string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/posts", nil)
	req.Header.Set("Authorization", "Bearer "+value)
	return req
}

// A verified token identifies its holder by role and subject, which is what
// keeps a refreshed token reading its own cache entry rather than starting a
// new one.
func TestIdentityForCacheFromAVerifiedToken(t *testing.T) {
	secret := []byte(jwtSecret)

	for name, tc := range map[string]struct {
		claims jwtClaims
		want   string
	}{
		"a role and a subject": {jwtClaims{Role: "authenticated", Sub: "user-1"}, "authenticated:user-1"},
		"a service role":       {jwtClaims{Role: "service_role", Sub: "user-1"}, "service_role:user-1"},
		"no role named":        {jwtClaims{Sub: "user-1"}, "authenticated:user-1"},
		"a role of spaces":     {jwtClaims{Role: "  ", Sub: "user-1"}, "authenticated:user-1"},
		"not yet expired":      {jwtClaims{Role: "authenticated", Sub: "user-1", Exp: time.Now().Add(time.Hour).Unix()}, "authenticated:user-1"},
	} {
		if got := IdentityForCache(bearer(token(t, jwtSecret, tc.claims)), secret, false); got != tc.want {
			t.Errorf("%s: identity = %q, want %q", name, got, tc.want)
		}
	}
}

// A token that cannot be trusted still has to produce a stable identity, or two
// unrelated callers would share an entry. Hashing the token gives each one its
// own scope without believing anything it says.
func TestAnUntrustedTokenGetsItsOwnScope(t *testing.T) {
	secret := []byte(jwtSecret)
	mine := token(t, jwtSecret, jwtClaims{Role: "authenticated", Sub: "user-1"})

	for name, value := range map[string]string{
		"signed by someone else": token(t, "another-secret", jwtClaims{Role: "authenticated", Sub: "user-1"}),
		"expired":                token(t, jwtSecret, jwtClaims{Role: "authenticated", Sub: "user-1", Exp: time.Now().Add(-time.Hour).Unix()}),
		"not three segments":     "a.b",
		"a payload that is not base64": func() string {
			parts := token(t, jwtSecret, jwtClaims{Sub: "x"})
			return parts[:len(parts)/3] + ".!!!!." + parts[len(parts)-10:]
		}(),
		"not a token at all": "opaque-api-key",
	} {
		got := IdentityForCache(bearer(value), secret, false)
		if got == "" {
			t.Errorf("%s: no identity at all", name)
		}
		if got == "authenticated:user-1" {
			t.Errorf("%s: an untrusted token was believed", name)
		}
		// Stable, so the same caller keeps hitting its own entry.
		if again := IdentityForCache(bearer(value), secret, false); again != got {
			t.Errorf("%s: identity is not stable: %q then %q", name, got, again)
		}
		if got == IdentityForCache(bearer(mine), secret, false) {
			t.Errorf("%s: shares an entry with a verified caller", name)
		}
	}
}

// A signature that is not valid base64 cannot be compared, so the token is not
// verified.
func TestASignatureThatIsNotBase64(t *testing.T) {
	if claims := parseVerifiedJWT("header.payload.!!!", []byte(jwtSecret)); claims != nil {
		t.Error("a token whose signature cannot be decoded was accepted")
	}
}

// With no secret configured there is nothing to verify against, so the claims
// are read unverified. That is a weaker identity, not a wrong one: the cache
// key is not an authorization decision.
func TestWithNoSecretTheClaimsAreReadUnverified(t *testing.T) {
	value := token(t, "any-secret", jwtClaims{Role: "authenticated", Sub: "user-1"})
	if got := IdentityForCache(bearer(value), nil, false); got != "authenticated:user-1" {
		t.Errorf("identity = %q", got)
	}
}

// A payload that is not JSON, or claims with nothing to identify, cannot name
// a caller.
func TestParseJWTUnsafeRefusesWhatItCannotRead(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))

	for name, value := range map[string]string{
		"not three segments": "a.b",
		"payload not base64": header + ".!!!.sig",
		"payload not JSON":   header + "." + base64.RawURLEncoding.EncodeToString([]byte("{")) + ".sig",
	} {
		if claims := parseJWTUnsafe(value); claims != nil {
			t.Errorf("%s: read claims out of it: %+v", name, claims)
		}
	}
}

// A token carrying no subject cannot identify anyone, so it falls through to
// the hash rather than putting every such caller in one scope.
func TestATokenWithNoSubjectFallsBackToTheHash(t *testing.T) {
	secret := []byte(jwtSecret)
	first := IdentityForCache(bearer(token(t, jwtSecret, jwtClaims{Role: "authenticated"})), secret, false)
	second := IdentityForCache(bearer(token(t, jwtSecret, jwtClaims{Role: "service_role"})), secret, false)

	if first == "authenticated:" {
		t.Errorf("identity = %q, want a hash rather than an empty subject", first)
	}
	if first == second {
		t.Error("two subjectless tokens were put in the same scope")
	}
}

// A request with no credential at all is anon, and every anonymous caller
// shares that scope, which is correct: they are indistinguishable.
func TestAnUnauthenticatedRequestIsAnon(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/posts", nil)
	if got := IdentityForCache(req, []byte(jwtSecret), false); got != "anon" {
		t.Errorf("identity = %q", got)
	}
}

// The public scope is one entry for everybody, whatever credential was sent.
func TestPublicScopeIgnoresTheCredential(t *testing.T) {
	value := token(t, jwtSecret, jwtClaims{Role: "authenticated", Sub: "user-1"})
	if got := IdentityForCache(bearer(value), []byte(jwtSecret), true); got != "global" {
		t.Errorf("identity = %q, want global", got)
	}
}

// PostgREST accepts the key in either place, so the cache has to look in both
// or two forms of the same request would not share an entry.
func TestBearerOrAPIKey(t *testing.T) {
	for name, tc := range map[string]struct {
		auth, apikey, want string
	}{
		"a bearer token":        {"Bearer abc", "", "abc"},
		"an apikey header":      {"", "abc", "abc"},
		"a bearer wins":         {"Bearer abc", "def", "abc"},
		"a padded bearer":       {"Bearer   abc  ", "", "abc"},
		"not a bearer scheme":   {"Basic abc", "", ""},
		"a bearer with nothing": {"Bearer ", "", ""},
		"neither":               {"", "", ""},
	} {
		req := httptest.NewRequest(http.MethodGet, "/posts", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		if tc.apikey != "" {
			req.Header.Set("apikey", tc.apikey)
		}
		if got := bearerOrAPIKey(req); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// ─── Directives and paths ─────────────────────────────────────────────────────

func TestParseClientPublicIgnoresWhatItDoesNotUnderstand(t *testing.T) {
	for name, tc := range map[string]struct {
		header string
		want   bool
	}{
		"public alone":         {"public", true},
		"alongside a max-age":  {"max-age=60, public", true},
		"oddly cased":          {"max-age=60, PUBLIC", true},
		"padded":               {"max-age=60,   public  ", true},
		"no directive at all":  {"", false},
		"only whitespace":      {"   ", false},
		"a max-age only":       {"max-age=60", false},
		"a word containing it": {"publicly", false},
	} {
		header := http.Header{}
		if tc.header != "" {
			header.Set("X-Supatype-Cache", tc.header)
		}
		if got := ParseClientPublic(header); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// Only a bare table path names a table. An RPC returns whatever its function
// returns, and its result set is not this table's.
func TestRestTableFromPathEdges(t *testing.T) {
	for name, tc := range map[string]struct{ path, want string }{
		"a table":             {"/posts", "posts"},
		"a row under a table": {"/posts/1", "posts"},
		"no leading slash":    {"posts", "posts"},
		"the root":            {"/", ""},
		"an empty path":       {"", ""},
		"an RPC call":         {"/rpc/do_thing", ""},
		"a segment of spaces": {"/   /x", ""},
		"trailing slash":      {"/posts/", "posts"},
	} {
		if got := RestTableFromPath(tc.path); got != tc.want {
			t.Errorf("%s: RestTableFromPath(%q) = %q, want %q", name, tc.path, got, tc.want)
		}
	}
}

func TestEffectiveTTLEdges(t *testing.T) {
	for name, tc := range map[string]struct {
		serverCap, clientMaxAge int
		allowed                 bool
		want                    int
	}{
		"the client's ask, under the cap":    {60, 30, true, 30},
		"the cap, when the client asks more": {60, 300, true, 60},
		"exactly the cap":                    {60, 60, true, 60},
		"the table is not allowed":           {60, 30, false, 0},
		"the server allows nothing":          {0, 30, true, 0},
		"a negative cap":                     {-1, 30, true, 0},
		"the client asked for nothing":       {60, 0, true, 0},
		"the client asked for a negative":    {60, -5, true, 0},
	} {
		if got := EffectiveTTL(tc.serverCap, tc.clientMaxAge, tc.allowed); got != tc.want {
			t.Errorf("%s: got %d, want %d", name, got, tc.want)
		}
	}
}

// The tenant namespaces the key, so getting this wrong would serve one
// project's rows to another.
func TestTenantRef(t *testing.T) {
	for name, tc := range map[string]struct {
		header, configured, want string
	}{
		"the routed tenant":      {"tenant-1", "proj-1", "tenant-1"},
		"the configured project": {"", "proj-1", "proj-1"},
		"neither":                {"", "", "local"},
		"a header of spaces":     {"   ", "proj-1", "proj-1"},
		"a configured of spaces": {"", "  ", "local"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/posts", nil)
		if tc.header != "" {
			req.Header.Set("X-Supatype-Tenant", tc.header)
		}
		if got := TenantRef(req, tc.configured); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// The prefix is what the admin API scans to list or purge a tenant's entries,
// so it has to match what BuildKey produces.
func TestRestKeyPrefixMatchesTheKeysBuilt(t *testing.T) {
	prefix := RestKeyPrefix("tenant-1")
	if prefix != "tenant:tenant-1:rest:" {
		t.Errorf("prefix = %q", prefix)
	}

	key := BuildKey(keyParts{Tenant: "tenant-1", Path: "/posts"})
	if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
		t.Errorf("key %q does not start with the prefix %q the admin API scans for", key, prefix)
	}
}

// ─── Eligibility ──────────────────────────────────────────────────────────────

// Outside managed mode the cache is always on offer; inside it, the tenant's
// grant decides, and anything that cannot be read is a refusal.
func TestServerCacheOffered(t *testing.T) {
	enabled, disabled := true, false
	withGrant := valkeytest.New().WithTenant("proj-1", &valkey.TenantConfig{RestCacheEnabled: &enabled})
	withoutGrant := valkeytest.New().WithTenant("proj-1", &valkey.TenantConfig{RestCacheEnabled: &disabled})
	unreadable := valkeytest.New()
	unreadable.TenantErr = valkeytest.ErrFailed

	for name, tc := range map[string]struct {
		cfg   *config.Config
		cache valkey.Client
		want  bool
	}{
		"no config at all":              {nil, nil, true},
		"dev":                           {&config.Config{Mode: "dev"}, nil, true},
		"standalone":                    {&config.Config{Mode: "standalone"}, nil, true},
		"managed, granted":              {&config.Config{Mode: "managed", ManagedProjectRef: "proj-1"}, withGrant, true},
		"managed, not granted":          {&config.Config{Mode: "managed", ManagedProjectRef: "proj-1"}, withoutGrant, false},
		"managed, no tenant config":     {&config.Config{Mode: "managed", ManagedProjectRef: "other"}, withGrant, false},
		"managed, cache unreadable":     {&config.Config{Mode: "managed", ManagedProjectRef: "proj-1"}, unreadable, false},
		"managed, no cache configured":  {&config.Config{Mode: "managed", ManagedProjectRef: "proj-1"}, nil, false},
		"managed with no tenant at all": {&config.Config{Mode: "managed"}, withGrant, false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/posts", nil)
		if got := ServerCacheOffered(context.Background(), tc.cfg, tc.cache, req); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// A managed deployment with no configured project still namespaces by the
// routed tenant, so a tenant header is enough.
func TestServerCacheOfferedUsesTheRoutedTenant(t *testing.T) {
	enabled := true
	cache := valkeytest.New().WithTenant("routed", &valkey.TenantConfig{RestCacheEnabled: &enabled})

	req := httptest.NewRequest(http.MethodGet, "/posts", nil)
	req.Header.Set("X-Supatype-Tenant", "routed")

	if !ServerCacheOffered(context.Background(), &config.Config{Mode: "managed"}, cache, req) {
		t.Error("the routed tenant's grant was not consulted")
	}
}

// ─── Storage failure ──────────────────────────────────────────────────────────

// An Entry is plain data and cannot fail to marshal, so the seam is what proves
// the branch reports rather than storing nothing and calling it a hit.
func TestStoreEntryReportsAMarshalFailure(t *testing.T) {
	original := marshalEntry
	t.Cleanup(func() { marshalEntry = original })
	marshalEntry = func(any) ([]byte, error) { return nil, valkeytest.ErrFailed }

	cache := valkeytest.New()
	if err := storeEntry(context.Background(), cache, "key", Entry{}, 30); err == nil {
		t.Fatal("want an error")
	}
	if cache.Sets != 0 {
		t.Error("something was written despite the failure")
	}
}
