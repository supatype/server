package studioauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The three wrappers Studio's routes go through: the one that answers "may I",
// the one that gates a route, and the one that forwards a request upstream. What
// each does when the caller is not admitted is the whole of their job.

// adminConfig admits any verified caller whose membership row says this role.
func membershipConfig(role string) Config {
	return Config{
		JWTSecret:      testSecret,
		ServiceRoleKey: "service-role-key",
		AnonKey:        "anon-key",
		AdminRoles:     DefaultAdminRoles,
		Mode:           "standalone",
		StudioRole:     func(string) (string, bool) { return role, role != "" },
	}
}

// bearer mints a token this config will verify, using the package's own signer.
func bearer(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	return signClaims(claims)
}

// request builds a request with an optional bearer token.
func studioRequest(method, path, token string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// ─── Verify ───────────────────────────────────────────────────────────────────

// Only a GET asks the question. Anything else is a request this endpoint has no
// answer for.
func TestVerifyTakesOnlyGET(t *testing.T) {
	handler := VerifyHandler(membershipConfig("admin"))

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := httptest.NewRecorder()
		handler(rec, studioRequest(method, "/studio/auth/verify", ""))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}
}

// An unauthenticated caller is told to authenticate; one who authenticated and
// is still not admitted is told they may not. Collapsing the two would send a
// signed-in user round the login loop for ever.
func TestVerifyDistinguishesUnauthenticatedFromRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		config Config
		token  string
		want   int
	}{
		"no token at all": {membershipConfig("admin"), "", http.StatusUnauthorized},
		"a verified user with no membership": {
			Config{JWTSecret: testSecret, AdminRoles: DefaultAdminRoles,
				StudioRole: func(string) (string, bool) { return "", false }},
			"member", http.StatusForbidden,
		},
	} {
		token := ""
		if tc.token != "" {
			token = bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
		}
		rec := httptest.NewRecorder()
		VerifyHandler(tc.config)(rec, studioRequest(http.MethodGet, "/studio/auth/verify", token))

		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// Dev bypass answers without a token at all, and says so in the identity it
// reports, so nobody mistakes it for a real session.
func TestVerifyUnderDevBypass(t *testing.T) {
	c := membershipConfig("admin")
	c.Mode = "dev"
	c.OpenDev = true
	c.PublicURLs = []string{"http://localhost:9999"}

	rec := httptest.NewRecorder()
	VerifyHandler(c)(rec, studioRequest(http.MethodGet, "/studio/auth/verify", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Allowed    bool   `json:"allowed"`
		Role       string `json:"role"`
		Sub        string `json:"sub"`
		Mode       string `json:"mode"`
		CanElevate bool   `json:"canElevate"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Allowed || body.Role != "dev-bypass" || body.Sub != "dev-bypass" {
		t.Errorf("body = %+v", body)
	}
	if body.Mode != ModeElevated || !body.CanElevate {
		t.Errorf("mode = %q, canElevate = %v, want the bypass to be elevated", body.Mode, body.CanElevate)
	}
}

// A caller admitted by the legacy claim path has no membership row, so no
// permission set. The legacy answer is full access, because that claim only ever
// meant "is an admin".
func TestTheLegacyClaimPathReportsFullAccess(t *testing.T) {
	c := Config{JWTSecret: testSecret, AdminRoles: DefaultAdminRoles} // no StudioRole
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "admin"})

	rec := httptest.NewRecorder()
	VerifyHandler(c)(rec, studioRequest(http.MethodGet, "/studio/auth/verify", token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Permissions StudioPermissions `json:"permissions"`
		CanElevate  bool              `json:"canElevate"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.CanElevate || !body.Permissions.ElevatedSQL {
		t.Errorf("permissions = %+v, want the legacy full set", body.Permissions)
	}
}

// ─── RequireAdmin ─────────────────────────────────────────────────────────────

func TestRequireAdmin(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusTeapot)
	})

	for name, tc := range map[string]struct {
		config      Config
		token       string
		want        int
		wantReached bool
	}{
		"a member":     {membershipConfig("admin"), "member", http.StatusTeapot, true},
		"no token":     {membershipConfig("admin"), "", http.StatusUnauthorized, false},
		"not a member": {membershipConfig(""), "member", http.StatusForbidden, false},
	} {
		reached = false
		token := ""
		if tc.token != "" {
			token = bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
		}

		rec := httptest.NewRecorder()
		RequireAdmin(tc.config, next).ServeHTTP(rec, studioRequest(http.MethodGet, "/studio/x", token))

		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
		if reached != tc.wantReached {
			t.Errorf("%s: reached = %v, want %v", name, reached, tc.wantReached)
		}
	}
}

