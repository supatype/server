package objstore

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
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

// listItem is the wire format returned by listObjects / removeObjects.
// It nests size and mimetype under "metadata" to match the shape expected by
// @supatype/client's StorageObject and the Studio's StorageBrowser.
type listItem struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	BucketID       string         `json:"bucket_id"`
	Owner          string         `json:"owner,omitempty"`
	CreatedAt      string         `json:"created_at,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty"`
	LastAccessedAt string         `json:"last_accessed_at,omitempty"`
	Metadata       map[string]any `json:"metadata"`
}

func metaToListItem(m ObjectMeta) listItem {
	return listItem{
		ID:             m.ID,
		Name:           m.Name,
		BucketID:       m.BucketID,
		Owner:          m.Owner,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
		LastAccessedAt: m.LastAccessedAt,
		Metadata: map[string]any{
			"size":     m.Size,
			"mimetype": m.ContentType,
		},
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
// different id for the same file. Anything keying on id — Studio's storage
// browser does — saw every object replaced on each refresh.
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

// urlPath extracts the wildcard portion from a chi route (strips leading slash).
func urlPath(r *http.Request) string {
	return strings.TrimPrefix(chi.URLParam(r, "*"), "/")
}

// ─── Upload ───────────────────────────────────────────────────────────────────

// uploadObject: POST /object/{bucket}/*
// Requires a valid JWT. Stores the request body as a file on disk.
// Header x-upsert: true allows overwriting existing objects.
func (s *store) uploadObject(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())

	bucket := chi.URLParam(r, "bucket")
	objPath := urlPath(r)

	if _, ok := s.resolveBucket(w, bucket); !ok {
		return
	}

	upsert := r.Header.Get("x-upsert") == "true"
	fileRel, err := s.objectFileRel(bucket, objPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	// Normalise content type — strip parameters (e.g. "image/jpeg; charset=…").
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = mt
	}

	if err := mkdirAll(filepath.Dir(filePath), 0o700); err != nil { // #nosec G703 -- filePath is built from validated bucket and object paths under storageRoot.
		writeError(w, http.StatusInternalServerError, "failed to create directory")
		return
	}

	size, err := writeTo(root, fileRel, r.Body)
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
// No auth required. Bucket must be public.
func (s *store) downloadPublic(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	objPath := urlPath(r)

	b, ok := s.resolveBucket(w, bucket)
	if !ok {
		return
	}
	if !b.Public {
		writeError(w, http.StatusForbidden, "bucket is not public")
		return
	}
	s.serveFile(w, r, bucket, objPath)
}

// downloadAuthenticated: GET /object/authenticated/{bucket}/*
// Requires a valid JWT.
func (s *store) downloadAuthenticated(w http.ResponseWriter, r *http.Request) {
	s.serveFile(w, r, chi.URLParam(r, "bucket"), urlPath(r))
}

// serveFile opens and streams a stored object. Content-Type is set from the
// saved metadata. Image transform query params (?width, ?height, etc.) are
// accepted but silently ignored — local dev serves the raw file.
func (s *store) serveFile(w http.ResponseWriter, r *http.Request, bucket, objPath string) {
	fileRel, err := s.objectFileRel(bucket, objPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	root, err := openRoot(s.root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open storage root")
		return
	}
	defer func() {
		_ = root.Close()
	}()

	f, err := root.Open(fileRel)
	if os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "object not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open file")
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

	// Set Content-Type from metadata (best effort).
	if meta, err := s.loadObjectMeta(bucket, objPath); err == nil {
		if meta.ContentType != "" {
			w.Header().Set("Content-Type", meta.ContentType)
		}
		// Update last accessed time (best effort — don't fail the request if this errors).
		meta.LastAccessedAt = time.Now().UTC().Format(time.RFC3339)
		_ = s.saveObjectMeta(meta, bucket, objPath)
	}

	// http.ServeContent handles Range, ETag, and Last-Modified negotiation.
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// ─── Remove objects ───────────────────────────────────────────────────────────

// removeObjects: DELETE /object/{bucket}
// Requires a valid JWT. Body: { prefixes: string[] }
// Returns the list of deleted ObjectMeta records (mirrors the Node.js service).
func (s *store) removeObjects(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")

	var body struct {
		Prefixes []string `json:"prefixes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var deleted []listItem
	root, err := openRoot(s.root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open storage root")
		return
	}
	defer func() {
		_ = root.Close()
	}()

	for _, prefix := range body.Prefixes {
		meta, _ := s.loadObjectMeta(bucket, prefix)
		fileRel, metaRel, err := s.objectPaths(bucket, prefix)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := root.Remove(fileRel); err == nil {
			_ = root.Remove(metaRel)
			if meta != nil {
				deleted = append(deleted, metaToListItem(*meta))
			}
		}
	}
	if deleted == nil {
		deleted = []listItem{}
	}
	utilities.WriteJSON(w, http.StatusOK, deleted)
}

// ─── List objects ─────────────────────────────────────────────────────────────

// listObjects: POST /object/list/{bucket}
// Requires a valid JWT. Body: { prefix?, limit?, offset? }
func (s *store) listObjects(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")

	var body struct {
		Prefix *string `json:"prefix"`
		Limit  *int    `json:"limit"`
		Offset *int    `json:"offset"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	prefix := ""
	if body.Prefix != nil {
		prefix = *body.Prefix
	}
	limit := 100
	if body.Limit != nil && *body.Limit > 0 {
		limit = *body.Limit
	}
	offset := 0
	if body.Offset != nil && *body.Offset > 0 {
		offset = *body.Offset
	}

	bucketDir, err := s.bucketDir(bucket)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	searchDir := bucketDir
	if prefix != "" {
		cleanPrefix, err := cleanObjectPath(prefix, true)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		searchDir = filepath.Join(bucketDir, cleanPrefix)
	}

	var results []ObjectMeta
	_ = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error { // #nosec G703 -- searchDir is under storageRoot after bucket/prefix validation above.
		if err != nil || info.IsDir() {
			return nil
		}
		// Skip .meta sidecar files.
		rel, _ := filepath.Rel(bucketDir, path)
		if strings.HasPrefix(rel, ".meta"+string(os.PathSeparator)) || rel == ".meta" {
			return nil
		}

		objPath := filepath.ToSlash(rel)
		if meta, err := s.loadObjectMeta(bucket, objPath); err == nil {
			results = append(results, *meta)
		} else {
			// Synthesise minimal metadata when the sidecar is missing.
			results = append(results, ObjectMeta{
				ID:       syntheticID(bucket, objPath),
				Name:     objPath,
				BucketID: bucket,
				Size:     info.Size(),
			})
		}
		return nil
	})

	if results == nil {
		results = []ObjectMeta{}
	}

	// Apply pagination.
	if offset < len(results) {
		results = results[offset:]
	} else {
		results = []ObjectMeta{}
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	// Convert to wire format (metadata nested under "metadata" key).
	items := make([]listItem, len(results))
	for i, r := range results {
		items[i] = metaToListItem(r)
	}
	utilities.WriteJSON(w, http.StatusOK, items)
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
	bucket := chi.URLParam(r, "bucket")
	objPath := urlPath(r)

	var body struct {
		ExpiresIn int64 `json:"expiresIn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ExpiresIn <= 0 {
		body.ExpiresIn = 3600
	}

	token := s.signToken(signedPayload{
		B:   bucket,
		P:   objPath,
		Exp: time.Now().Unix() + body.ExpiresIn,
	})

	// Build the full signed URL (includes /storage/v1 since callers strip that prefix before us).
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	signedURL := fmt.Sprintf("%s://%s/storage/v1/object/sign/%s/%s?token=%s",
		scheme, r.Host, bucket, objPath, token)

	utilities.WriteJSON(w, http.StatusOK, map[string]string{"signedURL": signedURL})
}

// serveSignedURL: GET /object/sign/{bucket}/*?token=...
// No auth required — the token is the credential.
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
