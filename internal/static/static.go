package static

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// maxOnTheFlyGzip caps streaming gzip for compressible text assets when no .gz exists on disk.
const maxOnTheFlyGzip = 4 << 20

func init() {
	_ = mime.AddExtensionType(".wasm", "application/wasm")
	_ = mime.AddExtensionType(".mjs", "text/javascript")
}

// CacheOpts configures Cache-Control for static.Handler.
// Empty HTML / HashedAssets use built-in defaults.
// Prefixes matches longest URL path prefix first (keys may omit a leading slash).
type CacheOpts struct {
	HTML         string
	HashedAssets string
	Prefixes     map[string]string
}

const (
	defaultHTMLCache   = "no-cache"
	defaultHashedCache = "public, max-age=31536000, immutable"
)

// Handler returns an http.Handler that serves static files from dir.
//
// A path with no file behind it is a 404 with no-store, carrying dir/404.html
// when the build ships one. When spa is true, the exception is a GET or HEAD
// that looks like a navigation (see wantsSPAFallback): that is served
// dir/index.html, the SPA client-side routing fallback.
//
// A directory, the root included, is served its own index.html. One without
// an index.html is the same 404, never a listing and never the SPA fallback.
//
// Cache headers use CacheOpts (defaults when empty): hashed bundle prefixes get a long immutable
// cache; .html and "/" get no-cache; SPA fallback uses the HTML policy.
//
// Precompressed assets: for request path P, if P.br or P.gz exists on disk and
// the client sends a matching Accept-Encoding token, that file is served with
// Content-Encoding set (prefer brotli over gzip when both are acceptable).
// If both uncompressed P and P.br exist, the precompressed file is preferred
// when the client accepts that encoding. When no precompressed sidecar exists,
// compressible text types may be gzip-compressed on the fly (small files only;
// Range requests skip on-the-fly gzip and use plain ServeContent).
func Handler(dir string, spa bool, cache CacheOpts) http.Handler {
	fs := http.FileServer(http.Dir(dir))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		setCacheHeaders(w, path, cache)

		if disk, enc, ok := pickVariant(dir, path, r); ok {
			serveVariant(w, r, dir, disk, enc, path)
			return
		}

		isDir, hasIndex := directoryIndex(dir, path)
		switch {
		case isDir && hasIndex:
			fs.ServeHTTP(w, r)
		case isDir:
			// Without an index.html, http.FileServer would list the directory, and
			// the SPA fallback would answer a bundle path with the app shell.
			serveNotFound(w, r, dir)
		case !anyRepresentableFile(dir, path):
			serveMiss(w, r, dir, spa, cache)
		default:
			fs.ServeHTTP(w, r)
		}
	})
}

// directoryIndex reports whether urlPath names a directory, the site root
// included, and whether that directory holds an index.html to serve.
func directoryIndex(dir, urlPath string) (isDir, hasIndex bool) {
	rel, ok := staticRelPath(urlPath)
	if !ok {
		return false, false
	}
	if rel == "" {
		rel = "."
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false, false
	}
	defer func() {
		_ = root.Close()
	}()

	fi, err := root.Stat(rel)
	if err != nil || !fi.IsDir() {
		return false, false
	}
	index, err := root.Stat(filepath.Join(rel, "index.html"))
	return true, err == nil && index.Mode().IsRegular()
}

// serveMiss answers a path with no file behind it.
func serveMiss(w http.ResponseWriter, r *http.Request, dir string, spa bool, cache CacheOpts) {
	if !spa || !wantsSPAFallback(r) {
		serveNotFound(w, r, dir)
		return
	}
	w.Header().Set("Cache-Control", cacheControlForSPAHTML(cache))
	http.ServeFile(w, r, filepath.Join(dir, "index.html"))
}

// wantsSPAFallback reports whether a missing path is a client-side route. Only
// a GET or HEAD can be a navigation. A last segment with no extension is one.
// A dotted one (/u/john.doe) is one only when the request is a browser
// navigation, which asks for text/html: a fetch with just */* is taken as a
// file request and gets the 404. Nothing under a hashed-asset prefix is a
// route, whatever it asks for.
func wantsSPAFallback(r *http.Request) bool {
	urlPath := r.URL.Path
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if isHashedAssetPath(urlPath) {
		return false
	}
	if path.Ext(path.Base(urlPath)) == "" {
		return true
	}
	return headerHasToken(r.Header.Get("Accept"), "text/html")
}

// serveNotFound answers a miss with 404 and no-store, so neither the hashed-asset
// policy nor a CDN holds on to a miss the next deploy may fill. The body is
// dir/404.html when the build ships one.
func serveNotFound(w http.ResponseWriter, r *http.Request, dir string) {
	w.Header().Set("Cache-Control", "no-store")
	page, ok := readNotFoundPage(dir)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(page)))
	w.WriteHeader(http.StatusNotFound)
	if r.Method != http.MethodHead {
		_, _ = w.Write(page)
	}
}

func readNotFoundPage(dir string) ([]byte, bool) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, false
	}
	defer func() {
		_ = root.Close()
	}()
	page, err := root.ReadFile("404.html")
	if err != nil {
		return nil, false
	}
	return page, true
}

