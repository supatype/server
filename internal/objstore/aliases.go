package objstore

import (
	"os"
	"path/filepath"
	"strings"
)

// A file system can answer to more than one name for a file or directory:
// where it ignores case, "FILES" opens "files", and on Windows an 8.3 short
// name such as LONGFI~1.TXT, or a name with a trailing dot, opens a file
// stored under another name. A name like that must never be read as an
// object, a bucket, or the store's bookkeeping: it would reach a file that
// belongs to some other name.

// foldsCaseAt reports whether the file system holding dir takes names that
// differ only in case for one name. It creates a probe file in dir, looks it
// up under an upper-case spelling, and removes it. ok is false when the probe
// could not be made, so the caller can fall back to the OS's usual answer.
func foldsCaseAt(dir string) (folds, ok bool) {
	f, err := os.CreateTemp(dir, "case-probe-")
	if err != nil {
		return false, false
	}
	name := f.Name()
	probe, probeErr := f.Stat()
	_ = f.Close()
	defer func() {
		_ = os.Remove(name)
	}()
	upper, err := os.Stat(filepath.Join(dir, strings.ToUpper(filepath.Base(name))))
	return probeErr == nil && err == nil && os.SameFile(probe, upper), probeErr == nil
}

// rulesFor is hostRules with case folding taken from the file system holding
// dir rather than assumed from the OS: a Linux server can store on a mount
// that ignores case, and macOS on a volume that keeps it. Short names exist
// only on Windows, so the rest of the rules still follow the OS.
func rulesFor(dir string) (nameRules, bool) {
	rules := hostRules
	folds, ok := probeFoldsCase(dir)
	if ok {
		rules.foldsCase = folds
	}
	return rules, ok
}

// diskRoot is the storage root as the disk names it, links resolved, so a
// path under it can be compared with what the disk calls that path.
func (s *store) diskRoot() (string, error) {
	if s.realRoot != "" {
		return s.realRoot, nil
	}
	return filepath.EvalSymlinks(s.root)
}

// onDisk is rel, a path relative to the storage root, as the disk names it:
// each part under its own stored name, however rel spelled it, with links
// resolved. ok is false when nothing is there. A path the disk resolves
// outside the storage root comes back empty, which differs from every rel.
func (s *store) onDisk(rel string) (string, bool) {
	root, err := s.diskRoot()
	if err != nil {
		return "", false
	}
	real, err := filepath.EvalSymlinks(filepath.Join(s.root, rel))
	if err != nil {
		return "", false
	}
	got, err := filepath.Rel(root, real)
	if err != nil || !filepath.IsLocal(got) {
		return "", true
	}
	return got, true
}

// aliasedOnDisk reports whether rel in bucket reaches an existing file or
// folder under a name other than its own, under Windows rules. The deepest
// part of rel that is there is asked for its stored name; a part that is not
// there cannot be another name for anything.
func (s *store) aliasedOnDisk(bucket, rel string) bool {
	if !s.rules.windows {
		return false
	}
	for p := filepath.Join(bucket, rel); p != bucket && p != "."; p = filepath.Dir(p) {
		if real, ok := s.onDisk(p); ok {
			return real != p
		}
	}
	return false
}

// offLimits reports whether rel in bucket must not be reached as an object or
// a folder of objects: it names the sidecar tree, or it reaches something on
// disk under a name other than its own, such as META~1 for the sidecar tree
// or LONGFI~1.TXT for longfilename.txt.
func (s *store) offLimits(bucket, rel string) bool {
	return s.rules.inSidecarTree(rel) || s.aliasedOnDisk(bucket, rel)
}

// aliases is a test of whether a directory under the storage root is the one
// the bucket id would be: by name under the store's rules, or, under Windows
// rules, because the disk resolves id to it, as SUPATY~1 or ".supatype."
// resolve to .supatype. The disk is asked once, however many directories
// are tested.
func (s *store) aliases(id string) func(dir string) bool {
	real, there := "", false
	if s.rules.windows {
		real, there = s.onDisk(id)
	}
	return func(dir string) bool {
		return s.rules.sameName(id, dir) || (there && real == dir)
	}
}
