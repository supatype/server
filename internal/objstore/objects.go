package objstore

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

func (s *store) loadObjectMeta(bucket, objPath string) (*ObjectMeta, error) {
	metaRel, err := s.objectMetaRel(bucket, objPath)
	if err != nil {
		return nil, err
	}
	root, err := openRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = root.Close()
	}()

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

func (s *store) saveObjectMeta(meta *ObjectMeta, bucket, objPath string) error {
	metaRel, err := s.objectMetaRel(bucket, objPath)
	if err != nil {
		return err
	}
	if err := mkdirAll(filepath.Join(s.root, filepath.Dir(metaRel)), 0o700); err != nil { // #nosec G703 -- metaRel is built from validated bucket and object paths under storageRoot.
		return err
	}
	data, err := marshalIndent(meta)
	if err != nil {
		return err
	}
	root, err := openRoot(s.root)
	if err != nil {
		return err
	}
	defer func() {
		_ = root.Close()
	}()

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
	claims := claimsFrom(r.Context())

	bucket, objPath, ok := objectRoute(w, r)
	if !ok {
		return
	}
	b, ok := s.resolveBucket(w, bucket)
	if !ok {
		return
	}

	upsert := r.Header.Get("x-upsert") == "true"
	fileRel, err := s.objectFileRel(bucket, objPath)
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
	filePath := filepath.Join(s.root, fileRel)
	root, err := openRoot(s.root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open storage root")
		return
	}
	defer func() {
		_ = root.Close()
	}()

	if !upsert {
		if _, err := root.Stat(fileRel); err == nil {
			writeError(w, http.StatusConflict, "object already exists")
			return
		}
	}

	if err := mkdirAll(filepath.Dir(filePath), 0o700); err != nil { // #nosec G703 -- filePath is built from validated bucket and object paths under storageRoot.
		writeError(w, http.StatusInternalServerError, "failed to create directory")
		return
	}

	size, err := writeTo(root, fileRel, body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write file")
		return
	}

	owner := claims.Sub
	now := time.Now().UTC().Format(time.RFC3339)
	meta := &ObjectMeta{
		ID:             newID(),
		Name:           objPath,
		BucketID:       bucket,
		Owner:          owner,
		ContentType:    contentType,
		Size:           size,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastAccessedAt: now,
	}
	if err := s.saveObjectMeta(meta, bucket, objPath); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save metadata")
		return
	}
	utilities.WriteJSON(w, http.StatusOK, map[string]string{"path": objPath})
}

// ─── Download ─────────────────────────────────────────────────────────────────

// downloadPublic: GET /object/public/{bucket}/*
// No auth required. The bucket's access mode must be public.
func (s *store) downloadPublic(w http.ResponseWriter, r *http.Request) {
	bucket, objPath, ok := objectRoute(w, r)
	if !ok {
		return
	}
	b, ok := s.resolveBucket(w, bucket)
	if !ok {
		return
	}
	if !b.servesAnonymously() {
		writeError(w, http.StatusForbidden, "bucket is not public")
		return
	}
	s.serveFile(w, r, bucket, objPath)
}

// downloadAuthenticated: GET /object/authenticated/{bucket}/*
// Requires a valid JWT.
func (s *store) downloadAuthenticated(w http.ResponseWriter, r *http.Request) {
	if bucket, objPath, ok := objectRoute(w, r); ok {
		s.serveFile(w, r, bucket, objPath)
	}
}

// serveFile opens and streams a stored object. Content-Type is set from the
// saved metadata. Image transform query params (?width, ?height, etc.) are
// accepted but silently ignored, because local dev serves the raw file.
// Public, authenticated and signed downloads all arrive here, so this is the
// one place the stored-XSS header policy has to be applied.
func (s *store) serveFile(w http.ResponseWriter, r *http.Request, bucket, objPath string) {
	f, ok := s.openObject(w, bucket, objPath)
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
	s.applyObjectMeta(w, bucket, objPath)

	// http.ServeContent handles Range, ETag, and Last-Modified negotiation. The
	// wrapper hardens the headers once ServeContent has settled the type.
	http.ServeContent(SafeObjectWriter(w, path.Base(objPath)), r, path.Base(objPath), info.ModTime(), f)
}

// openObject opens a stored object for reading, answering the request itself
// when it cannot.
func (s *store) openObject(w http.ResponseWriter, bucket, objPath string) (*os.File, bool) {
	fileRel, err := s.objectFileRel(bucket, objPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	root, err := openRoot(s.root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open storage root")
		return nil, false
	}
	defer func() {
		_ = root.Close()
	}()

	f, err := openIn(root, fileRel)
	if os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "object not found")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open file")
		return nil, false
	}
	return f, true
}

