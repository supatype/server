package pluginwidgets

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/utilities"
)

// BundlePrefix is where a bundle is served, followed by `<sha256>.js`.
const BundlePrefix = "/studio/plugins/widgets/"

// published is one widget as Studio reads it: what it edits and where to import it from.
type published struct {
	Plugin string `json:"plugin"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	URL    string `json:"url"`
}

// ManifestHandler answers GET /studio/plugins with the widgets Studio may load. The
// caller's Studio session is checked by the route, not here.
func ManifestHandler(store *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		entries, err := store.Entries()
		if err != nil {
			logrus.WithError(err).Error("plugin widgets: could not read the manifest")
			http.Error(w, "could not read the plugin widget manifest", http.StatusInternalServerError)
			return
		}
		widgets := make([]published, 0, len(entries))
		for _, e := range entries {
			widgets = append(widgets, published{Plugin: e.Plugin, Name: e.Name, Kind: e.Kind, URL: BundlePrefix + e.Bundle()})
		}
		w.Header().Set("Cache-Control", "no-cache")
		utilities.WriteJSON(w, http.StatusOK, map[string][]published{"widgets": widgets})
	})
}

// BundleHandler answers GET /studio/plugins/widgets/<sha256>.js with a bundle the
// manifest names, and 404 for anything else.
//
// Public on purpose: a module `import()` cannot send Studio's bearer token. That is
// safe because a bundle is Supatype's own code, already published on npm, holding no
// project data, and because nothing from the request reaches the filesystem but a
// hash the manifest names.
func BundleHandler(store *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hash := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, BundlePrefix), ".js")
		entry, ok, err := store.Bundle(hash)
		if err != nil || !ok {
			http.NotFound(w, req)
			return
		}
		body, err := os.ReadFile(filepath.Join(store.Dir, entry.Bundle()))
		if err != nil {
			http.NotFound(w, req)
			return
		}
		writeBundle(w, body)
	})
}

func writeBundle(w http.ResponseWriter, body []byte) {
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	// The URL is the content's hash, so its bytes never change.
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	// Studio on another origin (Cloud's studio.<domain>) imports it from here.
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	_, _ = w.Write(body)
}
