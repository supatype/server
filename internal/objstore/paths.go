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

// sidecarDir is the directory in each bucket that holds the objects' sidecars.
// It is this store's own bookkeeping, never an object or a folder of objects.
const sidecarDir = ".meta"

// inSidecarTree reports whether rel, a path relative to a bucket, resolves into
// the sidecar tree, however it is spelled: "./.meta" and "a/../.meta" are the
// same directory. Case is ignored because the file systems of Windows and
// macOS ignore it, so ".META" opens the same directory there.
func inSidecarTree(rel string) bool {
	first, _, _ := strings.Cut(filepath.ToSlash(filepath.Clean(rel)), "/")
	return strings.EqualFold(first, sidecarDir)
}

// objectPaths returns where an object and its sidecar live, relative to the
// storage root, validating the bucket and path once.
//
// The two used to be built by separate functions that each validated the same
// two strings. Any caller wanting both therefore validated twice and had to
// handle a second failure that could not happen: if the first call refused the
// input, the second never ran.
func (s *store) objectPaths(bucket, objPath string) (fileRel, metaRel string, err error) {
	bucket, rel, err := cleanObject(bucket, objPath)
	if err != nil {
		return "", "", err
	}
	return bucketPaths(bucket, rel)
}

// cleanObject is the bucket id and the object's path on disk relative to its
// bucket, or an error when either is refused.
func cleanObject(bucket, objPath string) (cleanBucket, rel string, err error) {
	if cleanBucket, err = cleanBucketID(bucket); err != nil {
		return "", "", err
	}
	if rel, err = cleanObjectPath(objPath, false); err != nil {
		return "", "", err
	}
	return cleanBucket, rel, nil
}

// bucketPaths is where the object at rel in bucket and its sidecar live,
// relative to the storage root, refusing a rel inside the sidecar tree.
func bucketPaths(bucket, rel string) (fileRel, metaRel string, err error) {
	if inSidecarTree(rel) {
		return "", "", fmt.Errorf("invalid object path")
	}
	dir, file := filepath.Split(rel)
	return filepath.Join(bucket, rel), filepath.Join(bucket, sidecarDir, dir, file+".json"), nil
}

// legacyPaths is where the object objPath and its sidecar were stored before
// names were encoded, when that is somewhere other than current, where the
// object is stored now relative to its bucket. bucket is already validated.
// On every OS a name starting with encodedMarker was stored as written, and
// on Windows a backslash in a name split it into folders. The legacy location
// passes the same checks as the current one, so it never leaves the bucket
// or reaches the sidecar tree. It is only ever read from or removed: writes
// always use the encoded name.
//
// Windows also dropped a trailing dot or space from a name, so the old file
// of "notes." is "notes", which may as well be the object "notes" itself. A
// name with such a part therefore has no legacy location under Windows rules:
// reading or removing it must never reach another object.
func (r nameRules) legacyPaths(bucket, current, objPath string) (fileRel, metaRel string, ok bool) {
	rel := filepath.FromSlash(strings.TrimPrefix(objPath, "/"))
	if rel == current || !filepath.IsLocal(rel) || (r.windows && renamesAPart(rel)) {
		return "", "", false
	}
	fileRel, metaRel, err := bucketPaths(bucket, rel)
	return fileRel, metaRel, err == nil
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

// nameRules is how a file system judges an object name's segments as file
// names. The rules of this OS are chosen once, as hostRules; a test builds the
// other OS's rules to check the encoding under them.
type nameRules struct {
	// windows is set where Windows refuses windowsForbidden and control
	// characters and drops a trailing dot or space from a name.
	windows bool
	// reserved reports whether the OS takes a name for a device rather than
	// a file. It is consulted only under Windows rules.
	reserved func(name string) bool
}

// hostRules are the rules of this OS. On Windows a device name is asked of
// the OS through filepath.IsLocal, which knows that Windows 11 no longer
// reserves "CON.txt".
var hostRules = nameRules{windows: onWindows, reserved: func(name string) bool { return !filepath.IsLocal(name) }}

// refusedByte reports whether Windows refuses c in a file name. Every byte it
// refuses is ASCII, so no byte of a multi-byte character is refused.
func refusedByte(c byte) bool {
	return c < 0x20 || strings.IndexByte(windowsForbidden, c) >= 0
}

// renamedSuffix reports whether Windows renames name by dropping its end: a
// trailing dot or space.
func renamedSuffix(name string) bool {
	return strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ")
}

// renamesAPart reports whether any part of rel, split at either slash, would
// be renamed by Windows. The dot segments are path structure, not names, and
// are left to the traversal check.
func renamesAPart(rel string) bool {
	for part := range strings.FieldsFuncSeq(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part != "." && part != ".." && renamedSuffix(part) {
			return true
		}
	}
	return false
}

// diskPath is objPath with each segment as hostRules store it. A path none of
// whose segments needs encoding, which is nearly every path, comes back as
// the same string without being copied.
func diskPath(objPath string) string {
	return mapSegments(objPath, hostRules.diskSegment)
}

// mapSegments is objPath with f applied to each slash-separated segment, or
// objPath itself, uncopied, when f changes no segment.
func mapSegments(objPath string, f func(string) string) string {
	for segment := range strings.SplitSeq(objPath, "/") {
		if f(segment) != segment {
			segments := strings.Split(objPath, "/")
			for i, s := range segments {
				segments[i] = f(s)
			}
			return strings.Join(segments, "/")
		}
	}
	return objPath
}

// diskSegment is the file name a segment of an object name is stored under.
func (r nameRules) diskSegment(segment string) string {
	if r.storableAsIs(segment) {
		return segment
	}
	return encodedMarker + escapeSegment(segment)
}

// storableAsIs reports whether a segment can be its own file name. An empty
// segment and the dot segments are path structure, not names: they are kept
// as they are for the traversal check to judge.
func (r nameRules) storableAsIs(segment string) bool {
	switch {
	case segment == "" || segment == "." || segment == "..":
		return true
	case strings.HasPrefix(segment, encodedMarker) || strings.ContainsRune(segment, 0):
		return false
	case !r.windows:
		return true
	}
	return !strings.ContainsFunc(segment, refusedRune) && !renamedSuffix(segment) && !r.reserved(segment)
}

// refusedRune is refusedByte for a character of a name.
func refusedRune(c rune) bool {
	return c < utf8.RuneSelf && refusedByte(byte(c))
}

// upperHex is the digits a percent-encoded byte is written with.
const upperHex = "0123456789ABCDEF"

// escapeSegment percent-encodes '%', the characters Windows refuses, and a
// trailing dot or space. The result is the same on every OS.
func escapeSegment(segment string) string {
	var b strings.Builder
	b.Grow(len(segment) + 8)
	last := len(segment) - 1
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if c == '%' || refusedByte(c) || (i == last && renamedSuffix(segment)) {
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&0x0F])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// objectSegment is the segment of an object name a stored file name holds. A
// name is decoded only when the decoded name would be stored under this very
// file name, so every listed name leads back to the file it was listed from.
// Anything else, such as a file placed by hand or one written under the other
// OS's rules, is read as the name it has.
func (r nameRules) objectSegment(diskName string) string {
	body, ok := strings.CutPrefix(diskName, encodedMarker)
	if !ok {
		return diskName
	}
	name, err := url.PathUnescape(body)
	if err != nil || r.diskSegment(name) != diskName {
		return diskName
	}
	return name
}
