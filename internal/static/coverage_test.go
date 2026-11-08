package static

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The gaps: a path that tries to leave the root, a precompressed sidecar that
// is not a file, a directory asked for by name, and the on-the-fly gzip that
// only some requests qualify for.

// tree writes files into a temporary directory and returns its path.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// get runs one request through a handler over dir.
func get(t *testing.T, dir, path string, spa bool, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	Handler(dir, spa, CacheOpts{}).ServeHTTP(rec, req)
	return rec
}

// ─── Path safety ──────────────────────────────────────────────────────────────

// A path that escapes the directory is not a path this serves. Everything
// downstream opens under an os.Root, so this is defence in depth rather than
// the only guard, but the guard has to actually refuse.
func TestStaticRelPath(t *testing.T) {
	for name, tc := range map[string]struct {
		urlPath string
		want    string
		ok      bool
	}{
		"a plain file":       {"/index.html", "index.html", true},
		"a nested file":      {"/assets/app.js", filepath.FromSlash("assets/app.js"), true},
		"the root":           {"/", "", true},
		"an empty path":      {"", "", true},
		"a parent reference": {"/../secrets", "", false},
		"a deeper escape":    {"/assets/../../secrets", "", false},
		"an absolute path":   {"//etc/passwd", "", false},
	} {
		got, ok := staticRelPath(tc.urlPath)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%s: staticRelPath(%q) = (%q, %v), want (%q, %v)", name, tc.urlPath, got, ok, tc.want, tc.ok)
		}
	}
}

// An escaping path finds nothing, whichever way it is asked.
func TestAnEscapingPathServesNothing(t *testing.T) {
	dir := tree(t, map[string]string{"index.html": "<html>"})

	if _, _, ok := pickVariant(dir, "/../outside", httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Error("pickVariant accepted a path outside the root")
	}
	if anyRepresentableFile(dir, "/../outside") {
		t.Error("anyRepresentableFile accepted a path outside the root")
	}
	if isDir, _ := directoryIndex(dir, "/../outside"); isDir {
		t.Error("directoryIndex accepted a path outside the root")
	}
}

// A directory that does not exist cannot be served from, and the handler has to
// say so rather than panicking on a nil root.
func TestAMissingRootDirectory(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "not-there")

	if _, _, ok := pickVariant(absent, "/app.js", httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Error("pickVariant found something under a directory that does not exist")
	}
	if anyRepresentableFile(absent, "/app.js") {
		t.Error("anyRepresentableFile found something under a directory that does not exist")
	}
	if isDir, _ := directoryIndex(absent, "/"); isDir {
		t.Error("directoryIndex found a directory under a directory that does not exist")
	}

	rec := httptest.NewRecorder()
	serveVariant(rec, httptest.NewRequest(http.MethodGet, "/app.js", nil), absent, "app.js", "", "/app.js")
	if rec.Code != http.StatusNotFound {
		t.Errorf("serveVariant status = %d, want 404", rec.Code)
	}
}

// ─── Variant selection ────────────────────────────────────────────────────────

