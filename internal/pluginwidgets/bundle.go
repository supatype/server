package pluginwidgets

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/filestamp"
)

// verifiedBundles remembers which bundle files were read and found to hash to their
// names, by the stamp of the file then, so a bundle is hashed once per version of
// the file rather than on every request.
type verifiedBundles struct {
	mu     sync.Mutex
	stamps map[string]filestamp.Stamp
}

// sumFile is the hex SHA-256 of what f holds. A variable so a test can count calls.
var sumFile = func(f io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// matches reports whether the open file f, stat'ed as info, holds the bytes whose
// SHA-256 is hash, and leaves f at its start for serving.
func (v *verifiedBundles) matches(hash string, f io.ReadSeeker, info os.FileInfo) (bool, error) {
	stamp := filestamp.FromInfo(info)
	v.mu.Lock()
	known := v.stamps[hash].Same(stamp)
	v.mu.Unlock()
	if known {
		return true, nil
	}
	sum, err := sumFile(f)
	if err != nil {
		return false, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	if sum != hash {
		return false, nil
	}
	v.mu.Lock()
	v.stamps[hash] = stamp
	v.mu.Unlock()
	return true, nil
}

// bundleMatches reports whether the open bundle f holds the bytes its hash names,
// logging why not when it does not.
func (s *Store) bundleMatches(hash string, f io.ReadSeeker, info os.FileInfo) bool {
	ok, err := s.bundles.matches(hash, f, info)
	log := logrus.WithField("bundle", hash)
	switch {
	case err != nil:
		log.WithError(err).Error("plugin widgets: could not read a bundle to check its hash")
	case !ok:
		log.Error("plugin widgets: a bundle does not hash to its name; not serving it")
	}
	return ok && err == nil
}
