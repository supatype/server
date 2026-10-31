package objstore

import (
	"net/http"
	"strings"
	"testing"
)

// A legacy lookup reads the name as it was once stored, but "." and ".." in it are steps
// through folders, not names: "%%x/../a" would open the file of object "a" and answer for it
// under another name. Such a name has no old location, under either rules.
func TestALegacyLookupNeverStepsThroughDotSegments(t *testing.T) {
	names := map[string][]string{
		"posix":   {"%%x/../a", "%%x/./a", "%%x/..", "./%%x", "a/%%x/../../a"},
		"windows": {"%%x/../a", `%%x\..\a`, `%%x\.\a`, `a\%%x\..`, `.\%%x`},
	}
	for rulesName, rules := range bothRules {
		for _, name := range names[rulesName] {
			if fileRel, _, ok := legacyUnder(rules, name); ok {
				t.Errorf("%s: %q falls back to %q", rulesName, name, fileRel)
			}
		}
	}
}

// Through the API, on the host's own rules: the object "a" exists, and a name that steps
// back to it from a legacy-looking folder must not be served.
func TestADotSegmentNameIsNotServedAsAnotherObject(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "a", "real")
	name := "%%x/../a"
	if onWindows {
		name = `%%x\..\a`
	}
	if rec := do(t, handler, http.MethodGet, "/object/public/files/"+escapeSegments(name), "", nil); rec.Code == http.StatusOK {
		t.Errorf("GET %q: %d %q, want it not to reach object a", name, rec.Code, rec.Body.String())
	}
}

// Any name, not only a legacy-looking one: "x/../a", "./a" and "x//a" were cleaned to the path of
// another object on disk, so a GET answered with that object's bytes under a name nobody uploaded,
// and an upload under it overwrote that object. Such a name is refused, never served or written.
func TestADotOrEmptySegmentNeverReachesAnotherObject(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "a", "real")
	upload(t, handler, "files", "x/a", "nested")
	for _, name := range []string{"x/../a", "./a", "x/./a", "x//a", "x/a/..", "x/a/."} {
		target := "/object/public/files/" + escapeSegments(name)
		if rec := do(t, handler, http.MethodGet, target, "", nil); rec.Code == http.StatusOK {
			t.Errorf("GET %q: %d %q, want it refused", name, rec.Code, rec.Body.String())
		}
		rec := do(t, handler, http.MethodPost, "/object/files/"+escapeSegments(name), serviceToken(t), strings.NewReader("overwritten"))
		if rec.Code < 400 {
			t.Errorf("upload %q: %d, want it refused", name, rec.Code)
		}
	}
	for name, want := range map[string]string{"a": "real", "x/a": "nested"} {
		if rec := do(t, handler, http.MethodGet, "/object/public/files/"+name, "", nil); rec.Body.String() != want {
			t.Errorf("object %q now holds %q, want %q", name, rec.Body.String(), want)
		}
	}
}
