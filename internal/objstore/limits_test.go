package objstore

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bucket's own limits are enforced on upload with the storage service's
// status codes and bodies, so code written against native dev behaves the
// same once it runs against the Docker stack.

// makeLimitedBucket creates a bucket from a raw create body.
func makeLimitedBucket(t *testing.T, handler http.Handler, body string) {
	t.Helper()
	rec := do(t, handler, http.MethodPost, "/bucket", serviceToken(t), strings.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("creating bucket: %d %s", rec.Code, rec.Body.String())
	}
}

// uploadAs sends one upload with the given Content-Type and extra headers.
func uploadAs(t *testing.T, handler http.Handler, target, contentType string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, body)
	req.Header.Set("Authorization", "Bearer "+userToken(t))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// unknownLength hides the length of a body, as a chunked upload does.
func unknownLength(s string) io.Reader {
	return io.MultiReader(strings.NewReader(s))
}

func TestAnUploadOverTheBucketSizeLimitIs413(t *testing.T) {
	handler, root := newStore(t)
	makeLimitedBucket(t, handler, `{"name":"small","file_size_limit":4}`)

	// Past the limit the rest of a body of unknown length is not read, so its
	// size is not known and the refusal does not state one.
	for name, tc := range map[string]struct {
		body io.Reader
		want string
	}{
		"declared length": {strings.NewReader("12345"), "File size 5 B exceeds the maximum allowed size of 4 B"},
		"unknown length":  {unknownLength("12345"), "File size exceeds the maximum allowed size of 4 B"},
	} {
		rec := uploadAs(t, handler, "/object/small/big.txt", "text/plain", tc.body, nil)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: status = %d, want 413 (%s)", name, rec.Code, rec.Body.String())
		}
		var got map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["error"] != tc.want {
			t.Errorf("%s: error = %q, want %q", name, got["error"], tc.want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "small", "big.txt")); !os.IsNotExist(err) {
		t.Errorf("a refused upload was stored: %v", err)
	}

	if rec := uploadAs(t, handler, "/object/small/fits.txt", "text/plain", unknownLength("1234"), nil); rec.Code != http.StatusOK {
		t.Errorf("an upload at the limit: %d %s", rec.Code, rec.Body.String())
	}
}

// countingBody is an upload body of a given size that counts what is read
// from it, standing in for a client still sending.
type countingBody struct {
	remaining, read int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	if c.remaining == 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > c.remaining {
		n = c.remaining
	}
	c.remaining -= n
	c.read += n
	return int(n), nil
}

// An upload over the limit is refused once the limit is passed: the rest of
// the body is not read, so a client cannot make the server swallow an
// unbounded stream to learn its size. A declared length over the limit is
// refused before any of the body is read.
func TestAnOversizedUploadIsRefusedWithoutReadingTheRest(t *testing.T) {
	handler, _ := newStore(t)
	makeLimitedBucket(t, handler, `{"name":"small","file_size_limit":4}`)

	for name, tc := range map[string]struct {
		declared, maxRead int64
		want              string
	}{
		"unknown length":  {-1, 5, "File size exceeds the maximum allowed size of 4 B"},
		"declared length": {64 << 20, 0, "File size 64.0 MB exceeds the maximum allowed size of 4 B"},
	} {
		body := &countingBody{remaining: 64 << 20}
		req := httptest.NewRequest(http.MethodPost, "/object/small/big.bin", body)
		req.ContentLength = tc.declared
		req.Header.Set("Authorization", "Bearer "+userToken(t))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: status = %d, want 413 (%s)", name, rec.Code, rec.Body.String())
		}
		if body.read > tc.maxRead {
			t.Errorf("%s: read %d bytes of the body, want at most %d", name, body.read, tc.maxRead)
		}
		var got map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["error"] != tc.want {
			t.Errorf("%s: error = %q, want %q", name, got["error"], tc.want)
		}
	}
}

// A refused overwrite leaves the object that was there.
func TestARefusedOverwriteKeepsTheObject(t *testing.T) {
	handler, root := newStore(t)
	makeLimitedBucket(t, handler, `{"name":"small","file_size_limit":4}`)
	upload(t, handler, "small", "a.txt", "old")

	rec := uploadAs(t, handler, "/object/small/a.txt", "text/plain", strings.NewReader("too long"), map[string]string{"x-upsert": "true"})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	got, err := os.ReadFile(filepath.Join(root, "small", "a.txt"))
	if err != nil || string(got) != "old" {
		t.Errorf("object = %q, %v; want it untouched", got, err)
	}
}

