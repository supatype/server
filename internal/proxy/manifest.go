package proxy

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

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

	// ViteDevURL overrides SUPATYPE_VITE_DEV_URL when set (dev HMR at /_vite/*).
	ViteDevURL string `json:"vite_dev_url,omitempty"`

	// RealtimeEnabled gates /realtime/v1 (tier / feature flag).
	RealtimeEnabled bool `json:"realtime_enabled"`

	// RealtimeURL overrides SUPATYPE_REALTIME_URL when set (internal realtime service base URL).
	RealtimeURL string `json:"realtime_url,omitempty"`

	// FunctionsEnabled indicates the Deno functions subsystem should start.
	FunctionsEnabled bool `json:"functions_enabled"`

	// FunctionsWorkerURL is the per-project worker base URL (Pro+ / self-host).
	FunctionsWorkerURL string `json:"functions_worker_url,omitempty"`

	// FunctionWorkerURLs maps function name → worker base URL (free-tier per-function pool).
	FunctionWorkerURLs map[string]string `json:"function_worker_urls,omitempty"`

	// CorsAllowedOrigins lists allowed browser Origin values (exact match).
	// Merged from the keyspace tenant config / manifest in managed mode; may be
	// combined with SUPATYPE_CORS_ALLOW_ORIGINS on the server.
	CorsAllowedOrigins []string `json:"cors_allowed_origins,omitempty"`

	// StaticCacheHTML overrides Cache-Control for HTML responses and SPA fallback (default no-cache).
	StaticCacheHTML string `json:"static_cache_html,omitempty"`

	// StaticCacheHashedAssets overrides Cache-Control for bundled/hashed asset paths (default immutable long cache).
	StaticCacheHashedAssets string `json:"static_cache_hashed_assets,omitempty"`

	// StaticCachePrefixes maps URL path prefix → Cache-Control (longest matching prefix wins).
	StaticCachePrefixes map[string]string `json:"static_cache_prefixes,omitempty"`

	// Hooks maps table name → lifecycle hooks, written by `supatype push`.
	//
	// Keyed by table because that is what a REST path carries; the model name never reaches the
	// wire. Absent for a project that declares none, which is the common case.
	Hooks map[string]TableHooks `json:"hooks,omitempty"`

	// Validators maps table name → per-field validators, written by `supatype push`.
	//
	// Separate from Hooks rather than an extra event inside it: a hook is keyed by lifecycle event
	// and a validator by column, and folding them into one map would mean a column named
	// "beforeChange" collided with an event.
	Validators map[string]TableValidators `json:"validators,omitempty"`

	// Cache maps table name → what the schema permits caching, written by `supatype push`.
	//
	// **A ceiling, not a setting.** The runtime may narrow any of it — lower a TTL, switch a table
	// off — and may never widen it. A table absent from this map cannot be cached by anyone, which
	// is the default and the reason absence is meaningful rather than merely empty.
	//
	// It arrives on the manifest rather than in the AST snapshot because the schema engine
	// re-serialises its own parsed struct when it writes that snapshot, and its platform
	// annotations know only `access` and `searchFields` — an unrecognised key is dropped in
	// silence. Hooks above take this route for the same reason.
	Cache map[string]TableCache `json:"cache,omitempty"`
}

// TableCache is what one table's schema permits. See RouteManifest.Cache.
//
// Every field is a pointer so that "the schema said nothing" and "the schema said false" stay
// distinguishable. They are different instructions: silence leaves the decision to the runtime
// default, and an explicit false is a hard opt-out that no runtime edit may undo. Collapsed into a
// bare bool they would be the same value, and the opt-out would quietly become a suggestion.
type TableCache struct {
	// Enabled permits the response cache. Without it nothing else here applies.
	Enabled *bool `json:"enabled,omitempty"`

	// MaxTTL caps how long a response may be held, in seconds. The effective TTL is the smallest
	// of this, the project's cache_max_ttl, and the client's max-age — every writer may only
	// shorten it.
	MaxTTL *int `json:"maxTtl,omitempty"`

	// Public permits one entry shared across callers instead of per-user keys.
	//
	// `supatype push` refuses this on a table whose read rule varies by caller, which is where the
	// check belongs: the model has the rule and the setting in one object. The server still makes
	// its own runtime decision from the access rules — this only says the schema allows it.
	Public *bool `json:"public,omitempty"`

	// Rows permits the Mode B row cache on primary-key reads.
	//
	// Declaration-only: unlike the rest of this struct there is no runtime switch that turns it on.
	// The row cache is eventual with a bound rather than read-your-writes, and whether a table
	// tolerates that is a design-time invariant its author knows and an operator does not.
	Rows *bool `json:"rows,omitempty"`
}

