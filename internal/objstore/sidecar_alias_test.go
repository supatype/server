package objstore

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shortNameOfSidecarDir is the 8.3 short name Windows gave the sidecar
// directory of bucket, skipping the test where the volume makes none.
func shortNameOfSidecarDir(t *testing.T, root, bucket string) string {
	t.Helper()
	sidecars, err := os.Stat(filepath.Join(root, bucket, sidecarDir))
	if err != nil {
		t.Fatal(err)
	}
	alias, err := os.Stat(filepath.Join(root, bucket, "META~1"))
	if err != nil || !os.SameFile(alias, sidecars) {
		t.Skip("this volume gives .meta no 8.3 short name")
	}
	return "META~1"
}

// On Windows the sidecar directory also answers to its 8.3 short name, such
// as META~1. No object route may reach the sidecars through it.
func TestTheSidecarDirIsUnreachableByItsShortName(t *testing.T) {
	if !onWindows {
		t.Skip("only Windows makes 8.3 short names")
	}
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "a.png", "png")
	short := shortNameOfSidecarDir(t, root, "files")
	sidecar := short + "/a.png.json"

	if rec := do(t, handler, http.MethodGet, "/object/public/files/"+sidecar, "", nil); rec.Code == http.StatusOK {
		t.Errorf("GET %s served %s", sidecar, rec.Body.String())
	}
	rec := do(t, handler, http.MethodPost, "/object/list/files", userToken(t), strings.NewReader(`{"prefix":"`+short+`"}`))
	if strings.Contains(rec.Body.String(), "a.png.json") {
		t.Errorf("list of %s: %s", short, rec.Body.String())
	}
	if rec := do(t, handler, http.MethodPost, "/object/files/"+short+"/b.png", userToken(t), strings.NewReader("x")); rec.Code == http.StatusOK {
		t.Errorf("upload into %s was stored", short)
	}
	rec = do(t, handler, http.MethodDelete, "/object/files", userToken(t), strings.NewReader(`{"prefixes":["`+sidecar+`"]}`))
	if strings.Contains(rec.Body.String(), "a.png.json") {
		t.Errorf("delete of %s: %s", sidecar, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "files", sidecarDir, "a.png.json")); err != nil {
		t.Errorf("sidecar after the delete: %v", err)
	}
}

// Under Windows rules, a name the disk resolves to the sidecar directory is
// refused whatever it is spelled. A symbolic link stands in for an 8.3 short
// name where the OS makes none; under posix rules the disk is not asked.
func TestANameTheDiskResolvesToTheSidecarDirIsRefused(t *testing.T) {
	root := t.TempDir()
	placeFile(t, root, "files/.meta/a.png.json", "{}")
	placeFile(t, root, "files/other/b.png", "b")
	if err := os.Symlink(".meta", filepath.Join(root, "files", "META~1")); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	outside := t.TempDir()
	placeFile(t, outside, "secret", "s")
	if err := os.Symlink(outside, filepath.Join(root, "files", "out")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		rules nameRules
		rel   string
		want  bool
	}{
		{windowsRules, "META~1/a.png.json", true},
		{windowsRules, "other/b.png", false},
		{windowsRules, "missing/b.png", false},
		{windowsRules, "out/secret", true},
		{posixRules, "META~1/a.png.json", false},
	} {
		s := &store{root: root, rules: tc.rules}
		if got := s.offLimits("files", filepath.FromSlash(tc.rel)); got != tc.want {
			t.Errorf("windows=%t offLimits(%q) = %t, want %t", tc.rules.windows, tc.rel, got, tc.want)
		}
	}
	// A store opened the usual way compares against the root it resolved once.
	opened := openStore(root, secret)
	opened.rules = windowsRules
	if !opened.offLimits("files", filepath.FromSlash("META~1/a.png.json")) {
		t.Error("an opened store reached the sidecar tree through a link")
	}
	// With no storage root to compare against, nothing can be shown to be an
	// alias, so nothing is refused for being one.
	gone := &store{root: filepath.Join(root, "gone"), rules: windowsRules}
	if gone.offLimits("files", "META~1") {
		t.Error("a store with no root refused a name as an alias")
	}
}

// Where the file system keeps case, ".Meta" is an ordinary folder: an object
// stored in it is listed, and its bucket is not empty, so deleting the bucket
// is refused rather than removing the object with it.
func TestACaseVariantOfTheSidecarDirIsAnOrdinaryFolderWhereCaseCounts(t *testing.T) {
	if hostRules.foldsCase {
		t.Skip("this file system ignores case, so .Meta is the sidecar directory")
	}
	handler, root := newStore(t)
	makeBucket(t, handler, "files", false)
	upload(t, handler, "files", ".Meta/report.pdf", "pdf")
	wantListed(t, handler, ".Meta", "report.pdf")
	if rec := do(t, handler, http.MethodDelete, "/bucket/files", serviceToken(t), nil); rec.Code != http.StatusBadRequest {
		t.Errorf("delete bucket: %d %s, want 400", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "files", ".Meta", "report.pdf")); err != nil {
		t.Errorf("report.pdf after the delete: %v", err)
	}
}
