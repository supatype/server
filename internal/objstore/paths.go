package objstore

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// errInvalidBucketID refuses a bucket id that cannot be a bucket.
var errInvalidBucketID = errors.New("invalid bucket id")

func cleanBucketID(bucket string) (string, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" || strings.ContainsAny(bucket, `/\`) || !filepath.IsLocal(bucket) {
		return "", errInvalidBucketID
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

// firstSegment is the first segment of rel, a path relative to a bucket, as
// it resolves: "./.meta/x" and "a/../.meta" both start with ".meta".
func firstSegment(rel string) string {
	first, _, _ := strings.Cut(filepath.ToSlash(filepath.Clean(rel)), "/")
	return first
}

// inSidecarTree reports whether rel, a path relative to a bucket, names the
// sidecar tree, however it is spelled: "./.meta" and "a/../.meta" are the same
// directory. Where the file system ignores case, as those of Windows and
// macOS do, ".META" opens the same directory and is refused too; elsewhere it
// is an ordinary folder of objects.
func (r nameRules) inSidecarTree(rel string) bool {
	return r.sameName(firstSegment(rel), sidecarDir)
}

// storeMetaDir is the directory under the storage root that holds the
// store's own bookkeeping, such as buckets.json. It is never a bucket.
const storeMetaDir = ".supatype"

// errInvalidObjectPath refuses an object name that cannot be stored.
var errInvalidObjectPath = errors.New("invalid object path")

// cleanObject is the bucket id and the object's path on disk relative to its
// bucket, or an error when either is refused: a name that leaves the bucket,
// names the sidecar tree, or reaches a file on disk under another name.
func (s *store) cleanObject(bucket, objPath string) (cleanBucket, rel string, err error) {
	if cleanBucket, err = cleanBucketID(bucket); err != nil {
		return "", "", err
	}
	if rel, err = cleanObjectPath(objPath, false); err != nil {
		return "", "", err
	}
	if s.offLimits(cleanBucket, rel) {
		return "", "", errInvalidObjectPath
	}
	return cleanBucket, rel, nil
}

// joinPaths is where the object at rel in bucket and its sidecar live,
// relative to the storage root. rel has been checked already.
func joinPaths(bucket, rel string) (fileRel, metaRel string) {
	dir, file := filepath.Split(rel)
	return filepath.Join(bucket, rel), filepath.Join(bucket, sidecarDir, dir, file+".json")
}

// legacyRel is where the object objPath was stored, relative to its bucket,
// before names were encoded, when that may be looked at: on every OS a name
// starting with encodedMarker was stored as written, and on Windows a
// backslash in a name split it into folders. It is only ever read from or
// removed: writes always use the encoded name.
//
// The old location is refused wherever it could be some other object's file:
//   - where it is the current location of some object name, which covers
//     the object's own current location too. "%%%25%25foo" was stored as
//     written, which is where "%%foo" is stored now; on Windows "%%CON" is
//     where "CON" is stored now, and `a\b` was split into a/b.
//   - under Windows rules, where a part ends in a dot or space: Windows
//     dropped it, so the old file of "notes." is "notes", which may as well
//     be the object "notes" itself.
//   - where a part is ".", "..", or empty: the path steps through folders or
//     collapses, so "%%x/../a" would open the file of the object "a" and
//     answer for it under another name.
//
// So a name Windows once split at a backslash no longer answers under that
// name, by decision: its file is exactly where the split name is stored now,
// and a file with no sidecar carries nothing to say which object it was. It
// answers to the split name,
// which is also the name it is listed as. Names starting with the marker
// that were stored as written are still found.
func (r nameRules) legacyRel(objPath string) (string, bool) {
	name := strings.TrimPrefix(objPath, "/")
	rel := filepath.FromSlash(name)
	if !filepath.IsLocal(rel) || r.hasStepPart(name) || (r.windows && r.renamesAPart(name)) || r.isStoredLocation(name) {
		return "", false
	}
	return rel, true
}

// hasStepPart reports whether name, split into parts as r splits a path, has a part that is
// not a name of its own: ".", "..", or an empty part between two separators.
func (r nameRules) hasStepPart(name string) bool {
	for {
		i := strings.IndexFunc(name, r.isSeparator)
		part := name
		if i >= 0 {
			part = name[:i]
		}
		if part == "" || part == "." || part == ".." {
			return true
		}
		if i < 0 {
			return false
		}
		name = name[i+1:] // every separator is one byte
	}
}

// isStoredLocation reports whether name, split into parts as r splits a
// path, is where r stores some object now: every part is the stored form of
// a name segment.
func (r nameRules) isStoredLocation(name string) bool {
	for segment := range strings.FieldsFuncSeq(name, r.isSeparator) {
		if !r.isStoredSegment(segment) {
			return false
		}
	}
	return true
}

// isStoredSegment reports whether r stores some name segment under the file
// name segment: the segment itself, or the name it decodes to. Where the file
// system ignores case, a spelling that differs only in case counts, since it
// opens the same file.
func (r nameRules) isStoredSegment(segment string) bool {
	if r.sameName(r.diskSegment(segment), segment) {
		return true
	}
	body, ok := strings.CutPrefix(segment, encodedMarker)
	if !ok {
		return false
	}
	name, err := url.PathUnescape(body)
	return err == nil && r.sameName(r.diskSegment(name), segment)
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
// names. A store takes hostRules when it opens, with case folding probed from
// the storage file system; a test builds the other OS's rules to check the
// encoding under them.
type nameRules struct {
	// windows is set where Windows refuses windowsForbidden and control
	// characters and drops a trailing dot or space from a name.
	windows bool
	// reserved reports whether the OS takes a name for a device rather than
	// a file. It is consulted only under Windows rules.
	reserved func(name string) bool
	// foldsCase is set where the file system takes names that differ only in
	// case for the same name.
	foldsCase bool
}

// hostRules are the rules of this OS. On Windows a device name is asked of
// the OS through filepath.IsLocal, which knows that Windows 11 no longer
// reserves "CON.txt". Their case folding is the usual one for the OS (the
// file systems of Windows and macOS ignore case by default, Linux's do not),
// which a store keeps only when it cannot probe its own file system.
var hostRules = nameRules{
	windows:   onWindows,
	reserved:  func(name string) bool { return !filepath.IsLocal(name) },
	foldsCase: onWindows || runtime.GOOS == "darwin",
}

// sameName reports whether the file system takes a and b for the same name.
func (r nameRules) sameName(a, b string) bool {
	return a == b || (r.foldsCase && strings.EqualFold(a, b))
}

// isSeparator reports whether c separates the parts of a path: a slash, and
// under Windows rules a backslash too.
func (r nameRules) isSeparator(c rune) bool {
	return c == '/' || (r.windows && c == '\\')
}

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

// renamesAPart reports whether any part of rel would be renamed by Windows.
// The dot segments are path structure, not names, and are left to the
// traversal check.
func (r nameRules) renamesAPart(rel string) bool {
	for part := range strings.FieldsFuncSeq(rel, r.isSeparator) {
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
	return c < utf8.RuneSelf && refusedByte(byte(c)) // #nosec G115 -- c < utf8.RuneSelf, so it fits a byte
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
