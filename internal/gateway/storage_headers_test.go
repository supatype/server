package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// storageThrough proxies one GET through the storage mount to an upstream that
// answers with the given Content-Type, and returns what the client saw.
func storageThrough(t *testing.T, contentType, path string) *httptest.ResponseRecorder {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, "<script>alert(1)</script>")
	}))
	t.Cleanup(upstream.Close)

	cfg := config.Config{Mode: "standalone", StorageURL: upstream.URL}
	d := depsFor(t, &cfg, &proxy.RouteManifest{Schema: "public"})
	rec := httptest.NewRecorder()
	buildStorage(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	return rec
}

// A page proxied from storage-api is a sandboxed download, not a document
// running on the gateway origin.
func TestProxiedStorageHTMLIsASandboxedAttachment(t *testing.T) {
	rec := storageThrough(t, "text/html; charset=utf-8", "/object/public/b/pages/evil.html")
	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Content-Disposition":     "attachment; filename=evil.html",
		"Content-Security-Policy": "sandbox; default-src 'none'",
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

// A proxied image stays inline but is never sniffed, and is sandboxed.
func TestProxiedStorageImageStaysInline(t *testing.T) {
	rec := storageThrough(t, "image/png", "/object/public/b/a.png")
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("disposition = %q, want none", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "sandbox; default-src 'none'" {
		t.Errorf("csp = %q", got)
	}
}

// A type the storage service passes through as written, here a list a browser
// may read as HTML, leaves as an octet-stream download in a sandbox.
func TestProxiedStorageTypeListIsASandboxedDownload(t *testing.T) {
	rec := storageThrough(t, "text/plain, text/html", "/object/public/b/x/evil.txt")
	want := map[string]string{
		"Content-Type":            "application/octet-stream",
		"Content-Disposition":     "attachment; filename=evil.txt",
		"Content-Security-Policy": "sandbox; default-src 'none'",
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}
