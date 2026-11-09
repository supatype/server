package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
)

// storageOffMux builds the outer mux with storage switched off, every storage
// source still configured: a local store directory, and a storage upstream
// that counts what reaches it.
func storageOffMux(t *testing.T, provider string) (http.Handler, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	cfg := &config.Config{
		Mode: "dev", PostgRESTURL: upstream.URL, StorageURL: upstream.URL,
		StorageProvider: provider, StoragePath: t.TempDir(), StorageDisabled: true,
	}
	manifest := &proxy.RouteManifest{Schema: "public", PostgRESTURL: upstream.URL, StorageURL: upstream.URL}
	mf := func(*http.Request) *proxy.RouteManifest { return manifest }
	hp := func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") }
	return buildOuterMux(cfg, mf, hp, http.NotFoundHandler(), nil, "test", nil, nil), &hits
}

// TestBuildOuterMux_StorageDisabledAnswersWhy covers a native `supatype dev`
// stack whose storage service could not start. The gateway used to fall back
// to its built-in object store, which applies no read rules, so every object
// was readable by anyone. With storage switched off, the gateway answers every
// /storage/v1 request itself with 503 and a stable code, whichever provider and
// upstream are configured, and nothing is served from disk or sent upstream.
func TestBuildOuterMux_StorageDisabledAnswersWhy(t *testing.T) {
	for _, provider := range []string{"local", ""} {
		handler, hits := storageOffMux(t, provider)
		for _, tc := range []struct{ method, path string }{
			{http.MethodGet, "/storage/v1/object/public/bucket/a.txt"},
			{http.MethodPost, "/storage/v1/object/bucket/b.txt"},
			{http.MethodGet, "/storage/v1/bucket"},
		} {
			name := provider + " " + tc.method + " " + tc.path
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, strings.NewReader("x")))
			assertStorageUnavailable(t, name, rr)
		}
		if n := hits.Load(); n != 0 {
			t.Errorf("provider %q: %d request(s) reached the storage upstream although storage is off", provider, n)
		}
	}
}

func assertStorageUnavailable(t *testing.T, name string, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("%s: status %d, want 503: %s", name, rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: Content-Type %q, want application/json", name, ct)
	}
	var body struct{ Code, Message string }
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: body is not JSON: %v: %s", name, err, rr.Body.String())
	}
	if body.Code != "storage_unavailable" {
		t.Errorf("%s: code %q, want storage_unavailable", name, body.Code)
	}
	want := "The storage service is not running; supatype dev printed why when it started."
	if body.Message != want {
		t.Errorf("%s: message %q, want %q", name, body.Message, want)
	}
}