// Brotli is preferred over gzip when the client takes both, and a sidecar is
// preferred over the plain file, which is the whole point of building them.
func TestPickVariantPrefersTheBestEncodingOffered(t *testing.T) {
	dir := tree(t, map[string]string{
		"app.js":    "plain",
		"app.js.br": "brotli",
		"app.js.gz": "gzipped",
	})

	for name, tc := range map[string]struct {
		accept   string
		wantPath string
		wantEnc  string
	}{
		"brotli and gzip":  {"br, gzip", "app.js.br", "br"},
		"gzip only":        {"gzip", "app.js.gz", "gzip"},
		"brotli only":      {"br", "app.js.br", "br"},
		"neither":          {"identity", "app.js", ""},
		"nothing declared": {"", "app.js", ""},
		"with q-values":    {"gzip;q=0.8, deflate;q=0.5", "app.js.gz", "gzip"},
		"oddly cased":      {"GZIP", "app.js.gz", "gzip"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
		req.Header.Set("Accept-Encoding", tc.accept)

		disk, enc, ok := pickVariant(dir, "/app.js", req)
		if !ok {
			t.Errorf("%s: nothing picked", name)
			continue
		}
		if disk != filepath.FromSlash(tc.wantPath) || enc != tc.wantEnc {
			t.Errorf("%s: picked (%q, %q), want (%q, %q)", name, disk, enc, tc.wantPath, tc.wantEnc)
		}
	}
}

// A sidecar with no plain file beside it is still served: a build may ship only
// the compressed form.
func TestASidecarWithoutItsPlainFile(t *testing.T) {
	dir := tree(t, map[string]string{"app.js.br": "brotli", "style.css.gz": "gzipped"})

	for name, tc := range map[string]struct {
		path, accept, wantPath, wantEnc string
		wantOK                          bool
	}{
		"brotli only on disk": {"/app.js", "br", "app.js.br", "br", true},
		"gzip only on disk":   {"/style.css", "gzip", "style.css.gz", "gzip", true},
		"but not if unwanted": {"/app.js", "identity", "", "", false},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Accept-Encoding", tc.accept)

		disk, enc, ok := pickVariant(dir, tc.path, req)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", name, ok, tc.wantOK)
			continue
		}
		if ok && (disk != filepath.FromSlash(tc.wantPath) || enc != tc.wantEnc) {
			t.Errorf("%s: picked (%q, %q)", name, disk, enc)
		}
	}
}

// A directory is the file server's business, not a variant to serve.
func TestPickVariantDeclinesADirectory(t *testing.T) {
	dir := tree(t, map[string]string{"assets/app.js": "x"})

	if _, _, ok := pickVariant(dir, "/assets", httptest.NewRequest(http.MethodGet, "/assets", nil)); ok {
		t.Error("a directory was picked as a variant")
	}
}

// A sidecar that is a directory rather than a file must not be served as one.
func TestASidecarThatIsNotAFileIsIgnored(t *testing.T) {
	dir := tree(t, map[string]string{"app.js": "plain", "app.js.br/inside": "x", "app.js.gz/inside": "x"})

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")

	disk, enc, ok := pickVariant(dir, "/app.js", req)
	if !ok {
		t.Fatal("nothing picked")
	}
	// The plain file is compressible and small, so it qualifies for on-the-fly
	// gzip rather than either sidecar.
	if disk != "app.js" || enc != "gzip-dynamic" {
		t.Errorf("picked (%q, %q), want the plain file", disk, enc)
	}
}

// ─── On-the-fly gzip ──────────────────────────────────────────────────────────

// Compressing on the fly is worth it only for text, only when the client asked,
// only when nothing better exists, and never for a Range request, which would
// mean compressing a byte range the client then cannot reassemble.
func TestOnTheFlyGzipQualification(t *testing.T) {
	big := strings.Repeat("x", maxOnTheFlyGzip+1)
	dir := tree(t, map[string]string{
		"app.js":    "console.log(1)",
		"logo.png":  "not text",
		"empty.css": "",
		"huge.js":   big,
	})

	for name, tc := range map[string]struct {
		path, accept, rangeHdr string
		wantEnc                string
	}{
		"a small script":        {"/app.js", "gzip", "", "gzip-dynamic"},
		"the client wants none": {"/app.js", "identity", "", ""},
		"a binary type":         {"/logo.png", "gzip", "", ""},
		"an empty file":         {"/empty.css", "gzip", "", ""},
		"a file over the cap":   {"/huge.js", "gzip", "", ""},
		"a range request":       {"/app.js", "gzip", "bytes=0-3", ""},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Accept-Encoding", tc.accept)
		if tc.rangeHdr != "" {
			req.Header.Set("Range", tc.rangeHdr)
		}

		_, enc, ok := pickVariant(dir, tc.path, req)
		if !ok {
			t.Errorf("%s: nothing picked", name)
			continue
		}
		if enc != tc.wantEnc {
			t.Errorf("%s: encoding = %q, want %q", name, enc, tc.wantEnc)
		}
	}
}

// The body the client gets back has to actually be gzip, and has to decompress
// to the file.
func TestOnTheFlyGzipProducesAReadableBody(t *testing.T) {
	dir := tree(t, map[string]string{"app.js": "console.log('hello')"})
	rec := get(t, dir, "/app.js", false, map[string]string{"Accept-Encoding": "gzip"})

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("content-encoding = %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding so a cache does not serve this to a client that cannot read it", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("content-type = %q, want the logical path's type, not the sidecar's", got)
	}

	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "console.log('hello')" {
		t.Errorf("body = %q", body)
	}
}

