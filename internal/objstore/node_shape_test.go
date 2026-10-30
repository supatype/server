package objstore

import (
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The storage service in the supatype repo (packages/storage) is the contract
// for what a list and a remove answer: @supatype/client checks every list entry
// field by field and refuses one that differs. This store used to answer in its
// own shape: owner and the timestamps left out when empty, size and mimetype
// only nested under metadata, and a remove answering whole entries. So a client
// pointed at native dev, which serves storage from here, could not list.
//
// testdata/node-list-entry.json is a byte-identical copy of the supatype repo's
// packages/storage/tests/fixtures/list-entry.json, a list entry captured from
// the Node service. Both servers are checked against it.

// nodeContract is the fixture: one real file entry, one folder entry, and the fields of a file
// entry that may be null.
type nodeContract struct {
	Entry    map[string]any `json:"entry"`
	Folder   map[string]any `json:"folder"`
	Nullable []string       `json:"nullable"`
}

func loadNodeContract(t *testing.T) nodeContract {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "node-list-entry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract nodeContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	return contract
}

// kindOf names a decoded JSON value's kind: string, number, bool, object, array or null.
func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case map[string]any:
		return "object"
	default:
		return "array"
	}
}

// assertNodeShape fails unless entry has exactly the contract's fields, each of
// the contract's kind, or null where the contract allows it.
func assertNodeShape(t *testing.T, entry map[string]any) {
	t.Helper()
	contract := loadNodeContract(t)
	want := slices.Sorted(maps.Keys(contract.Entry))
	got := slices.Sorted(maps.Keys(entry))
	if !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	for _, k := range want {
		gotKind, wantKind := kindOf(entry[k]), kindOf(contract.Entry[k])
		if gotKind != wantKind && (gotKind != "null" || !slices.Contains(contract.Nullable, k)) {
			t.Errorf("%s is %s (%v), want %s", k, gotKind, entry[k], wantKind)
		}
	}
}

