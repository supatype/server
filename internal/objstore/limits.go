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
// is written, so a refused overwrite leaves the old object in place. A declared
// length over the limit is refused before any of the body is read, and a body
// that runs past the limit is refused as soon as it does, after what is left
// of it has been drained up to drainCap.
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
	if int64(len(body)) > limit {
		drainRefused(w, r.Body)
		refuseSize(w, unknownSize, limit)
		return nil, false
	}
	return bytes.NewReader(body), true
}

// drainRefused reads and drops the rest of a refused body, up to drainCap, so
// a client still sending finishes and then reads the refusal. Go's server
// discards at most 256 KB of an unread body itself and then closes the
// connection, which a client still sending sees as a reset rather than a 413.
// A body that runs past the cap, or fails to read, is abandoned and the
// connection closed after the response.
func drainRefused(w http.ResponseWriter, body io.Reader) {
	n, err := io.Copy(io.Discard, io.LimitReader(body, drainCap+1))
	if err != nil || n > drainCap {
		w.Header().Set("Connection", "close")
	}
}

// drainCap is how much of a refused body is read and dropped so the client
// can finish sending and read the 413. 32 MiB covers the ordinary case, a file
// somewhat too big for a bucket's limit, and reads in well under a second on
// the local connections native dev serves. It also bounds what a client can
// make the server read: nothing past it is read, so a stream with no end costs
// at most this much before the connection is closed.
const drainCap = 32 << 20

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

// unknownSize stands for the size of a body refused before its end was read.
const unknownSize = -1

// refuseSize answers an upload over the bucket's size limit. The size is
// stated when it is known, as the storage service states it.
func refuseSize(w http.ResponseWriter, size, limit int64) {
	msg := fmt.Sprintf("File size exceeds the maximum allowed size of %s", formatBytes(limit))
	if size != unknownSize {
		msg = fmt.Sprintf("File size %s exceeds the maximum allowed size of %s", formatBytes(size), formatBytes(limit))
	}
	utilities.WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": msg})
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
