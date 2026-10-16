package studioauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
)

// Studio has no unauthenticated mode, in dev or anywhere else.
//
// STUDIO_OPEN_DEV used to open it: in dev mode on a locally addressed deployment the
// proxy admitted any request as elevated and injected the service role key. Dev mode
// also allows every CORS origin, so any page open in the developer's browser could
// read and write the whole database through it. These tests set up exactly the
// deployment that switch used to open, through the real configuration path, and
// check that every gate it used to skip now asks for a session.

// openDevDeployment is a dev deployment on localhost with the retired switch set.
func openDevDeployment(t *testing.T) Config {
	t.Helper()
	t.Setenv("SUPATYPE_MODE", "dev")
	t.Setenv("STUDIO_OPEN_DEV", "1")
	t.Setenv("SUPATYPE_API_EXTERNAL_URL", "http://localhost:18473")

	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := ConfigFromServer(loaded)
	c.JWTSecret = testSecret
	c.ServiceRoleKey = "service-role-key"
	c.StudioRole = func(string) (string, bool) { return "admin", true }
	return c
}

func TestOpenDevNoLongerAnswersVerify(t *testing.T) {
	c := openDevDeployment(t)

	rec := httptest.NewRecorder()
	VerifyHandler(c)(rec, studioRequest(http.MethodGet, "/studio/auth/verify", ""))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated verify: status = %d, want 401 (%s)", rec.Code, rec.Body.String())
	}
}

func TestOpenDevNoLongerOpensTheProxy(t *testing.T) {
	c := openDevDeployment(t)

	next := &proxyUpstream{}
	rec := httptest.NewRecorder()
	ProxyHandler(next, c).ServeHTTP(rec, studioRequest(http.MethodGet, "/rest/v1/posts", ""))

	if next.reached {
		t.Fatalf("an unauthenticated request reached the upstream with authorization %q", next.auth)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated proxy: status = %d, want 401 (%s)", rec.Code, rec.Body.String())
	}
}

// The other gates the switch used to skip. Each is called the way its handler calls
// it, with no token, and must refuse.
func TestOpenDevNoLongerSkipsAnyStudioGate(t *testing.T) {
	c := openDevDeployment(t)
	req := func() *http.Request { return studioRequest(http.MethodGet, "/studio/x", "") }

	gates := map[string]func(http.ResponseWriter) bool{
		"RequireAdmin": func(w http.ResponseWriter) bool {
			var reached bool
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
			RequireAdmin(c, next).ServeHTTP(w, req())
			return reached
		},
		"CORS origins": func(w http.ResponseWriter) bool {
			return CORSAccess(c)(w, req(), true)
		},
		"members": func(w http.ResponseWriter) bool {
			_, ok := requireMemberAdmin(w, req(), c, true)
			return ok
		},
		"bootstrap": func(w http.ResponseWriter) bool {
			_, _, ok := bootstrapCaller(w, req(), c)
			return ok
		},
		"preview links": func(w http.ResponseWriter) bool {
			_, _, ok := requirePreviewMinter(w, req(), c, PreviewScopeProject, PublishingSettings{AllowProjectScope: true})
			return ok
		},
	}

	for name, gate := range gates {
		rec := httptest.NewRecorder()
		if gate(rec) {
			t.Errorf("%s admitted an unauthenticated request", name)
			continue
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}
