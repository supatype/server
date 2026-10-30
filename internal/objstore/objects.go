package objstore

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/supatype/server/internal/studioctx"
	"github.com/supatype/server/internal/utilities"
)

// ─── Object metadata ──────────────────────────────────────────────────────────

// ObjectMeta is the on-disk sidecar format for a stored object.
type ObjectMeta struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	BucketID       string `json:"bucket_id"`
	Owner          string `json:"owner"`
	ContentType    string `json:"content_type,omitempty"`
	Size           int64  `json:"size"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	LastAccessedAt string `json:"last_accessed_at"`
}

// listItem is the wire format of one list entry: the storage service's in the
// supatype repo (packages/storage), field for field, because @supatype/client
// checks every entry against that shape. A value this store does not have is
// null, never left out, and size and mimetype are lifted out of metadata as the
// service lifts them. A folder is an entry too, null in every field but the
// bucket and its name. testdata/node-list-entry.json pins both shapes.
type listItem struct {
	ID             *string        `json:"id"`
	BucketID       string         `json:"bucket_id"`
	Name           string         `json:"name"`
	Owner          *string        `json:"owner"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      *string        `json:"created_at"`
	UpdatedAt      *string        `json:"updated_at"`
	LastAccessedAt *string        `json:"last_accessed_at"`
	Size           *int64         `json:"size"`
	Mimetype       *string        `json:"mimetype"`
}

// removedItem is what a remove answers for each object it removed, as the
// storage service answers it: the name and the bucket, nothing more.
type removedItem struct {
	Name     string `json:"name"`
	BucketID string `json:"bucket_id"`
}

// orNull is s, or nil for an empty string, so it encodes as null.
func orNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// metaToListItem is a file's list entry. Its id and timestamps are always sent, even empty, as a
// file entry in the contract never has them null.
func metaToListItem(m ObjectMeta) listItem {
	metadata := map[string]any{"size": m.Size}
	if m.ContentType != "" {
		metadata["mimetype"] = m.ContentType
	}
	return listItem{
		ID:             &m.ID,
		BucketID:       m.BucketID,
		Name:           m.Name,
		Owner:          orNull(m.Owner),
		Metadata:       metadata,
		CreatedAt:      &m.CreatedAt,
		UpdatedAt:      &m.UpdatedAt,
		LastAccessedAt: &m.LastAccessedAt,
		Size:           &m.Size,
		Mimetype:       orNull(m.ContentType),
	}
}

// folderItem is a subfolder's list entry: the bucket and the folder's name, and null for the rest.
func folderItem(bucket, name string) listItem {
	return listItem{BucketID: bucket, Name: name}
}

// sidecarlessMeta stands in for the sidecar of a file written without one. The
// file's own modification time serves for every timestamp, so an entry still
// has all three, as every entry the storage service lists does; no owner and no
// type are known, so those stay empty and encode as null.
func sidecarlessMeta(bucket, objPath string, info os.FileInfo) ObjectMeta {
	modified := info.ModTime().UTC().Format(time.RFC3339)
	return ObjectMeta{
		ID:             syntheticID(bucket, objPath),
		Name:           objPath,
		BucketID:       bucket,
		Size:           info.Size(),
		CreatedAt:      modified,
		UpdatedAt:      modified,
		LastAccessedAt: modified,
	}
}

// ─── Filesystem helpers ───────────────────────────────────────────────────────

// openStorageRoot opens the storage root, answering the request itself when
// it cannot.
func (s *store) openStorageRoot(w http.ResponseWriter) (*os.Root, bool) {
	root, err := openRoot(s.root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open storage root")
		return nil, false
	}
	return root, true
}

// loadMetaAt reads the sidecar at metaRel under root.
func loadMetaAt(root *os.Root, metaRel string) (*ObjectMeta, error) {
	f, err := root.Open(metaRel)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()

	data, err := readAll(f)
	if err != nil {
		return nil, err
	}
	var meta ObjectMeta
	return &meta, json.Unmarshal(data, &meta)
}

// saveNewMeta writes the sidecar at metaRel, creating the folders it goes in,
// the storage root included.
func (s *store) saveNewMeta(meta *ObjectMeta, metaRel string) error {
	if err := mkdirAll(filepath.Join(s.root, filepath.Dir(metaRel)), 0o700); err != nil { // #nosec G703 -- metaRel is built from validated bucket and object paths under storageRoot.
		return err
	}
	root, err := openRoot(s.root)
	if err != nil {
		return err
	}
	defer func() {
		_ = root.Close()
	}()
	return saveMetaAt(root, meta, metaRel)
}

