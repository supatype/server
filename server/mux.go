package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/admin"
	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/deno"
	"github.com/supatype/server/internal/functions"
	"github.com/supatype/server/internal/maskedfields"
	"github.com/supatype/server/internal/modes"
	"github.com/supatype/server/internal/objstore"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/platformproxy"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/restcache"
	"github.com/supatype/server/internal/serverconf"
	"github.com/supatype/server/internal/sqlrunner"
	"github.com/supatype/server/internal/static"
	"github.com/supatype/server/internal/studioauth"
	"github.com/supatype/server/internal/studiomembers"
	"github.com/supatype/server/internal/valkey"
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
//	/realtime/v1/*            → external realtime WebSocket proxy
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
	r.Use(WithOuterAccessLogContext(cfg.Mode, cfg.ManagedProjectRef))
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
	if valkeyClient == nil && strings.TrimSpace(cfg.ValkeyAddr) != "" {
		if client, err := valkey.New(cfg.ValkeyAddr); err != nil {
			logrus.WithError(err).Warn("mux: failed to init valkey client")
		} else {
			valkeyClient = client
		}
	}

	// ── Admin API ─────────────────────────────────────────────────────────────
	r.Mount("/admin/v1", http.StripPrefix("/admin/v1", admin.Handler(apiStore, cfg, valkeyClient)))
	logrus.Info("mux: admin API mounted at /admin/v1")

	// ── Studio config ─────────────────────────────────────────────────────────
	studioCfg := studioauth.ConfigFromServer(cfg)
	// Resolve Studio capability from `_supatype.studio_members` rather than from
	// the token's claims. Without a DSN there is nothing to read, so the legacy
	// claim path stays in place instead of locking the deployment out of Studio.
	if studiomembers.Available() {
		studioCfg.StudioRole = studiomembers.Lookup
		logrus.Info("mux: Studio capability resolved from _supatype.studio_members")
	} else {
		logrus.Warn("mux: no database DSN — Studio capability falls back to JWT claims")
	}
	studioConfigInner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, err := studioauth.ReadAdminConfigFile(cfg.AdminConfigPath)
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
	r.Post("/studio-config", studioauth.RequireAdmin(studioCfg, studioConfigInner).ServeHTTP)

	// Studio membership assignment. Mounted outside /admin/v1 (the service-role
	// admin API) because this is authenticated as a project user with a Studio
	// role, not with the service role key.
	membersAPI := studioauth.MembersAPI(studioCfg)
	r.Handle("/admin/studio-roles", membersAPI)
	r.Handle("/admin/studio-members", membersAPI)
	r.Handle("/admin/studio-members/*", membersAPI)
	logrus.Info("mux: Studio membership API mounted at /admin/studio-members")

	r.Post("/sql", sqlrunner.Handler().ServeHTTP)

	r.Mount("/auth/v1", http.StripPrefix("/auth/v1", authHandler))

	// ── PostgREST ─────────────────────────────────────────────────────────────
	restProxy := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
	})
	restSchemaFor := func(req *http.Request) string {
		m := manifestFor(req)
		defaultSchema := m.Schema
		if defaultSchema == "" {
			defaultSchema = "public"
		}
		restCfg, _ := apiStore.Get(req.Context())
		if restCfg.Rest.Schema != "" {
			return restCfg.Rest.Schema
		}
		return defaultSchema
	}
	restMaxRowsFor := func(req *http.Request) string {
		restCfg, _ := apiStore.Get(req.Context())
		if restCfg.Rest.MaxRows > 0 && restCfg.Rest.MaxRows != apiconfig.DefaultApiConfig().Rest.MaxRows {
			return fmt.Sprintf("%d", restCfg.Rest.MaxRows)
		}
		return ""
	}
	// The masked-field header sits outside the response cache so it is present on hits as
	// well as misses. Safe there because it describes the schema's restrictions, not one
	// caller's verdicts.
	r.Mount("/rest/v1", http.StripPrefix("/rest/v1", maskedfields.Middleware(
		restcache.Middleware(
			apiStore, valkeyClient, cfg, restSchemaFor, restMaxRowsFor, restProxy,
		),
	)))
	logrus.Info("mux: PostgREST proxy mounted at /rest/v1")

	// ── pg_graphql (PostgREST RPC: graphql_public.graphql) ───────────────────
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
		} else {
			u2 := *req2.URL
			req2.URL = &u2
		}
		req2.URL.Path = "/rpc/graphql"
		req2.URL.RawPath = ""
		req2.Header.Set("Content-Profile", "graphql_public")
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

	if cfg.DenoFunctionsDir != "" || strings.TrimSpace(cfg.FunctionsWorkerURL) != "" {
		r.Mount("/functions/v1", http.StripPrefix("/functions/v1",
			functionsInvocationProxy(cfg, manifestFor, denoManager != nil),
		))
		logrus.Info("mux: Functions invocation proxy mounted at /functions/v1")
	}

	r.Mount("/platform/v1", http.StripPrefix("/platform/v1", platformproxy.Handler()))
	logrus.Info("mux: Platform control plane proxy mounted at /platform/v1")

	baseM := manifestFor(nil)
	if baseM.RealtimeEnabled || strings.TrimSpace(cfg.RealtimeURL) != "" {
		r.Mount("/realtime/v1", http.StripPrefix("/realtime/v1",
			realtimeInvocationProxy(cfg, manifestFor),
		))
		logrus.Info("mux: Realtime invocation proxy mounted at /realtime/v1")
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

	// Studio admin API must register before app catch-all mounts at "/".
	serviceHandler := r
	r.Get("/studio/auth/verify", studioauth.VerifyHandler(studioCfg))
	// Studio's bootstrap: the schema filtered to what the caller may reach, and
	// what they may do with it. Both answered from the database per request.
	r.Get("/studio/schema", studioauth.SchemaHandler(studioCfg))
	r.Get("/studio/session", studioauth.SessionHandler(studioCfg))
	r.Mount("/studio/proxy", http.StripPrefix("/studio/proxy", studioauth.ProxyHandler(serviceHandler, studioCfg)))
	logrus.Info("mux: Studio auth mounted at /studio/auth/verify and /studio/proxy")

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
		// Data-plane requests must carry a project API key; without this an
		// unkeyed request runs as the anon role.
		inner := http.Handler(modes.APIKeyMiddleware(cfg.JWTSecret, r))
		if cfg.JWTSecret == "" {
			logrus.Error("mux: managed mode but JWT secret is unset — data-plane requests will be refused")
		}
		if cfg.TenantHMACSecret != "" {
			inner = modes.TenantMiddleware(cfg.TenantHMACSecret, inner)
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

// functionsInvocationProxy forwards to per-tenant / per-function workers or in-process Deno.
func functionsInvocationProxy(
	cfg *serverconf.ServerConfig,
	manifestFor func(*http.Request) *proxy.RouteManifest,
	inProcessDeno bool,
) http.Handler {
	opts := proxy.ProxyOpts{RequestTimeout: defaultUpstreamHTTPTimeout}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		m := manifestFor(req)
		if m != nil && !m.FunctionsEnabled && strings.TrimSpace(cfg.FunctionsWorkerURL) == "" {
			http.Error(w, "functions disabled", http.StatusNotFound)
			return
		}
		fnName := firstURLSegment(req.URL.Path)
		u, err := resolveFunctionsUpstreamURL(cfg, m, fnName, inProcessDeno)
		if err != nil {
			logrus.WithError(err).Error("mux: functions upstream resolve failed")
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		proxy.WebSocketProxy(u, proxy.New(u, opts)).ServeHTTP(w, req)
	})
}

// realtimeInvocationProxy forwards /realtime/v1 to the external realtime service.
func realtimeInvocationProxy(
	cfg *serverconf.ServerConfig,
	manifestFor func(*http.Request) *proxy.RouteManifest,
) http.Handler {
	opts := proxy.ProxyOpts{RequestTimeout: defaultUpstreamHTTPTimeout}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		m := manifestFor(req)
		if m != nil && !m.RealtimeEnabled {
			http.Error(w, "realtime disabled", http.StatusNotFound)
			return
		}
		u, err := resolveRealtimeUpstreamURL(cfg, m)
		if err != nil {
			logrus.WithError(err).Error("mux: realtime upstream resolve failed")
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		proxy.WebSocketProxy(u, proxy.New(u, opts)).ServeHTTP(w, req)
	})
}