func TestAnUploadOfATypeTheBucketRefusesIs415(t *testing.T) {
	handler, _ := newStore(t)
	makeLimitedBucket(t, handler, `{"name":"docs","allowed_mime_types":["image/*","application/pdf"]}`)

	rec := uploadAs(t, handler, "/object/docs/a.txt", "Text/Plain; charset=utf-8", strings.NewReader("hi"), nil)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 (%s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Error   string   `json:"error"`
		Allowed []string `json:"allowed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := "Content type 'text/plain' is not allowed for bucket 'docs'. Allowed types: image/*, application/pdf"
	if got.Error != want {
		t.Errorf("error = %q, want %q", got.Error, want)
	}
	if strings.Join(got.Allowed, ",") != "image/*,application/pdf" {
		t.Errorf("allowed = %v", got.Allowed)
	}

	for name, contentType := range map[string]string{
		"wildcard":   "image/png",
		"exact":      "application/pdf",
		"parameters": "IMAGE/JPEG; q=1",
	} {
		rec := uploadAs(t, handler, "/object/docs/"+name, contentType, strings.NewReader("x"), nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// The empty placeholder that makes a folder show is exempt from the type rule,
// as in the storage service; anything with content under that name is not.
func TestAFolderPlaceholderIsExemptFromTheTypeRule(t *testing.T) {
	handler, _ := newStore(t)
	makeLimitedBucket(t, handler, `{"name":"docs","allowed_mime_types":["image/*"]}`)

	if rec := uploadAs(t, handler, "/object/docs/dir/.emptyFolderPlaceholder", "", strings.NewReader(""), nil); rec.Code != http.StatusOK {
		t.Errorf("empty placeholder: %d %s", rec.Code, rec.Body.String())
	}
	if rec := uploadAs(t, handler, "/object/docs/other/.emptyFolderPlaceholder", "", strings.NewReader("x"), nil); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("placeholder with content: %d, want 415", rec.Code)
	}
}

// buckets.json is also written by the CLI, which records how the bucket is
// accessed. An update rewrites the file, and must not drop what it does not
// itself manage.
func TestABucketUpdateKeepsTheAccessModeTheCLIWrote(t *testing.T) {
	handler, root := newStore(t)
	written := `[{"id":"media","name":"media","public":false,"file_size_limit":null,"allowed_mime_types":null,` +
		`"access_mode":"custom","s3_bucket_policy":"{\"Version\":\"2012-10-17\"}","created_at":"x","updated_at":"x"}]`
	if err := os.WriteFile(filepath.Join(root, ".supatype", "buckets.json"), []byte(written), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := do(t, handler, http.MethodPut, "/bucket/media", serviceToken(t), strings.NewReader(`{"public":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(root, ".supatype", "buckets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved []map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved[0]["access_mode"] != "custom" {
		t.Errorf("access_mode = %v, want custom", saved[0]["access_mode"])
	}
	if saved[0]["s3_bucket_policy"] != `{"Version":"2012-10-17"}` {
		t.Errorf("s3_bucket_policy = %v", saved[0]["s3_bucket_policy"])
	}
}

// A bucket made through the API records its access mode the way the CLI does.
func TestACreatedBucketRecordsItsAccessMode(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "open", true)
	makeBucket(t, handler, "closed", false)

	for id, want := range map[string]string{"open": "public", "closed": "private"} {
		rec := do(t, handler, http.MethodGet, "/bucket/"+id, serviceToken(t), nil)
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["access_mode"] != want {
			t.Errorf("%s: access_mode = %v, want %s", id, got["access_mode"], want)
		}
	}
}

// getBucketJSON reads one bucket as the API answers it.
func getBucketJSON(t *testing.T, handler http.Handler, id string) map[string]any {
	t.Helper()
	rec := do(t, handler, http.MethodGet, "/bucket/"+id, serviceToken(t), nil)
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("get %s: %d %s", id, rec.Code, rec.Body.String())
	}
	return got
}

