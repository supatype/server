package pluginwidgets

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
			manifestUnreadable(w)
			return
		}
		// Only bundles that may run are listed, so Studio shows its built-in editor for the
		// rest instead of importing a URL that answers with an error.
		widgets := make([]published, 0, len(entries))
		for _, e := range entries {
			if store.servableOnDisk(e.SHA256) {
				widgets = append(widgets, published{Widget: e.Widget, URL: BundlePrefix + e.Bundle()})
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		utilities.WriteJSON(w, http.StatusOK, map[string][]published{"widgets": widgets})
	})
}

// BundleHandler answers GET /studio/plugins/widgets/<sha256>.js with a bundle the
// manifest names, and 404 for anything else.
//
// Public on purpose: a module `import()` cannot send Studio's bearer token. A bundle
// holds no project data, and nothing from the request reaches the filesystem but a
// hash the manifest names, whose file is checked to hash to that name.
//
// What proves a bundle is Supatype's code is its minisign signature, checked against
// the release key (Trust); the first-party list only says which plugins may name a
// widget at all. A development server also serves an unsigned bundle, as a plugin
// built from source has; a production server never does.
func BundleHandler(store *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hash := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, BundlePrefix), ".js")
		named, err := store.Names(hash)
		if err != nil {
			manifestUnreadable(w)
			return
		}
		if !named {
			http.NotFound(w, req)
			return
		}
		serveBundle(w, req, store, hash)
	})
}

// manifestUnreadable answers a request the manifest could not be read for. A broken
// manifest is the server's fault, so it is not a 404; the Store has logged why.
func manifestUnreadable(w http.ResponseWriter) {
	http.Error(w, "could not read the plugin widget manifest", http.StatusInternalServerError)
}

// serveBundle sends the bundle the manifest names by hash, once its bytes are shown
// to hash to that name and its signature to be Supatype's, leaving conditional and
// Range requests to http.ServeContent.
func serveBundle(w http.ResponseWriter, req *http.Request, store *Store, hash string) {
	f, err := os.Open(filepath.Join(store.Dir, hash+".js"))
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
	// A file the manifest names that may not run (its bytes or signature are not what
	// they should be) is the server's problem, not a missing page; the Store has logged why.
	if !store.bundleServable(hash, f, info) {
		http.Error(w, "plugin widget bundle may not be served", http.StatusInternalServerError)
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