func TestCompressiblePath(t *testing.T) {
	for _, path := range []string{"a.js", "a.mjs", "a.css", "a.html", "a.htm", "a.svg", "a.json", "a.xml", "a.txt", "a.map", "A.JS"} {
		if !compressiblePath(path) {
			t.Errorf("%s should be compressible", path)
		}
	}
	for _, path := range []string{"a.png", "a.woff2", "a.wasm", "a.gz", "a", ""} {
		if compressiblePath(path) {
			t.Errorf("%s should not be compressed on the fly", path)
		}
	}
}

// ─── Serving ──────────────────────────────────────────────────────────────────

// The content type comes from the URL, not the file on disk: app.js.br is
// JavaScript, not brotli-flavoured octets.
func TestASidecarIsTypedByTheLogicalPath(t *testing.T) {
	dir := tree(t, map[string]string{"app.js": "plain", "app.js.br": "brotli-bytes"})
	rec := get(t, dir, "/app.js", false, map[string]string{"Accept-Encoding": "br"})

	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("content-type = %q", got)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Errorf("content-encoding = %q", got)
	}
	if rec.Body.String() != "brotli-bytes" {
		t.Errorf("body = %q, want the sidecar's bytes", rec.Body.String())
	}
}

// An extension nothing recognises is bytes, and saying so is better than
// letting a browser sniff it.
func TestAnUnknownExtensionIsOctetStream(t *testing.T) {
	dir := tree(t, map[string]string{"thing.zzz": "bytes"})
	rec := get(t, dir, "/thing.zzz", false, nil)

	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("content-type = %q", got)
	}
}

