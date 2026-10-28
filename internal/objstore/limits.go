package objstore

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/supatype/server/internal/utilities"
)

// The bucket's own upload limits, answered with the storage service's status
// codes and bodies (packages/storage, middleware/storage-limits.ts) so that code
// written against native dev sees the same refusals in Docker. The service's
// tier limits and project quota have no meaning here and are not applied.

// folderPlaceholder is the empty object that makes a folder show. Its empty
// form is exempt from the bucket's type rule.
const folderPlaceholder = ".emptyFolderPlaceholder"

// acceptUpload answers an upload the bucket refuses and otherwise returns the
// body to store. The type is checked first, then the size, as the service does.
func acceptUpload(w http.ResponseWriter, r *http.Request, b *Bucket, objPath, contentType string) (io.Reader, bool) {
	placeholder := path.Base(objPath) == folderPlaceholder && r.ContentLength == 0
	if !placeholder && !typeAllowed(b.AllowedMimeTypes, contentType) {
		utilities.WriteJSON(w, http.StatusUnsupportedMediaType, map[string]any{
			"error": fmt.Sprintf("Content type '%s' is not allowed for bucket '%s'. Allowed types: %s",
				contentType, b.Name, strings.Join(b.AllowedMimeTypes, ", ")),
			"allowed": b.AllowedMimeTypes,
		})
		return nil, false
	}
	if b.FileSizeLimit == nil || *b.FileSizeLimit < 0 {
		return r.Body, true
	}
	return limitedBody(w, r, *b.FileSizeLimit)
}

// limitedBody reads a body of at most limit bytes into memory before anything
// is written, so a refused overwrite leaves the old object in place.
func limitedBody(w http.ResponseWriter, r *http.Request, limit int64) (io.Reader, bool) {
	if r.ContentLength > limit {
		refuseSize(w, r.ContentLength, limit)
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read request body")
		return nil, false
	}
	if size := int64(len(body)); size > limit {
		// The rest is read only to say how large the file was.
		rest, _ := io.Copy(io.Discard, r.Body)
		refuseSize(w, size+rest, limit)
		return nil, false
	}
	return bytes.NewReader(body), true
}

// typeAllowed reports whether a media type is one the bucket allows: any when
// it lists none, otherwise an exact match or a "type/*" wildcard, ignoring case.
func typeAllowed(allowed []string, mediaType string) bool {
	if len(allowed) == 0 {
		return true
	}
	mediaType = strings.ToLower(mediaType)
	for _, a := range allowed {
		a = strings.ToLower(a)
		if mediaType == a || (strings.HasSuffix(a, "/*") && strings.HasPrefix(mediaType, strings.TrimSuffix(a, "*"))) {
			return true
		}
	}
	return false
}

// refuseSize answers an upload over the bucket's size limit.
func refuseSize(w http.ResponseWriter, size, limit int64) {
	utilities.WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
		"error": fmt.Sprintf("File size %s exceeds the maximum allowed size of %s", formatBytes(size), formatBytes(limit)),
	})
}

// formatBytes renders a size the way the storage service's messages do.
func formatBytes(n int64) string {
	const kb, mb, gb = 1024, 1024 * 1024, 1024 * 1024 * 1024
	switch {
	case n < kb:
		return fmt.Sprintf("%d B", n)
	case n < mb:
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	case n < gb:
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/gb)
	}
}
