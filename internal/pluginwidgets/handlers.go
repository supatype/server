package pluginwidgets

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/static"
	"github.com/supatype/server/internal/utilities"
)

// BundlePrefix is where a bundle is served, followed by `<sha256>.js`.
const BundlePrefix = "/studio/plugins/widgets/"

// published is one widget as Studio reads it: what it edits and where to import it from.
type published struct {
	Widget
	URL string `json:"url"`
}

// ManifestHandler answers GET /studio/plugins with the widgets Studio may load. The
// caller's Studio session is checked by the route, not here.
func ManifestHandler(store *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		entries, err := store.Entries()
		if err != nil {
			manifestUnreadable(w, err)
			return
		}
		widgets := make([]published, 0, len(entries))
		for _, e := range entries {
			widgets = append(widgets, published{Widget: e.Widget, URL: BundlePrefix + e.Bundle()})
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
		named, err := store.Names(hash)
		if err != nil {
			manifestUnreadable(w, err)
			return
		}
		if !named {
			http.NotFound(w, req)
			return
		}
		serveBundle(w, req, filepath.Join(store.Dir, hash+".js"))
	})
}

// manifestUnreadable answers a request the manifest could not be read for. A broken
// manifest is the server's fault, so it is logged and is not a 404.
func manifestUnreadable(w http.ResponseWriter, err error) {
	logrus.WithError(err).Error("plugin widgets: could not read the manifest")
	http.Error(w, "could not read the plugin widget manifest", http.StatusInternalServerError)
}

// serveBundle sends the bundle at path, leaving conditional and Range requests to
// http.ServeContent.
func serveBundle(w http.ResponseWriter, req *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, req)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	// The URL is the content's hash, so its bytes never change.
	h.Set("Cache-Control", static.DefaultHashedCache)
	// Studio on another origin (Cloud's studio.<domain>) imports it from here.
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	http.ServeContent(w, req, info.Name(), info.ModTime(), f)
}