// saveMetaAt writes meta to the sidecar at metaRel under root, whose folder
// is already there.
func saveMetaAt(root *os.Root, meta *ObjectMeta, metaRel string) error {
	data, err := marshalIndent(meta)
	if err != nil {
		return err
	}
	_, err = writeTo(root, metaRel, bytes.NewReader(data))
	return err
}

// newID generates a random UUID v4.
//
// crypto/rand.Read does not fail, so the error is not checked; a short read
// would be a broken runtime rather than a condition to handle.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return formatUUID(b, 4)
}

// syntheticID derives a stable id for an object that has no sidecar.
//
// It used to be newID(), so listing the same directory twice returned a
// different id for the same file. Anything keying on id (Studio's storage
// browser does) saw every object replaced on each refresh.
func syntheticID(bucket, objPath string) string {
	sum := sha256.Sum256([]byte(bucket + "\x00" + objPath))
	return formatUUID(sum[:16], 8)
}

// formatUUID renders 16 bytes as a UUID of the given version.
func formatUUID(b []byte, version byte) string {
	id := make([]byte, 16)
	copy(id, b)
	id[6] = (id[6] & 0x0f) | (version << 4)
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:])
}

// ─── Upload ───────────────────────────────────────────────────────────────────

// uploadObject: POST /object/{bucket}/*
// Requires a valid JWT. Stores the request body as a file on disk.
// Header x-upsert: true allows overwriting existing objects.
func (s *store) uploadObject(w http.ResponseWriter, r *http.Request) {
	b, objPath, ok := s.knownObjectRoute(w, r)
	if !ok {
		return
	}
	bucket := b.ID
	loc, err := s.locate(bucket, objPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Keep the parsed media type only, never the header as written.
	contentType := storedContentType(r.Header.Get("Content-Type"))
	body, ok := acceptUpload(w, r, b, objPath, contentType)
	if !ok {
		return
	}
	size, ok := s.storeUpload(w, loc, body, r.Header.Get("x-upsert") == "true")
	if !ok {
		return
	}
	meta := ObjectMeta{Name: objPath, BucketID: bucket, Owner: claimsFrom(r.Context()).Sub, ContentType: contentType, Size: size}
	if err := s.saveNewMeta(stampedMeta(meta), loc.metaRel); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save metadata")
		return
	}
	utilities.WriteJSON(w, http.StatusOK, map[string]string{"path": objPath})
}

// storeUpload writes body to loc's file under the storage root and returns its
// size, answering the request itself when it cannot. Without upsert an
// object already there, under either layout, is refused rather than
// replaced. An upsert over an object stored under the old layout removes the
// old file and sidecar once the new file is written, so the name is not left
// answering from two places.
func (s *store) storeUpload(w http.ResponseWriter, loc objectLocation, body io.Reader, upsert bool) (int64, bool) {
	root, ok := s.openStorageRoot(w)
	if !ok {
		return 0, false
	}
	defer func() {
		_ = root.Close()
	}()

	existingFile, existingMeta := s.stored(root, loc)
	if _, err := root.Stat(existingFile); err == nil && !upsert {
		writeError(w, http.StatusConflict, "object already exists")
		return 0, false
	}
	size, ok := s.writeUpload(w, root, loc.fileRel, body)
	if ok && existingFile != loc.fileRel {
		_ = root.Remove(existingFile)
		_ = root.Remove(existingMeta)
	}
	return size, ok
}

// writeUpload writes body to fileRel under root, creating its folders, and
// returns its size, answering the request itself when it cannot.
func (s *store) writeUpload(w http.ResponseWriter, root *os.Root, fileRel string, body io.Reader) (int64, bool) {
	filePath := filepath.Join(s.root, fileRel)
	if err := mkdirAll(filepath.Dir(filePath), 0o700); err != nil { // #nosec G703 -- filePath is built from validated bucket and object paths under storageRoot.
		writeError(w, http.StatusInternalServerError, "failed to create directory")
		return 0, false
	}
	size, err := writeTo(root, fileRel, body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write file")
		return 0, false
	}
	return size, true
}