func anyRepresentableFile(dir, urlPath string) bool {
	rel, ok := staticRelPath(urlPath)
	if !ok {
		return false
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer func() {
		_ = root.Close()
	}()

	if fi, err := root.Stat(rel); err == nil && fi.Mode().IsRegular() {
		return true
	}
	if _, err := root.Stat(rel + ".br"); err == nil {
		return true
	}
	if _, err := root.Stat(rel + ".gz"); err == nil {
		return true
	}
	return false
}

func pickVariant(dir, urlPath string, r *http.Request) (diskPath, contentEncoding string, ok bool) {
	rel, ok := staticRelPath(urlPath)
	if !ok {
		return "", "", false
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", "", false
	}
	defer func() {
		_ = root.Close()
	}()

	fi, err := root.Stat(rel)
	if err == nil && fi.IsDir() {
		return "", "", false
	}

	brOK := headerHasToken(r.Header.Get("Accept-Encoding"), "br")
	gzipOK := headerHasToken(r.Header.Get("Accept-Encoding"), "gzip")

	brRel := rel + ".br"
	gzRel := rel + ".gz"
	brInfo, brErr := root.Stat(brRel)
	gzInfo, gzErr := root.Stat(gzRel)
	brExists := brErr == nil && brInfo.Mode().IsRegular()
	gzExists := gzErr == nil && gzInfo.Mode().IsRegular()

	if err == nil && fi.Mode().IsRegular() {
		if brOK && brExists {
			return brRel, "br", true
		}
		if gzipOK && gzExists {
			return gzRel, "gzip", true
		}
		if gzipOK && !gzExists && r.Header.Get("Range") == "" &&
			compressiblePath(rel) && fi.Size() > 0 && fi.Size() <= maxOnTheFlyGzip {
			return rel, "gzip-dynamic", true
		}
		return rel, "", true
	}

	if brOK && brExists {
		return brRel, "br", true
	}
	if gzipOK && gzExists {
		return gzRel, "gzip", true
	}

	return "", "", false
}

func staticRelPath(urlPath string) (string, bool) {
	rel := filepath.FromSlash(strings.TrimPrefix(urlPath, "/"))
	if rel == "" {
		return rel, true
	}
	if !filepath.IsLocal(rel) {
		return "", false
	}
	return rel, true
}

// headerHasToken reports whether a comma-separated header such as Accept or
// Accept-Encoding lists want, ignoring parameters like q-values and case.
func headerHasToken(header, want string) bool {
	if header == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		token := strings.TrimSpace(strings.Split(part, ";")[0])
		if strings.EqualFold(token, want) {
			return true
		}
	}
	return false
}

func serveVariant(w http.ResponseWriter, r *http.Request, dir, diskPath, contentEncoding, logicalURLPath string) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer func() {
		_ = root.Close()
	}()

	f, err := root.Open(diskPath)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer func() {
		_ = f.Close()
	}()

	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	ext := filepath.Ext(logicalURLPath)
	ctype := mime.TypeByExtension(ext)
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)

	if contentEncoding == "gzip-dynamic" {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		zw := gzip.NewWriter(w)
		if _, err := io.Copy(zw, f); err != nil {
			return
		}
		if err := zw.Close(); err != nil {
			return
		}
		return
	}

	if contentEncoding != "" {
		w.Header().Set("Content-Encoding", contentEncoding)
		w.Header().Set("Vary", "Accept-Encoding")
	}

	name := filepath.Base(logicalURLPath)
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func compressiblePath(fullPath string) bool {
	switch strings.ToLower(filepath.Ext(fullPath)) {
	case ".js", ".mjs", ".css", ".html", ".htm", ".svg", ".json", ".xml", ".txt", ".map":
		return true
	default:
		return false
	}
}

func cacheControlForSPAHTML(o CacheOpts) string {
	if s := strings.TrimSpace(o.HTML); s != "" {
		return s
	}
	return defaultHTMLCache
}

func normalizeURLPrefix(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// cacheControlForPath returns the Cache-Control value for this URL path, or "" when unset.
func cacheControlForPath(path string, o CacheOpts) string {
	path = normalizeURLPrefix(path)
	var bestPref string
	var bestCC string
	for pk, cc := range o.Prefixes {
		pref := normalizeURLPrefix(pk)
		if pref == "" {
			continue
		}
		if strings.HasPrefix(path, pref) && len(pref) > len(bestPref) {
			bestPref = pref
			bestCC = cc
		}
	}
	if bestPref != "" {
		return strings.TrimSpace(bestCC)
	}
	if isHashedAssetPath(path) {
		if s := strings.TrimSpace(o.HashedAssets); s != "" {
			return s
		}
		return defaultHashedCache
	}
	if strings.HasSuffix(path, ".html") || path == "/" {
		if s := strings.TrimSpace(o.HTML); s != "" {
			return s
		}
		return defaultHTMLCache
	}
	return ""
}

// setCacheHeaders sets Cache-Control when cacheControlForPath returns non-empty.
func setCacheHeaders(w http.ResponseWriter, path string, o CacheOpts) {
	if cc := cacheControlForPath(path, o); cc != "" {
		w.Header().Set("Cache-Control", cc)
	}
}

// isHashedAssetPath returns true for paths that contain hashed content and
// can be cached indefinitely at the edge.
var hashedAssetPrefixes = []string{
	"/assets/",
	"/_next/",
	"/_astro/",
	"/static/",
	"/_app/",
	"/_nuxt/",
}

func isHashedAssetPath(path string) bool {
	for _, prefix := range hashedAssetPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