// The file can go between the pick and the serve; that is a 404, not a panic.
func TestServeVariantOnAFileThatVanished(t *testing.T) {
	dir := tree(t, map[string]string{"app.js": "x"})

	rec := httptest.NewRecorder()
	serveVariant(rec, httptest.NewRequest(http.MethodGet, "/gone.js", nil), dir, "gone.js", "", "/gone.js")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// A directory opened as a variant is not a regular file, and serving its
// listing under a script's content type would be worse than a 404.
func TestServeVariantOnADirectory(t *testing.T) {
	dir := tree(t, map[string]string{"assets/app.js": "x"})

	rec := httptest.NewRecorder()
	serveVariant(rec, httptest.NewRequest(http.MethodGet, "/assets", nil), dir, "assets", "", "/assets")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// ─── SPA fallback ─────────────────────────────────────────────────────────────

// A client-side route is not a file, and serving index.html is what makes a
// deep link work on reload. A path that does name a file must not be swallowed
// by the fallback.
func TestSPAFallback(t *testing.T) {
	dir := tree(t, map[string]string{
		"index.html":    "<html>app</html>",
		"assets/app.js": "console.log(1)",
		"only.br":       "compressed",
	})

	for name, tc := range map[string]struct {
		path string
		want string
	}{
		"a client-side route":      {"/dashboard/settings", "<html>app</html>"},
		"a file that exists":       {"/assets/app.js", "console.log(1)"},
		"a directory that exists":  {"/assets/", ""},
		"a path with only sidecar": {"/only", ""},
	} {
		rec := get(t, dir, tc.path, true, nil)
		if tc.want != "" && rec.Body.String() != tc.want {
			t.Errorf("%s: body = %q, want %q", name, rec.Body.String(), tc.want)
		}
		if tc.want == "" && rec.Body.String() == "<html>app</html>" {
			t.Errorf("%s: the fallback swallowed a path that exists", name)
		}
	}
}

// Without the SPA flag an unknown path is a 404, which is what an API or a
// plain static site wants.
func TestWithoutSPAAnUnknownPathIs404(t *testing.T) {
	dir := tree(t, map[string]string{"index.html": "<html>"})
	if rec := get(t, dir, "/dashboard", false, nil); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// anyRepresentableFile decides whether the fallback applies, so it has to count
// a compressed-only asset as present.
func TestAnyRepresentableFile(t *testing.T) {
	dir := tree(t, map[string]string{
		"index.html": "x",
		"assets/a":   "x",
		"br-only.br": "x",
		"gz-only.gz": "x",
	})

	for name, tc := range map[string]struct {
		path string
		want bool
	}{
		"a file": {"/index.html", true},
		// Directories are settled by directoryIndex before this is asked, so
		// neither one nor the root counts as a file here.
		"a directory":             {"/assets", false},
		"the root":                {"/", false},
		"a brotli sidecar alone":  {"/br-only", true},
		"a gzip sidecar alone":    {"/gz-only", true},
		"nothing of the sort":     {"/absent", false},
		"a path outside the root": {"/../outside", false},
	} {
		if got := anyRepresentableFile(dir, tc.path); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// ─── Cache-Control ────────────────────────────────────────────────────────────

// A hashed bundle can be cached forever because its name changes when it does;
// HTML cannot, because its name does not.
func TestCacheControlForPath(t *testing.T) {
	custom := CacheOpts{
		HTML:         "no-store",
		HashedAssets: "public, max-age=60",
		Prefixes:     map[string]string{"/docs": "public, max-age=300", "docs/api": "public, max-age=900", "  ": "ignored"},
	}

	for name, tc := range map[string]struct {
		path string
		opts CacheOpts
		want string
	}{
		"a hashed asset, by default": {"/assets/app.abc123.js", CacheOpts{}, defaultHashedCache},
		"HTML, by default":           {"/index.html", CacheOpts{}, defaultHTMLCache},
		"the root, by default":       {"/", CacheOpts{}, defaultHTMLCache},
		"anything else":              {"/robots.txt", CacheOpts{}, ""},
		"a hashed asset, configured": {"/_next/static/x.js", custom, "public, max-age=60"},
		"HTML, configured":           {"/about.html", custom, "no-store"},
		"a configured prefix":        {"/docs/intro", custom, "public, max-age=300"},
		"the longest prefix wins":    {"/docs/api/v1", custom, "public, max-age=900"},
		"a prefix without its slash": {"/docs", custom, "public, max-age=300"},
		"a prefix beats a hashed path": {
			"/assets/x.js",
			CacheOpts{Prefixes: map[string]string{"/assets": "public, max-age=1"}},
			"public, max-age=1",
		},
		"a configured value of spaces": {
			"/docs/x",
			CacheOpts{Prefixes: map[string]string{"/docs": "   "}},
			"",
		},
		"a blank HTML setting falls back": {
			"/index.html", CacheOpts{HTML: "   "}, defaultHTMLCache,
		},
		"a blank hashed setting falls back": {
			"/assets/x.js", CacheOpts{HashedAssets: "  "}, defaultHashedCache,
		},
	} {
		if got := cacheControlForPath(tc.path, tc.opts); got != tc.want {
			t.Errorf("%s: cacheControlForPath(%q) = %q, want %q", name, tc.path, got, tc.want)
		}
	}
}

func TestNormalizeURLPrefix(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"already absolute":  {"/docs", "/docs"},
		"missing its slash": {"docs", "/docs"},
		"padded":            {"  docs  ", "/docs"},
		"empty":             {"", ""},
		"only whitespace":   {"   ", ""},
	} {
		if got := normalizeURLPrefix(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestIsHashedAssetPath(t *testing.T) {
	for _, path := range []string{"/assets/a", "/_next/a", "/_astro/a", "/static/a", "/_app/a", "/_nuxt/a"} {
		if !isHashedAssetPath(path) {
			t.Errorf("%s should be a hashed asset path", path)
		}
	}
	for _, path := range []string{"/", "/index.html", "/assets", "/my-assets/a"} {
		if isHashedAssetPath(path) {
			t.Errorf("%s should not be a hashed asset path", path)
		}
	}
}

// The SPA fallback serves HTML, so it gets the HTML policy and not whatever the
// requested path would have had. (A hashed-asset path never falls back, so the
// path's own policy here comes from a configured prefix.)
func TestTheSPAFallbackCarriesTheHTMLPolicy(t *testing.T) {
	dir := tree(t, map[string]string{"index.html": "<html>"})

	req := httptest.NewRequest(http.MethodGet, "/app/route", nil)
	rec := httptest.NewRecorder()
	opts := CacheOpts{HTML: "private, max-age=0", Prefixes: map[string]string{"/app/": "public, max-age=600"}}
	Handler(dir, true, opts).ServeHTTP(rec, req)

	if rec.Body.String() != "<html>" {
		t.Fatalf("body = %q, want index.html", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=0" {
		t.Errorf("cache-control = %q, want the HTML policy rather than the prefix one", got)
	}
}

func TestCacheControlForSPAHTML(t *testing.T) {
	if got := cacheControlForSPAHTML(CacheOpts{}); got != defaultHTMLCache {
		t.Errorf("got %q", got)
	}
	if got := cacheControlForSPAHTML(CacheOpts{HTML: "no-store"}); got != "no-store" {
		t.Errorf("got %q", got)
	}
	if got := cacheControlForSPAHTML(CacheOpts{HTML: "  "}); got != defaultHTMLCache {
		t.Errorf("got %q", got)
	}
}

func TestHeaderHasToken(t *testing.T) {
	for name, tc := range map[string]struct {
		header, token string
		want          bool
	}{
		"exactly the token":     {"gzip", "gzip", true},
		"one of several":        {"br, gzip, deflate", "gzip", true},
		"with a q-value":        {"gzip;q=0.5", "gzip", true},
		"padded":                {"  gzip  ", "gzip", true},
		"cased differently":     {"GZip", "gzip", true},
		"not offered":           {"br", "gzip", false},
		"nothing offered":       {"", "gzip", false},
		"a prefix, not a token": {"gzipper", "gzip", false},
	} {
		if got := headerHasToken(tc.header, tc.token); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// failingWriter is a ResponseWriter whose body writes fail after failAfter
// successful ones, which is what a client hanging up mid-response looks like.
type failingWriter struct {
	header    http.Header
	failAfter int
	writes    int
	status    int
}

func (f *failingWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}

func (f *failingWriter) WriteHeader(status int) { f.status = status }

func (f *failingWriter) Write(b []byte) (int, error) {
	f.writes++
	if f.writes > f.failAfter {
		return 0, errors.New("connection reset by peer")
	}
	return len(b), nil
}

// A client that hangs up mid-response leaves nothing useful to say: the status
// line is already out. What matters is that the handler returns rather than
// panicking or writing a second header.
func TestOnTheFlyGzipGivesUpWhenTheClientGoesAway(t *testing.T) {
	dir := tree(t, map[string]string{"app.js": strings.Repeat("console.log(1);", 1000)})

	for name, failAfter := range map[string]int{
		// gzip writes its header on the first Write, so failing immediately
		// breaks the copy.
		"the copy fails": 0,
		// Everything the compressor buffered is flushed by Close, so a writer
		// that tolerates the copy can still fail there.
		"the close fails": 1,
	} {
		w := &failingWriter{failAfter: failAfter}
		serveVariant(w, httptest.NewRequest(http.MethodGet, "/app.js", nil), dir, "app.js", "gzip-dynamic", "/app.js")

		if w.status != 0 {
			t.Errorf("%s: a status was written after the body had started: %d", name, w.status)
		}
	}
}

// ─── Misses under the SPA fallback ────────────────────────────────────────────

// The fallback is for client-side routes. A request for a file that is not
// there (a stale hashed bundle, a favicon the build does not ship) has to be a
// 404: index.html under a script's URL fails in the browser with a MIME error,
// and under the hashed-asset policy the miss is cached for a year. A dotted
// last segment is a client route only when a browser navigates to it, which is
// what Accept: text/html says; with only */* the request is taken as a file
// fetch and gets the 404.
func TestSPAFallbackOnlyAnswersNavigations(t *testing.T) {
	dir := tree(t, map[string]string{"index.html": "<html>app</html>"})

	for name, tc := range map[string]struct {
		path, accept string
		wantStatus   int
		wantIndex    bool
	}{
		"a client route":                 {"/dashboard", "", http.StatusOK, true},
		"a client route under a dir":     {"/dashboard/settings", "*/*", http.StatusOK, true},
		"a missing hashed bundle":        {"/assets/missing-abc.js", "*/*", http.StatusNotFound, false},
		"a hashed path asking for HTML":  {"/assets/page", "text/html", http.StatusNotFound, false},
		"a missing favicon":              {"/favicon.ico", "image/avif,image/webp,image/*,*/*;q=0.8", http.StatusNotFound, false},
		"a dotted route a browser opens": {"/u/john.doe", "text/html,application/xhtml+xml,*/*;q=0.8", http.StatusOK, true},
		"a dotted route fetched as */*":  {"/u/john.doe", "*/*", http.StatusNotFound, false},
	} {
		rec := get(t, dir, tc.path, true, map[string]string{"Accept": tc.accept})
		if rec.Code != tc.wantStatus {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, tc.wantStatus)
		}
		if gotIndex := rec.Body.String() == "<html>app</html>"; gotIndex != tc.wantIndex {
			t.Errorf("%s: served index.html = %v, want %v", name, gotIndex, tc.wantIndex)
		}
		if tc.wantStatus == http.StatusNotFound {
			if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("%s: cache-control = %q, want no-store", name, cc)
			}
		}
	}
}

// A build that ships 404.html gets it as the body of every miss, with the 404
// status rather than the 200 a plain file serve would give.
func TestSPAMissServesTheBuilds404Page(t *testing.T) {
	dir := tree(t, map[string]string{
		"index.html": "<html>app</html>",
		"404.html":   "<html>gone</html>",
	})

	rec := get(t, dir, "/assets/missing-abc.js", true, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if rec.Body.String() != "<html>gone</html>" {
		t.Errorf("body = %q, want the 404 page", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}

	head := httptest.NewRequest(http.MethodHead, "/favicon.ico", nil)
	hrec := httptest.NewRecorder()
	Handler(dir, true, CacheOpts{}).ServeHTTP(hrec, head)
	if hrec.Code != http.StatusNotFound || hrec.Body.Len() != 0 {
		t.Errorf("HEAD: status = %d, body length = %d, want 404 with no body", hrec.Code, hrec.Body.Len())
	}
}

// Only GET and HEAD are navigations. A POST to a missing path is a form or an
// API call that went to the wrong place, and answering it with the app's shell
// and a 200 hides that.
func TestSPAMissForOtherMethodsIs404(t *testing.T) {
	dir := tree(t, map[string]string{"index.html": "<html>app</html>"})

	for _, path := range []string{"/dashboard", "/assets/missing-abc.js"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		Handler(dir, true, CacheOpts{}).ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || rec.Body.String() == "<html>app</html>" {
			t.Errorf("POST %s: status = %d body = %q, want a 404 that is not index.html", path, rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("POST %s: cache-control = %q, want no-store", path, cc)
		}
	}
}

// Without the SPA fallback a miss is still a 404, but a build that ships
// 404.html gets it as the body, under the same no-store policy.
func TestWithoutSPAAMissServesTheBuilds404Page(t *testing.T) {
	dir := tree(t, map[string]string{
		"index.html": "<html>app</html>",
		"404.html":   "<html>gone</html>",
	})

	for _, path := range []string{"/dashboard", "/assets/missing-abc.js"} {
		rec := get(t, dir, path, false, nil)
		if rec.Code != http.StatusNotFound || rec.Body.String() != "<html>gone</html>" {
			t.Errorf("%s: status = %d body = %q, want 404 with the 404 page", path, rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: cache-control = %q, want no-store", path, cc)
		}
	}
}

// A directory that cannot be opened has no 404.html to serve, so the miss is the plain 404, still
// under no-store.
func TestAMissInADirectoryThatCannotBeOpened(t *testing.T) {
	rec := get(t, filepath.Join(t.TempDir(), "gone"), "/dashboard", false, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache-control = %q, want no-store", cc)
	}
}

// serve runs one request with the given method and Accept through an SPA
// handler over dir.
func serve(t *testing.T, dir, method, target, accept string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	Handler(dir, true, CacheOpts{}).ServeHTTP(rec, req)
	return rec
}

// After a redeploy a stale tab asks for chunks the build no longer has. Each
// of those requests, in the forms a browser sends them, must be a 404 and not
// the app shell, or the script fails with a MIME error instead of a clean miss.
func TestAMissingAssetIsNeverTheAppShell(t *testing.T) {
	const shell = "<html>app</html>"
	dir := tree(t, map[string]string{"index.html": shell})

	const script = "*/*"
	const style = "text/css,*/*;q=0.1"
	const image = "image/avif,image/webp,image/*,*/*;q=0.8"
	for _, tc := range []struct{ method, target, accept string }{
		{http.MethodGet, "/assets/missing-abc.js", script},
		{http.MethodGet, "/assets/missing-abc.js?v=2", script},
		{http.MethodGet, "/assets/missing-abc.js", ""},
		{http.MethodHead, "/assets/missing-abc.js", script},
		{http.MethodGet, "/assets/missing-abc.css", style},
		{http.MethodGet, "/assets/nested/missing", script},
		{http.MethodGet, "/chunk-abc.js", script},
		{http.MethodGet, "/favicon.ico", image},
		{http.MethodGet, "/favicon.ico?x=1", image},
		{http.MethodHead, "/favicon.ico", ""},
	} {
		rec := serve(t, dir, tc.method, tc.target, tc.accept)
		name := tc.method + " " + tc.target + " Accept=" + tc.accept
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), shell) {
			t.Errorf("%s: served the app shell", name)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: cache-control = %q, want no-store", name, cc)
		}
	}
}

// A deep link is still the app shell, exactly as built, with the HTML policy,
// whatever its query string carries.
func TestADeepLinkIsTheAppShellByteForByte(t *testing.T) {
	const shell = "<!doctype html>\n<html><body>\xef\xbb\xbfapp \t</body></html>\n"
	dir := tree(t, map[string]string{"index.html": shell})

	const nav = "text/html,application/xhtml+xml,*/*;q=0.8"
	for _, target := range []string{"/dashboard", "/dashboard/settings?tab=1", "/dashboard?file=x.js", "/a/b/c/"} {
		rec := serve(t, dir, http.MethodGet, target, nav)
		if rec.Code != http.StatusOK || rec.Body.String() != shell {
			t.Errorf("GET %s: status = %d body = %q, want 200 with index.html", target, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("GET %s: content-type = %q", target, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != defaultHTMLCache {
			t.Errorf("GET %s: cache-control = %q, want %q", target, cc, defaultHTMLCache)
		}
	}

	head := serve(t, dir, http.MethodHead, "/dashboard", nav)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(shell)) {
		t.Errorf("HEAD /dashboard: status = %d body length = %d content-length = %q, want 200, 0, %d",
			head.Code, head.Body.Len(), head.Header().Get("Content-Length"), len(shell))
	}
}

// A file that exists keeps the policy for its kind: hashed bundles are cached
// for good, the shell at / is revalidated, and a HEAD agrees with the GET.
func TestAnExistingFileKeepsItsCachePolicy(t *testing.T) {
	dir := tree(t, map[string]string{
		"index.html":        "<html>app</html>",
		"assets/app-abc.js": "console.log(1)",
	})

	for _, tc := range []struct{ method, target, want string }{
		{http.MethodGet, "/assets/app-abc.js", defaultHashedCache},
		{http.MethodGet, "/assets/app-abc.js?v=2", defaultHashedCache},
		{http.MethodHead, "/assets/app-abc.js", defaultHashedCache},
		{http.MethodGet, "/", defaultHTMLCache},
		{http.MethodHead, "/", defaultHTMLCache},
	} {
		rec := serve(t, dir, tc.method, tc.target, "*/*")
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s: status = %d, want 200", tc.method, tc.target, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != tc.want {
			t.Errorf("%s %s: cache-control = %q, want %q", tc.method, tc.target, cc, tc.want)
		}
	}
}

// A directory with no index.html behind it is a miss: 404 with no-store, with
// or without the trailing slash, for GET and HEAD, at any depth, SPA or not.
// It is never a listing of the bundle and never the app shell, even when the
// request looks like a navigation.
func TestADirectoryWithoutAnIndexIsAMiss(t *testing.T) {
	const shell = "<html>app</html>"
	dir := tree(t, map[string]string{
		"index.html":              shell,
		"assets/app-abc.js":       "console.log(1)",
		"assets/nested/chunk.js":  "console.log(2)",
		"images/logo.png":         "png",
		"odd/index.html/inner.js": "console.log(3)",
	})

	const nav = "text/html,application/xhtml+xml,*/*;q=0.8"
	targets := []string{"/assets/", "/assets", "/assets/nested/", "/assets/nested", "/images/", "/images", "/odd/"}
	for _, spa := range []bool{true, false} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, accept := range []string{nav, "*/*"} {
				for _, target := range targets {
					req := httptest.NewRequest(method, target, nil)
					req.Header.Set("Accept", accept)
					rec := httptest.NewRecorder()
					Handler(dir, spa, CacheOpts{}).ServeHTTP(rec, req)

					name := method + " " + target + " Accept=" + accept + " spa=" + strconv.FormatBool(spa)
					if rec.Code != http.StatusNotFound {
						t.Errorf("%s: status = %d, want 404", name, rec.Code)
					}
					if body := rec.Body.String(); strings.Contains(body, shell) || strings.Contains(body, ".js") || strings.Contains(body, ".png") {
						t.Errorf("%s: body = %q, want neither the shell nor a listing", name, body)
					}
					if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
						t.Errorf("%s: cache-control = %q, want no-store", name, cc)
					}
				}
			}
		}
	}
}

// The site root with no index.html is the same miss, not a listing.
func TestARootWithoutAnIndexIsAMiss(t *testing.T) {
	dir := tree(t, map[string]string{"assets/app-abc.js": "console.log(1)"})

	for _, spa := range []bool{true, false} {
		rec := get(t, dir, "/", spa, nil)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "assets") {
			t.Errorf("spa=%v: status = %d body = %q, want 404 with no listing", spa, rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("spa=%v: cache-control = %q, want no-store", spa, cc)
		}
	}
}

// The site root is a directory like any other: with an index.html it is
// served it in either mode, which without the SPA fallback used to be a 404.
func TestARootWithAnIndexServesIt(t *testing.T) {
	const shell = "<html>app</html>"
	dir := tree(t, map[string]string{"index.html": shell})

	for _, spa := range []bool{true, false} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := httptest.NewRecorder()
			Handler(dir, spa, CacheOpts{}).ServeHTTP(rec, httptest.NewRequest(method, "/", nil))
			if rec.Code != http.StatusOK || rec.Header().Get("Content-Length") != strconv.Itoa(len(shell)) {
				t.Errorf("spa=%v %s /: status = %d content-length = %q, want 200, %d", spa, method, rec.Code, rec.Header().Get("Content-Length"), len(shell))
			}
			if method == http.MethodGet && rec.Body.String() != shell {
				t.Errorf("spa=%v GET /: body = %q, want %q", spa, rec.Body.String(), shell)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
				t.Errorf("spa=%v %s /: content-type = %q", spa, method, ct)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != defaultHTMLCache {
				t.Errorf("spa=%v %s /: cache-control = %q, want %q", spa, method, cc, defaultHTMLCache)
			}
		}
	}
}

// A directory that has its own index.html keeps being served it, and the bare
// name keeps redirecting to the slash form, as http.FileServer does.
func TestADirectoryWithAnIndexServesIt(t *testing.T) {
	const page = "<html>docs</html>"
	dir := tree(t, map[string]string{
		"index.html":            "<html>app</html>",
		"docs/index.html":       page,
		"docs/guide/index.html": "<html>guide</html>",
	})

	for _, spa := range []bool{true, false} {
		for _, tc := range []struct{ target, want string }{
			{"/docs/", page},
			{"/docs/guide/", "<html>guide</html>"},
		} {
			rec := get(t, dir, tc.target, spa, nil)
			if rec.Code != http.StatusOK || rec.Body.String() != tc.want {
				t.Errorf("spa=%v GET %s: status = %d body = %q, want 200 %q", spa, tc.target, rec.Code, rec.Body.String(), tc.want)
			}
		}

		head := httptest.NewRecorder()
		Handler(dir, spa, CacheOpts{}).ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/docs/", nil))
		if head.Code != http.StatusOK || head.Body.Len() != 0 {
			t.Errorf("spa=%v HEAD /docs/: status = %d body length = %d, want 200, 0", spa, head.Code, head.Body.Len())
		}

		rec := get(t, dir, "/docs", spa, nil)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "docs/" {
			t.Errorf("spa=%v GET /docs: status = %d location = %q, want 301 to docs/", spa, rec.Code, rec.Header().Get("Location"))
		}
	}
}