// stampedMeta is m as a new object's sidecar: a fresh id, and the current time
// for its creation, update and last access.
func stampedMeta(m ObjectMeta) *ObjectMeta {
	now := time.Now().UTC().Format(time.RFC3339)
	m.ID = newID()
	m.CreatedAt, m.UpdatedAt, m.LastAccessedAt = now, now, now
	return &m
}

// ─── Download ─────────────────────────────────────────────────────────────────

// downloadPublic: GET /object/public/{bucket}/*
// No auth required. The bucket's access mode must be public.
func (s *store) downloadPublic(w http.ResponseWriter, r *http.Request) {
	b, objPath, ok := s.knownObjectRoute(w, r)
	if !ok {
		return
	}
	bucket := b.ID
	if !b.servesAnonymously() {
		writeError(w, http.StatusForbidden, "bucket is not public")
		return
	}
	s.serveFile(w, r, bucket, objPath)
}

// downloadAuthenticated: GET /object/authenticated/{bucket}/*
// Requires a valid JWT.
func (s *store) downloadAuthenticated(w http.ResponseWriter, r *http.Request) {
	if b, objPath, ok := s.knownObjectRoute(w, r); ok {
		s.serveFile(w, r, b.ID, objPath)
	}
}

// serveFile opens and streams a stored object. Content-Type is set from the
// saved metadata. Image transform query params (?width, ?height, etc.) are
// accepted but silently ignored, because local dev serves the raw file.
// Public, authenticated and signed downloads all arrive here, so this is the
// one place the stored-XSS header policy has to be applied.
func (s *store) serveFile(w http.ResponseWriter, r *http.Request, bucket, objPath string) {
	root, ok := s.openStorageRoot(w)
	if !ok {
		return
	}
	defer func() {
		_ = root.Close()
	}()
	f, metaRel, ok := s.openObject(w, root, bucket, objPath)
	if !ok {
		return
	}
	defer func() {
		_ = f.Close()
	}()

	info, err := statFile(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to stat file")
		return
	}
	applyObjectMeta(w, root, metaRel)

	// http.ServeContent handles Range, ETag, and Last-Modified negotiation. The
	// wrapper hardens the headers once ServeContent has settled the type.
	http.ServeContent(SafeObjectWriter(w, path.Base(objPath)), r, path.Base(objPath), info.ModTime(), f)
}

// openObject opens a stored object under root for reading and returns where
// its sidecar is, answering the request itself when it cannot.
func (s *store) openObject(w http.ResponseWriter, root *os.Root, bucket, objPath string) (*os.File, string, bool) {
	loc, err := s.locate(bucket, objPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, "", false
	}
	fileRel, metaRel := s.stored(root, loc)
	f, err := openIn(root, fileRel)
	if os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "object not found")
		return nil, "", false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open file")
		return nil, "", false
	}
	return f, metaRel, true
}

// objectLocation is a validated object name: its bucket, the name, and where
// its file and sidecar are stored now, relative to the storage root.
type objectLocation struct {
	bucket, name, fileRel, metaRel string
}

// locate validates bucket and objPath and says where the object is stored now.
func (s *store) locate(bucket, objPath string) (objectLocation, error) {
	bucket, rel, err := s.cleanObject(bucket, objPath)
	if err != nil {
		return objectLocation{}, err
	}
	fileRel, metaRel := joinPaths(bucket, rel)
	return objectLocation{bucket: bucket, name: objPath, fileRel: fileRel, metaRel: metaRel}, nil
}

// stored is where loc's file and sidecar are under root: where it is stored
// now, or, when no file is there, where it was stored before names were
// encoded, if a file is there instead. Uploads look here only to find what
// they replace; they always write where loc says.
//
// The checks run cheapest first. Nearly every name has no old location, so
// most requests stop at legacyRel, which reads nothing; the next stop is a
// file at the current location. Only then is the old location checked on
// disk, which is never the current one, since legacyRel refuses that.
func (s *store) stored(root *os.Root, loc objectLocation) (fileRel, metaRel string) {
	rel, ok := s.rules.legacyRel(loc.name)
	if !ok {
		return loc.fileRel, loc.metaRel
	}
	if _, err := root.Stat(loc.fileRel); !os.IsNotExist(err) || s.offLimits(loc.bucket, rel) {
		return loc.fileRel, loc.metaRel
	}
	legacyFile, legacyMeta := joinPaths(loc.bucket, rel)
	if info, err := root.Stat(legacyFile); err != nil || !info.Mode().IsRegular() {
		return loc.fileRel, loc.metaRel
	}
	return legacyFile, legacyMeta
}

