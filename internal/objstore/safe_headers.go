package objstore

import (
	"mime"
	"net/http"
	"strings"
)

// sandboxPolicy stops a served object from running script or loading anything,
// even if a browser ends up rendering it.
const sandboxPolicy = "sandbox; default-src 'none'"

// activeContentTypes are media types a browser will execute or render as a
// document on the serving origin. Any type ending in +xml is active too.
var activeContentTypes = map[string]bool{
	"text/html":                 true,
	"image/svg+xml":             true,
	"application/xhtml+xml":     true,
	"text/xml":                  true,
	"application/xml":           true,
	"application/javascript":    true,
	"text/javascript":           true,
	"application/ecmascript":    true,
	"text/ecmascript":           true,
	"application/x-javascript":  true,
	"multipart/x-mixed-replace": true,
}

// ApplySafeObjectHeaders hardens the response headers for a user-supplied
// object so it cannot become stored XSS on the storage origin. Every object is
// marked nosniff. An active content type is also forced to download as an
// attachment inside a sandbox CSP. name is the object's base name, used as the
// download filename when the response does not already name one.
func ApplySafeObjectHeaders(h http.Header, name string) {
	h.Set("X-Content-Type-Options", "nosniff")
	if !isActiveContentType(h.Get("Content-Type")) {
		return
	}
	h.Set("Content-Disposition", attachmentDisposition(h.Get("Content-Disposition"), name))
	h.Set("Content-Security-Policy", sandboxPolicy)
}

// isActiveContentType compares the media type with parameters stripped and
// case ignored. A missing type, or one that cannot be read at all, is treated
// as active, because a browser left to guess may guess HTML.
func isActiveContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType, _, _ = strings.Cut(contentType, ";")
		mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	}
	if mediaType == "" {
		return true
	}
	return activeContentTypes[mediaType] || strings.HasSuffix(mediaType, "+xml")
}

// attachmentDisposition keeps a filename the response already carries,
// otherwise names the download after the object, and always says attachment.
func attachmentDisposition(existing, name string) string {
	if _, params, err := mime.ParseMediaType(existing); err == nil && params["filename"] != "" {
		name = params["filename"]
	}
	if name == "" {
		return "attachment"
	}
	if formatted := mime.FormatMediaType("attachment", map[string]string{"filename": name}); formatted != "" {
		return formatted
	}
	return "attachment"
}

// safeObjectWriter applies ApplySafeObjectHeaders at the moment the final
// status is written, after whatever served the object has set Content-Type.
type safeObjectWriter struct {
	http.ResponseWriter
	name    string
	applied bool
}

// SafeObjectWriter wraps w so the object it serves leaves with safe headers.
func SafeObjectWriter(w http.ResponseWriter, name string) http.ResponseWriter {
	return &safeObjectWriter{ResponseWriter: w, name: name}
}

func (w *safeObjectWriter) WriteHeader(code int) {
	if !w.applied && code >= http.StatusOK {
		w.applied = true
		ApplySafeObjectHeaders(w.Header(), w.name)
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *safeObjectWriter) Write(b []byte) (int, error) {
	if !w.applied {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer for flushes.
func (w *safeObjectWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
