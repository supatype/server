package objstore

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/utilities"
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
	data, err := readFile(s.bucketsPath())
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
	data, err := marshalIndent(buckets)
	if err != nil {
		return err
	}
	return writeFile(s.bucketsPath(), data, 0o600)
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

// lookupBucket reads one bucket under the read lock.
//
// The error used to be dropped at three call sites — upload, public download
// and empty — so a buckets.json that could not be read answered "bucket not
// found". That tells the caller their bucket is gone when in fact the store is
// broken, and it is the kind of 404 someone acts on by recreating things.
func (s *store) lookupBucket(id string) (*Bucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	buckets, err := s.loadBuckets()
	if err != nil {
		return nil, err
	}
	_, bucket := s.findBucket(buckets, id)
	return bucket, nil
}

// resolveBucket answers the request itself when the bucket cannot be read or
// does not exist, and otherwise hands it back.
func (s *store) resolveBucket(w http.ResponseWriter, id string) (*Bucket, bool) {
	bucket, err := s.lookupBucket(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load buckets")
		return nil, false
	}
	if bucket == nil {
		writeError(w, http.StatusNotFound, "bucket not found")
		return nil, false
	}
	return bucket, true
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// listBuckets: GET /bucket
func (s *store) listBuckets(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	buckets, err := s.loadBuckets()
	s.mu.RUnlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load buckets")
		return
	}
	utilities.WriteJSON(w, http.StatusOK, buckets)
}

// createBucket: POST /bucket
// Body: { id?, name, public?, file_size_limit?, allowed_mime_types? }
func (s *store) createBucket(w http.ResponseWriter, r *http.Request) {
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
	bucketID, err := cleanBucketID(body.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body.ID = bucketID

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
	// bucket.ID came from cleanBucketID above, so it needs no second pass.
	if err := mkdirAll(filepath.Join(s.root, bucket.ID), 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create bucket directory")
		return
	}
	utilities.WriteJSON(w, http.StatusOK, map[string]string{"name": bucket.Name})
}

// getBucket: GET /bucket/{id}
func (s *store) getBucket(w http.ResponseWriter, r *http.Request) {
	bucket, ok := s.resolveBucket(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	utilities.WriteJSON(w, http.StatusOK, bucket)
}

// updateBucket: PUT /bucket/{id}
// Body: { public?, file_size_limit?, allowed_mime_types? }
func (s *store) updateBucket(w http.ResponseWriter, r *http.Request) {
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
	// Only what the body actually named: a PUT that mentions nothing changes
	// nothing, rather than resetting the fields it omitted.
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
	utilities.WriteJSON(w, http.StatusOK, map[string]string{"message": "Successfully updated"})
}

// deleteBucket: DELETE /bucket/{id}
// The bucket must be empty.
func (s *store) deleteBucket(w http.ResponseWriter, r *http.Request) {
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

	bucketDir, err := s.bucketDir(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entries, err := readDir(bucketDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read bucket")
		return
	}
	// The sidecar directory is this implementation's own bookkeeping, not the
	// caller's data, so it does not make a bucket non-empty.
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
	if err := removeAll(bucketDir); err != nil { // #nosec G703 -- bucketDir is under storageRoot after bucket id validation above.
		writeError(w, http.StatusInternalServerError, "failed to delete bucket directory")
		return
	}
	utilities.WriteJSON(w, http.StatusOK, map[string]string{"message": "Successfully deleted"})
}

// emptyBucket: POST /bucket/{id}/empty
// Deletes everything in the bucket, including the sidecars.
func (s *store) emptyBucket(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if _, ok := s.resolveBucket(w, id); !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	bucketDir, err := s.bucketDir(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entries, err := readDir(bucketDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read bucket")
		return
	}
	for _, e := range entries {
		if err := removeAll(filepath.Join(bucketDir, e.Name())); err != nil { // #nosec G703 -- bucketDir is validated and entry names come from readDir(bucketDir).
			writeError(w, http.StatusInternalServerError, "failed to remove bucket entry")
			return
		}
	}
	utilities.WriteJSON(w, http.StatusOK, map[string]string{"message": "Successfully emptied"})
}
