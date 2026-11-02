package pluginwidgets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/filestamp"
)

// SignatureSuffix names a bundle's minisign signature beside it: `<sha256>.js.minisig`.
const SignatureSuffix = ".minisig"

// bundleStamps is a bundle file and its signature file as they were when checked.
type bundleStamps struct {
	bundle    filestamp.Stamp
	signature filestamp.Stamp
	signed    bool
}

func (a bundleStamps) same(b bundleStamps) bool {
	return a.signed == b.signed && a.bundle.Same(b.bundle) && (!a.signed || a.signature.Same(b.signature))
}

// checkedBundle is the verdict on a bundle at the stamps it was checked at: "" when
// it may run, otherwise why not.
type checkedBundle struct {
	stamps bundleStamps
	why    string
}

// verifiedBundles remembers the verdict on each bundle by the stamps of the bundle and
// its signature then, so each is hashed, verified and, when refused, logged once per
// version of its files rather than on every request.
type verifiedBundles struct {
	mu      sync.Mutex
	checked map[string]checkedBundle
}

func (v *verifiedBundles) known(hash string, stamps bundleStamps) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	seen, ok := v.checked[hash]
	return seen.why, ok && seen.stamps.same(stamps)
}

func (v *verifiedBundles) remember(hash string, stamps bundleStamps, why string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.checked[hash] = checkedBundle{stamps: stamps, why: why}
}

// sumFile is the hex SHA-256 of what f holds. A variable so a test can count calls.
var sumFile = func(f io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// signatureOf reads the bundle's signature, if it has one, with its file's stamp.
func (s *Store) signatureOf(hash string) ([]byte, filestamp.Stamp, bool, error) {
	path := filepath.Join(s.Dir, hash+".js"+SignatureSuffix)
	stamp, err := filestamp.Of(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, filestamp.Stamp{}, false, nil
	}
	if err != nil {
		return nil, filestamp.Stamp{}, false, err
	}
	signature, err := os.ReadFile(path)
	return signature, stamp, err == nil, err
}

// bundleRefusal is why a bundle holding body may not run in Studio, or "" when it may:
// it must hash to its name, and be signed by Supatype unless this is a development
// server.
func (s *Store) bundleRefusal(hash string, body []byte, signature []byte, signed bool) (string, error) {
	if sum, err := sumFile(bytes.NewReader(body)); err != nil || sum != hash {
		return "it does not hash to its name", err
	}
	return s.trust.refusal(body, signature, signed), nil
}

// bundleServable reports whether the open bundle f, stat'ed as info, may run in Studio.
// A refusal is logged when the bundle is first checked at these stamps, and f is left
// at its start for serving.
func (s *Store) bundleServable(hash string, f io.ReadSeeker, info os.FileInfo) bool {
	log := logrus.WithField("bundle", hash)
	signature, sigStamp, signed, err := s.signatureOf(hash)
	if err != nil {
		log.WithError(err).Error("plugin widgets: could not read a bundle's signature")
		return false
	}
	stamps := bundleStamps{bundle: filestamp.FromInfo(info), signature: sigStamp, signed: signed}
	if why, ok := s.bundles.known(hash, stamps); ok {
		return why == ""
	}
	body, err := readAndRewind(f)
	why := ""
	if err == nil {
		why, err = s.bundleRefusal(hash, body, signature, signed)
	}
	if err != nil {
		log.WithError(err).Error("plugin widgets: could not read a bundle to check it")
		return false
	}
	if why != "" {
		log.Errorf("plugin widgets: not serving a bundle: %s; Studio uses its built-in editor", why)
	}
	s.bundles.remember(hash, stamps, why)
	return why == ""
}

// readAndRewind reads all of f and returns it to its start.
func readAndRewind(f io.ReadSeeker) ([]byte, error) {
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	_, err = f.Seek(0, io.SeekStart)
	return body, err
}

// servableOnDisk opens the bundle with this hash and reports whether it may run.
func (s *Store) servableOnDisk(hash string) bool {
	f, err := os.Open(filepath.Join(s.Dir, hash+".js"))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return s.bundleServable(hash, f, info)
}
