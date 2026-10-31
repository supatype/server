package objstore

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// requestWithRawPath is a request whose raw path is rawPath as given, as a proxy in front of the
// store may forward one that Go's own parser would have refused.
func requestWithRawPath(method, path, rawPath, bearer, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.URL.RawPath = rawPath
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

// A route parameter that does not decode is refused with 400 on every route: the name it would be
// read as is a guess, and no bucket or object is looked up, created or removed under a guess.
func TestARouteParameterThatDoesNotDecodeIsRefused(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "a", "real")

	for name, tc := range map[string]struct{ method, path, raw, body string }{
		"get a bucket":            {http.MethodGet, "/bucket/f%zz", "/bucket/f%zz", ""},
		"update a bucket":         {http.MethodPut, "/bucket/f%zz", "/bucket/f%zz", `{"public":false}`},
		"delete a bucket":         {http.MethodDelete, "/bucket/f%zz", "/bucket/f%zz", ""},
		"empty a bucket":          {http.MethodPost, "/bucket/f%zz/empty", "/bucket/f%zz/empty", ""},
		"upload, bucket":          {http.MethodPost, "/object/f%zz/a", "/object/f%zz/a", "x"},
		"upload, object":          {http.MethodPost, "/object/files/a%zz", "/object/files/a%zz", "x"},
		"remove from a bucket":    {http.MethodDelete, "/object/f%zz", "/object/f%zz", `{"prefixes":["a"]}`},
		"list a bucket's objects": {http.MethodPost, "/object/list/f%zz", "/object/list/f%zz", `{"prefix":""}`},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, requestWithRawPath(tc.method, "/", tc.raw, serviceToken(t), tc.body))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid path") {
			t.Errorf("%s: %d %s, want 400 invalid path", name, rec.Code, rec.Body.String())
		}
	}
	if got := do(t, handler, http.MethodGet, "/object/public/files/a", "", nil).Body.String(); got != "real" {
		t.Errorf("object a now reads %q, want it untouched", got)
	}
	if entries, _ := filepath.Glob(filepath.Join(root, "files", "*")); len(entries) == 0 {
		t.Error("the bucket was emptied or removed")
	}
}

// A bucket policy that is neither a string nor null is refused, and the bucket keeps its own.
func TestABucketPolicyThatIsNotAStringIsRefused(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	rec := do(t, handler, http.MethodPut, "/bucket/files", serviceToken(t), strings.NewReader(`{"s3_bucket_policy":123}`))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "s3_bucket_policy must be a string or null") {
		t.Errorf("update: %d %s, want 400 naming s3_bucket_policy", rec.Code, rec.Body.String())
	}
}

// A size limit of a gigabyte or more is named in gigabytes in the refusal, as the smaller units are
// named in theirs.
func TestASizeLimitInGigabytesIsNamedInGigabytes(t *testing.T) {
	handler, _ := newStore(t)
	makeLimitedBucket(t, handler, `{"name":"big","file_size_limit":1073741824}`)
	body := &countingBody{remaining: 2 << 30}
	req := httptest.NewRequest(http.MethodPost, "/object/big/huge.bin", body)
	req.ContentLength = 2 << 30
	req.Header.Set("Authorization", "Bearer "+userToken(t))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body.String(), err)
	}
	if want := "File size 2.0 GB exceeds the maximum allowed size of 1.0 GB"; rec.Code != http.StatusRequestEntityTooLarge || got["error"] != want {
		t.Errorf("%d %q, want 413 %q", rec.Code, got["error"], want)
	}
	if body.read != 0 {
		t.Errorf("read %d bytes of a body declared over the limit", body.read)
	}
}

// vanishedEntry is a directory entry whose file was removed after the directory was read.
type vanishedEntry struct{ name string }

func (e vanishedEntry) Name() string               { return e.name }
func (e vanishedEntry) IsDir() bool                { return false }
func (e vanishedEntry) Type() fs.FileMode          { return 0 }
func (e vanishedEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }

// A file removed between reading a folder and reading the file's details is left out of the
// listing, not listed with details it no longer has.
func TestAFileRemovedWhileAFolderIsListedIsLeftOut(t *testing.T) {
	s := &store{root: t.TempDir(), rules: hostRules}
	listing := folderListing{bucket: "files", folder: "", rel: "", dir: s.root}
	if item, ok := s.childItem(listing, vanishedEntry{name: "gone.txt"}); ok {
		t.Errorf("a vanished file was listed: %+v", item)
	}
}

// Under Windows rules a segment Windows cannot name a file with (here one with a colon) and no
// encoding marker is not where any object is stored now: every such name is stored encoded.
func TestASegmentWindowsRefusesIsNoStoredLocation(t *testing.T) {
	if windowsRules.isStoredLocation("notes/a:b") {
		t.Error(`"notes/a:b" was taken for a stored location under Windows rules`)
	}
	if !windowsRules.isStoredLocation("notes/" + windowsRules.diskSegment("a:b")) {
		t.Error("the encoded form of a:b was not taken for its stored location")
	}
}

// The host reserves the names its file system cannot give a file of their own: never a plain
// name, always an empty or parent name, and on Windows the device names.
func TestTheHostReservesWhatItsFileSystemCannotName(t *testing.T) {
	for _, name := range []string{"", ".."} {
		if !hostRules.reserved(name) {
			t.Errorf("%q is not reserved", name)
		}
	}
	if hostRules.reserved("notes.txt") {
		t.Error(`"notes.txt" is reserved`)
	}
	if runtime.GOOS == "windows" && !hostRules.reserved("NUL") {
		t.Error(`"NUL" is not reserved on Windows`)
	}
}
