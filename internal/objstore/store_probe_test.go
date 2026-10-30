package objstore

import (
	"os"
	"path/filepath"
	"testing"
)

// withProbe states what the storage file system answers to the case probe
// for the length of the test.
func withProbe(t *testing.T, folds, ok bool) {
	t.Helper()
	previous := probeFoldsCase
	t.Cleanup(func() { probeFoldsCase = previous })
	probeFoldsCase = func(string) (bool, bool) { return folds, ok }
}

// Whether names that differ only in case are one name is a property of the
// file system holding the storage root, not of the OS: a Linux server can
// store on a mount that ignores case, and macOS on a volume that keeps it.
// The store takes it from the probe, and falls back to the OS's usual answer
// only when the probe cannot be made.
func TestCaseFoldingComesFromTheStorageFileSystem(t *testing.T) {
	for _, folds := range []bool{false, true} {
		withProbe(t, folds, true)
		if got := openStore(t.TempDir(), secret).rules.foldsCase; got != folds {
			t.Errorf("the probe said folds=%t, the store went by %t", folds, got)
		}
	}
	withProbe(t, !hostRules.foldsCase, false)
	if got := openStore(t.TempDir(), secret).rules.foldsCase; got != hostRules.foldsCase {
		t.Errorf("with no probe the store went by %t, want the OS default %t", got, hostRules.foldsCase)
	}
}

// The probe answers for the file system it runs on, here the usual one for
// this OS, and leaves nothing behind. A directory it cannot write in gives no
// answer.
func TestTheCaseProbeReadsTheFileSystem(t *testing.T) {
	dir := t.TempDir()
	folds, ok := foldsCaseAt(dir)
	if !ok || folds != hostRules.foldsCase {
		t.Errorf("foldsCaseAt = %t, %t; want %t, true", folds, ok, hostRules.foldsCase)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the probe left %d entries behind", len(entries))
	}
	if _, ok := foldsCaseAt(filepath.Join(dir, "absent")); ok {
		t.Error("a probe in a missing directory claimed an answer")
	}
}
