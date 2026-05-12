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
	if err := os.WriteFile(filepath.Join(dir, "a.js.gz"), gzBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, false)
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
	if err := os.WriteFile(filepath.Join(dir, "pick.js"), []byte("raw"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pick.js.br"), []byte("br-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write([]byte("gz-bytes"))
	_ = gw.Close()
	if err := os.WriteFile(filepath.Join(dir, "pick.js.gz"), gzBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, false)
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
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, false)
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
	if err := os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{color:red}"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, false)
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
	if err := os.WriteFile(filepath.Join(dir, "b.js"), []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write([]byte("gz"))
	_ = gw.Close()
	if err := os.WriteFile(filepath.Join(dir, "b.js.gz"), gzBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, false)
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
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, true)
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
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<spa/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := Handler(dir, true)
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