// applyObjectMeta sets Content-Type from the saved metadata and records the
// access time. Both are best effort: neither fails the download.
func (s *store) applyObjectMeta(w http.ResponseWriter, bucket, objPath string) {
	meta, err := s.loadObjectMeta(bucket, objPath)
	if err != nil {
		return
	}
	if meta.ContentType != "" {
		w.Header().Set("Content-Type", meta.ContentType)
	}
	meta.LastAccessedAt = time.Now().UTC().Format(time.RFC3339)
	_ = s.saveObjectMeta(meta, bucket, objPath)
}

// ─── Remove objects ───────────────────────────────────────────────────────────

// removeObjects: DELETE /object/{bucket}
// Requires a valid JWT. Body: { prefixes: string[] }
// Answers each removed object's name and bucket, as the storage service does.
// An object with no sidecar is removed and reported like any other.
func (s *store) removeObjects(w http.ResponseWriter, r *http.Request) {
	bucket, ok := routeParam(w, r, "bucket")
	if !ok {
		return
	}

	var body struct {
		Prefixes []string `json:"prefixes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	deleted := []removedItem{}
	root, err := openRoot(s.root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open storage root")
		return
	}
	defer func() {
		_ = root.Close()
	}()

	for _, prefix := range body.Prefixes {
		fileRel, metaRel, err := s.objectPaths(bucket, prefix)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := root.Remove(fileRel); err == nil {
			_ = root.Remove(metaRel)
			deleted = append(deleted, removedItem{Name: prefix, BucketID: bucket})
		}
	}
	utilities.WriteJSON(w, http.StatusOK, deleted)
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
	bucket, ok := routeParam(w, r, "bucket")
	if !ok {
		return
	}

	var body listRequest
	_ = json.NewDecoder(r.Body).Decode(&body)

	dir, folder, err := s.listFolder(bucket, body.Prefix)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items := matchingItems(s.folderChildren(bucket, dir, folder), body.Search)

	w.Header().Set("X-Total-Count", strconv.Itoa(len(items)))
	w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")
	utilities.WriteJSON(w, http.StatusOK, pageOf(items, body.Limit, body.Offset))
}

// listFolder is the directory a list prefix names and the folder's object path ("" for the
// bucket root), with the slashes around the prefix stripped.
func (s *store) listFolder(bucket, prefix string) (dir, folder string, err error) {
	bucketDir, err := s.bucketDir(bucket)
	if err != nil {
		return "", "", err
	}
	folder = strings.Trim(prefix, "/")
	rel, err := cleanObjectPath(folder, true)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(bucketDir, rel), folder, nil
}

// folderChildren is the folder's direct children: an entry for each subfolder with an object
// somewhere below it, then one for each file, each group in byte order by name. A folder that is
// not there, or is a file, has none, and the sidecar tree is never listed. Each child is named
// by the object name its file name holds, which is also what the order is by.
func (s *store) folderChildren(bucket, dir, folder string) []listItem {
	if folder == ".meta" || strings.HasPrefix(folder, ".meta/") {
		return []listItem{}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []listItem{}
	}
	var folders, files []listItem
	for _, e := range entries {
		switch {
		case folder == "" && e.Name() == ".meta":
			// The sidecar tree, not a folder of objects.
		case e.IsDir():
			if holdsAnObject(filepath.Join(dir, e.Name())) {
				folders = append(folders, folderItem(bucket, objectSegment(e.Name())))
			}
		default:
			// A file removed since the directory was read has no info and is no longer a child.
			if info, err := e.Info(); err == nil {
				files = append(files, s.fileItem(bucket, folder, info))
			}
		}
	}
	byName := func(a, b listItem) int { return strings.Compare(a.Name, b.Name) }
	slices.SortFunc(folders, byName)
	slices.SortFunc(files, byName)
	return append(append(make([]listItem, 0, len(folders)+len(files)), folders...), files...)
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

// fileItem is the list entry of a file directly in the folder, named by its own name.
func (s *store) fileItem(bucket, folder string, info os.FileInfo) listItem {
	name := objectSegment(info.Name())
	objPath := path.Join(folder, name)
	meta, err := s.loadObjectMeta(bucket, objPath)
	if err != nil {
		fallback := sidecarlessMeta(bucket, objPath, info)
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
	bucket, objPath, ok := objectRoute(w, r)
	if !ok {
		return
	}

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
	segments := strings.Split(objPath, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
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
	s.serveFile(w, r, p.B, p.P)
}
