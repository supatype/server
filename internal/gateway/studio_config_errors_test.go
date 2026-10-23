package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
)

// The config endpoint's failures have the shape every other Studio endpoint
// uses: `error` is the code a client branches on, `message` the sentence. It
// used to put the sentence in `error`, as text/plain, so a client branching on
// the code saw "schema not pushed yet" instead of not_found.
func TestTheStudioConfigEndpointSaysWhatFailed(t *testing.T) {
	for name, tc := range map[string]struct {
		path    string
		status  int
		code    string
		message string
	}{
		"nothing pushed": {"no-admin-config-here.json", http.StatusNotFound, "not_found", "schema not pushed yet"},
		"cannot be read": {"..", http.StatusInternalServerError, "internal_error", "failed to read admin config"},
	} {
		t.Run(name, func(t *testing.T) {
			deps := depsFor(t, &config.Config{Mode: "dev", AdminConfigPath: tc.path, JWTSecret: testJWTSecret}, nil)
			rec := httptest.NewRecorder()
			buildStudioConfig(deps).ServeHTTP(rec, studioAdminRequest(t, "/studio-config"))
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("content-type = %q, want application/json", got)
			}
			var body struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("not JSON: %q", rec.Body.String())
			}
			if body.Error != tc.code || body.Message != tc.message {
				t.Errorf("body = %+v, want error %q and message %q", body, tc.code, tc.message)
			}
		})
	}
}

// Without a session the config endpoint refuses in the same shape: the code
// for 401 and the sentence, not the sentence in `error`.
func TestTheStudioConfigEndpointRefusesWithACode(t *testing.T) {
	deps := depsFor(t, &config.Config{Mode: "dev", AdminConfigPath: "no-admin-config-here.json", JWTSecret: testJWTSecret}, nil)
	rec := httptest.NewRecorder()
	buildStudioConfig(deps).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/studio-config", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %q", rec.Body.String())
	}
	if body.Error != "unauthorized" || body.Message != "Authentication required" {
		t.Errorf("body = %+v, want error unauthorized and message Authentication required", body)
	}
}
