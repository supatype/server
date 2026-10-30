package objstore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wantBucketNotFound fails unless rec answers as the bucket routes do for a
// bucket that is not in the registry.
func wantBucketNotFound(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusNotFound || body["message"] != "bucket not found" {
		t.Errorf("%s: %d %s, want 404 bucket not found", what, rec.Code, rec.Body.String())
	}
}

// The store keeps its own bookkeeping in .supatype beside the buckets. It is
// not a bucket, so no object route reads, lists or removes what is in it.
func TestTheStoresOwnMetadataIsNotABucket(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)

	wantBucketNotFound(t, "read", do(t, handler, http.MethodGet, "/object/authenticated/.supatype/buckets.json", userToken(t), nil))
	wantBucketNotFound(t, "list", do(t, handler, http.MethodPost, "/object/list/.supatype", userToken(t), strings.NewReader(`{"prefix":""}`)))
	wantBucketNotFound(t, "remove", do(t, handler, http.MethodDelete, "/object/.supatype", userToken(t), strings.NewReader(`{"prefixes":["buckets.json"]}`)))

	rec := do(t, handler, http.MethodGet, "/bucket/files", serviceToken(t), nil)
	if rec.Code != http.StatusOK {
		t.Errorf("bucket files after the attempts: %d %s", rec.Code, rec.Body.String())
	}
}

// Every object route refuses a bucket that is not in the registry before it
// touches the disk, answering as the bucket routes do. A directory left on
// disk by hand does not make a bucket.
func TestEveryObjectRouteRefusesAnUnknownBucket(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "a.txt", "a")
	signed := signedPath(t, handler, "a.txt")
	placeFile(t, root, "ghost/a.txt", "stray")

	for name, tc := range map[string]struct{ method, path, bearer, body string }{
		"public":        {http.MethodGet, "/object/public/ghost/a.txt", "", ""},
		"authenticated": {http.MethodGet, "/object/authenticated/ghost/a.txt", userToken(t), ""},
		"sign":          {http.MethodPost, "/object/sign/ghost/a.txt", userToken(t), `{"expiresIn":60}`},
		"list":          {http.MethodPost, "/object/list/ghost", userToken(t), `{"prefix":""}`},
		"upload":        {http.MethodPost, "/object/ghost/b.txt", userToken(t), "b"},
		"remove":        {http.MethodDelete, "/object/ghost", userToken(t), `{"prefixes":["a.txt"]}`},
	} {
		wantBucketNotFound(t, name, do(t, handler, tc.method, tc.path, tc.bearer, strings.NewReader(tc.body)))
	}
	if _, err := os.Stat(filepath.Join(root, "ghost", "a.txt")); err != nil {
		t.Errorf("ghost/a.txt after the remove: %v", err)
	}

	// A signed URL names its bucket in its token; once that bucket is gone,
	// the URL answers as any other route does for it.
	if rec := do(t, handler, http.MethodDelete, "/bucket/files", serviceToken(t), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete of a non-empty bucket: %d", rec.Code)
	}
	rec := do(t, handler, http.MethodPost, "/bucket/files/empty", serviceToken(t), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, handler, http.MethodDelete, "/bucket/files", serviceToken(t), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	wantBucketNotFound(t, "signed", do(t, handler, http.MethodGet, signed, "", nil))
}

// .supatype is the store's own directory, so no bucket may be created as it,
// nor under a name the file system takes for it.
func TestABucketCannotBeTheStoresOwnDirectory(t *testing.T) {
	handler, root := newStore(t)
	ids := []string{".supatype"}
	if hostRules.foldsCase {
		ids = append(ids, ".SUPATYPE", ".SupaType")
	}
	if onWindows {
		ids = append(ids, ".supatype.", ".supatype. .")
		if short := "SUPATY~1"; (&store{root: root}).sameDir(short, storeMetaDir) {
			ids = append(ids, short)
		} else {
			t.Log("this volume gives .supatype no 8.3 short name")
		}
	}
	for _, id := range ids {
		body := `{"id":` + jsonString(t, id) + `,"name":` + jsonString(t, id) + `}`
		if rec := do(t, handler, http.MethodPost, "/bucket", serviceToken(t), strings.NewReader(body)); rec.Code != http.StatusBadRequest {
			t.Errorf("create %q: %d %s, want 400", id, rec.Code, rec.Body.String())
		}
	}
}

// Native dev's everyday flow still works in a created bucket: upload, list,
// download over every path, and remove.
func TestACreatedBucketStillServesItsObjects(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "dir/a.txt", "hello")
	wantListed(t, handler, "dir", "a.txt")
	for name, rec := range downloads(t, handler, "dir/a.txt") {
		if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	rec := do(t, handler, http.MethodDelete, "/object/files", userToken(t), strings.NewReader(`{"prefixes":["dir/a.txt"]}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "dir/a.txt") {
		t.Errorf("remove: %d %s", rec.Code, rec.Body.String())
	}
}

// createBucketAs posts a create for id and returns the answer.
func createBucketAs(t *testing.T, handler http.Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"id":` + jsonString(t, id) + `,"name":` + jsonString(t, id) + `}`
	return do(t, handler, http.MethodPost, "/bucket", serviceToken(t), strings.NewReader(body))
}

// A bucket id the file system takes for an existing bucket's directory is
// that bucket: creating it is refused as a duplicate, rather than giving two
// registry entries one directory.
func TestABucketCannotAliasAnotherBucket(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	makeBucket(t, handler, "longbucketname", true)
	var ids []string
	if hostRules.foldsCase {
		ids = append(ids, "FILES", "Files")
	}
	if onWindows {
		ids = append(ids, "files.", "files. .")
		if short := "LONGBU~1"; (&store{root: root}).sameDir(short, "longbucketname") {
			ids = append(ids, short)
		} else {
			t.Log("this volume gives longbucketname no 8.3 short name")
		}
	}
	for _, id := range ids {
		if rec := createBucketAs(t, handler, id); rec.Code != http.StatusConflict {
			t.Errorf("create %q beside files: %d %s, want 409", id, rec.Code, rec.Body.String())
		}
	}
	if rec := createBucketAs(t, handler, "files2"); rec.Code != http.StatusOK {
		t.Errorf("create files2: %d %s", rec.Code, rec.Body.String())
	}
}

// Under Windows rules FILES is the bucket files, as its directory is; under
// posix rules it is a bucket of its own.
func TestABucketAliasFollowsTheNameRules(t *testing.T) {
	root := t.TempDir()
	s := &store{root: root}
	placeFile(t, root, "files/a.txt", "a")
	registered := []Bucket{{ID: "files"}}
	for _, tc := range []struct {
		rules string
		id    string
		want  bool
	}{
		{"windows", "FILES", true},
		{"windows", "files", true},
		{"windows", "other", false},
		{"posix", "FILES", false},
		{"posix", "files", true},
	} {
		got := s.aliasedBucket(bothRules[tc.rules], registered, tc.id) != nil
		if got != tc.want {
			t.Errorf("%s: %q aliases files = %t, want %t", tc.rules, tc.id, got, tc.want)
		}
	}
}
