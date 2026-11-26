package site

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/proxy"
	"golang.org/x/sync/errgroup"
)

const (
	completeMarker = ".complete"
	blobsMarker    = ".blobs"
	downloadLimit  = 4
	maxManifest    = 64 << 20
)

// removeAll and rename are the two filesystem calls whose failure cannot be provoked on every OS from a
// test (a folder that cannot be deleted, a rename that cannot happen), so a test replaces them.
var (
	removeAll = os.RemoveAll
	rename    = os.Rename
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// file is one manifest entry: where it goes in the site, and the content that goes there.
type file struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type deploymentManifest struct {
	Files []file `json:"files"`
}

func (s *Syncer) blobPath(sha string) string { return filepath.Join(s.cfg.Dir, "blobs", sha) }
func (s *Syncer) versionDir(id string) string {
	return filepath.Join(s.cfg.Dir, "versions", id)
}

// versionComplete reports whether a finished folder for id is on disk. A folder without the marker
// is a crash's leftover and is rebuilt.
func (s *Syncer) versionComplete(id string) bool {
	fi, err := os.Stat(filepath.Join(s.versionDir(id), completeMarker))
	return err == nil && fi.Mode().IsRegular()
}

// build fetches ref's manifest, validates it, downloads the blobs the disk lacks and writes the
// version folder. Nothing is served until all of that has succeeded.
func (s *Syncer) build(ctx context.Context, ref *proxy.SiteRef) error {
	m, err := s.fetchManifest(ctx, ref)
	if err != nil {
		return err
	}
	if err := s.validate(m.Files, s.budget(ref)); err != nil {
		return err
	}
	if err := s.download(ctx, ref, m.Files); err != nil {
		return err
	}
	return s.link(ref.DeploymentID, m.Files)
}

// validate refuses a manifest that could write outside the version folder, name a blob that is not a
// hash, repeat a path, or exceed the byte budget. It runs before any download.
func (s *Syncer) validate(files []file, budget int64) error {
	seen := make(map[string]bool, len(files))
	var total int64
	for _, f := range files {
		if !validSitePath(f.Path) {
			return fmt.Errorf("site: unsafe path %q in manifest", f.Path)
		}
		if seen[f.Path] {
			return fmt.Errorf("site: duplicate path %q in manifest", f.Path)
		}
		seen[f.Path] = true
		if !sha256Pattern.MatchString(f.SHA256) {
			return fmt.Errorf("site: %q has an invalid sha256", f.Path)
		}
		if f.Size < 0 || (budget > 0 && f.Size > budget) {
			return fmt.Errorf("site: %q has an invalid size %d", f.Path, f.Size)
		}
		total += f.Size
		if budget > 0 && total > budget {
			return fmt.Errorf("site: deployment is over the %d byte budget", budget)
		}
	}
	return nil
}

// validSitePath accepts a clean, relative, slash separated path with no control characters, one that
// is not the syncer's own marker.
func validSitePath(p string) bool {
	if p == "" || path.Clean(p) != p || !filepath.IsLocal(filepath.FromSlash(p)) {
		return false
	}
	if strings.ContainsAny(p, "\\") || p == completeMarker || p == blobsMarker {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (s *Syncer) siteURL(id string, parts ...string) string {
	base := strings.TrimRight(s.cfg.SourceURL, "/") + "/internal/sites/" + url.PathEscape(s.cfg.TenantID) +
		"/deployments/" + url.PathEscape(id)
	for _, p := range parts {
		base += "/" + url.PathEscape(p)
	}
	return base
}

func (s *Syncer) get(ctx context.Context, ref *proxy.SiteRef, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+ref.Token)
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("site: fetch: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("site: control plane answered %d for %s", resp.StatusCode, path.Base(resp.Request.URL.Path))
	}
	return resp, nil
}

func (s *Syncer) fetchManifest(ctx context.Context, ref *proxy.SiteRef) (*deploymentManifest, error) {
	resp, err := s.get(ctx, ref, s.siteURL(ref.DeploymentID, "manifest"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var m deploymentManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifest)).Decode(&m); err != nil {
		return nil, fmt.Errorf("site: manifest: %w", err)
	}
	return &m, nil
}

// download fetches every distinct blob the disk does not hold, four at a time.
func (s *Syncer) download(ctx context.Context, ref *proxy.SiteRef, files []file) error {
	need := map[string]int64{}
	for _, f := range files {
		if _, err := os.Stat(s.blobPath(f.SHA256)); err != nil {
			need[f.SHA256] = f.Size
		}
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(downloadLimit)
	for sha, size := range need {
		g.Go(func() error { return s.fetchBlob(gctx, ref, sha, size) })
	}
	return g.Wait()
}

// fetchBlob streams one blob to a temp file, checks its size and hash, and only then renames it into
// blobs/, so a blob under that name is always whole and correct.
func (s *Syncer) fetchBlob(ctx context.Context, ref *proxy.SiteRef, sha string, size int64) error {
	resp, err := s.get(ctx, ref, s.siteURL(ref.DeploymentID, "blobs", sha))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	tmp, err := os.CreateTemp(filepath.Join(s.cfg.Dir, "blobs"), ".tmp-*")
	if err != nil {
		return fmt.Errorf("site: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("site: download %s: %w", sha, err)
	}
	if n != size {
		return fmt.Errorf("site: blob %s is %d bytes, the manifest says %d", sha, n, size)
	}
	if hex.EncodeToString(h.Sum(nil)) != sha {
		return fmt.Errorf("site: blob %s does not match its hash", sha)
	}
	if err := os.Rename(tmp.Name(), s.blobPath(sha)); err != nil {
		return fmt.Errorf("site: %w", err)
	}
	return nil
}

// link builds versions/<id>.tmp-<rand> from hard links to blobs, marks it complete and renames it to
// versions/<id>. All paths go through an os.Root on cfg.Dir, so a path cannot leave it.
func (s *Syncer) link(id string, files []file) error {
	root, err := os.OpenRoot(s.cfg.Dir)
	if err != nil {
		return fmt.Errorf("site: %w", err)
	}
	defer func() { _ = root.Close() }()

	tmpName := fmt.Sprintf("%s.tmp-%d", id, time.Now().UnixNano())
	tmpRel := path.Join("versions", tmpName)
	if err := root.MkdirAll(tmpRel, 0o700); err != nil {
		return fmt.Errorf("site: %w", err)
	}
	if err := s.fill(root, tmpRel, files); err != nil {
		_ = os.RemoveAll(filepath.Join(s.cfg.Dir, "versions", tmpName))
		return err
	}
	final := s.versionDir(id)
	if err := removeAll(final); err != nil {
		return fmt.Errorf("site: %w", err)
	}
	if err := rename(filepath.Join(s.cfg.Dir, "versions", tmpName), final); err != nil {
		return fmt.Errorf("site: %w", err)
	}
	return nil
}

func (s *Syncer) fill(root *os.Root, tmpRel string, files []file) error {
	var shas strings.Builder
	for _, f := range files {
		dest := path.Join(tmpRel, f.Path)
		if err := root.MkdirAll(path.Dir(dest), 0o700); err != nil {
			return fmt.Errorf("site: %w", err)
		}
		if err := root.Link(path.Join("blobs", f.SHA256), dest); err != nil {
			return fmt.Errorf("site: link %q: %w", f.Path, err)
		}
		shas.WriteString(f.SHA256 + "\n")
	}
	if err := root.WriteFile(path.Join(tmpRel, blobsMarker), []byte(shas.String()), 0o600); err != nil {
		return fmt.Errorf("site: %w", err)
	}
	if err := root.WriteFile(path.Join(tmpRel, completeMarker), nil, 0o600); err != nil {
		return fmt.Errorf("site: %w", err)
	}
	return nil
}

// prune keeps only the served version, removes every other version (after RetireAfter, so a request
// that began on one can finish) and any crashed build, and deletes blobs the served version does not
// use. Rolling back to a removed version downloads only the blobs it lacks.
func (s *Syncer) prune() {
	keep := map[string]bool{}
	if cur := s.serve.Load(); cur != nil {
		keep[cur.id] = true
	}
	entries, err := os.ReadDir(filepath.Join(s.cfg.Dir, "versions"))
	if err != nil {
		logrus.WithError(err).Warn("site: prune: list versions")
		return
	}
	for _, e := range entries {
		switch {
		case strings.Contains(e.Name(), ".tmp-"):
			_ = os.RemoveAll(filepath.Join(s.cfg.Dir, "versions", e.Name()))
		case !keep[e.Name()]:
			s.retire(e.Name())
		}
	}
	s.collectBlobs(keep)
}

// retire removes a version that left the kept set, after RetireAfter so a request that began on it can
// finish.
func (s *Syncer) retire(id string) {
	if s.cfg.RetireAfter <= 0 {
		removeVersion(s.versionDir(id))
		return
	}
	if s.retiring[id] {
		return
	}
	s.retiring[id] = true
	time.AfterFunc(s.cfg.RetireAfter, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.retiring, id)
		if cur := s.serve.Load(); cur != nil && cur.id == id {
			return
		}
		removeVersion(s.versionDir(id))
	})
}

func removeVersion(dir string) {
	if err := removeAll(dir); err != nil {
		logrus.WithError(err).WithField("dir", dir).Warn("site: could not remove an old version")
	}
}

// collectBlobs deletes every blob not listed by a kept version, and temp files left by a crash.
func (s *Syncer) collectBlobs(keep map[string]bool) {
	used := map[string]bool{}
	for id := range keep {
		raw, err := os.ReadFile(filepath.Join(s.versionDir(id), blobsMarker))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			logrus.WithError(err).Warn("site: prune: read blob list")
			return
		}
		for _, sha := range strings.Fields(string(raw)) {
			used[sha] = true
		}
	}
	blobs, err := os.ReadDir(filepath.Join(s.cfg.Dir, "blobs"))
	if err != nil {
		return
	}
	for _, b := range blobs {
		if !used[b.Name()] {
			_ = os.Remove(s.blobPath(b.Name()))
		}
	}
}
