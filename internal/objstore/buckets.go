package objstore

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/supatype/server/internal/utilities"
)

// ─── Bucket metadata ──────────────────────────────────────────────────────────

// Bucket mirrors the StorageBucketMeta shape expected by @supatype/client.
//
// AccessMode and S3BucketPolicy are written by the CLI when it provisions the
// schema's buckets. AccessMode decides anonymous reads; the policy is kept but
// not enforced. Every update rewrites buckets.json from this struct, so a field
// missing from it is a field the first update deletes.
type Bucket struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Public           bool     `json:"public"`
	FileSizeLimit    *int64   `json:"file_size_limit"`
	AllowedMimeTypes []string `json:"allowed_mime_types"`
	AccessMode       string   `json:"access_mode,omitempty"`
	S3BucketPolicy   *string  `json:"s3_bucket_policy"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

// accessModeOf is the access mode of a bucket that names none, the same default
// the CLI and the storage service use.
func accessModeOf(public bool) string {
	if public {
		return "public"
	}
	return "private"
}

// setAccessMode stores a mode and brings public into line with it, so the two
// never disagree about whether the bucket is public. A custom bucket is not
// public, as in the storage service: its rules decide who reads it, and a
// public flag kept beside them claimed otherwise.
func (b *Bucket) setAccessMode(mode string) {
	b.AccessMode = mode
	b.Public = mode == "public"
}

// servesAnonymously reports whether the bucket's objects may be read without a
// token. The mode decides, as in the storage service's checkReadAccess
// (middleware/access-control.ts): only a public mode does, and a custom mode is
// asked about as a private one is, whatever public says. A bucket recorded
// without a mode goes by public.
func (b *Bucket) servesAnonymously() bool {
	return b.mode() == "public"
}

// mode is the bucket's access mode. A bucket recorded without one goes by
// public.
func (b *Bucket) mode() string {
	if b.AccessMode == "" {
		return accessModeOf(b.Public)
	}
	return b.AccessMode
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
// Upload, public download and empty used to drop the error, so a buckets.json
// that could not be read answered "bucket not found". That tells the caller
// their bucket is gone when in fact the store is broken, and it is the kind of
// 404 someone acts on by recreating things.
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

// ─── Request bodies ───────────────────────────────────────────────────────────

// errAccessMode refuses an access mode the storage service does not know.
var errAccessMode = errors.New("access_mode must be public, private or custom")

// checkAccessMode accepts an absent mode or one of the three the storage
// service knows.
func checkAccessMode(mode *string) error {
	if mode == nil {
		return nil
	}
	switch *mode {
	case "public", "private", "custom":
		return nil
	}
	return errAccessMode
}

// bucketCreate is the body of POST /bucket. `supatype push` sends the schema's
// access mode and bucket policy with it; a body naming no mode gets the one
// public implies.
type bucketCreate struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Public           bool     `json:"public"`
	FileSizeLimit    *int64   `json:"file_size_limit"`
	AllowedMimeTypes []string `json:"allowed_mime_types"`
	AccessMode       *string  `json:"access_mode"`
	S3BucketPolicy   *string  `json:"s3_bucket_policy"`
}

// decodeBucketCreate reads and checks a create body, defaulting the id to the
// name.
func decodeBucketCreate(r io.Reader) (bucketCreate, error) {
	var body bucketCreate
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return body, errors.New("invalid request body")
	}
	if body.Name == "" {
		return body, errors.New("name is required")
	}
	if body.ID == "" {
		body.ID = body.Name
	}
	id, err := cleanBucketID(body.ID)
	if err != nil {
		return body, err
	}
	body.ID = id
	return body, checkAccessMode(body.AccessMode)
}

// bucket is the new bucket the body describes. A mode the body names wins
// over its public flag.
func (body bucketCreate) bucket(now string) Bucket {
	mode := accessModeOf(body.Public)
	if body.AccessMode != nil {
		mode = *body.AccessMode
	}
	b := Bucket{
		ID:               body.ID,
		Name:             body.Name,
		Public:           body.Public,
		FileSizeLimit:    body.FileSizeLimit,
		AllowedMimeTypes: body.AllowedMimeTypes,
		S3BucketPolicy:   body.S3BucketPolicy,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	b.setAccessMode(mode)
	return b
}

// bucketUpdate is the body of PUT /bucket/{id}. Only what the body names
// changes: a PUT that mentions nothing changes nothing, rather than resetting
// the fields it omitted. The policy is kept raw so that null, which clears it,
// can be told apart from absent, which keeps it.
type bucketUpdate struct {
	Public           *bool           `json:"public"`
	FileSizeLimit    *int64          `json:"file_size_limit"`
	AllowedMimeTypes []string        `json:"allowed_mime_types"`
	AccessMode       *string         `json:"access_mode"`
	S3BucketPolicy   json.RawMessage `json:"s3_bucket_policy"`
	policy           *string
}

// decodeBucketUpdate reads and checks an update body.
func decodeBucketUpdate(r io.Reader) (bucketUpdate, error) {
	var body bucketUpdate
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return body, errors.New("invalid request body")
	}
	if body.S3BucketPolicy != nil {
		if err := json.Unmarshal(body.S3BucketPolicy, &body.policy); err != nil {
			return body, errors.New("s3_bucket_policy must be a string or null")
		}
	}
	return body, checkAccessMode(body.AccessMode)
}

// apply changes the bucket as the body says. Every update writes the mode and
// public together, so a pair recorded out of step (a custom bucket marked
// public by an older build or by hand, or no mode at all) is corrected by the
// next update, whatever it names.
func (body bucketUpdate) apply(b *Bucket) {
	b.setAccessMode(body.nextAccessMode(b))
	if body.S3BucketPolicy != nil {
		b.S3BucketPolicy = body.policy
	}
	if body.FileSizeLimit != nil {
		b.FileSizeLimit = body.FileSizeLimit
	}
	if body.AllowedMimeTypes != nil {
		b.AllowedMimeTypes = body.AllowedMimeTypes
	}
}

// nextAccessMode is the bucket's mode after the update. A mode the body names
// wins over a public flag sent with it. Otherwise a public or private mode
// follows public, as it would have been derived from it, while a custom mode
// stays until it is named and the flag is ignored.
func (body bucketUpdate) nextAccessMode(b *Bucket) string {
	if body.AccessMode != nil {
		return *body.AccessMode
	}
	mode := b.mode()
	if body.Public != nil && mode != "custom" {
		return accessModeOf(*body.Public)
	}
	return mode
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
// Body: { id?, name, public?, file_size_limit?, allowed_mime_types?,
// access_mode?, s3_bucket_policy? }
func (s *store) createBucket(w http.ResponseWriter, r *http.Request) {
	body, err := decodeBucketCreate(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
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

	bucket := body.bucket(time.Now().UTC().Format(time.RFC3339))
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
	id, ok := routeParam(w, r, "id")
	if !ok {
		return
	}
	bucket, ok := s.resolveBucket(w, id)
	if !ok {
		return
	}
	utilities.WriteJSON(w, http.StatusOK, bucket)
}

// updateBucket: PUT /bucket/{id}
// Body: { public?, file_size_limit?, allowed_mime_types?, access_mode?,
// s3_bucket_policy? }
func (s *store) updateBucket(w http.ResponseWriter, r *http.Request) {
	id, ok := routeParam(w, r, "id")
	if !ok {
		return
	}

	body, err := decodeBucketUpdate(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	body.apply(&buckets[i])
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
	id, ok := routeParam(w, r, "id")
	if !ok {
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
		if e.Name() != sidecarDir {
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
	id, ok := routeParam(w, r, "id")
	if !ok {
		return
	}

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
