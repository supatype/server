package gateway

import (
	"net/http"
	"path"

	"github.com/supatype/server/internal/objstore"
)

// safeStorageWriter hardens what the storage service sends back with the same
// header policy the local object store applies, so an uploaded page or SVG is
// never rendered on the gateway origin. The download is named after the last
// segment of the request path unless the upstream already named it.
func safeStorageWriter(w http.ResponseWriter, req *http.Request) http.ResponseWriter {
	return objstore.SafeObjectWriter(w, path.Base(req.URL.Path))
}
