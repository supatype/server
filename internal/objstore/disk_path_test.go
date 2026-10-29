package objstore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unsafe"
)

// A path whose segments all need no encoding is stored as given: diskPath
// hands back the caller's string without building a new one.
func TestDiskPathLeavesAPlainPathAlone(t *testing.T) {
	for _, objPath := range []string{"plain.txt", "users/1/avatar.png", "a%%b/literal%41.txt", "my file (1).png", ""} {
		got := diskPath(objPath)
		if got != objPath {
			t.Fatalf("diskPath(%q) = %q", objPath, got)
		}
		if objPath != "" && unsafe.StringData(got) != unsafe.StringData(objPath) {
			t.Errorf("diskPath(%q) built a copy of a path it did not change", objPath)
		}
		if allocs := testing.AllocsPerRun(100, func() { _ = diskPath(objPath) }); allocs != 0 {
			t.Errorf("diskPath(%q) allocated %.0f times for a path it did not change", objPath, allocs)
		}
	}
}

// Only the segments that need it are encoded; the rest keep their spelling.
func TestDiskPathEncodesOnlyTheSegmentsThatNeedIt(t *testing.T) {
	for objPath, want := range map[string]string{
		"%%x/b.txt":      "%%%25%25x/b.txt",
		"a/%%x":          "a/%%%25%25x",
		"a/nul\x00/c":    "a/%%nul%00/c",
		"a/b/c\x00.txt":  "a/b/%%c%00.txt",
		"a//%%":          "a//%%%25%25",
		"../%%x/./y.txt": "../%%%25%25x/./y.txt",
	} {
		if got := diskPath(objPath); got != want {
			t.Errorf("diskPath(%q) = %q, want %q", objPath, got, want)
		}
	}
}

// A stored name that decodes to a different name is listed where the decoded
// name sorts, not where its file name does: "%%z%00.txt" is "z\x00.txt", which
// comes after "m.txt" although its file name comes first. NUL is encoded on
// every OS, so the names decode under either set of rules.
func TestListingOrdersByTheDecodedName(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", false)
	bucket := filepath.Join(root, "avatars")
	for _, rel := range []string{"m.txt", "%%z%00.txt", "b/x.txt", "%%y%00/x.txt"} {
		file := filepath.Join(bucket, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	names, _ := listNames(t, handler, `{"prefix":""}`)
	if want := []string{"b/", "y\x00/", "m.txt", "z\x00.txt"}; !slices.Equal(names, want) {
		t.Fatalf("listed %q, want %q", names, want)
	}
}

// mapSegments maps each slash-separated segment and nothing else, wherever
// the first changed segment falls and however the slashes are placed.
func TestMapSegmentsMapsEverySegment(t *testing.T) {
	upper := func(s string) string {
		if s == "x" {
			return "X!"
		}
		return s
	}
	for _, objPath := range []string{"", "x", "a", "/", "x/", "/x", "a/x", "x/a", "a//x", "x//", "a/b/x/c/x", "a/b/c", "x/x/x"} {
		want := strings.Join(slicesMap(strings.Split(objPath, "/"), upper), "/")
		if got := mapSegments(objPath, upper); got != want {
			t.Errorf("mapSegments(%q) = %q, want %q", objPath, got, want)
		}
	}
}

func slicesMap(in []string, f func(string) string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = f(s)
	}
	return out
}
