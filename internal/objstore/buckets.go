package objstore

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
)

// ─── Bucket metadata ──────────────────────────────────────────────────────────

// Bucket mirrors the StorageBucketMeta shape expected by @supatype/client.
type Bucket struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Public           bool     `json:"public"`
	FileSizeLimit    *int64   `json:"file_size_limit"`
	AllowedMimeTypes []string `json:"allowed_mime_types"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

// bucketsPath returns the path to the bucket metadata JSON file.
func (s *store) bucketsPath() string {
	return filepath.Join(s.root, ".supatype", "buckets.json")
}

func (s *store) loadBuckets() ([]Bucket, error) {
	data, err := os.ReadFile(s.bucketsPath())
	if os.IsNotExist(err) {
		return []Bucket{}, nil
	}
	if err != nil {
		return nil, err
	}
	var buckets []Bucket
	return buckets, json.Unmarshal(data, &buckets)
}

func (s *store) saveBuckets(buckets []Bucket) error {
	data, err := json.MarshalIndent(buckets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.bucketsPath(), data, 0o644)
}

// findBucket returns the index and pointer for a bucket with the given ID,
// or (-1, nil) if not found.
func (s *store) findBucket(buckets []Bucket, id string) (int, *Bucket) {
	for i := range buckets {
		if buckets[i].ID == id {
			return i, &buckets[i]
		}
	}
	return -1, nil
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// listBuckets: GET /bucket
// Requires service_role. Returns all buckets.
func (s *store) listBuckets(w http.ResponseWriter, r *http.Request) {
	if !isServiceRole(s.extractClaims(r)) {
		writeError(w, http.StatusUnauthorized, "service role required")
		return
	}
	s.mu.RLock()
	buckets, err := s.loadBuckets()
	s.mu.RUnlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load buckets")
		return
	}
	writeJSON(w, http.StatusOK, buckets)
}

// createBucket: POST /bucket
// Requires service_role. Body: { id?, name, public?, file_size_limit?, allowed_mime_types? }
func (s *store) createBucket(w http.ResponseWriter, r *http.Request) {
	if !isServiceRole(s.extractClaims(r)) {
		writeError(w, http.StatusUnauthorized, "service role required")
		return
	}

	var body struct {
		ID               string   `json:"id"`
		Name             string   `json:"name"`
		Public           bool     `json:"public"`
		FileSizeLimit    *int64   `json:"file_size_limit"`
		AllowedMimeTypes []string `json:"allowed_mime_types"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if body.ID == "" {
		body.ID = body.Name
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	buckets, err := s.loadBuckets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load buckets")
		return
	}
	if _, existing := s.findBucket(buckets, body.ID); existing != nil {
		writeError(w, http.StatusConflict, "bucket already exists")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	bucket := Bucket{
		ID:               body.ID,
		Name:             body.Name,
		Public:           body.Public,
		FileSizeLimit:    body.FileSizeLimit,
		AllowedMimeTypes: body.AllowedMimeTypes,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	buckets = append(buckets, bucket)

	if err := s.saveBuckets(buckets); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save bucket")
		return
	}
	if err := os.MkdirAll(filepath.Join(s.root, bucket.ID), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create bucket directory")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": bucket.Name})
}

// getBucket: GET /bucket/{id}
// Requires service_role.
func (s *store) getBucket(w http.ResponseWriter, r *http.Request) {
	if !isServiceRole(s.extractClaims(r)) {
		writeError(w, http.StatusUnauthorized, "service role required")
		return
	}
	id := chi.URLParam(r, "id")
	s.mu.RLock()
	buckets, err := s.loadBuckets()
	s.mu.RUnlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load buckets")
		return
	}
	_, b := s.findBucket(buckets, id)
	if b == nil {
		writeError(w, http.StatusNotFound, "bucket not found")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// updateBucket: PUT /bucket/{id}
// Requires service_role. Body: { public?, file_size_limit?, allowed_mime_types? }
func (s *store) updateBucket(w http.ResponseWriter, r *http.Request) {
	if !isServiceRole(s.extractClaims(r)) {
		writeError(w, http.StatusUnauthorized, "service role required")
		return
	}
	id := chi.URLParam(r, "id")

	var body struct {
		Public           *bool    `json:"public"`
		FileSizeLimit    *int64   `json:"file_size_limit"`
		AllowedMimeTypes []string `json:"allowed_mime_types"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	buckets, err := s.loadBuckets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load buckets")
		return
	}
	i, b := s.findBucket(buckets, id)
	if b == nil {
		writeError(w, http.StatusNotFound, "bucket not found")
		return
	}
	if body.Public != nil {
		buckets[i].Public = *body.Public
	}
	if body.FileSizeLimit != nil {
		buckets[i].FileSizeLimit = body.FileSizeLimit
	}
	if body.AllowedMimeTypes != nil {
		buckets[i].AllowedMimeTypes = body.AllowedMimeTypes
	}
	buckets[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := s.saveBuckets(buckets); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save bucket")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Successfully updated"})
}

// deleteBucket: DELETE /bucket/{id}
// Requires service_role. Bucket must be empty.
func (s *store) deleteBucket(w http.ResponseWriter, r *http.Request) {
	if !isServiceRole(s.extractClaims(r)) {
		writeError(w, http.StatusUnauthorized, "service role required")
		return
	}
	id := chi.URLParam(r, "id")

	s.mu.Lock()
	defer s.mu.Unlock()

	buckets, err := s.loadBuckets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load buckets")
		return
	}
	i, b := s.findBucket(buckets, id)
	if b == nil {
		writeError(w, http.StatusNotFound, "bucket not found")
		return
	}

	// Refuse if non-empty (ignore .meta directory).
	bucketDir := filepath.Join(s.root, id)
	entries, _ := os.ReadDir(bucketDir)
	for _, e := range entries {
		if e.Name() != ".meta" {
			writeError(w, http.StatusBadRequest, "bucket must be empty before deletion")
			return
		}
	}

	buckets = append(buckets[:i], buckets[i+1:]...)
	if err := s.saveBuckets(buckets); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save buckets")
		return
	}
	_ = os.RemoveAll(bucketDir)
	writeJSON(w, http.StatusOK, map[string]string{"message": "Successfully deleted"})
}

// emptyBucket: POST /bucket/{id}/empty
// Requires service_role. Deletes all objects in the bucket.
func (s *store) emptyBucket(w http.ResponseWriter, r *http.Request) {
	if !isServiceRole(s.extractClaims(r)) {
		writeError(w, http.StatusUnauthorized, "service role required")
		return
	}
	id := chi.URLParam(r, "id")

	s.mu.Lock()
	defer s.mu.Unlock()

	buckets, _ := s.loadBuckets()
	_, b := s.findBucket(buckets, id)
	if b == nil {
		writeError(w, http.StatusNotFound, "bucket not found")
		return
	}

	bucketDir := filepath.Join(s.root, id)
	entries, err := os.ReadDir(bucketDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read bucket")
		return
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(bucketDir, e.Name()))
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Successfully emptied"})
}
