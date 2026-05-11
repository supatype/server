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

	// CorsAllowedOrigins lists allowed browser Origin values (exact match).
	// Merged from Valkey tenant config / manifest in managed mode; may be
	// combined with SUPATYPE_CORS_ALLOW_ORIGINS on the server.
	CorsAllowedOrigins []string `json:"cors_allowed_origins,omitempty"`
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

// ParseRouteManifestJSON unmarshals JSON bytes into RouteManifest (same shape as manifest.json).
func ParseRouteManifestJSON(data []byte) (*RouteManifest, error) {
	var m RouteManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Schema == "" {
		m.Schema = "public"
	}
	return &m, nil
}

// CloneRouteManifest returns a copy with schema default applied.
func CloneRouteManifest(m *RouteManifest) *RouteManifest {
	if m == nil {
		return &RouteManifest{Schema: "public"}
	}
	cm := *m
	if cm.Schema == "" {
		cm.Schema = "public"
	}
	return &cm
}

// MergeRouteManifest copies non-empty string fields and bool fields from overlay onto base (mutates base).
// Used when applying tenant:{ref}:manifest over lower-priority layers.
func MergeRouteManifest(base, overlay *RouteManifest) {
	if base == nil || overlay == nil {
		return
	}
	if overlay.Schema != "" {
		base.Schema = overlay.Schema
	}
	if overlay.PostgRESTURL != "" {
		base.PostgRESTURL = overlay.PostgRESTURL
	}
	if overlay.GraphQLURL != "" {
		base.GraphQLURL = overlay.GraphQLURL
	}
	if overlay.StorageURL != "" {
		base.StorageURL = overlay.StorageURL
	}
	if overlay.AppMode != "" {
		base.AppMode = overlay.AppMode
	}
	if overlay.AppStaticDir != "" {
		base.AppStaticDir = overlay.AppStaticDir
	}
	if overlay.AppUpstream != "" {
		base.AppUpstream = overlay.AppUpstream
	}
	base.RealtimeEnabled = overlay.RealtimeEnabled
	base.FunctionsEnabled = overlay.FunctionsEnabled
	if len(overlay.CorsAllowedOrigins) > 0 {
		base.CorsAllowedOrigins = append([]string(nil), overlay.CorsAllowedOrigins...)
	}
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