// TableValidators is one table's per-field validators, keyed by **column** name.
type TableValidators map[string]HookConfig

// TableHooks is one table's lifecycle hooks, keyed by event
// ("beforeChange", "afterChange", "beforeDelete", "afterDelete").
type TableHooks map[string]HookConfig

// HookConfig is one hook: the edge function to call and how to treat its silence.
type HookConfig struct {
	// Function is the function name, as discovered by the worker (its directory name).
	Function string `json:"function"`

	// TimeoutMs abandons the hook after this long. The CLI fills a default well below the
	// edge-function ceiling, so a hung hook fails fast instead of holding an invocation slot.
	TimeoutMs int `json:"timeout,omitempty"`

	// OnUnavailable is what a hook that does not *answer* means — a timeout, a connection failure,
	// a 5xx, an unparseable body. "reject" fails the write, "log" allows it.
	//
	// Deliberately not consulted for a 4xx: that is the hook working correctly and saying no, and it
	// reaches the caller as the status the hook chose. Collapsing the two would mean either a broken
	// hook silently passing writes it was meant to check, or a considered rejection reading as an
	// outage.
	OnUnavailable string `json:"onUnavailable,omitempty"`
}

// Load reads and parses the manifest at path.
// Returns an empty manifest (not an error) if the file does not exist yet —
// this is normal on first run before `supatype push` has been called.
func Load(path string) (*RouteManifest, error) {
	data, err := readFileUnderParent(path)
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

func readFileUnderParent(path string) ([]byte, error) {
	dir, name := filepath.Split(filepath.Clean(path))
	if dir == "" {
		dir = "."
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = root.Close()
	}()

	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()
	return io.ReadAll(f)
}

// ParseRouteManifestJSON unmarshals JSON bytes into a RouteManifest (the same
// shape as manifest.json).
//
// It does not apply the public-schema default, because the only caller is the
// managed overlay: a tenant manifest that says nothing about the schema must
// leave the layer below it alone. Defaulting here made every overlay claim
// "public", so a tenant whose config set another schema had it silently reset
// the moment it also had a manifest override — and every REST request then went
// to the wrong schema. Load applies the default for the file, which is a whole
// manifest rather than a layer.
func ParseRouteManifestJSON(data []byte) (*RouteManifest, error) {
	var m RouteManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
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
	// Deep-copied like every other map here. A shared hook map would let one tenant's reload mutate
	// what another tenant's in-flight request is reading.
	if len(m.Hooks) > 0 {
		cm.Hooks = make(map[string]TableHooks, len(m.Hooks))
		for table, events := range m.Hooks {
			copied := make(TableHooks, len(events))
			for event, cfg := range events {
				copied[event] = cfg
			}
			cm.Hooks[table] = copied
		}
	}
	if len(m.Cache) > 0 {
		// Copied for the same reason as the maps around it. The struct's own fields are pointers,
		// but they are never written through after a load — only read — so copying the map is
		// enough to keep one tenant's reload away from another's in-flight request.
		cm.Cache = make(map[string]TableCache, len(m.Cache))
		for table, c := range m.Cache {
			cm.Cache[table] = c
		}
	}
	if len(m.Validators) > 0 {
		cm.Validators = make(map[string]TableValidators, len(m.Validators))
		for table, fields := range m.Validators {
			copied := make(TableValidators, len(fields))
			for field, cfg := range fields {
				copied[field] = cfg
			}
			cm.Validators[table] = copied
		}
	}
	if len(m.StaticCachePrefixes) > 0 {
		cm.StaticCachePrefixes = make(map[string]string, len(m.StaticCachePrefixes))
		for k, v := range m.StaticCachePrefixes {
			cm.StaticCachePrefixes[k] = v
		}
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
	if overlay.ViteDevURL != "" {
		base.ViteDevURL = overlay.ViteDevURL
	}
	base.RealtimeEnabled = overlay.RealtimeEnabled
	if overlay.RealtimeURL != "" {
		base.RealtimeURL = overlay.RealtimeURL
	}
	base.FunctionsEnabled = overlay.FunctionsEnabled
	if overlay.FunctionsWorkerURL != "" {
		base.FunctionsWorkerURL = overlay.FunctionsWorkerURL
	}
	if len(overlay.FunctionWorkerURLs) > 0 {
		if base.FunctionWorkerURLs == nil {
			base.FunctionWorkerURLs = make(map[string]string)
		}
		for k, v := range overlay.FunctionWorkerURLs {
			if v != "" {
				base.FunctionWorkerURLs[k] = v
			}
		}
	}
	if len(overlay.CorsAllowedOrigins) > 0 {
		base.CorsAllowedOrigins = append([]string(nil), overlay.CorsAllowedOrigins...)
	}
	if overlay.StaticCacheHTML != "" {
		base.StaticCacheHTML = overlay.StaticCacheHTML
	}
	if overlay.StaticCacheHashedAssets != "" {
		base.StaticCacheHashedAssets = overlay.StaticCacheHashedAssets
	}
	if len(overlay.StaticCachePrefixes) > 0 {
		if base.StaticCachePrefixes == nil {
			base.StaticCachePrefixes = make(map[string]string)
		}
		for k, v := range overlay.StaticCachePrefixes {
			base.StaticCachePrefixes[k] = v
		}
	}
	// Replaced wholesale rather than merged per table. A hook removed from the schema must stop
	// firing, and a per-key merge would keep calling it — the overlay is the current truth about
	// which hooks exist.
	if overlay.Hooks != nil {
		base.Hooks = overlay.Hooks
	}
	// Wholesale for the same reason, and more sharply: Cache is a ceiling. A table dropped from the
	// schema's declaration must stop being cacheable at once, and a per-table merge would leave the
	// old permission standing — the one direction the ceiling is not allowed to drift.
	if overlay.Cache != nil {
		base.Cache = overlay.Cache
	}
}

// newWatcher is the fsnotify constructor, indirected so the failure to create
// one — which needs an exhausted inotify budget to provoke — can be exercised.
var newWatcher = fsnotify.NewWatcher

// Watch starts a goroutine that calls fn whenever the manifest file at path
// changes. The goroutine exits when the watcher is closed.
func Watch(path string, fn func(*RouteManifest)) error {
	watcher, err := newWatcher()
	if err != nil {
		return err
	}

	if err := watcher.Add(path); err != nil {
		// Whether the close also fails is not actionable and not the failure
		// worth reporting: the caller is already being told the watch could not
		// be set up.
		_ = watcher.Close()
		return err
	}

	go func() {
		defer watcher.Close() //nolint:errcheck
		watchLoop(watcher.Events, watcher.Errors, path, fn)
	}()
	return nil
}

// watchLoop reloads the manifest on every write, until both channels close.
//
// Separated from Watch so it can be driven directly: the interesting cases are
// a reload that fails to parse and a watcher that reports an error, and neither
// can be arranged reliably through a real filesystem.
func watchLoop(events <-chan fsnotify.Event, errs <-chan error, path string, fn func(*RouteManifest)) {
	for events != nil || errs != nil {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}
			m, err := Load(path)
			if err != nil {
				// A half-written file is the ordinary case here: an editor or a
				// deploy writes in two syscalls and the first event catches it
				// mid-write. Keep serving what is already loaded and wait for the
				// event that completes it.
				logrus.WithError(err).Warn("manifest reload failed")
				continue
			}
			fn(m)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			logrus.WithError(err).Warn("manifest watcher error")
		}
	}
}