// `supatype push` provisions buckets over HTTP with the schema's access mode
// and bucket policy, and the store keeps both as sent.
func TestACreatedBucketKeepsTheAccessModeAndPolicyItWasGiven(t *testing.T) {
	handler, _ := newStore(t)
	makeLimitedBucket(t, handler, `{"id":"media","name":"media","public":false,"access_mode":"custom","s3_bucket_policy":"{\"Version\":\"2012-10-17\"}"}`)

	got := getBucketJSON(t, handler, "media")
	if got["access_mode"] != "custom" {
		t.Errorf("access_mode = %v, want custom", got["access_mode"])
	}
	if got["s3_bucket_policy"] != `{"Version":"2012-10-17"}` {
		t.Errorf("s3_bucket_policy = %v", got["s3_bucket_policy"])
	}
}

func TestABucketWithAnUnknownAccessModeIsRefused(t *testing.T) {
	handler, _ := newStore(t)
	rec := do(t, handler, http.MethodPost, "/bucket", serviceToken(t), strings.NewReader(`{"name":"media","access_mode":"open"}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create: %d %s, want 400", rec.Code, rec.Body.String())
	}
	makeBucket(t, handler, "media", false)
	rec = do(t, handler, http.MethodPut, "/bucket/media", serviceToken(t), strings.NewReader(`{"access_mode":"open"}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("update: %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// An update keeps public and the access mode consistent: a named mode is
// stored, a mode that only followed public follows it again, and a custom mode
// is the caller's to change.
func TestABucketUpdateKeepsTheAccessModeConsistent(t *testing.T) {
	for name, tc := range map[string]struct {
		create, update, wantMode string
		wantPublic               bool
	}{
		"public follows":     {`{"name":"m","public":false}`, `{"public":true}`, "public", true},
		"private follows":    {`{"name":"m","public":true}`, `{"public":false}`, "private", false},
		"named mode wins":    {`{"name":"m","public":false}`, `{"public":true,"access_mode":"custom"}`, "custom", true},
		"mode alone":         {`{"name":"m","public":true}`, `{"access_mode":"private"}`, "private", true},
		"custom is kept":     {`{"name":"m","access_mode":"custom"}`, `{"public":true}`, "custom", true},
		"other fields alone": {`{"name":"m","access_mode":"custom"}`, `{"file_size_limit":10}`, "custom", false},
	} {
		t.Run(name, func(t *testing.T) {
			handler, _ := newStore(t)
			makeLimitedBucket(t, handler, tc.create)
			if rec := do(t, handler, http.MethodPut, "/bucket/m", serviceToken(t), strings.NewReader(tc.update)); rec.Code != http.StatusOK {
				t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
			}
			got := getBucketJSON(t, handler, "m")
			if got["access_mode"] != tc.wantMode || got["public"] != tc.wantPublic {
				t.Errorf("access_mode = %v, public = %v; want %s, %t", got["access_mode"], got["public"], tc.wantMode, tc.wantPublic)
			}
		})
	}
}

// The policy is set by naming it, cleared by naming null, and kept otherwise.
func TestABucketUpdateSetsAndClearsThePolicy(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "m", false)

	for _, step := range []struct {
		body string
		want any
	}{
		{`{"s3_bucket_policy":"p1"}`, "p1"},
		{`{"public":true}`, "p1"},
		{`{"s3_bucket_policy":null}`, nil},
	} {
		if rec := do(t, handler, http.MethodPut, "/bucket/m", serviceToken(t), strings.NewReader(step.body)); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", step.body, rec.Code, rec.Body.String())
		}
		if got := getBucketJSON(t, handler, "m")["s3_bucket_policy"]; got != step.want {
			t.Errorf("after %s: s3_bucket_policy = %v, want %v", step.body, got, step.want)
		}
	}
}

// A body that fails while it is read against the limit is a failed upload, as
// it is when it streams to disk.
func TestALimitedUploadWhoseBodyFails(t *testing.T) {
	handler, _ := newStore(t)
	makeLimitedBucket(t, handler, `{"name":"small","file_size_limit":4}`)

	if rec := uploadAs(t, handler, "/object/small/a.txt", "text/plain", failingReader{}, nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}