func resolveRealtimeUpstreamURL(
	cfg *serverconf.ServerConfig,
	m *proxy.RouteManifest,
) (*url.URL, error) {
	if m != nil {
		if u := strings.TrimSpace(m.RealtimeURL); u != "" {
			return url.Parse(u)
		}
	}
	if u := strings.TrimSpace(cfg.RealtimeURL); u != "" {
		return url.Parse(u)
	}
	return nil, fmt.Errorf("no realtime upstream configured")
}

func firstURLSegment(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	if i := strings.Index(path, "/"); i >= 0 {
		return path[:i]
	}
	return path
}

func resolveFunctionsUpstreamURL(
	cfg *serverconf.ServerConfig,
	m *proxy.RouteManifest,
	fnName string,
	inProcessDeno bool,
) (*url.URL, error) {
	if m != nil && fnName != "" {
		if u := strings.TrimSpace(m.FunctionWorkerURLs[fnName]); u != "" {
			return url.Parse(u)
		}
	}
	if m != nil {
		if u := strings.TrimSpace(m.FunctionsWorkerURL); u != "" {
			return url.Parse(u)
		}
	}
	if u := strings.TrimSpace(cfg.FunctionsWorkerURL); u != "" {
		return url.Parse(u)
	}
	if inProcessDeno {
		return functionsUpstreamURL(cfg)
	}
	return nil, fmt.Errorf("no functions worker configured")
}

// functionsUpstreamURL resolves the in-process Deno subprocess target.
func functionsUpstreamURL(cfg *serverconf.ServerConfig) (*url.URL, error) {
	if u := strings.TrimSpace(cfg.FunctionsWorkerURL); u != "" {
		parsed, err := url.Parse(u)
		if err != nil {
			return nil, err
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("SUPATYPE_FUNCTIONS_WORKER_URL must include scheme and host")
		}
		return parsed, nil
	}
	return &url.URL{
		Scheme: "http",
		Host:   "127.0.0.1:" + cfg.DenoPort,
	}, nil
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
