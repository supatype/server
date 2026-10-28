package objstore

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

func cleanBucketID(bucket string) (string, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" || strings.ContainsAny(bucket, `/\`) || !filepath.IsLocal(bucket) {
		return "", fmt.Errorf("invalid bucket id")
	}
	return bucket, nil
}

func cleanObjectPath(objPath string, allowEmpty bool) (string, error) {
	objPath = strings.TrimPrefix(objPath, "/")
	if objPath == "" {
		if allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("invalid object path")
	}

	// The traversal check runs on the path as it will be on disk: an encoded
	// segment is one plain file name, so a backslash or dot it carries can
	// never act as a separator or a parent reference.
	rel := filepath.FromSlash(diskPath(objPath))
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("invalid object path")
	}
	return rel, nil
}

func (s *store) bucketDir(bucket string) (string, error) {
	bucket, err := cleanBucketID(bucket)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, bucket), nil
}

// objectPaths returns where an object and its sidecar live, relative to the
// storage root, validating the bucket and path once.
//
// The two used to be built by separate functions that each validated the same
// two strings. Any caller wanting both therefore validated twice and had to
// handle a second failure that could not happen: if the first call refused the
// input, the second never ran.
func (s *store) objectPaths(bucket, objPath string) (fileRel, metaRel string, err error) {
	bucket, err = cleanBucketID(bucket)
	if err != nil {
		return "", "", err
	}
	rel, err := cleanObjectPath(objPath, false)
	if err != nil {
		return "", "", err
	}
	dir, file := filepath.Split(rel)
	return filepath.Join(bucket, rel), filepath.Join(bucket, ".meta", dir, file+".json"), nil
}

func (s *store) objectFileRel(bucket, objPath string) (string, error) {
	fileRel, _, err := s.objectPaths(bucket, objPath)
	return fileRel, err
}

func (s *store) objectMetaRel(bucket, objPath string) (string, error) {
	_, metaRel, err := s.objectPaths(bucket, objPath)
	return metaRel, err
}

// routeParam returns a route parameter as the caller meant it, answering the
// request itself when it cannot be decoded.
//
// The client escapes each segment of a name with encodeURIComponent, which
// escapes characters Go leaves alone in a path (':' '@' '+' and others). Go
// then keeps that spelling as RawPath, chi routes on it, and the parameter
// arrives still escaped. With no RawPath chi routed on the decoded Path, so
// the parameter is already decoded, and decoding it again would read a name
// containing a literal "%41" as "A".
func routeParam(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	value := chi.URLParam(r, key)
	if r.URL.RawPath == "" {
		return value, true
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path")
		return "", false
	}
	return decoded, true
}

// objectRoute returns the bucket and object path of an object route.
func objectRoute(w http.ResponseWriter, r *http.Request) (bucket, objPath string, ok bool) {
	if bucket, ok = routeParam(w, r, "bucket"); !ok {
		return "", "", false
	}
	if objPath, ok = routeParam(w, r, "*"); !ok {
		return "", "", false
	}
	return bucket, strings.TrimPrefix(objPath, "/"), true
}

// ─── Object names on disk ─────────────────────────────────────────────────────
//
// An object name is stored as a path on disk, one directory or file per
// segment. A segment the filesystem can hold as it is keeps its name, so
// everything stored before this encoding existed is still found. Any other
// segment is stored as encodedMarker followed by the segment with '%' and
// every character the filesystem refuses percent-encoded. Windows refuses
// <>:"|?*\ and control characters, renames a trailing dot or space away,
// and reserves device names such as CON; elsewhere only NUL is refused.
//
// A segment that already starts with encodedMarker is encoded too, so a
// stored name that starts with the marker is always an encoded one and reads
// back as the name it came from.

// encodedMarker starts a stored segment that holds a name in encoded form.
const encodedMarker = "%%"

// windowsForbidden is the characters Windows refuses in a file name, besides
// control characters.
const windowsForbidden = `<>:"|?*\`

// onWindows is whether names are stored under Windows rules here.
const onWindows = runtime.GOOS == "windows"

// diskPath is objPath with each segment as diskSegment stores it here.
func diskPath(objPath string) string {
	segments := strings.Split(objPath, "/")
	for i, segment := range segments {
		segments[i] = diskSegment(segment, onWindows)
	}
	return strings.Join(segments, "/")
}

// diskSegment is the file name a segment of an object name is stored under,
// under Windows rules when windows is set.
func diskSegment(segment string, windows bool) string {
	if storableAsIs(segment, windows) {
		return segment
	}
	return encodedMarker + escapeSegment(segment)
}

// storableAsIs reports whether a segment can be its own file name. An empty
// segment and the dot segments are path structure, not names: they are kept
// as they are for the traversal check to judge.
func storableAsIs(segment string, windows bool) bool {
	switch {
	case segment == "" || segment == "." || segment == "..":
		return true
	case strings.HasPrefix(segment, encodedMarker) || strings.ContainsRune(segment, 0):
		return false
	case !windows:
		return true
	}
	return !strings.ContainsFunc(segment, refusedOnWindows) &&
		!strings.HasSuffix(segment, ".") && !strings.HasSuffix(segment, " ") &&
		!windowsReserved(segment)
}

func refusedOnWindows(r rune) bool {
	return r < 0x20 || strings.ContainsRune(windowsForbidden, r)
}

// escapeSegment percent-encodes '%', the characters Windows refuses, and a
// trailing dot or space. The result is the same on every OS.
func escapeSegment(segment string) string {
	var b strings.Builder
	last := len(segment) - 1
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if c == '%' || c < 0x20 || strings.IndexByte(windowsForbidden, c) >= 0 || (i == last && (c == '.' || c == ' ')) {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// objectSegment is the segment of an object name a stored file name holds.
// Only a name escapeSegment could have written is decoded; anything else,
// such as a file placed by hand, is read as the name it has.
func objectSegment(diskName string) string {
	body, ok := strings.CutPrefix(diskName, encodedMarker)
	if !ok {
		return diskName
	}
	name, err := url.PathUnescape(body)
	if err != nil || name == "" || name == "." || name == ".." || escapeSegment(name) != body {
		return diskName
	}
	return name
}

// windowsReserved reports whether Windows takes name for a device rather than
// a file. On Windows that is asked of the OS through filepath.IsLocal, which
// knows that Windows 11 no longer reserves "CON.txt". Elsewhere, where it is
// only asked to show the encoding, every reserved base name counts, with or
// without an extension.
func windowsReserved(name string) bool {
	if runtime.GOOS == "windows" {
		return !filepath.IsLocal(name)
	}
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	for _, device := range []string{"COM", "LPT"} {
		// Over-matching only encodes a name that did not need it, which is harmless.
		if n, ok := strings.CutPrefix(base, device); ok && utf8.RuneCountInString(n) == 1 && strings.Contains("0123456789¹²³", n) {
			return true
		}
	}
	return false
}
