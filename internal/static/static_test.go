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

// A directory with no index.html is neither the SPA shell nor a listing.
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

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
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

// A path under a service prefix that no service answered is that service's miss, never the app
// shell: the SPA fallback must not turn /realtime/v1/websocket into index.html.
func TestSPAFallbackNeverAnswersAServicePrefix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>shell</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := Handler(dir, true, CacheOpts{})

	for _, p := range []string{
		"/realtime/v1/websocket", "/functions/v1/hello", "/rest/v1/todos",
		"/auth/v1/token", "/storage/v1/object/a", "/graphql/v1",
	} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "shell") {
			t.Errorf("%s: served index.html", p)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("%s: Content-Type = %q, want JSON", p, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", p, got)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "shell") {
		t.Errorf("/dashboard: status %d body %q, want the app shell", rec.Code, rec.Body.String())
	}
}

// Only a whole first segment is reserved: /restaurant and /authors are the site's own routes.
func TestReservedAppPrefixMatchesWholeSegments(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/rest/", true}, {"/rest/v1/x", true}, {"/graphql/v1", true},
		{"/restaurant", false}, {"/rest", false}, {"/authors/x", false}, {"/", false},
	} {
		if got := reservedAppPrefix(tc.path); got != tc.want {
			t.Errorf("reservedAppPrefix(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func prettyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"index.html":      "shell",
		"about.html":      "about-page",
		"docs/intro.html": "intro-page",
		"blog.html":       "blog-file",
		"blog/index.html": "blog-index",
		"404.html":        "custom-404",
		"data.json":       "{}",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func getPath(h http.Handler, method, p string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, p, nil))
	return rec
}

// An extensionless path with no file behind it tries <path>.html before the SPA fallback or the
// 404 page, so a multi-page build's /about is its about.html.
func TestPrettyURLServesTheHTMLFile(t *testing.T) {
	t.Parallel()
	dir := prettyDir(t)

	for _, spa := range []bool{false, true} {
		h := Handler(dir, spa, CacheOpts{})
		for p, want := range map[string]string{"/about": "about-page", "/docs/intro": "intro-page"} {
			rec := getPath(h, http.MethodGet, p)
			if rec.Code != http.StatusOK || rec.Body.String() != want {
				t.Errorf("spa=%v %s: %d %q, want 200 %q", spa, p, rec.Code, rec.Body.String(), want)
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
				t.Errorf("spa=%v %s: Content-Type = %q", spa, p, got)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
				t.Errorf("spa=%v %s: Cache-Control = %q, want no-cache", spa, p, got)
			}
		}
		if rec := getPath(h, http.MethodHead, "/about"); rec.Code != http.StatusOK {
			t.Errorf("spa=%v HEAD /about: %d", spa, rec.Code)
		}
	}
}

// A directory with an index wins over a sibling .html, a path with no page still 404s with the
// build's 404.html, a POST is never rewritten, and a service prefix stays a JSON 404.
func TestPrettyURLEdges(t *testing.T) {
	t.Parallel()
	dir := prettyDir(t)
	h := Handler(dir, false, CacheOpts{})

	if rec := getPath(h, http.MethodGet, "/blog/"); rec.Body.String() != "blog-index" {
		t.Errorf("/blog/ = %q, want the directory index", rec.Body.String())
	}
	if rec := getPath(h, http.MethodGet, "/nope"); rec.Code != http.StatusNotFound || rec.Body.String() != "custom-404" {
		t.Errorf("/nope = %d %q, want the 404 page", rec.Code, rec.Body.String())
	}
	if rec := getPath(h, http.MethodPost, "/about"); rec.Code != http.StatusNotFound {
		t.Errorf("POST /about = %d, want 404", rec.Code)
	}
	if rec := getPath(h, http.MethodGet, "/data"); rec.Code != http.StatusNotFound {
		t.Errorf("/data (only data.json) = %d, want 404", rec.Code)
	}
	if rec := getPath(Handler(dir, true, CacheOpts{}), http.MethodGet, "/rest/about"); !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("/rest/about served a page")
	}
}
