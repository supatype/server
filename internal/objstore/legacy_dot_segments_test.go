package objstore

import (
	"net/http"
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
