package platformproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
)

// testServiceKey is the service-role key every test deployment is configured
// with, and serviceRequest is a request that presents it.
const testServiceKey = "the-key"

func serviceRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+testServiceKey)
	return req
}

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

	h, err := Handler(&config.Config{Mode: "dev", ServiceRoleKey: testServiceKey, ControlPlaneURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, serviceRequest("/projects"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/projects" {
		t.Errorf("upstream saw %q, want /projects", gotPath)
	}
}

// Without a service role the mount must refuse, since it fronts the control
// plane. Dev mode is no exception: the gateway presents the control plane
// secret on every request it forwards, so a keyless path in dev let a function
// deployed to a dev stack act on the control plane through the gateway.
func TestHandlerRequiresServiceRole(t *testing.T) {
	for _, mode := range []string{"dev", "standalone", "managed"} {
		t.Run(mode, func(t *testing.T) {
			reached := false
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			h, err := Handler(&config.Config{
				Mode:               mode,
				ServiceRoleKey:     testServiceKey,
				ControlPlaneURL:    upstream.URL,
				ControlPlaneSecret: "the-secret",
			})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects", nil))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("unauthenticated request got %d, want 401", rec.Code)
			}
			if reached || strings.Contains(rec.Body.String(), "projects") {
				t.Error("the request reached the control plane")
			}
		})
	}
}

// upstreamRecordingSecret stands in for the control plane and records every
// value of the secret header it was sent.
func upstreamRecordingSecret(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Values(SecretHeader)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	return upstream, &seen
}

// forward sends one request through the handler, with the client's own copies
// of the secret header, and fails unless the upstream answered.
func forward(t *testing.T, secret, upstream string, clientCopies ...string) {
	t.Helper()
	h, err := Handler(&config.Config{Mode: "dev", ServiceRoleKey: testServiceKey, ControlPlaneURL: upstream, ControlPlaneSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	req := serviceRequest("/projects/p/status")
	for _, v := range clientCopies {
		req.Header.Add(SecretHeader, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

// The control plane answers only callers holding the secret, and the gateway
// is the one that holds it: every forwarded request has to carry it.
func TestHandlerPresentsTheControlPlaneSecret(t *testing.T) {
	upstream, seen := upstreamRecordingSecret(t)
	forward(t, "the-secret", upstream.URL)
	if len(*seen) != 1 || (*seen)[0] != "the-secret" {
		t.Errorf("upstream saw %q, want exactly [the-secret]", *seen)
	}
}

// A client must not be able to supply the secret itself: a guessed or leaked
// value sent through the gateway is replaced, never forwarded alongside.
func TestHandlerReplacesAClientsCopyOfTheSecret(t *testing.T) {
	upstream, seen := upstreamRecordingSecret(t)
	forward(t, "the-secret", upstream.URL, "forged", "another")
	if len(*seen) != 1 || (*seen)[0] != "the-secret" {
		t.Errorf("upstream saw %q, want exactly [the-secret]", *seen)
	}
}

// With no secret configured the client's copy is still removed, so the gateway
// never becomes a way to relay one.
func TestHandlerStripsAClientsCopyWhenItHasNoSecret(t *testing.T) {
	upstream, seen := upstreamRecordingSecret(t)
	forward(t, "", upstream.URL, "forged")
	if len(*seen) != 0 {
		t.Errorf("upstream saw %q, want no secret header", *seen)
	}
}
