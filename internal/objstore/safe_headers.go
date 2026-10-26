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

// octetStream is the type of an object whose own type cannot be trusted.
const octetStream = "application/octet-stream"

// ApplySafeObjectHeaders hardens the response headers for a user-supplied
// object so it cannot become stored XSS on the storage origin. Every object is
// marked nosniff, sandboxed by CSP, and served with its type as parsed rather
// than as written. An active content type is also forced to download as an
// attachment. name is the object's base name, used as the download filename
// when the response does not already name one.
func ApplySafeObjectHeaders(h http.Header, name string) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", sandboxPolicy)
	served, active := servedContentType(h.Get("Content-Type"))
	if served == "" {
		// Present but empty, so net/http does not sniff one in from the body.
		h["Content-Type"] = nil
	} else {
		h.Set("Content-Type", served)
	}
	if active {
		h.Set("Content-Disposition", attachmentDisposition(h.Get("Content-Disposition"), name))
	}
}

// parseContentType reads a Content-Type as exactly one type/subtype with its
// parameters. A comma anywhere is refused: a browser may read
// `text/plain, text/html` as a list and pick the type it likes.
func parseContentType(contentType string) (string, map[string]string, bool) {
	if strings.Contains(contentType, ",") {
		return "", nil, false
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || strings.Count(mediaType, "/") != 1 {
		return "", nil, false
	}
	return mediaType, params, true
}

// servedContentType is the canonical form of contentType and whether it is
// active. A missing type, or one that cannot be read, is active, because a
// browser left to guess may guess HTML; an unreadable one is served as
// octetStream, and a missing one stays missing.
func servedContentType(contentType string) (string, bool) {
	mediaType, params, ok := parseContentType(contentType)
	if !ok {
		if strings.TrimSpace(contentType) == "" {
			return "", true
		}
		return octetStream, true
	}
	canonical := mime.FormatMediaType(mediaType, params)
	if canonical == "" {
		return octetStream, true
	}
	return canonical, activeContentTypes[mediaType] || strings.HasSuffix(mediaType, "+xml")
}

// storedContentType is the type an upload is kept under: its media type
// without parameters, or octetStream when the header is missing, cannot be
// read, or lists more than one type.
func storedContentType(contentType string) string {
	mediaType, _, ok := parseContentType(contentType)
	if !ok {
		return octetStream
	}
	return mediaType
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