// applyObjectMeta sets Content-Type from the sidecar at metaRel under root
// and records the access time. Both are best effort: neither fails the
// download.
func applyObjectMeta(w http.ResponseWriter, root *os.Root, metaRel string) {
	meta, err := loadMetaAt(root, metaRel)
	if err != nil {
		return
	}
	if meta.ContentType != "" {
		w.Header().Set("Content-Type", meta.ContentType)
	}
	meta.LastAccessedAt = time.Now().UTC().Format(time.RFC3339)
	_ = saveMetaAt(root, meta, metaRel)
}

// ─── Remove objects ───────────────────────────────────────────────────────────

// removeObjects: DELETE /object/{bucket}
// Requires a valid JWT. Body: { prefixes: string[] }
// Answers each removed object's name and bucket, as the storage service does.
// An object with no sidecar is removed and reported like any other.
func (s *store) removeObjects(w http.ResponseWriter, r *http.Request) {
	bucket, ok := s.knownBucketRoute(w, r)
	if !ok {
		return
	}
	prefixes, ok := removePrefixes(w, r)
	if !ok {
		return
	}
	root, ok := s.openStorageRoot(w)
	if !ok {
		return
	}
	defer func() {
		_ = root.Close()
	}()

	deleted := []removedItem{}
	for _, prefix := range prefixes {
		removed, err := s.removeObject(root, bucket, prefix)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if removed {
			deleted = append(deleted, removedItem{Name: prefix, BucketID: bucket})
		}
	}
	utilities.WriteJSON(w, http.StatusOK, deleted)
}

