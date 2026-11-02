package objstore

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// countRegistryReads counts the reads of buckets.json for the length of the
// test.
func countRegistryReads(t *testing.T) *int {
	t.Helper()
	reads := 0
	previous := readFile
	t.Cleanup(func() { readFile = previous })
	readFile = func(name string) ([]byte, error) {
		if filepath.Base(name) == "buckets.json" {
			reads++
		}
		return previous(name)
	}
	return &reads
}

// Every object request looks its bucket up. The registry is read from disk
// only when it has changed: this store's own writes keep the copy in memory
// current, and a write by anything else, as the CLI's provisioning is, is
// read on the next request.
func TestTheBucketRegistryIsReadOnlyWhenItChanges(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "files", true)
	upload(t, handler, "files", "a.txt", "a")
	reads := countRegistryReads(t)

	for range 5 {
		wantContent(t, handler, "/object/public/files/a.txt", "", "a")
	}
	if *reads != 0 {
		t.Errorf("five downloads read buckets.json %d times, want 0", *reads)
	}

	registry := filepath.Join(root, storeMetaDir, "buckets.json")
	if err := os.WriteFile(registry, []byte(`[{"id":"files","name":"files","public":false,"access_mode":"private"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(registry, later, later); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, handler, http.MethodGet, "/object/public/files/a.txt", "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("after the bucket was made private elsewhere: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if *reads != 1 {
		t.Errorf("the changed registry was read %d times, want 1", *reads)
	}
}
