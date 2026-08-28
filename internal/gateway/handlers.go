package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/admin"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/functions"
	"github.com/supatype/server/internal/maskedfields"
	"github.com/supatype/server/internal/modelhooks"
	"github.com/supatype/server/internal/objstore"
	"github.com/supatype/server/internal/platformproxy"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/restcache"
	"github.com/supatype/server/internal/sqlrunner"
	"github.com/supatype/server/internal/static"
	"github.com/supatype/server/internal/studioauth"
	"github.com/supatype/server/internal/utilities"
)

// One builder per mounted service. Each returns the handler for its route and
// nothing else: no mounting, no ordering, no logging of its own.

// itoa keeps the header-building code free of strconv noise.
func itoa(n int) string { return strconv.Itoa(n) }

// upstreamProxy is the proxy every mount uses, with the shared round-trip cap
// that stops a wedged upstream hanging requests indefinitely.
func upstreamProxy(u *url.URL, opts ...func(*proxy.ProxyOpts)) http.Handler {
	o := proxy.ProxyOpts{RequestTimeout: defaultUpstreamHTTPTimeout}
	for _, apply := range opts {
		apply(&o)
	}
	return proxy.New(u, o)
}

// withHeaders adds request headers to an upstream proxy.
func withHeaders(fn func(*http.Request) map[string]string) func(*proxy.ProxyOpts) {
	return func(o *proxy.ProxyOpts) { o.HeaderFunc = fn }
}

// badGateway is the answer when an upstream cannot be worked out at all.
func badGateway(w http.ResponseWriter, message string) {
	http.Error(w, message, http.StatusBadGateway)
}

func buildAdminAPI(d *Deps) http.Handler {
	return admin.Handler(d.APIStore, d.Config, d.Cache)
}

func buildSQLRunner(d *Deps) http.Handler {
	return sqlrunner.Handler(d.Config, d.AdminPool)
}

func buildStudioMembers(d *Deps) http.Handler { return studioauth.MembersAPI(d.Studio) }
func buildStudioVerify(d *Deps) http.Handler  { return studioauth.VerifyHandler(d.Studio) }
func buildStudioSchema(d *Deps) http.Handler  { return studioauth.SchemaHandler(d.Studio) }
func buildStudioSession(d *Deps) http.Handler { return studioauth.SessionHandler(d.Studio) }

// buildStudioProxy fronts every other service on this router, which is why it
// needs the router itself.
func buildStudioProxy(d *Deps) http.Handler {
	return studioauth.ProxyHandler(d.Router, d.Studio)
}

