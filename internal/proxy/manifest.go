package proxy

import (
	"encoding/json"
	"os"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

// RouteManifest describes the active upstream services for this project.
// It is written by `supatype push` (engine generate --manifest-out) and read
// by supatype-server on startup and on SIGHUP / file change.
type RouteManifest struct {
	// Schema is the Postgres schema name (default: "public").
	Schema string `json:"schema"`

	// PostgRESTURL overrides SUPATYPE_POSTGREST_URL when set.
	PostgRESTURL string `json:"postgrest_url,omitempty"`

	// GraphQLURL overrides SUPATYPE_GRAPHQL_URL when set.
	GraphQLURL string `json:"graphql_url,omitempty"`

	// StorageURL overrides SUPATYPE_STORAGE_URL when set.
	StorageURL string `json:"storage_url,omitempty"`

	// AppMode overrides SUPATYPE_APP_MODE when set ("none"|"static"|"proxy").
	AppMode string `json:"app_mode,omitempty"`

	// AppStaticDir overrides SUPATYPE_APP_STATIC_DIR when set.
	AppStaticDir string `json:"app_static_dir,omitempty"`

	// AppUpstream overrides SUPATYPE_APP_UPSTREAM when set.
	AppUpstream string `json:"app_upstream,omitempty"`

	// RealtimeEnabled indicates the realtime LISTEN/NOTIFY subsystem should start.
	RealtimeEnabled bool `json:"realtime_enabled"`

	// FunctionsEnabled indicates the Deno functions subsystem should start.
	FunctionsEnabled bool `json:"functions_enabled"`
}

// Load reads and parses the manifest at path.
// Returns an empty manifest (not an error) if the file does not exist yet —
// this is normal on first run before `supatype push` has been called.
func Load(path string) (*RouteManifest, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &RouteManifest{Schema: "public"}, nil
	}
	if err != nil {
		return nil, err
	}
	var m RouteManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Schema == "" {
		m.Schema = "public"
	}
	return &m, nil
}

// Watch starts a goroutine that calls fn whenever the manifest file at path
// changes. The goroutine exits when ctx is done.
func Watch(path string, fn func(*RouteManifest)) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := watcher.Add(path); err != nil {
		watcher.Close() //nolint:errcheck
		return err
	}

	go func() {
		defer watcher.Close() //nolint:errcheck
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					m, err := Load(path)
					if err != nil {
						logrus.WithError(err).Warn("manifest reload failed")
						continue
					}
					fn(m)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logrus.WithError(err).Warn("manifest watcher error")
			}
		}
	}()
	return nil
}