// removePrefixes is the object names a remove's body lists, answering the
// request itself when the body cannot be read.
func removePrefixes(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var body struct {
		Prefixes []string `json:"prefixes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return nil, false
	}
	return body.Prefixes, true
}

// removeObject removes one object and its sidecar, reporting whether there
// was an object to remove, or an error when the name is refused.
func (s *store) removeObject(root *os.Root, bucket, objPath string) (bool, error) {
	loc, err := s.locate(bucket, objPath)
	if err != nil {
		return false, err
	}
	fileRel, metaRel := s.stored(root, loc)
	removed := root.Remove(fileRel) == nil
	if removed {
		_ = root.Remove(metaRel)
	}
	return removed, nil
}

// ─── List objects ─────────────────────────────────────────────────────────────

// listRequest is the body of a list: the folder, the text a name must contain, and the page.
// A limit or offset that is absent, zero or negative is the default: 100 entries from the first.
type listRequest struct {
	Prefix string `json:"prefix"`
	Search string `json:"search"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

// listObjects: POST /object/list/{bucket}
// Requires a valid JWT. Body: { prefix?, limit?, offset?, search? }
// Answers the folder's direct children as the storage service does: one entry per subfolder, then
// the files, each named relative to the folder. X-Total-Count is how many matched the search
// before the page was cut.
func (s *store) listObjects(w http.ResponseWriter, r *http.Request) {
	bucket, ok := s.knownBucketRoute(w, r)
	if !ok {
		return
	}

	var body listRequest
	_ = json.NewDecoder(r.Body).Decode(&body)

	l, err := s.listFolder(bucket, body.Prefix)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if l.root, ok = s.openStorageRoot(w); !ok {
		return
	}
	defer func() {
		_ = l.root.Close()
	}()
	items := matchingItems(s.folderChildren(l), body.Search)

	w.Header().Set("X-Total-Count", strconv.Itoa(len(items)))
	w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")
	utilities.WriteJSON(w, http.StatusOK, pageOf(items, body.Limit, body.Offset))
}

// folderListing is one folder being listed: the bucket, the folder's object
// path ("" for the bucket root), its path on disk relative to the bucket and
// as a directory, and the storage root the sidecars are read from.
type folderListing struct {
	bucket, folder, rel, dir string
	root                     *os.Root
}

// listFolder is the folder a list prefix names in bucket, which the registry
// has already vouched for. The slashes around the prefix are stripped and the
// path cleaned, so the folder is spelled one way however the prefix reached
// it: "a/.." is the bucket root, not a folder beside it that would list the
// sidecar tree.
func (s *store) listFolder(bucket, prefix string) (folderListing, error) {
	folder := strings.Trim(prefix, "/")
	rel, err := cleanObjectPath(folder, true)
	if err != nil {
		return folderListing{}, err
	}
	if folder = path.Clean(folder); folder == "." {
		folder = ""
	}
	return folderListing{bucket: bucket, folder: folder, rel: rel, dir: filepath.Join(s.root, bucket, rel)}, nil
}

// folderChildren is the folder's direct children: an entry for each subfolder with an object
// somewhere below it, then one for each file, each group in byte order by name. A folder that is
// not there, or is a file, has none, and neither the sidecar tree nor a folder reached by
// another name, such as a short name, is ever listed. Each child is named by the object name its
// file name holds, which is also what the order is by.
//
// os.ReadDir returns entries in byte order by file name already, so the groups
// are sorted again only when a stored name was decoded into a different one,
// which can move it.
func (s *store) folderChildren(l folderListing) []listItem {
	if s.offLimits(l.bucket, l.rel) {
		return []listItem{}
	}
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return []listItem{}
	}
	var folders, files []listItem
	decoded := false
	for _, e := range entries {
		item, ok := s.childItem(l, e)
		if !ok {
			continue
		}
		if e.IsDir() {
			folders = append(folders, item)
		} else {
			files = append(files, item)
		}
		decoded = decoded || item.Name != e.Name()
	}
	if decoded {
		byName := func(a, b listItem) int { return strings.Compare(a.Name, b.Name) }
		slices.SortFunc(folders, byName)
		slices.SortFunc(files, byName)
	}
	return append(append(make([]listItem, 0, len(folders)+len(files)), folders...), files...)
}

// childItem is the list entry for one entry of a folder's directory, or false
// when the entry is not a child: the sidecar tree, a subfolder with no object
// anywhere below it, or a file removed since the directory was read.
func (s *store) childItem(l folderListing, e fs.DirEntry) (listItem, bool) {
	switch {
	case l.folder == "" && s.rules.inSidecarTree(e.Name()):
		return listItem{}, false
	case e.IsDir():
		if !holdsAnObject(filepath.Join(l.dir, e.Name())) {
			return listItem{}, false
		}
		return folderItem(l.bucket, s.rules.objectSegment(e.Name())), true
	}
	info, err := e.Info()
	if err != nil {
		return listItem{}, false
	}
	return s.fileItem(l, info), true
}

// holdsAnObject reports whether a file lies anywhere below dir, so an empty tree is not a folder.
func holdsAnObject(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error { // #nosec G703 -- dir is a child of the folder listFolder validated under the storage root.
		if err != nil || d.IsDir() {
			return nil
		}
		found = true
		return fs.SkipAll
	})
	return found
}

// fileItem is the list entry of a file directly in the folder, named by its
// own name. Its sidecar is the one stored beside its own file name: the name
// came from the directory, so it needs no second check.
func (s *store) fileItem(l folderListing, info os.FileInfo) listItem {
	name := s.rules.objectSegment(info.Name())
	objPath := path.Join(l.folder, name)
	_, metaRel := joinPaths(l.bucket, filepath.Join(l.rel, info.Name()))
	meta, err := loadMetaAt(l.root, metaRel)
	if err != nil {
		fallback := sidecarlessMeta(l.bucket, objPath, info)
		meta = &fallback
	}
	item := metaToListItem(*meta)
	item.Name = name
	return item
}

// matchingItems is the items whose name contains search, ignoring case; all of them when search
// is empty. The text is matched literally.
func matchingItems(items []listItem, search string) []listItem {
	if search == "" {
		return items
	}
	needle := strings.ToLower(search)
	kept := make([]listItem, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Name), needle) {
			kept = append(kept, item)
		}
	}
	return kept
}

// pageOf is `limit` items from `offset`, with the defaults listRequest describes.
func pageOf(items []listItem, limit, offset int) []listItem {
	if limit <= 0 {
		limit = 100
	}
	offset = max(offset, 0)
	if offset >= len(items) {
		return []listItem{}
	}
	items = items[offset:]
	return items[:min(limit, len(items))]
}

// ─── Signed URLs ──────────────────────────────────────────────────────────────

