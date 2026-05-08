package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/supatype/auth/internal/admin"
	"github.com/supatype/auth/internal/apiconfig"
	"github.com/supatype/auth/internal/deno"
	"github.com/supatype/auth/internal/functions"
	"github.com/supatype/auth/internal/modes"
	"github.com/supatype/auth/internal/objstore"
	"github.com/supatype/auth/internal/proxy"
	"github.com/supatype/auth/internal/realtime"
	"github.com/supatype/auth/internal/serverconf"
	"github.com/supatype/auth/internal/sqlrunner"
	"github.com/supatype/auth/internal/static"
	"github.com/supatype/auth/internal/valkey"
)

// buildOuterMux constructs the top-level chi.Mux that wraps all services.
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
// In dev mode the mux is wrapped in DevMiddleware (permissive CORS + Vite HMR proxy).
// In managed mode the mux is wrapped in TenantMiddleware (HMAC signature verification).
func buildOuterMux(cfg *serverconf.ServerConfig, manifest *proxy.RouteManifest, authHandler http.Handler, denoManager *deno.Manager) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestLogger(&middleware.DefaultLogFormatter{
		Logger:  logrus.StandardLogger(),
		NoColor: true,
	}))

	// ── API config store ──────────────────────────────────────────────────────
	apiStore := apiconfig.NewFileStore(cfg.ApiConfigPath)
	var valkeyClient *valkey.Client
	if cfg.Mode == "managed" && cfg.ValkeyAddr != "" {
		if client, err := valkey.New(cfg.ValkeyAddr); err != nil {
			logrus.WithError(err).Warn("mux: failed to init valkey client for managed credentials")
		} else {
			valkeyClient = client
		}
	}

	// ── Admin API ─────────────────────────────────────────────────────────────
	// Admin uses stdlib http.ServeMux, which matches r.URL.Path. Chi Mount does
	// not rewrite Path for non-chi handlers (unlike sub-routers), so strip the
	// mount prefix the same way as /auth/v1 below.
	r.Mount("/admin/v1", http.StripPrefix("/admin/v1", admin.Handler(apiStore, cfg, valkeyClient)))
	logrus.Info("mux: admin API mounted at /admin/v1")

	// ── Studio config ─────────────────────────────────────────────────────────
	// POST /studio-config → serve the engine-generated admin-config.json.
	// Studio posts an empty body and expects AdminConfig JSON back.
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

	// ── SQL runner (studio Database view) ────────────────────────────────────
	r.Post("/sql", sqlrunner.Handler().ServeHTTP)

	// ── Auth ──────────────────────────────────────────────────────────────────
	r.Mount("/auth/v1", http.StripPrefix("/auth/v1", authHandler))

	// ── PostgREST ─────────────────────────────────────────────────────────────
	postgreSTURL := firstNonEmpty(manifest.PostgRESTURL, cfg.PostgRESTURL, "http://localhost:3000")
	if u, err := url.Parse(postgreSTURL); err == nil {
		defaultSchema := manifest.Schema
		r.Mount("/rest/v1", http.StripPrefix("/rest/v1",
			proxy.New(u, proxy.ProxyOpts{
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
			}),
		))
		logrus.WithField("url", postgreSTURL).Info("mux: PostgREST proxy mounted at /rest/v1")
	} else {
		logrus.WithError(err).Warn("mux: invalid PostgREST URL, /rest/v1 not mounted")
	}

	// ── pg_graphql ────────────────────────────────────────────────────────────
	// PostgREST serves pg_graphql at /graphql/v1 on the same host as REST.
	// Default upstream is the PostgREST base URL (manifest / env / local).
	graphQLUpstream := firstNonEmpty(manifest.GraphQLURL, cfg.GraphQLURL, postgreSTURL)
	if graphQLUpstream != "" {
		if u, err := url.Parse(graphQLUpstream); err == nil {
			// chi.Mount strips the /graphql/v1 prefix before calling this handler.
			// A plain StripPrefix + proxy previously forwarded POST /graphql/v1 as GET / on
			// PostgREST, which responds with 405 Unsupported method.
			r.Mount("/graphql/v1", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
				proxy.New(u, proxy.ProxyOpts{}).ServeHTTP(w, req2)
			}))
			logrus.WithField("url", graphQLUpstream).Info("mux: GraphQL proxy mounted at /graphql/v1")
		} else {
			logrus.WithError(err).Warn("mux: invalid GraphQL upstream URL, /graphql/v1 not mounted")
		}
	}

	// ── Storage ───────────────────────────────────────────────────────────────
	// In local dev (STORAGE_PROVIDER=local) the built-in filesystem handler is
	// used so that storage works with no external service or MinIO required.
	// In all other cases (production, S3 mode) requests are proxied to
	// SUPATYPE_STORAGE_URL (or the URL from the engine manifest).
	if cfg.StorageProvider == "local" && cfg.StoragePath != "" {
		r.Mount("/storage/v1", http.StripPrefix("/storage/v1",
			objstore.Handler(cfg.StoragePath, cfg.JWTSecret)))
		logrus.WithField("path", cfg.StoragePath).Info("mux: local storage handler mounted at /storage/v1")
	} else {
		storageURL := firstNonEmpty(manifest.StorageURL, cfg.StorageURL)
		if storageURL != "" {
			if u, err := url.Parse(storageURL); err == nil {
				r.Mount("/storage/v1", http.StripPrefix("/storage/v1", proxy.New(u, proxy.ProxyOpts{})))
				logrus.WithField("url", storageURL).Info("mux: Storage proxy mounted at /storage/v1")
			}
		}
	}

	// ── Functions Admin API ───────────────────────────────────────────────────
	// Mounted unconditionally when a functions dir is configured so that the
	// studio can list/manage functions in all modes (dev, self-hosted, cloud).
	// denoManager may be nil when Deno is not running; all handlers degrade
	// gracefully in that case.
	if cfg.DenoFunctionsDir != "" {
		r.Mount("/functions/v1/admin", functions.Handler(cfg.DenoFunctionsDir, denoManager))
		logrus.WithField("dir", cfg.DenoFunctionsDir).Info("mux: Functions admin handler mounted at /functions/v1/admin")
	}

	// ── Deno Edge Functions Proxy ─────────────────────────────────────────────
	// The admin mount above takes priority for /functions/v1/admin/* because
	// chi's radix tree matches the longer (more specific) prefix first.
	// In local/self-host dev we mount the proxy whenever a functions directory
	// is configured. This keeps invocation behavior aligned with admin listing.
	if cfg.DenoFunctionsDir != "" {
		denoURL := &url.URL{
			Scheme: "http",
			Host:   "127.0.0.1:" + cfg.DenoPort,
		}
		r.Mount("/functions/v1", http.StripPrefix("/functions/v1",
			proxy.WebSocketProxy(denoURL, proxy.New(denoURL, proxy.ProxyOpts{})),
		))
		logrus.WithField("port", cfg.DenoPort).Info("mux: Deno functions proxy mounted at /functions/v1")
	}

	// ── Realtime ──────────────────────────────────────────────────────────────
	if manifest.RealtimeEnabled {
		hub := realtime.NewHub()
		presenceTrackers := make(map[string]*realtime.PresenceTracker)
		var presenceMu sync.Mutex
		r.Mount("/realtime/v1", realtime.Handler(hub, cfg.ServiceRoleKey, presenceTrackers, &presenceMu))
		logrus.Info("mux: Realtime WebSocket handler mounted at /realtime/v1")
	}

	// ── App ───────────────────────────────────────────────────────────────────
	appMode := firstNonEmpty(manifest.AppMode, cfg.AppMode, "none")
	switch appMode {
	case "static":
		dir := firstNonEmpty(manifest.AppStaticDir, cfg.AppStaticDir)
		if dir != "" {
			r.Mount("/", static.Handler(dir, true))
			logrus.WithField("dir", dir).Info("mux: static app handler mounted")
		}

	case "proxy":
		upstream := firstNonEmpty(manifest.AppUpstream, cfg.AppUpstream)
		if upstream != "" {
			if u, err := url.Parse(upstream); err == nil {
				r.Mount("/", proxy.WebSocketProxy(u, proxy.New(u, proxy.ProxyOpts{})))
				logrus.WithField("upstream", upstream).Info("mux: app proxy mounted")
			}
		}
	}

	// ── Mode middleware ───────────────────────────────────────────────────────
	var handler http.Handler = r

	switch cfg.Mode {
	case "dev":
		handler = modes.DevMiddleware(r, cfg.AppUpstream)
	case "managed":
		if cfg.TenantHMACSecret != "" {
			handler = modes.TenantMiddleware(cfg.TenantHMACSecret, r)
		} else {
			logrus.Warn("mux: managed mode but SUPATYPE_TENANT_HMAC_SECRET is unset — tenant verification disabled")
		}
	}

	return handler
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