// listEntries lists a bucket and decodes the answer without this package's types.
func listEntries(t *testing.T, handler http.Handler, bucket string) []map[string]any {
	t.Helper()
	rec := do(t, handler, http.MethodPost, "/object/list/"+bucket, userToken(t), strings.NewReader(`{}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var entries []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestAListEntryHasTheNodeServiceShape(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	rec := do(t, handler, http.MethodPost, "/object/avatars/a.txt", userToken(t), strings.NewReader("four"))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}

	entries := listEntries(t, handler, "avatars")
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}
	assertNodeShape(t, entries[0])
	if entries[0]["size"] != float64(4) || entries[0]["owner"] != "user-1" {
		t.Errorf("entry = %v, want size 4 and the uploader as owner", entries[0])
	}
}

// A file with no sidecar has no owner and no recorded type: those are null, as
// the Node service sends them, not left out.
func TestAnEntryWithNoSidecarHasTheNodeServiceShape(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)
	if err := os.WriteFile(filepath.Join(root, "avatars", "orphan.png"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries := listEntries(t, handler, "avatars")
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}
	assertNodeShape(t, entries[0])
	if entries[0]["owner"] != nil || entries[0]["mimetype"] != nil || entries[0]["size"] != float64(1) {
		t.Errorf("entry = %v, want no owner, no type and size 1", entries[0])
	}
}

// A remove answers each removed object's name and bucket, and nothing else.
func TestRemoveAnswersWhatTheNodeServiceAnswers(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "one")

	rec := do(t, handler, http.MethodDelete, "/object/avatars", userToken(t), strings.NewReader(`{"prefixes":["a.png"]}`))
	if got := strings.TrimSpace(rec.Body.String()); got != `[{"name":"a.png","bucket_id":"avatars"}]` {
		t.Errorf("body = %s", got)
	}
}

// A subfolder lists as the contract's folder entry: exactly its fields, every one null but the
// bucket and the folder's own name.
func TestAListedFolderHasTheContractsFolderShape(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "photos/a.txt", "four")

	contract := loadNodeContract(t)
	if len(contract.Folder) == 0 {
		t.Fatal("the fixture has no folder entry")
	}
	entries := listEntries(t, handler, "avatars")
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want only the photos folder", entries)
	}
	want := slices.Sorted(maps.Keys(contract.Folder))
	if got := slices.Sorted(maps.Keys(entries[0])); !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	for _, k := range want {
		if gotKind, wantKind := kindOf(entries[0][k]), kindOf(contract.Folder[k]); gotKind != wantKind {
			t.Errorf("%s is %s (%v), want %s", k, gotKind, entries[0][k], wantKind)
		}
	}
	if entries[0]["bucket_id"] != "avatars" || entries[0]["name"] != "photos" {
		t.Errorf("folder = %v, want bucket avatars and name photos", entries[0])
	}
}

// The sidecar tree that holds each object's metadata is never listed, whether
// the prefix names it or a folder inside it.
func TestTheSidecarTreeIsNeverListed(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "photos/a.txt", "four")

	for _, prefix := range []string{".meta", ".meta/", ".meta/photos"} {
		rec := do(t, handler, http.MethodPost, "/object/list/avatars", userToken(t),
			strings.NewReader(`{"prefix":"`+prefix+`"}`))
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" || rec.Header().Get("X-Total-Count") != "0" {
			t.Errorf("prefix %q: %d %s (total %q), want an empty listing", prefix, rec.Code, rec.Body.String(), rec.Header().Get("X-Total-Count"))
		}
	}
}

// A prefix that only resolves to the sidecar tree, or to the bucket root by a
// detour, hides the sidecars as the plain prefix does. Comparing the prefix as
// written let "./.meta" list every sidecar's JSON.
func TestTheSidecarTreeIsNeverListedByAnotherSpelling(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "photos/a.txt", "four")

	for _, prefix := range []string{"./.meta", "photos/../.meta", "photos/../.meta/photos", ".//.meta/"} {
		names, total := listNames(t, handler, `{"prefix":"`+prefix+`"}`)
		if len(names) != 0 || total != "0" {
			t.Errorf("prefix %q listed %q (total %q), want an empty listing", prefix, names, total)
		}
	}
	for _, prefix := range []string{"photos/..", "./", "photos/../."} {
		names, _ := listNames(t, handler, `{"prefix":"`+prefix+`"}`)
		if want := []string{"photos/"}; !slices.Equal(names, want) {
			t.Errorf("prefix %q listed %q, want %q", prefix, names, want)
		}
	}
}

// No object route reaches a sidecar, however its path is spelled: a download
// would hand out the bookkeeping, and a delete or an upload would corrupt it.
func TestNoObjectRouteReachesASidecar(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "photos/a.txt", "four")
	sidecar := filepath.Join(root, "avatars", ".meta", "photos", "a.txt.json")

	for _, objPath := range []string{".meta/photos/a.txt.json", "photos/%2E%2E/.meta/photos/a.txt.json", "./.meta/photos/a.txt.json"} {
		for _, target := range []string{"/object/public/avatars/", "/object/authenticated/avatars/"} {
			if rec := do(t, handler, http.MethodGet, target+objPath, userToken(t), nil); rec.Code == http.StatusOK {
				t.Errorf("GET %s%s served the sidecar: %s", target, objPath, rec.Body.String())
			}
		}
		rec := do(t, handler, http.MethodPost, "/object/avatars/"+objPath, userToken(t), strings.NewReader("{}"))
		if rec.Code == http.StatusOK {
			t.Errorf("upload over %s was accepted", objPath)
		}
	}
	body := `{"prefixes":["photos/../.meta/photos/a.txt.json"]}`
	do(t, handler, http.MethodDelete, "/object/avatars", userToken(t), strings.NewReader(body))
	if data, err := os.ReadFile(sidecar); err != nil || !strings.Contains(string(data), `"name": "photos/a.txt"`) {
		t.Errorf("sidecar after the attempts: %v %q", err, data)
	}
}
