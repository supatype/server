package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/supatype/auth/internal/admin"
	"github.com/supatype/auth/internal/apiconfig"
	"github.com/supatype/auth/internal/deno"
	"github.com/supatype/auth/internal/functions"
	"github.com/supatype/auth/internal/modes"
	"github.com/supatype/auth/internal/objstore"
	"github.com/supatype/auth/internal/outerhealth"
	"github.com/supatype/auth/internal/proxy"
	"github.com/supatype/auth/internal/realtime"
	"github.com/supatype/auth/internal/serverconf"
	"github.com/supatype/auth/internal/sqlrunner"
	"github.com/supatype/auth/internal/static"
	"github.com/supatype/auth/internal/valkey"
)

// defaultUpstreamHTTPTimeout caps reverse-proxy round-trips so a wedged upstream
// cannot hang requests indefinitely (including under go test).
const defaultUpstreamHTTPTimeout = 2 * time.Minute

// buildOuterMux constructs the top-level chi.Mux that wraps all services.
//
// manifestFor returns the effective route manifest for a request (or nil
// request for baseline-only mounts: realtime, static app). healthProbes
// should reflect file-layer manifest for /health (not per-tenant Valkey).
//
// sharedValkey, when non-nil, is used for the admin API Valkey client instead
// of opening a second connection.
//
// Route layout:
//
//	/auth/v1/*                → GoTrue (existing authHandler)
//	/rest/v1/*                → PostgREST
//	/graphql/v1/*             → pg_graphql
//	/storage/v1/*             → Supatype Storage
//	/functions/v1/admin/*     → Functions admin API (service-role protected)
//	/functions/v1/*           → Deno edge functions proxy
//	/realtime/v1/*            → LISTEN/NOTIFY WebSocket hub
//	/*                        → App (none/static/proxy per config)
//
// In dev mode the mux is wrapped in DevMiddleware (permissive CORS). Vite HMR is mounted
// on this router at /_vite/* when SUPATYPE_VITE_DEV_URL (or manifest vite_dev_url) is set,
// or SUPATYPE_APP_UPSTREAM when app mode is not proxy (legacy), so outer JSON access logs apply.
// In managed mode the mux is wrapped in ManagedCORSMiddleware (when configured) outside
// TenantMiddleware (HMAC), then TenantMiddleware, so OPTIONS preflight is not blocked.
func buildOuterMux(
	cfg *serverconf.ServerConfig,
	manifestFor func(*http.Request) *proxy.RouteManifest,
	healthProbes func() outerhealth.ProbeConfig,
	authHandler http.Handler,
	denoManager *deno.Manager,
	version string,
	sharedValkey *valkey.Client,
	sendEmailHook http.Handler,
) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestLogger(outerAccessLogFormatter{}))

	outerhealth.Attach(r, cfg, version, healthProbes)

	if sendEmailHook != nil {
		r.Post("/internal/v0hooks/send-email", sendEmailHook.ServeHTTP)
		logrus.Info("mux: send-email hook receiver mounted at POST /internal/v0hooks/send-email")
	}

	// ── API config store ──────────────────────────────────────────────────────
	apiStore := apiconfig.NewFileStore(cfg.ApiConfigPath)
	valkeyClient := sharedValkey
	if valkeyClient == nil && cfg.Mode == "managed" && strings.TrimSpace(cfg.ValkeyAddr) != "" {
		if client, err := valkey.New(cfg.ValkeyAddr); err != nil {
			logrus.WithError(err).Warn("mux: failed to init valkey client for managed credentials")
		} else {
			valkeyClient = client
		}
	}

	// ── Admin API ─────────────────────────────────────────────────────────────
	r.Mount("/admin/v1", http.StripPrefix("/admin/v1", admin.Handler(apiStore, cfg, valkeyClient)))
	logrus.Info("mux: admin API mounted at /admin/v1")

	// ── Studio config ─────────────────────────────────────────────────────────
	r.Post("/studio-config", func(w http.ResponseWriter, req *http.Request) {
		data, err := os.ReadFile(cfg.AdminConfigPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, `{"error":"schema not pushed yet"}`, http.StatusNotFound)
				return
			}
			http.Error(w, `{"error":"failed to read admin config"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})

	r.Post("/sql", sqlrunner.Handler().ServeHTTP)

	r.Mount("/auth/v1", http.StripPrefix("/auth/v1", authHandler))

	// ── PostgREST ─────────────────────────────────────────────────────────────
	r.Mount("/rest/v1", http.StripPrefix("/rest/v1", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		m := manifestFor(req)
		postURL := firstNonEmpty(m.PostgRESTURL, cfg.PostgRESTURL, "http://localhost:3000")
		u, err := url.Parse(postURL)
		if err != nil {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		defaultSchema := m.Schema
		if defaultSchema == "" {
			defaultSchema = "public"
		}
		proxy.New(u, proxy.ProxyOpts{
			RequestTimeout: defaultUpstreamHTTPTimeout,
			HeaderFunc: func(req *http.Request) map[string]string {
				restCfg, _ := apiStore.Get(req.Context())
				schema := restCfg.Rest.Schema
				if schema == "" {
					schema = defaultSchema
				}
				h := map[string]string{"X-Pg-Schema": schema}
				if restCfg.Rest.MaxRows > 0 && restCfg.Rest.MaxRows != apiconfig.DefaultApiConfig().Rest.MaxRows {
					h["Prefer"] = fmt.Sprintf("max-rows=%d", restCfg.Rest.MaxRows)
				}
				return h
			},
		}).ServeHTTP(w, req)
	})))
	logrus.Info("mux: PostgREST proxy mounted at /rest/v1")

	// ── pg_graphql ────────────────────────────────────────────────────────────
	r.Mount("/graphql/v1", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		m := manifestFor(req)
		graphQLUpstream := firstNonEmpty(m.GraphQLURL, cfg.GraphQLURL,
			firstNonEmpty(m.PostgRESTURL, cfg.PostgRESTURL, "http://localhost:3000"))
		u, err := url.Parse(graphQLUpstream)
		if err != nil {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		req2 := req.Clone(req.Context())
		if req2.URL == nil {
			req2.URL = &url.URL{}
		}
		p := req2.URL.Path
		switch {
		case p == "" || p == "/":
			req2.URL.Path = "/graphql/v1"
		case strings.HasPrefix(p, "/graphql/v1"):
			req2.URL.Path = p
		default:
			req2.URL.Path = "/graphql/v1" + p
		}
		req2.URL.RawPath = ""
		endUserAuth := strings.TrimSpace(req.Header.Get("Authorization"))
		px := proxy.New(u, proxy.ProxyOpts{
			RequestTimeout: defaultUpstreamHTTPTimeout,
			HeaderFunc: func(_ *http.Request) map[string]string {
				h := map[string]string{}
				sr := strings.TrimSpace(cfg.ServiceRoleKey)
				if sr == "" {
					return h
				}
				if strings.HasPrefix(strings.ToLower(sr), "bearer ") {
					h["Authorization"] = sr
				} else {
					h["Authorization"] = "Bearer " + sr
				}
				if endUserAuth != "" {
					h["X-Supatype-End-User-Authorization"] = endUserAuth
				}
				return h
			},
		})
		px.ServeHTTP(w, req2)
	}))
	logrus.Info("mux: GraphQL proxy mounted at /graphql/v1")

	// ── Storage ───────────────────────────────────────────────────────────────
	if cfg.StorageProvider == "local" && cfg.StoragePath != "" {
		r.Mount("/storage/v1", http.StripPrefix("/storage/v1",
			objstore.Handler(cfg.StoragePath, cfg.JWTSecret)))
		logrus.WithField("path", cfg.StoragePath).Info("mux: local storage handler mounted at /storage/v1")
	} else {
		r.Mount("/storage/v1", http.StripPrefix("/storage/v1", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			m := manifestFor(req)
			storageURL := firstNonEmpty(m.StorageURL, cfg.StorageURL)
			if storageURL == "" {
				http.Error(w, "storage not configured", http.StatusBadGateway)
				return
			}
			u, err := url.Parse(storageURL)
			if err != nil {
				http.Error(w, "bad gateway", http.StatusBadGateway)
				return
			}
			proxy.New(u, proxy.ProxyOpts{RequestTimeout: defaultUpstreamHTTPTimeout}).ServeHTTP(w, req)
		})))
		logrus.Info("mux: Storage proxy mounted at /storage/v1")
	}

	if cfg.DenoFunctionsDir != "" {
		r.Mount("/functions/v1/admin", functions.Handler(cfg.DenoFunctionsDir, denoManager))
		logrus.WithField("dir", cfg.DenoFunctionsDir).Info("mux: Functions admin handler mounted at /functions/v1/admin")
	}

	if cfg.DenoFunctionsDir != "" {
		denoURL := &url.URL{
			Scheme: "http",
			Host:   "127.0.0.1:" + cfg.DenoPort,
		}
		r.Mount("/functions/v1", http.StripPrefix("/functions/v1",
			proxy.WebSocketProxy(denoURL, proxy.New(denoURL, proxy.ProxyOpts{RequestTimeout: defaultUpstreamHTTPTimeout})),
		))
		logrus.WithField("port", cfg.DenoPort).Info("mux: Deno functions proxy mounted at /functions/v1")
	}

	baseM := manifestFor(nil)
	if baseM.RealtimeEnabled {
		r.Get("/realtime/v1/health", realtime.LivenessHandler())
		hub := realtime.NewHub()
		presenceTrackers := make(map[string]*realtime.PresenceTracker)
		var presenceMu sync.Mutex
		r.Mount("/realtime/v1", realtime.Handler(hub, cfg.ServiceRoleKey, presenceTrackers, &presenceMu))
		logrus.Info("mux: Realtime WebSocket handler mounted at /realtime/v1")
	}

	appMode := firstNonEmpty(baseM.AppMode, cfg.AppMode, "none")

	if strings.EqualFold(strings.TrimSpace(cfg.Mode), "dev") {
		vu := firstNonEmpty(baseM.ViteDevURL, cfg.ViteDevURL)
		if vu == "" && appMode != "proxy" {
			vu = strings.TrimSpace(cfg.AppUpstream)
		}
		if vu != "" {
			if u, err := url.Parse(vu); err == nil && u.Scheme != "" && u.Host != "" {
				vh := proxy.WebSocketProxy(u, proxy.New(u, proxy.ProxyOpts{RequestTimeout: defaultUpstreamHTTPTimeout}))
				r.Handle("/_vite/*", http.StripPrefix("/_vite", vh))
				logrus.WithField("vite_dev_url", vu).Info("mux: Vite HMR proxy mounted at /_vite")
			}
		}
	}

	switch appMode {
	case "static":
		dir := firstNonEmpty(baseM.AppStaticDir, cfg.AppStaticDir)
		if dir != "" {
			r.Mount("/", static.Handler(dir, cfg.AppSPAFallback, staticCacheOpts(cfg, baseM)))
			logrus.WithField("dir", dir).Info("mux: static app handler mounted")
		}

	case "proxy":
		upstream := firstNonEmpty(baseM.AppUpstream, cfg.AppUpstream)
		if upstream != "" {
			if u, err := url.Parse(upstream); err == nil {
				r.Mount("/", proxy.WebSocketProxy(u, proxy.New(u, proxy.ProxyOpts{RequestTimeout: defaultUpstreamHTTPTimeout})))
				logrus.WithField("upstream", upstream).Info("mux: app proxy mounted")
			}
		}
	}

	var handler http.Handler = r

	switch cfg.Mode {
	case "dev":
		handler = modes.DevMiddleware(r)
	case "managed":
		inner := http.Handler(r)
		if cfg.TenantHMACSecret != "" {
			inner = modes.TenantMiddleware(cfg.TenantHMACSecret, r)
		} else {
			logrus.Warn("mux: managed mode but SUPATYPE_TENANT_HMAC_SECRET is unset — tenant verification disabled")
		}
		handler = modes.ManagedCORSMiddleware(cfg.CorsAllowOrigins, manifestFor, inner)
	}

	if cfg.Mode == "standalone" {
		if o := modes.ParseCSV(cfg.CorsAllowOrigins); len(o) > 0 {
			handler = modes.AllowlistCORSMiddleware(func(*http.Request) []string { return o }, handler)
		}
	}

	return handler
}

func staticCacheOpts(cfg *serverconf.ServerConfig, m *proxy.RouteManifest) static.CacheOpts {
	html := cfg.StaticCacheHTML
	hashed := cfg.StaticCacheHashedAssets
	prefixes := parseStaticPrefixesJSON(cfg.StaticCachePrefixesJSON)
	if m != nil {
		if m.StaticCacheHTML != "" {
			html = m.StaticCacheHTML
		}
		if m.StaticCacheHashedAssets != "" {
			hashed = m.StaticCacheHashedAssets
		}
		if len(m.StaticCachePrefixes) > 0 {
			if prefixes == nil {
				prefixes = make(map[string]string)
			}
			for k, v := range m.StaticCachePrefixes {
				prefixes[k] = v
			}
		}
	}
	return static.CacheOpts{
		HTML:         html,
		HashedAssets: hashed,
		Prefixes:     prefixes,
	}
}

func parseStaticPrefixesJSON(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		logrus.WithError(err).Warn("SUPATYPE_STATIC_CACHE_PREFIXES_JSON: invalid JSON — ignoring")
		return nil
	}
	return out
}

// firstNonEmpty returns the first non-empty string from vals.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
