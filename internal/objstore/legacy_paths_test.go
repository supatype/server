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

// Before names were encoded, Windows split a name at a backslash into folders
// and dropped a trailing dot or space. An object stored that way is still
// read, served, signed and removed under the name it was uploaded as, along
// with the sidecar it was given then.
func TestAnObjectStoredBeforeTheEncodingIsStillReachedByName(t *testing.T) {
	if !onWindows {
		t.Skip("names were only stored differently on Windows")
	}
	for name, legacy := range map[string]string{
		`photos\a.txt`:     "photos/a.txt",
		"notes.":           "notes",
		"trailing space ":  "trailing space",
		`deep\er\b.txt`:    "deep/er/b.txt",
		`dir/inner\c.txt.`: "dir/inner/c.txt",
	} {
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
	placeFile(t, root, "files/notes", "old")
	rec := do(t, handler, http.MethodPost, "/object/files/notes.", userToken(t), strings.NewReader("new"))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "files", "%%notes%2E")); err != nil {
		t.Errorf("encoded file: %v", err)
	}
	wantContent(t, handler, "/object/public/files/notes.", "", "new")
	wantContent(t, handler, "/object/public/files/notes", "", "old")
}

// The legacy location passes the same checks as the encoded one: it never
// leaves the bucket and never reaches the sidecar tree.
func TestALegacyLocationStaysInItsBucket(t *testing.T) {
	if !onWindows {
		t.Skip("names were only stored differently on Windows")
	}
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	makeBucket(t, handler, "other", true)
	placeFile(t, root, "secret", "root secret")
	placeFile(t, root, "other/x.txt", "other bucket")
	upload(t, handler, "files", "a.txt", "mine")

	for _, name := range []string{`..\secret`, `..\other\x.txt`, `a\..\..\secret`, `.meta\a.txt.json`, `x\..\.meta\a.txt.json`, `.META\a.txt.json`} {
		rec := do(t, handler, http.MethodGet, "/object/public/files/"+escapeSegments(name), "", nil)
		if rec.Code == http.StatusOK {
			t.Errorf("GET %q served %q", name, rec.Body.String())
		}
	}
}
