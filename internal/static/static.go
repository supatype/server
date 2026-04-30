package static

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Handler returns an http.Handler that serves static files from dir.
//
// When spa is true, requests for paths that don't match an existing file are
// served with dir/index.html (SPA client-side routing fallback).
//
// Cache headers are applied automatically:
//   - Paths under /assets/, /_next/, /_astro/, /static/, /_app/ get
//     Cache-Control: public, max-age=31536000, immutable
//   - All .html files (including the SPA fallback) get Cache-Control: no-cache
func Handler(dir string, spa bool) http.Handler {
	fs := http.FileServer(http.Dir(dir))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Determine cache header before serving.
		setCacheHeaders(w, path)

		if spa {
			// Check if the file exists on disk. If not, serve index.html.
			fullPath := filepath.Join(dir, filepath.FromSlash(path))
			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				// Serve index.html for the SPA catch-all.
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeFile(w, r, filepath.Join(dir, "index.html"))
				return
			}
		}

		fs.ServeHTTP(w, r)
	})
}

// setCacheHeaders sets the appropriate Cache-Control header for the path.
func setCacheHeaders(w http.ResponseWriter, path string) {
	if isHashedAssetPath(path) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	if strings.HasSuffix(path, ".html") || path == "/" {
		w.Header().Set("Cache-Control", "no-cache")
		return
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
