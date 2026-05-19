package static

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrecompressedGzipServedWhenAccepted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write([]byte("hello-gzip")); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.js.gz"), gzBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, false, CacheOpts{})
	req := httptest.NewRequest(http.MethodGet, "/a.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got == "" {
		t.Fatal("expected Content-Type")
	}
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello-gzip" {
		t.Fatalf("body = %q", body)
	}
}

func TestPrecompressedBrotliPreferredOverGzip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pick.js"), []byte("raw"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pick.js.br"), []byte("br-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write([]byte("gz-bytes"))
	_ = gw.Close()
	if err := os.WriteFile(filepath.Join(dir, "pick.js.gz"), gzBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, false, CacheOpts{})
	req := httptest.NewRequest(http.MethodGet, "/pick.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q want br", got)
	}
	b, _ := io.ReadAll(rec.Body)
	if string(b) != "br-bytes" {
		t.Fatalf("body = %q", b)
	}
}

func TestOnTheFlyGzipWhenNoPrecompressedSidecar(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	payload := strings.Repeat("console.log(1);", 100)
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, false, CacheOpts{})
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q want gzip", got)
	}
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != payload {
		t.Fatalf("body mismatch")
	}
}

func TestRangeRequestSkipsOnTheFlyGzip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{color:red}"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, false, CacheOpts{})
	req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Range", "bytes=0-4")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("unexpected Content-Encoding %q", rec.Header().Get("Content-Encoding"))
	}
}

func TestUncompressedWhenNoAcceptEncodingEvenIfGzipExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.js"), []byte("plain"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write([]byte("gz"))
	_ = gw.Close()
	if err := os.WriteFile(filepath.Join(dir, "b.js.gz"), gzBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, false, CacheOpts{})
	req := httptest.NewRequest(http.MethodGet, "/b.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("unexpected Content-Encoding %q", rec.Header().Get("Content-Encoding"))
	}
	b, _ := io.ReadAll(rec.Body)
	if string(b) != "plain" {
		t.Fatalf("body = %q", b)
	}
}

func TestSPAFallbackWhenNoAssetOrPrecompressed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, true, CacheOpts{})
	req := httptest.NewRequest(http.MethodGet, "/app/route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	b, _ := io.ReadAll(rec.Body)
	if string(b) != "<html></html>" {
		t.Fatalf("body = %q", b)
	}
}

func TestSPADoesNotMaskExistingDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<spa/>"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, true, CacheOpts{})
	req := httptest.NewRequest(http.MethodGet, "/docs/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	b, _ := io.ReadAll(rec.Body)
	if bytes.Contains(b, []byte("<spa/>")) {
		t.Fatalf("directory request incorrectly served SPA index: body=%q", b)
	}
}

func TestCacheOptsPrefixLongestWins(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets", "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "deep", "x.js"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := CacheOpts{
		Prefixes: map[string]string{
			"/assets/":      "short",
			"/assets/deep/": "longer-wins",
		},
	}
	h := Handler(dir, false, opts)
	req := httptest.NewRequest(http.MethodGet, "/assets/deep/x.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "longer-wins" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestCacheOptsHTMLOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := CacheOpts{HTML: "max-age=0, must-revalidate"}
	h := Handler(dir, false, opts)
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "max-age=0, must-revalidate" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestCacheOptsHashedDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "a.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := Handler(dir, false, CacheOpts{})
	req := httptest.NewRequest(http.MethodGet, "/assets/a.bin", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	want := "public, max-age=31536000, immutable"
	if got := rec.Header().Get("Cache-Control"); got != want {
		t.Fatalf("Cache-Control = %q want %q", got, want)
	}
}

func TestSPAFallbackUsesHTMLCachePolicy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := CacheOpts{HTML: "private, no-store"}
	h := Handler(dir, true, opts)
	req := httptest.NewRequest(http.MethodGet, "/client/route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}