// signedPayload is the JSON payload embedded in a local signed URL token.
type signedPayload struct {
	B   string `json:"b"`   // bucket ID
	P   string `json:"p"`   // object path
	Exp int64  `json:"exp"` // Unix expiry timestamp
}

// signToken encodes a payload and appends an HMAC-SHA256 signature.
// Format: base64url(JSON(payload)) + "." + base64url(HMAC(payload))
func (s *store) signToken(p signedPayload) string {
	// signedPayload is three scalars, so it cannot fail to encode.
	data, _ := json.Marshal(p)
	encoded := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, s.jwtSecret)
	mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + sig
}

// verifyToken validates a signed token and returns the decoded payload.
func (s *store) verifyToken(token string) (*signedPayload, bool) {
	idx := strings.LastIndex(token, ".")
	if idx < 0 {
		return nil, false
	}
	encoded, sigB64 := token[:idx], token[idx+1:]

	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, false
	}
	mac := hmac.New(sha256.New, s.jwtSecret)
	mac.Write([]byte(encoded))
	if !hmac.Equal(mac.Sum(nil), sigBytes) {
		return nil, false
	}

	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	var p signedPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, false
	}
	if p.Exp > 0 && time.Now().Unix() > p.Exp {
		return nil, false
	}
	return &p, true
}

// createSignedURL: POST /object/sign/{bucket}/*
// Requires a valid JWT. Body: { expiresIn: number (seconds) }
// Returns: { signedURL: string }
func (s *store) createSignedURL(w http.ResponseWriter, r *http.Request) {
	b, objPath, ok := s.knownObjectRoute(w, r)
	if !ok {
		return
	}
	bucket := b.ID

	expiresIn, err := signingExpiry(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	token := s.signToken(signedPayload{
		B:   bucket,
		P:   objPath,
		Exp: time.Now().Unix() + expiresIn,
	})

	// Build the full signed URL (includes /storage/v1 since callers strip that prefix before us).
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// The token is base64url and a dot, so it needs no escaping; the names do.
	signedURL := fmt.Sprintf("%s://%s/storage/v1/object/sign/%s/%s?token=%s",
		scheme, r.Host, url.PathEscape(bucket), escapeSegments(objPath), token)

	utilities.WriteJSON(w, http.StatusOK, map[string]string{"signedURL": signedURL})
}

// escapeSegments escapes each segment of an object path for use in a URL,
// keeping the slashes between them. Serving reads the path from the token, so
// the router only has to decode it back to the same route.
func escapeSegments(objPath string) string {
	return mapSegments(objPath, url.PathEscape)
}

// defaultSignedURLSeconds is the life of a signed URL that names none.
const defaultSignedURLSeconds = 3600

// signingExpiry reads `expiresIn` from a signing body by its exact name,
// defaulting a missing or non-positive one to an hour. Struct decoding would
// match any casing with the last key winning, so `{"expiresIn":60,
// "expiresin":31536000}` signed for a year; a key that only case-folds to
// `expiresIn` is refused instead. A request the Studio proxy forwards is
// capped at studioctx.MaxSignedURLSeconds here too.
func signingExpiry(r *http.Request) (int64, error) {
	var fields map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		return 0, err
	}
	for name := range fields {
		if name != "expiresIn" && strings.EqualFold(name, "expiresIn") {
			return 0, fmt.Errorf("key %q is not expiresIn", name)
		}
	}
	var expiresIn int64
	if raw, ok := fields["expiresIn"]; ok {
		if err := json.Unmarshal(raw, &expiresIn); err != nil {
			return 0, err
		}
	}
	if expiresIn <= 0 {
		expiresIn = defaultSignedURLSeconds
	}
	if studioctx.IsProxied(r.Context()) {
		expiresIn = min(expiresIn, studioctx.MaxSignedURLSeconds)
	}
	return expiresIn, nil
}

// serveSignedURL: GET /object/sign/{bucket}/*?token=...
// No auth required: the token is the credential.
func (s *store) serveSignedURL(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "missing token")
		return
	}
	p, ok := s.verifyToken(token)
	if !ok {
		writeError(w, http.StatusForbidden, "invalid or expired token")
		return
	}
	if _, ok := s.registeredBucket(w, p.B); ok {
		s.serveFile(w, r, p.B, p.P)
	}
}