// Dev bypass lets everything through without a token, which is the point and
// also why DevBypass is so hard to switch on.
func TestRequireAdminUnderDevBypass(t *testing.T) {
	c := membershipConfig("admin")
	c.Mode = "dev"
	c.OpenDev = true

	var reached bool
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
	RequireAdmin(c, next).ServeHTTP(httptest.NewRecorder(), studioRequest(http.MethodGet, "/studio/x", ""))

	if !reached {
		t.Error("the bypass did not let the request through")
	}
}

// ─── ProxyHandler ─────────────────────────────────────────────────────────────

// upstream records what the proxy forwarded.
type proxyUpstream struct {
	reached bool
	auth    string
	apikey  string
	path    string
}

func (u *proxyUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.reached = true
	u.auth = r.Header.Get("Authorization")
	u.apikey = r.Header.Get("apikey")
	u.path = r.URL.Path
	w.WriteHeader(http.StatusOK)
}

func TestProxyRefusesWhoItMust(t *testing.T) {
	for name, tc := range map[string]struct {
		config Config
		token  bool
		want   int
	}{
		"no token":     {membershipConfig("admin"), false, http.StatusUnauthorized},
		"not a member": {membershipConfig(""), true, http.StatusForbidden},
	} {
		token := ""
		if tc.token {
			token = bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
		}
		next := &proxyUpstream{}
		rec := httptest.NewRecorder()
		ProxyHandler(next, tc.config).ServeHTTP(rec, studioRequest(http.MethodGet, "/rest/v1/posts", token))

		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
		if next.reached {
			t.Errorf("%s: the request reached the upstream", name)
		}
	}
}

// An elevated request carries the service role key, and a deployment that has
// not configured one cannot make it: refusing beats forwarding an unelevated
// request that quietly reads less than the caller expects.
func TestProxyRefusesElevationWithNoServiceRoleKey(t *testing.T) {
	c := membershipConfig("admin")
	c.ServiceRoleKey = "  "
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})

	next := &proxyUpstream{}
	rec := httptest.NewRecorder()
	ProxyHandler(next, c).ServeHTTP(rec, studioRequest(http.MethodGet, "/rest/v1/posts", token))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (%s)", rec.Code, rec.Body.String())
	}
	if next.reached {
		t.Error("the request was forwarded without the elevation it asked for")
	}
}

// Acting as the caller leaves their own token in place — PostgREST assumes their
// role and their own policies apply — and sends the anon key, which carries no
// privilege, so a gateway that requires one still sees one.
func TestActingAsTheCallerDoesNotReElevate(t *testing.T) {
	c := membershipConfig("editor")
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})

	next := &proxyUpstream{}
	rec := httptest.NewRecorder()
	req := studioRequest(http.MethodGet, "/rest/v1/posts", token)
	ProxyHandler(next, c).ServeHTTP(rec, req)

	if !next.reached {
		t.Fatalf("the request did not reach the upstream: %d %s", rec.Code, rec.Body.String())
	}
	if next.auth != "Bearer "+token {
		t.Errorf("authorization = %q, want the caller's own token", next.auth)
	}
	if next.apikey == c.ServiceRoleKey {
		t.Error("the service role key was sent on an unelevated request")
	}
	if next.apikey != c.AnonKey {
		t.Errorf("apikey = %q, want the anon key", next.apikey)
	}
	if got := rec.Header().Get(ActingModeHeader); got != ModeSelf {
		t.Errorf("acting mode header = %q", got)
	}
}

