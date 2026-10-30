package objstore

import (
	"net/http"
	"strings"
	"testing"
)

// On Windows an object answers to other names than its own: its 8.3 short
// name, such as LONGFI~1.TXT, and any other casing. None of them reaches it,
// for reading, writing or removing, and neither does a folder's short name.
func TestAnObjectIsUnreachableByAnotherNameForIt(t *testing.T) {
	if !onWindows {
		t.Skip("only Windows answers to short names")
	}
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "longfilename.txt", "long")
	upload(t, handler, "files", "longdirectoryname/a.txt", "in dir")
	if !sameFileAt(root, "files/LONGFI~1.TXT", "files/longfilename.txt") || !sameFileAt(root, "files/LONGDI~1", "files/longdirectoryname") {
		t.Skip("this volume makes no 8.3 short names")
	}

	for _, name := range []string{"LONGFI~1.TXT", "LONGFILENAME.TXT", "LONGDI~1/a.txt", "LongDirectoryName/a.txt", "LONGDI~1/new.txt"} {
		if rec := do(t, handler, http.MethodGet, "/object/public/files/"+name, "", nil); rec.Code == http.StatusOK {
			t.Errorf("GET %s served %q", name, rec.Body.String())
		}
		rec := uploadAs(t, handler, "/object/files/"+name, "", strings.NewReader("over"), map[string]string{"x-upsert": "true"})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("upsert %s: %d %s, want 400", name, rec.Code, rec.Body.String())
		}
		rec = do(t, handler, http.MethodDelete, "/object/files", userToken(t), strings.NewReader(`{"prefixes":["`+name+`"]}`))
		if strings.Contains(rec.Body.String(), name) {
			t.Errorf("delete %s: %s", name, rec.Body.String())
		}
	}
	rec := do(t, handler, http.MethodPost, "/object/list/files", userToken(t), strings.NewReader(`{"prefix":"LONGDI~1"}`))
	if strings.Contains(rec.Body.String(), "a.txt") {
		t.Errorf("list of LONGDI~1: %s", rec.Body.String())
	}
	wantContent(t, handler, "/object/public/files/longfilename.txt", "", "long")
	wantContent(t, handler, "/object/public/files/longdirectoryname/a.txt", "", "in dir")
}
