package platformproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
)

func TestHandlerRejectsAnUnusableUpstream(t *testing.T) {
	for name, upstream := range map[string]string{
		"unparseable": "http://[::1]:namedport",
		"no scheme":   "control-plane:8080",

		"scheme only": "http://",
	} {
		if _, err := Handler(&config.Config{ControlPlaneURL: upstream}); err == nil {
			t.Errorf("%s (%q): want an error, got nil", name, upstream)
		}
	}
}

// The old code panicked here, which reported a mistyped variable as a crash.
func TestHandlerDoesNotPanicOnBadUpstream(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Handler panicked instead of returning an error: %v", r)
		}
	}()
	if _, err := Handler(&config.Config{ControlPlaneURL: "://nonsense"}); err == nil {
		t.Fatal("want an error")
	}
}

func TestHandlerProxiesToTheConfiguredUpstream(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Dev mode is config now, not an environment variable: the service-role
	// middleware reads cfg.Mode.
	h, err := Handler(&config.Config{Mode: "dev", ControlPlaneURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/projects" {
		t.Errorf("upstream saw %q, want /projects", gotPath)
	}
}

// Without a service role the mount must refuse, since it fronts the control
// plane.
func TestHandlerRequiresServiceRole(t *testing.T) {
	h, err := Handler(&config.Config{
		Mode:            "managed",
		ServiceRoleKey:  "the-key",
		ControlPlaneURL: "http://control-plane:8080",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects", nil))
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Errorf("unauthenticated request got %d, want 401 or 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "projects") {
		t.Error("response should not have come from the upstream")
	}
}
