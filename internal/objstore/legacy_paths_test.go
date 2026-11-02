package objstore

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// placeFile writes content at rel under root, creating its folders.
func placeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Before names were encoded, a name starting with the marker was stored as
// written. An object stored that way is still read, served, signed and
// removed under the name it was uploaded as, along with the sidecar it was
// given then.
//
// A name Windows split at a backslash is not reached this way: its old file
// is where the object of the split name is stored now, so it answers to that
// name, which is also the one it is listed as.
func TestAnObjectStoredBeforeTheEncodingIsStillReachedByName(t *testing.T) {
	for _, name := range []string{"%%x", "dir/%%y"} {
		legacy := name
		t.Run(name, func(t *testing.T) {
			handler, root := newStore(t)
			makeBucket(t, handler, "files", true)
			placeFile(t, root, "files/"+legacy, "old")
			sidecarDir, sidecarFile := filepath.Split(filepath.FromSlash(name))
			sidecar := filepath.Join("files", ".meta", sidecarDir, sidecarFile+".json")
			placeFile(t, root, filepath.ToSlash(sidecar), `{"content_type":"text/x-legacy"}`)
			escaped := escapeSegments(name)

			wantContent(t, handler, "/object/authenticated/files/"+escaped, userToken(t), "old")
			wantContent(t, handler, signedPath(t, handler, escaped), "", "old")
			rec := do(t, handler, http.MethodGet, "/object/public/files/"+escaped, "", nil)
			if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/x-legacy") {
				t.Errorf("public GET: %d %q", rec.Code, rec.Header().Get("Content-Type"))
			}

			body := `{"prefixes":[` + jsonString(t, name) + `]}`
			rec = do(t, handler, http.MethodDelete, "/object/files", userToken(t), strings.NewReader(body))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), jsonString(t, name)) {
				t.Errorf("delete: %d %s", rec.Code, rec.Body.String())
			}
			for _, gone := range []string{filepath.Join(root, "files", filepath.FromSlash(legacy)), filepath.Join(root, sidecar)} {
				if _, err := os.Stat(gone); !os.IsNotExist(err) {
					t.Errorf("%s after delete: %v", gone, err)
				}
			}
		})
	}
}

// An upload always writes the encoded name, even when a legacy file answers
// for the name, and the new object is what is then read.
func TestAnUploadWritesTheEncodedNameOverALegacyOne(t *testing.T) {
	if !onWindows {
		t.Skip("names were only stored differently on Windows")
	}
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	placeFile(t, root, "files/a/b.txt", "old")
	rec := do(t, handler, http.MethodPost, "/object/files/a%5Cb.txt", userToken(t), strings.NewReader("new"))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "files", "%%a%5Cb.txt")); err != nil {
		t.Errorf("encoded file: %v", err)
	}
	wantContent(t, handler, "/object/public/files/a%5Cb.txt", "", "new")
	wantContent(t, handler, "/object/public/files/a/b.txt", "", "old")
}

// The legacy location passes the same checks as the encoded one: it never
// leaves the bucket and never reaches the sidecar tree.
func TestALegacyLocationStaysInItsBucket(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	makeBucket(t, handler, "other", true)
	placeFile(t, root, "secret", "root secret")
	placeFile(t, root, "other/x.txt", "other bucket")
	upload(t, handler, "files", "a.txt", "mine")

	for _, name := range []string{`..\secret`, `..\other\x.txt`, `a\..\..\secret`, `.meta\a.txt.json`, `x\..\.meta\a.txt.json`, `.META\a.txt.json`, "%%/../.meta/a.txt.json", "%%x/../../secret"} {
		rec := do(t, handler, http.MethodGet, "/object/public/files/"+escapeSegments(name), "", nil)
		if rec.Code == http.StatusOK {
			t.Errorf("GET %q served %q", name, rec.Body.String())
		}
	}
}

// A name that starts with the marker was stored as written on every OS before
// names were encoded, so its old location is looked for under either rules.
// Without it, a "%%x" stored then, or placed by hand and listed as "%%x", is
// listed but answers 404.
func TestAMarkerNameStoredAsWrittenIsFoundUnderEitherRules(t *testing.T) {
	for rulesName, rules := range bothRules {
		for _, name := range []string{"%%x", "a/%%100%25"} {
			fileRel, metaRel, ok := legacyUnder(rules, name)
			wantFile := filepath.Join("files", filepath.FromSlash(name))
			dir, file := filepath.Split(filepath.FromSlash(name))
			wantMeta := filepath.Join("files", ".meta", dir, file+".json")
			if !ok || fileRel != wantFile || metaRel != wantMeta {
				t.Errorf("%s %q: (%q, %q, %t), want (%q, %q, true)", rulesName, name, fileRel, metaRel, ok, wantFile, wantMeta)
			}
		}
	}
}

// A listed lookalike is served under the name it is listed as.
func TestAListedLookalikeIsServedUnderItsListedName(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	placeFile(t, root, "files/%%100%25", "lookalike")
	wantListed(t, handler, "", "%%100%25")
	wantContent(t, handler, "/object/public/files/"+escapeSegments("%%100%25"), "", "lookalike")
}

// Windows dropped a trailing dot or space, so the old location of "notes." is
// the file "notes", which is just as likely the object "notes" itself. That
// location is never looked at: reading or removing "notes." must not reach
// another object. Elsewhere the name was stored exactly, so it is found.
func TestATrailingDotOrSpaceNeverReachesAnotherObject(t *testing.T) {
	for _, name := range []string{"notes.", "trailing space ", `dir/inner\c.txt.`, `dir. \x`} {
		if fileRel, _, ok := legacyUnder(windowsRules, name); ok {
			t.Errorf("windows: %q falls back to %q", name, fileRel)
		}
	}
	// Elsewhere "notes." is still stored exactly, so the exact name is its
	// current location and there is nothing to fall back to.
	if fileRel, _, ok := legacyUnder(posixRules, "notes."); ok {
		t.Errorf("notes. is stored as written under posix rules, yet falls back to %q", fileRel)
	}

	if !onWindows {
		return
	}
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "notes", "real")
	if rec := do(t, handler, http.MethodGet, "/object/public/files/notes.", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET notes.: %d %s, want 404", rec.Code, rec.Body.String())
	}
	rec := do(t, handler, http.MethodDelete, "/object/files", userToken(t), strings.NewReader(`{"prefixes":["notes."]}`))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("delete notes.: %d %s, want nothing removed", rec.Code, rec.Body.String())
	}
	wantContent(t, handler, "/object/public/files/notes", "", "real")
	if _, err := os.Stat(filepath.Join(root, "files", "notes")); err != nil {
		t.Errorf("notes after delete of notes.: %v", err)
	}
}
