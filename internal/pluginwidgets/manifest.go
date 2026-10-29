package pluginwidgets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Entry is one widget as the CLI's manifest names it. Its bundle is the file
// `<SHA256>.js` beside the manifest.
type Entry struct {
	Plugin string `json:"plugin"`
	// Name is the field type or composite the widget edits: its id in the admin config.
	Name string `json:"name"`
	// Kind is "field" or "composite".
	Kind   string `json:"kind"`
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
// session's schema reload reaches Studio without a restart.
type Store struct {
	Dir string

	mu      sync.Mutex
	modTime time.Time
	entries []Entry
	warned  map[string]bool
}

var stores sync.Map

// StoreFor is the one Store for dir, shared by the manifest and bundle routes so the
// manifest is parsed, and a refused plugin logged, once.
func StoreFor(dir string) *Store {
	store, _ := stores.LoadOrStore(dir, &Store{Dir: dir, warned: map[string]bool{}})
	return store.(*Store)
}

// Entries is every widget Studio may load: named by the manifest, from a
// first-party plugin, with a well-formed hash. No manifest means no widgets.
func (s *Store) Entries() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.Dir, "manifest.json")
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		s.entries, s.modTime = nil, time.Time{}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.ModTime().Equal(s.modTime) {
		if err := s.reload(path, info.ModTime()); err != nil {
			return nil, err
		}
	}
	return s.entries, nil
}

func (s *Store) reload(path string, modTime time.Time) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	s.entries = s.servable(m.Widgets)
	s.modTime = modTime
	return nil
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

// Bundle is the entry whose bundle has this hash, if the manifest names one.
func (s *Store) Bundle(sha256 string) (Entry, bool, error) {
	entries, err := s.Entries()
	if err != nil {
		return Entry{}, false, err
	}
	for _, e := range entries {
		if e.SHA256 == sha256 {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}
