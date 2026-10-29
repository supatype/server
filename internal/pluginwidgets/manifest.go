package pluginwidgets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/filestamp"
)

// Widget is what a widget edits, as both the CLI's manifest and Studio name it.
type Widget struct {
	Plugin string `json:"plugin"`
	// Name is the field type or composite the widget edits: its id in the admin config.
	Name string `json:"name"`
	// Kind is "field" or "composite".
	Kind string `json:"kind"`
}

// Entry is one widget as the CLI's manifest names it. Its bundle is the file
// `<SHA256>.js` beside the manifest.
type Entry struct {
	Widget
	SHA256 string `json:"sha256"`
}

// Bundle is the entry's file name in the widgets directory.
func (e Entry) Bundle() string { return e.SHA256 + ".js" }

type manifest struct {
	Version int     `json:"version"`
	Widgets []Entry `json:"widgets"`
}

var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Store reads the manifest in Dir, again only when the file has changed, so a dev
// session's schema reload reaches Studio without a restart. The manifest and bundle
// routes share one, so the manifest is parsed, and a refused plugin logged, once.
type Store struct {
	Dir string

	mu    sync.Mutex
	stamp filestamp.Stamp
	// entries is the last manifest read whole; loaded says there was one.
	entries []Entry
	loaded  bool
	// broken is why the manifest at stamp could not be read, when there is no
	// earlier one to fall back on.
	broken error
	warned map[string]bool

	bundles verifiedBundles
}

// NewStore is a Store for the widgets directory dir.
func NewStore(dir string) *Store {
	return &Store{
		Dir:     dir,
		warned:  map[string]bool{},
		bundles: verifiedBundles{stamps: map[string]filestamp.Stamp{}},
	}
}

// Entries is every widget Studio may load: named by the manifest, from a
// first-party plugin, with a well-formed hash. No manifest means no widgets.
//
// Each version of the file is read once. One that cannot be read or parsed, such as
// a manifest caught mid-write, leaves the last good one in place until the file
// changes again; with no good one yet, it is an error until then.
func (s *Store) Entries() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.Dir, "manifest.json")
	stamp, err := filestamp.Of(path)
	if errors.Is(err, os.ErrNotExist) {
		s.stamp, s.entries, s.loaded, s.broken = filestamp.Stamp{}, nil, false, nil
		return nil, nil
	}
	if err != nil {
		logrus.WithError(err).Error("plugin widgets: could not read the manifest")
		return nil, err
	}
	if !stamp.Same(s.stamp) {
		s.stamp = stamp
		s.reload(path)
	}
	return s.entries, s.broken
}

func (s *Store) reload(path string) {
	entries, err := s.read(path)
	if err == nil {
		s.entries, s.loaded, s.broken = entries, true, nil
		return
	}
	if s.loaded {
		logrus.WithError(err).Warn("plugin widgets: could not read the manifest; serving the last one read until it changes")
		return
	}
	logrus.WithError(err).Error("plugin widgets: could not read the manifest")
	s.broken = err
}

func (s *Store) read(path string) ([]Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return s.servable(m.Widgets), nil
}

func (s *Store) servable(widgets []Entry) []Entry {
	out := make([]Entry, 0, len(widgets))
	for _, w := range widgets {
		if !hexSHA256.MatchString(w.SHA256) {
			continue
		}
		if !FirstParty(w.Plugin) {
			s.refuseOnce(w.Plugin)
			continue
		}
		out = append(out, w)
	}
	return out
}

func (s *Store) refuseOnce(plugin string) {
	if s.warned[plugin] {
		return
	}
	s.warned[plugin] = true
	logrus.WithField("plugin", plugin).Warn("plugin widgets: not loading a widget from a plugin that is not Supatype's own; its fields use Studio's built-in editors")
}

// Names reports whether Studio may load the bundle with this hash.
func (s *Store) Names(sha256 string) (bool, error) {
	entries, err := s.Entries()
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.SHA256 == sha256 {
			return true, nil
		}
	}
	return false, nil
}
