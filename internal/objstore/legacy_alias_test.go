package objstore

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uploadNamed uploads content under name, escaping it as the client does.
func uploadNamed(t *testing.T, handler http.Handler, name, content string) {
	t.Helper()
	upload(t, handler, "files", escapeSegments(name), content)
}

// wantUnreached fails if reading or removing name reaches the object other,
// whose content is otherContent: the read must not serve it, and the remove
// must report nothing and leave other in place.
func wantUnreached(t *testing.T, handler http.Handler, name, other, otherContent string) {
	t.Helper()
	if rec := do(t, handler, http.MethodGet, "/object/public/files/"+escapeSegments(name), "", nil); rec.Code == http.StatusOK {
		t.Errorf("GET %q served %q", name, rec.Body.String())
	}
	rec := do(t, handler, http.MethodDelete, "/object/files", userToken(t), strings.NewReader(`{"prefixes":[`+jsonString(t, name)+`]}`))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("delete %q: %d %s, want nothing removed", name, rec.Code, rec.Body.String())
	}
	wantContent(t, handler, "/object/public/files/"+escapeSegments(other), "", otherContent)
}

// The old location of "%%%25%25foo" is the file "%%%25%25foo", which is where
// the object "%%foo" is stored now. Reading or removing the first must never
// reach the second.
func TestALegacyFallbackNeverReachesAnotherObjectsFile(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	uploadNamed(t, handler, "%%foo", "foo's")
	wantUnreached(t, handler, "%%%25%25foo", "%%foo", "foo's")
}

// On Windows the object "CON" is stored as "%%CON", which is also the old
// location of the object "%%CON".
func TestALegacyFallbackNeverReachesAReservedNamesFile(t *testing.T) {
	if !onWindows {
		t.Skip("CON is only encoded on Windows")
	}
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	uploadNamed(t, handler, "CON", "device")
	wantUnreached(t, handler, "%%CON", "CON", "device")
}

// On Windows the old location of `a\b` is the file a/b, which is where the
// object "a/b" is stored now.
func TestALegacyFallbackNeverReachesTheObjectABackslashSplitsInto(t *testing.T) {
	if !onWindows {
		t.Skip("a backslash only split a name on Windows")
	}
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	uploadNamed(t, handler, "a/b", "ab")
	wantUnreached(t, handler, `a\b`, "a/b", "ab")
}

// placeLegacyMarkerObject places "%%x" as it was stored before names were
// encoded: the file under its own name, and its sidecar beside it.
func placeLegacyMarkerObject(t *testing.T, root string) {
	t.Helper()
	placeFile(t, root, "files/%%x", "old")
	placeFile(t, root, "files/.meta/%%x.json", `{"name":"%%x","content_type":"text/x-old"}`)
}

// An object stored under the old layout exists: an upload without upsert is
// refused as it is for any other existing object.
func TestAnUploadOverALegacyObjectWithoutUpsertConflicts(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	placeLegacyMarkerObject(t, root)

	rec := do(t, handler, http.MethodPost, "/object/files/"+escapeSegments("%%x"), userToken(t), strings.NewReader("new"))
	if rec.Code != http.StatusConflict {
		t.Errorf("upload: %d %s, want 409", rec.Code, rec.Body.String())
	}
	wantContent(t, handler, "/object/public/files/"+escapeSegments("%%x"), "", "old")
}

// An upsert over an object stored under the old layout replaces it: the old
// file and sidecar go, so the name is listed once and reads the new bytes.
func TestAnUpsertOverALegacyObjectLeavesNothingBehind(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	placeLegacyMarkerObject(t, root)

	rec := uploadAs(t, handler, "/object/files/"+escapeSegments("%%x"), "", strings.NewReader("new"), map[string]string{"x-upsert": "true"})
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
	}
	for _, rel := range []string{"files/%%x", "files/.meta/%%x.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s after the upsert: %v", rel, err)
		}
	}
	wantContent(t, handler, "/object/public/files/"+escapeSegments("%%x"), "", "new")
	rec = do(t, handler, http.MethodPost, "/object/list/files", userToken(t), strings.NewReader(`{"prefix":""}`))
	if got := strings.Count(rec.Body.String(), `"name":"%%x"`); got != 1 {
		t.Errorf("%%%%x listed %d times: %s", got, rec.Body.String())
	}
}