// With no anon key configured there is nothing safe to send, so the header goes
// rather than being left as whatever the caller supplied.
func TestActingAsTheCallerWithNoAnonKey(t *testing.T) {
	c := membershipConfig("editor")
	c.AnonKey = "   "
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})

	next := &proxyUpstream{}
	req := studioRequest(http.MethodGet, "/rest/v1/posts", token)
	req.Header.Set("apikey", "something-the-caller-sent")
	ProxyHandler(next, c).ServeHTTP(httptest.NewRecorder(), req)

	if next.apikey != "" {
		t.Errorf("apikey = %q, want it removed", next.apikey)
	}
}

// Under dev bypass everything is elevated and nothing is verified, which is
// what makes it a local-only switch.
func TestProxyUnderDevBypass(t *testing.T) {
	c := membershipConfig("admin")
	c.Mode = "dev"
	c.OpenDev = true

	next := &proxyUpstream{}
	rec := httptest.NewRecorder()
	ProxyHandler(next, c).ServeHTTP(rec, studioRequest(http.MethodGet, "/rest/v1/posts", ""))

	if !next.reached {
		t.Fatal("the request did not reach the upstream")
	}
	if next.auth != "Bearer "+c.ServiceRoleKey {
		t.Errorf("authorization = %q, want the service role key", next.auth)
	}
	if got := rec.Header().Get(ActingModeHeader); got != ModeElevated {
		t.Errorf("acting mode header = %q", got)
	}
}

// A request with no path is not a valid request-target, so it becomes the root
// rather than being forwarded as nothing.
func TestAnEmptyPathBecomesTheRoot(t *testing.T) {
	c := membershipConfig("admin")
	c.Mode = "dev"
	c.OpenDev = true

	next := &proxyUpstream{}
	req := studioRequest(http.MethodGet, "/rest/v1/posts", "")
	req.URL.Path = ""
	ProxyHandler(next, c).ServeHTTP(httptest.NewRecorder(), req)

	if next.path != "/" {
		t.Errorf("path = %q, want /", next.path)
	}
}

// ─── Admin roles from configuration ───────────────────────────────────────────

