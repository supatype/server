package filestamp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAStampMovesWhenTheFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Of(path)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Of(path)
	if !first.Same(again) {
		t.Error("an unchanged file stamped differently")
	}
	if err := os.WriteFile(path, []byte("three"), 0o600); err != nil {
		t.Fatal(err)
	}
	if grown, _ := Of(path); first.Same(grown) {
		t.Error("a file of a new size kept its stamp")
	}
	later := time.Now().Add(time.Hour)
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if touched, _ := Of(path); first.Same(touched) {
		t.Error("a file with a new modification time kept its stamp")
	}
}

func TestAMissingFileHasTheZeroStamp(t *testing.T) {
	s, err := Of(filepath.Join(t.TempDir(), "absent"))
	if err == nil || !s.Same(Stamp{}) {
		t.Fatalf("Of(absent) = %v, %v; want the zero stamp and an error", s, err)
	}
}