// buildStudioConfig serves the engine's generated admin config to Studio.
func buildStudioConfig(d *Deps) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := studioauth.ReadAdminConfigFile(d.Config.AdminConfigPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, `{"error":"schema not pushed yet"}`, http.StatusNotFound)
				return
			}
			http.Error(w, `{"error":"failed to read admin config"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	return studioauth.RequireAdmin(d.Studio, inner)
}

// buildREST is the PostgREST mount.
//
// The layering is deliberate and each layer is where it is for a reason. The
// masked-field header sits outside the response cache so it is present on hits
// as well as misses, which is safe because it describes the schema's
// restrictions and not one caller's verdicts. Model hooks sit inside the cache
// and immediately outside the proxy: a cached read never reaches them, and a
// write reaches them before PostgREST sees it, which is the only place a hook
// can still reject or rewrite one.
func buildREST(d *Deps) http.Handler {
	return maskedfields.Middleware(d.MaskedFields,
		restcache.Middleware(
			restcache.Deps{
				Store:          d.APIStore,
				Cache:          d.Cache,
				Config:         d.Config,
				SchemaFor:      d.RestSchema,
				MaxRowsFor:     d.RestMaxRows,
				IdentityScoped: d.IdentityScopedTables,
			},
			d.Hooks(restProxyHandler(d)),
		),
	)
}

// restProxyHandler forwards to PostgREST, telling it which schema to use and
// what row cap to apply.
func restProxyHandler(d *Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u, err := url.Parse(PostgRESTUpstream(d.ManifestFor(req), d.Config))
		if err != nil {
			badGateway(w, "bad gateway")
			return
		}
		upstreamProxy(u, withHeaders(func(req *http.Request) map[string]string {
			headers := map[string]string{"X-Pg-Schema": d.RestSchema(req)}
			if maxRows := d.RestMaxRows(req); maxRows != "" {
				headers["Prefer"] = fmt.Sprintf("max-rows=%s", maxRows)
			}
			return headers
		})).ServeHTTP(w, req)
	})
}

// buildGraphQL answers /graphql/v1 by calling the graphql_public.graphql RPC on
// PostgREST, so the path is rewritten rather than forwarded.
//
// It authenticates upstream as the service role and passes the caller's own
// token along in a separate header, so the RPC can act on the caller's behalf
// without the gateway's privilege being confused with theirs.
func buildGraphQL(d *Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u, err := url.Parse(GraphQLUpstream(d.ManifestFor(req), d.Config))
		if err != nil {
			badGateway(w, "bad gateway")
			return
		}
		rpc := rewriteToGraphQLRPC(req)
		endUserAuth := strings.TrimSpace(req.Header.Get("Authorization"))
		upstreamProxy(u, withHeaders(func(*http.Request) map[string]string {
			return graphQLHeaders(d.Config.ServiceRoleKey, endUserAuth)
		})).ServeHTTP(w, rpc)
	})
}

// rewriteToGraphQLRPC points a copy of the request at the RPC endpoint.
func rewriteToGraphQLRPC(req *http.Request) *http.Request {
	rpc := req.Clone(req.Context())
	if rpc.URL == nil {
		rpc.URL = &url.URL{}
	} else {
		cloned := *rpc.URL
		rpc.URL = &cloned
	}
	rpc.URL.Path = "/rpc/graphql"
	rpc.URL.RawPath = ""
	rpc.Header.Set("Content-Profile", "graphql_public")
	return rpc
}

// graphQLHeaders authenticates as the service role and forwards the caller's
// own token separately. An unconfigured service role sends neither.
func graphQLHeaders(serviceRoleKey, endUserAuth string) map[string]string {
	headers := map[string]string{}
	key := strings.TrimSpace(serviceRoleKey)
	if key == "" {
		return headers
	}
	if strings.HasPrefix(strings.ToLower(key), "bearer ") {
		headers["Authorization"] = key
	} else {
		headers["Authorization"] = "Bearer " + key
	}
	if endUserAuth != "" {
		headers["X-Supatype-End-User-Authorization"] = endUserAuth
	}
	return headers
}

// buildStorage serves objects from disk when a local store is configured, and
// otherwise proxies to the storage service.
func buildStorage(d *Deps) http.Handler {
	if d.Config.StorageProvider == "local" && d.Config.StoragePath != "" {
		logrus.WithField("path", d.Config.StoragePath).
			Info("mux: local storage handler mounted at /storage/v1")
		return objstore.Handler(d.Config.StoragePath, d.Config.JWTSecret)
	}
	logrus.Info("mux: Storage proxy mounted at /storage/v1")
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		storageURL := StorageUpstream(d.ManifestFor(req), d.Config)
		if storageURL == "" {
			badGateway(w, "storage not configured")
			return
		}
		u, err := url.Parse(storageURL)
		if err != nil {
			badGateway(w, "bad gateway")
			return
		}
		upstreamProxy(u).ServeHTTP(w, req)
	})
}

func buildFunctionsAdmin(d *Deps) http.Handler {
	logrus.WithField("dir", d.Config.DenoFunctionsDir).
		Info("mux: Functions admin handler mounted at /functions/v1/admin")
	return functions.Handler(d.Config, d.Config.DenoFunctionsDir, d.Deno)
}

func buildFunctions(d *Deps) http.Handler {
	return functionsInvocationProxy(d.Config, d.ManifestFor, d.Deno != nil)
}

func buildRealtime(d *Deps) http.Handler {
	return realtimeInvocationProxy(d.Config, d.ManifestFor)
}

// buildPlatform fronts the self-host control plane. An unusable upstream skips
// the mount rather than stopping the process; the configured default always
// parses, so this only fires on a mistyped value.
func buildPlatform(d *Deps) http.Handler {
	handler, err := platformproxy.Handler(d.Config)
	if err != nil {
		logrus.WithError(err).Error("mux: Platform control plane proxy not mounted")
		return nil
	}
	return handler
}

// buildVite proxies Vite's HMR socket in dev mode. With no Vite URL configured
// it falls back to the app upstream, but only when that upstream is not already
// serving the catch-all, since proxying it twice would be pointless.
func buildVite(d *Deps) http.Handler {
	target := ViteDevURL(d.Baseline, d.Config)
	if target == "" && AppMode(d.Baseline, d.Config) != "proxy" {
		target = strings.TrimSpace(d.Config.AppUpstream)
	}
	if target == "" {
		return nil
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	logrus.WithField("vite_dev_url", target).Info("mux: Vite HMR proxy mounted at /_vite")
	return proxy.WebSocketProxy(u, upstreamProxy(u))
}

// buildApp answers everything not claimed by a service above: nothing, a
// directory, or another server.
func buildApp(d *Deps) http.Handler {
	switch AppMode(d.Baseline, d.Config) {
	case "static":
		dir := AppStaticDir(d.Baseline, d.Config)
		if dir == "" {
			return nil
		}
		logrus.WithField("dir", dir).Info("mux: static app handler mounted")
		return static.Handler(dir, d.Config.AppSPAFallback, staticCacheOpts(d.Config, d.Baseline))

	case "proxy":
		upstream := AppUpstream(d.Baseline, d.Config)
		if upstream == "" {
			return nil
		}
		u, err := url.Parse(upstream)
		if err != nil {
			return nil
		}
		logrus.WithField("upstream", upstream).Info("mux: app proxy mounted")
		return proxy.WebSocketProxy(u, upstreamProxy(u))
	}
	return nil
}

// newHookCallback builds the previous() endpoint, which reads the rows a write
// is about to change as the service role. See internal/modelhooks/previous.go
// for why that is the right privilege and what the token pins.
func newHookCallback(d *Deps) *modelhooks.Callback {
	callback, err := modelhooks.NewCallback(
		func(req *http.Request) string { return PostgRESTUpstream(d.ManifestFor(req), d.Config) },
		d.RestSchema,
		d.Config.ServiceRoleKey,
		nil,
	)
	if err != nil {
		logrus.WithError(err).Warn("mux: hook previous() callback unavailable")
		return nil
	}
	return callback
}

// newHookMiddleware runs a project's schema-declared hooks and validators.
func newHookMiddleware(d *Deps) func(http.Handler) http.Handler {
	return modelhooks.Middleware(modelhooks.Options{
		Dispatcher: modelhooks.NewDispatcher(nil, d.Config.JWTSecret),
		Hooks: func(req *http.Request) map[string]modelhooks.TableHooksView {
			m := d.ManifestFor(req)
			if m == nil {
				return nil
			}
			return modelhooks.ViewsFromManifest(m.Hooks)
		},
		Validators: func(req *http.Request) map[string]modelhooks.TableValidatorsView {
			m := d.ManifestFor(req)
			if m == nil {
				return nil
			}
			return modelhooks.ValidatorViewsFromManifest(m.Validators)
		},
		ResolveURL: func(req *http.Request, function string) (string, error) {
			// The request, not nil: a managed server reads the caller's tenant
			// from it, and the hook map already comes from that tenant's config.
			// Resolving the URL from the file manifest instead sent every hooked
			// write to whatever worker the platform happened to be configured
			// with, or to none, which is a 503 on every write to a hooked table.
			return hookUpstreamURL(d.Config, d.ManifestFor(req), function, d.Deno != nil)
		},
		Claims:    modelhooks.ClaimsFromBearer(d.Config.JWTSecret),
		RequestID: func(req *http.Request) string { return utilities.GetRequestID(req.Context()) },
		Callback:  d.HookCallback,
	})
}

func staticCacheOpts(cfg *config.Config, m *proxy.RouteManifest) static.CacheOpts {
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
