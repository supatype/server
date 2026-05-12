package static

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
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
// When spa is true, requests for paths that don't match an existing file are
// served with dir/index.html (SPA client-side routing fallback).
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
			serveVariant(w, r, disk, enc, path)
			return
		}

		if spa && !anyRepresentableFile(dir, path) {
			w.Header().Set("Cache-Control", cacheControlForSPAHTML(cache))
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}

		fs.ServeHTTP(w, r)
	})
}

func anyRepresentableFile(dir, urlPath string) bool {
	rel := strings.TrimPrefix(urlPath, "/")
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if fi, err := os.Stat(full); err == nil {
		if fi.IsDir() || fi.Mode().IsRegular() {
			return true
		}
	}
	if _, err := os.Stat(full + ".br"); err == nil {
		return true
	}
	if _, err := os.Stat(full + ".gz"); err == nil {
		return true
	}
	return false
}

func pickVariant(dir, urlPath string, r *http.Request) (diskPath, contentEncoding string, ok bool) {
	rel := strings.TrimPrefix(urlPath, "/")
	fullPath := filepath.Join(dir, filepath.FromSlash(rel))

	fi, err := os.Stat(fullPath)
	if err == nil && fi.IsDir() {
		return "", "", false
	}

	brOK := acceptEncodingToken(r.Header.Get("Accept-Encoding"), "br")
	gzipOK := acceptEncodingToken(r.Header.Get("Accept-Encoding"), "gzip")

	brPath := fullPath + ".br"
	gzPath := fullPath + ".gz"
	brInfo, brErr := os.Stat(brPath)
	gzInfo, gzErr := os.Stat(gzPath)
	brExists := brErr == nil && brInfo.Mode().IsRegular()
	gzExists := gzErr == nil && gzInfo.Mode().IsRegular()

	if err == nil && fi.Mode().IsRegular() {
		if brOK && brExists {
			return brPath, "br", true
		}
		if gzipOK && gzExists {
			return gzPath, "gzip", true
		}
		if gzipOK && !gzExists && r.Header.Get("Range") == "" &&
			compressiblePath(fullPath) && fi.Size() > 0 && fi.Size() <= maxOnTheFlyGzip {
			return fullPath, "gzip-dynamic", true
		}
		return fullPath, "", true
	}

	if brOK && brExists {
		return brPath, "br", true
	}
	if gzipOK && gzExists {
		return gzPath, "gzip", true
	}

	return "", "", false
}

func acceptEncodingToken(ae, enc string) bool {
	if ae == "" {
		return false
	}
	for _, part := range strings.Split(ae, ",") {
		token := strings.TrimSpace(strings.Split(part, ";")[0])
		if strings.EqualFold(token, enc) {
			return true
		}
	}
	return false
}

func serveVariant(w http.ResponseWriter, r *http.Request, diskPath, contentEncoding, logicalURLPath string) {
	f, err := os.Open(diskPath)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close() //nolint:errcheck

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
