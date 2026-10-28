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

	for name, body := range map[string]io.Reader{
		"declared length": strings.NewReader("12345"),
		"unknown length":  unknownLength("12345"),
	} {
		rec := uploadAs(t, handler, "/object/small/big.txt", "text/plain", body, nil)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: status = %d, want 413 (%s)", name, rec.Code, rec.Body.String())
		}
		var got map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := "File size 5 B exceeds the maximum allowed size of 4 B"
		if got["error"] != want {
			t.Errorf("%s: error = %q, want %q", name, got["error"], want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "small", "big.txt")); !os.IsNotExist(err) {
		t.Errorf("a refused upload was stored: %v", err)
	}

	if rec := uploadAs(t, handler, "/object/small/fits.txt", "text/plain", unknownLength("1234"), nil); rec.Code != http.StatusOK {
		t.Errorf("an upload at the limit: %d %s", rec.Code, rec.Body.String())
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

// A body that fails while it is read against the limit is a failed upload, as
// it is when it streams to disk.
func TestALimitedUploadWhoseBodyFails(t *testing.T) {
	handler, _ := newStore(t)
	makeLimitedBucket(t, handler, `{"name":"small","file_size_limit":4}`)

	if rec := uploadAs(t, handler, "/object/small/a.txt", "text/plain", failingReader{}, nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}