func TestAdminRolesFromOverride(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want []string
	}{
		"nothing":             {"", DefaultAdminRoles},
		"only whitespace":     {"   ", DefaultAdminRoles},
		"only separators":     {" , , ", DefaultAdminRoles},
		"one role":            {"owner", []string{"owner"}},
		"several, padded":     {" owner , admin ", []string{"owner", "admin"}},
		"with an empty entry": {"owner,,admin", []string{"owner", "admin"}},
	} {
		got := AdminRolesFromOverride(tc.raw)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// The defaults are handed out by copy, so a caller appending to what it got
// cannot change what the next caller receives.
func TestTheDefaultRolesAreNotShared(t *testing.T) {
	first := AdminRolesFromOverride("")
	first[0] = "mutated"

	if second := AdminRolesFromOverride(""); second[0] == "mutated" {
		t.Error("the defaults are shared between callers")
	}
}

// A config file that cannot be read leaves the override in place rather than
// clearing the admin roles, which would lock everyone out.
func TestAdminRolesFromConfigFileFallsBack(t *testing.T) {
	for name, path := range map[string]string{
		"no path":                  "",
		"only whitespace":          "   ",
		"a file that is not there": "nowhere/admin-config.json",
		"an absolute path":         "/etc/admin-config.json",
		"a path that escapes":      "../admin-config.json",
	} {
		got := AdminRolesFromConfigFile(path, "owner")
		if len(got) != 1 || got[0] != "owner" {
			t.Errorf("%s: got %v, want the override", name, got)
		}
	}
}

// And a file that is there but says nothing useful.
func TestAdminRolesFromAConfigFileWithNothingInIt(t *testing.T) {
	dir := t.TempDir()
	previous := chdir(t, dir)
	defer previous()

	for name, content := range map[string]string{
		"not JSON":           "{",
		"no roles":           `{"other":true}`,
		"an empty role list": `{"adminRoles":[]}`,
	} {
		writeFile(t, "admin-config.json", content)
		got := AdminRolesFromConfigFile("admin-config.json", "owner")
		if len(got) != 1 || got[0] != "owner" {
			t.Errorf("%s: got %v, want the override", name, got)
		}
	}
}

// chdir moves into dir for the duration, returning the undo.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(previous) }
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The admin config path is read from the working directory and nowhere else, so
// what it refuses is what stops a config pointing at somebody else's file.
func TestReadAdminConfigFileRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		path string
		want string
	}{
		"nothing":            {"", "file does not exist"},
		"only whitespace":    {"   ", "file does not exist"},
		"an absolute path":   {filepath.Join(os.TempDir(), "admin-config.json"), "must be relative"},
		"a parent reference": {"..", "escapes"},
		"a path that leaves": {".." + string(os.PathSeparator) + "admin-config.json", "escapes"},
	} {
		_, err := ReadAdminConfigFile(tc.path)
		if err == nil {
			t.Errorf("%s: want an error", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

// A working directory that cannot be opened is reported rather than read past.
func TestReadAdminConfigFileWithNoWorkingDirectory(t *testing.T) {
	original := openWorkingDirectory
	t.Cleanup(func() { openWorkingDirectory = original })
	openWorkingDirectory = func() (*os.Root, error) { return nil, errors.New("gone") }

	if _, err := ReadAdminConfigFile("admin-config.json"); err == nil {
		t.Error("want an error")
	}
}

// Every address a deployment answers on has to be local before the bypass will
// open Studio, and these are the forms a local one takes.
func TestLocallyAddressed(t *testing.T) {
	for name, tc := range map[string]struct {
		urls []string
		want bool
	}{
		"nothing configured":      {nil, true},
		"an empty entry":          {[]string{"", "   "}, true},
		"localhost":               {[]string{"http://localhost:9999"}, true},
		"loopback v4":             {[]string{"http://127.0.0.1:9999"}, true},
		"loopback v6":             {[]string{"http://[::1]:9999"}, true},
		"a .localhost name":       {[]string{"http://api.localhost:9999"}, true},
		"loopback v6 with a path": {[]string{"http://[::1]:9999/studio"}, true},
		"a bare v6 loopback":      {[]string{"::1"}, true},
		"a .local name":           {[]string{"http://box.local"}, true},
		"lvh.me":                  {[]string{"http://lvh.me:9999"}, true},
		"a subdomain of lvh.me":   {[]string{"http://api.lvh.me:9999"}, true},
		"the docker host":         {[]string{"http://host.docker.internal:9999"}, true},
		"no scheme":               {[]string{"localhost:9999"}, true},
		"a public address":        {[]string{"https://api.example.com"}, false},
		"one public among local":  {[]string{"http://localhost:9999", "https://api.example.com"}, false},
	} {
		if got := locallyAddressed(tc.urls); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// Without a secret nothing can be verified, and that is a configuration fault
// rather than the caller's.
func TestVerifyWithNoSecretConfigured(t *testing.T) {
	result := VerifyBearerToken("a.b.c", "   ", DefaultAdminRoles)
	if result.Allowed {
		t.Error("a token was accepted with no secret to verify it against")
	}
	if result.Message != "JWT secret not configured" {
		t.Errorf("message = %q", result.Message)
	}
}

// A token with no subject names nobody, so there is no identity to resolve a
// membership row against.
func TestATokenWithNoSubject(t *testing.T) {
	for name, claims := range map[string]jwt.MapClaims{
		"no sub at all":  {"role": "admin"},
		"an empty sub":   {"sub": "", "role": "admin"},
		"a sub of space": {"sub": "   ", "role": "admin"},
	} {
		result := VerifyBearerToken(signClaims(claims), testSecret, DefaultAdminRoles)
		if result.Allowed {
			t.Errorf("%s: a token with no subject was accepted", name)
		}
		if result.Message != "Token missing subject claim" {
			t.Errorf("%s: message = %q", name, result.Message)
		}
	}
}
