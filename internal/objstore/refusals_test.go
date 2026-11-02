package objstore

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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

// The request layer strips one leading slash from a name, and only one. A name that still starts
// with "/" has an empty first part and is refused: "///a" reached object "a", one slash stripped by
// the route and another by the path check.
func TestAnExtraLeadingSlashNeverReachesAnotherObject(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "a", "real")
	if rec := do(t, handler, http.MethodGet, "/object/public/files///a", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("GET ///a: %d %q, want 400", rec.Code, rec.Body.String())
	}
	rec := do(t, handler, http.MethodDelete, "/object/files", serviceToken(t), strings.NewReader(`{"prefixes":["//a"]}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("remove //a: %d %s, want 400", rec.Code, rec.Body.String())
	}
	if got := do(t, handler, http.MethodGet, "/object/public/files/a", "", nil).Body.String(); got != "real" {
		t.Errorf("object a now reads %q, want it kept", got)
	}
}

// A remove that names one object it refuses removes nothing: every name is checked before the
// first is deleted. It used to delete the names before the refused one and then answer 400.
func TestARemoveWithARefusedNameRemovesNothing(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "a", "first")
	upload(t, handler, "files", "b", "second")
	rec := do(t, handler, http.MethodDelete, "/object/files", serviceToken(t), strings.NewReader(`{"prefixes":["a","x/../b","b"]}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("remove: %d %s, want 400", rec.Code, rec.Body.String())
	}
	for name, want := range map[string]string{"a": "first", "b": "second"} {
		if got := do(t, handler, http.MethodGet, "/object/public/files/"+name, "", nil).Body.String(); got != want {
			t.Errorf("object %q now reads %q, want it kept", name, got)
		}
	}
}

// A signed URL is only made for a name that could be served: one the store refuses gets 400, not
// a URL that can never work.
func TestASignedURLIsRefusedForANameThatCannotBeServed(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", false)
	for _, name := range []string{"x/../a", ".meta/a.json", "a//b"} {
		rec := do(t, handler, http.MethodPost, "/object/sign/files/"+escapeSegments(name), userToken(t), strings.NewReader(`{"expiresIn":60}`))
		if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "signedURL") {
			t.Errorf("sign %q: %d %s, want 400 and no URL", name, rec.Code, rec.Body.String())
		}
	}
}

// itemNames is the names in a 200 answer listing items, a list or a remove's; the test fails
// when the answer is not a 200 or its body is not such a list.
func itemNames(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("want a 200 listing items, got %d %s", rec.Code, rec.Body.String())
	}
	var items []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("not a list of items: %v: %s", err, rec.Body.String())
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

// removedAs reports whether a remove answered name as removed: a refused remove removed nothing.
func removedAs(t *testing.T, rec *httptest.ResponseRecorder, name string) bool {
	t.Helper()
	if rec.Code == http.StatusBadRequest {
		return false
	}
	return slices.Contains(itemNames(t, rec), name)
}

// listPrefix is the status a list of prefix in bucket answers, and the names when it is a 200.
func listPrefix(t *testing.T, handler http.Handler, bucket, prefix string) (int, []string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"prefix": prefix})
	if err != nil {
		t.Fatal(err)
	}
	rec := do(t, handler, http.MethodPost, "/object/list/"+bucket, serviceToken(t), strings.NewReader(string(body)))
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	return rec.Code, itemNames(t, rec)
}

// A list prefix follows the rule a name does: one leading slash is stripped, and so is the one
// trailing slash a folder is normally written with. What is left has no empty part, as GET
// refuses one: "//", "a//" and "a//b" are refused rather than listing the root, a and a/b.
// A "." or ".." part is still cleaned, as "a/.." is the bucket root
// (TestTheSidecarTreeIsNeverListedByAnotherSpelling).
func TestAListPrefixStripsOneSlashAtEachEnd(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "a/x", "inside")
	for _, prefix := range []string{"a", "a/", "/a", "/a/"} {
		if code, names := listPrefix(t, handler, "files", prefix); code != http.StatusOK || len(names) != 1 || names[0] != "x" {
			t.Errorf("list %q: %d %v, want 200 [x]", prefix, code, names)
		}
	}
	for _, prefix := range []string{"///a", "//a", "//", "a//", "a//b"} {
		if code, names := listPrefix(t, handler, "files", prefix); code != http.StatusBadRequest {
			t.Errorf("list %q: %d %v, want 400", prefix, code, names)
		}
	}
}

// A refused remove says which name it refused, among the ones it lists.
func TestARefusedRemoveNamesTheNameItRefused(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	rec := do(t, handler, http.MethodDelete, "/object/files", serviceToken(t), strings.NewReader(`{"prefixes":["a","/x/../b"]}`))
	// As the client sent it, its leading slash included.
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `\"/x/../b\"`) {
		t.Errorf("remove: %d %s, want 400 naming \"/x/../b\"", rec.Code, rec.Body.String())
	}
}

// A signed URL whose token holds a name the store refuses, one signed before names were checked
// when a URL is made, is refused with 400 rather than served from the path the name resolves to.
func TestASignedURLForARefusedNameIsRefused(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "files", false)
	upload(t, handler, "files", "a", "real")
	signer := &store{root: root, jwtSecret: []byte(secret), mu: &sync.RWMutex{}}
	token := signer.signToken(signedPayload{B: "files", P: "x/../a", Exp: time.Now().Add(time.Hour).Unix()})

	rec := do(t, handler, http.MethodGet, "/object/sign/files/a?token="+token, "", nil)
	// Refused as a route's name is, naming the name: the URL in hand says nothing about it.
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `\"x/../a\"`) {
		t.Errorf("GET: %d %q, want 400 naming \"x/../a\"", rec.Code, rec.Body.String())
	}
}

// An anonymous download from a private bucket is refused for the bucket (403) before its name is
// looked at: it learns nothing about which names the store would take.
func TestAPrivateBucketRefusesAnAnonymousDownloadBeforeTheName(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", false)
	for _, name := range []string{"a", "x/../a"} {
		rec := do(t, handler, http.MethodGet, "/object/public/files/"+escapeSegments(name), "", nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %q: %d %s, want 403", name, rec.Code, rec.Body.String())
		}
	}
}
