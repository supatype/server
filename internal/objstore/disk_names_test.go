package objstore

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// Names the storage API accepts but Windows cannot use as file names as they
// are: its forbidden characters, a control character, a trailing dot or
// space, a reserved device name, and names that only look encoded.
var awkwardNames = []string{
	"what?.txt",
	"shot 10:30?#.txt",
	`all<>:"|?*\.txt`,
	"tab\there.txt",
	"trailing dot.",
	"trailing space ",
	"CON",
	"con.txt",
	"LPT1.log",
	"%%starts-like-encoded.txt",
	"100%3F.txt",
	"dir?/inner:file.txt",
}

// Every name the API accepts is stored, listed, served, signed and removed
// on this OS under the name the caller gave it.
func TestEveryLegalNameRoundTrips(t *testing.T) {
	for _, name := range awkwardNames {
		t.Run(name, func(t *testing.T) {
			handler, _ := newStore(t)
			makeBucket(t, handler, "files", true)
			escaped := escapeSegments(name)

			rec := do(t, handler, http.MethodPost, "/object/files/"+escaped, userToken(t), strings.NewReader("pixels"))
			if rec.Code != http.StatusOK {
				t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
			}
			folder, base := path.Split(name)
			wantListed(t, handler, strings.TrimSuffix(folder, "/"), base)
			if folder != "" {
				wantListed(t, handler, "", strings.TrimSuffix(folder, "/"))
			}
			wantContent(t, handler, "/object/authenticated/files/"+escaped, userToken(t), "pixels")
			wantContent(t, handler, "/object/public/files/"+escaped, "", "pixels")
			wantContent(t, handler, signedPath(t, handler, escaped), "", "pixels")

			body := `{"prefixes":[` + jsonString(t, name) + `]}`
			rec = do(t, handler, http.MethodDelete, "/object/files", userToken(t), strings.NewReader(body))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), jsonString(t, name)) {
				t.Errorf("delete: %d %s", rec.Code, rec.Body.String())
			}
			if rec := do(t, handler, http.MethodGet, "/object/public/files/"+escaped, "", nil); rec.Code != http.StatusNotFound {
				t.Errorf("after delete: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// A name that needs no encoding is the file name on disk, exactly as before,
// so objects stored before the encoding existed are still found.
func TestANameThatNeedsNoEncodingIsStoredAsItWas(t *testing.T) {
	for _, name := range []string{"plain.txt", "users/1/avatar.png", "literal%41.txt", "a%%b.txt", "my file (1).png"} {
		t.Run(name, func(t *testing.T) {
			handler, root := newStore(t)
			makeBucket(t, handler, "files", false)
			upload(t, handler, "files", escapeSegments(name), "x")
			if _, err := os.Stat(filepath.Join(root, "files", filepath.FromSlash(name))); err != nil {
				t.Errorf("stored file: %v", err)
			}
		})
	}
}

// A file written under the name before the encoding existed is listed and
// served under that name.
func TestAFileStoredBeforeTheEncodingIsStillServed(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	if err := os.MkdirAll(filepath.Join(root, "files", "photos"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "files", "photos", "old 100%.png"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantListed(t, handler, "photos", "old 100%.png")
	wantContent(t, handler, "/object/public/files/photos/old%20100%25.png", "", "old")
}

// An encoded name is one segment inside its bucket: a backslash or a dot it
// carries never becomes a separator or a parent reference on disk.
func TestAnEncodedNameStaysInItsBucket(t *testing.T) {
	s := &store{root: t.TempDir()}
	for _, name := range []string{`..\..\secrets`, `..\secrets`, "..%2Fsecrets", "%%..", "..?", "%%%2E%2E"} {
		fileRel, metaRel, err := s.objectPaths("files", name)
		if err != nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		if filepath.Dir(fileRel) != "files" || !filepath.IsLocal(fileRel) {
			t.Errorf("%q is stored at %q, outside its bucket", name, fileRel)
		}
		if filepath.Dir(metaRel) != filepath.Join("files", ".meta") {
			t.Errorf("%q has its sidecar at %q", name, metaRel)
		}
	}
	for _, name := range []string{"../secrets", "a/../../secrets", "./../x"} {
		if _, _, err := s.objectPaths("files", name); err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
}
