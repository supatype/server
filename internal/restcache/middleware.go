package restcache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/valkey"
)

// statusHeader tells the caller what the cache did: HIT, MISS, or BYPASS when
// caching was asked for and could not be done.
const statusHeader = "X-Supatype-Cache-Status"

// IdentityScoped answers which tables have a read rule that varies by caller.
//
// Taken as a value rather than reached for through the package that computes
// it, so the check that keeps one caller's rows out of another's cache entry
// can be stated in a test rather than arranged in a schema.
type IdentityScoped func(ctx context.Context) (map[string]bool, bool)

// Deps is what the cache needs to decide and to store.
type Deps struct {
	Store          apiconfig.Store
	Cache          valkey.Client
	Config         *config.Config
	SchemaFor      func(*http.Request) string
	MaxRowsFor     func(*http.Request) string
	IdentityScoped IdentityScoped
}

// plan is what the cache decided to do with one request.
type plan struct {
	key   string
	ttl   int
	table string
	scope string
}

// Middleware caches opt-in GET/HEAD /rest/v1 responses in Valkey.
func Middleware(d Deps, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p, cacheable, bypassed := d.planFor(req)
		if !cacheable {
			if bypassed {
				w.Header().Set(statusHeader, "BYPASS")
			}
			next.ServeHTTP(w, req)
			return
		}

		served, healthy := tryServeHit(req.Context(), d.Cache, w, req, p.key, p.ttl)
		if healthy && served {
			return
		}
		d.serveAndStore(w, req, next, p, healthy)
	})
}

// planFor decides whether this request may be served from, or added to, the
// cache.
//
// The second result is false when it may not. The third says the caller asked
// for caching and should be told it did not happen, which is not the same as a
// request that never wanted it.
func (d Deps) planFor(req *http.Request) (p plan, cacheable, bypassed bool) {
	if !cacheableMethod(req.Method) {
		return plan{}, false, false
	}

	clientMaxAge := ParseClientMaxAge(req.Header)
	asked := clientMaxAge > 0

	if !ServerCacheOffered(req.Context(), d.Config, d.Cache, req) {
		return plan{}, false, asked
	}

	restCfg, err := d.Store.Get(req.Context())
	if err != nil {
		logrus.WithError(err).Warn("restcache: api config read failed — bypass")
		return plan{}, false, false
	}

	table := RestTableFromPath(req.URL.Path)
	tableCfg, tableAllowed := restCfg.Rest.TableCacheAllowed(table)
	ttl := EffectiveTTL(restCfg.Rest.CacheMaxTTL, clientMaxAge, tableAllowed)
	if ttl <= 0 || d.Cache == nil {
		return plan{}, false, asked && d.Cache == nil
	}

	usePublic := d.publicScope(req, table, tableCfg.AllowPublic)
	return plan{
		key: BuildKey(keyParts{
			Tenant:   TenantRef(req, d.Config.ManagedProjectRef),
			Schema:   d.SchemaFor(req),
			Method:   req.Method,
			Path:     req.URL.Path,
			RawQuery: req.URL.RawQuery,
			AuthHash: IdentityForCache(req, []byte(d.Config.JWTSecret), usePublic),
			Accept:   req.Header.Get("Accept"),
			Range:    req.Header.Get("Range"),
			Language: req.Header.Get("Accept-Language"),
			MaxRows:  d.MaxRowsFor(req),
		}),
		ttl:   ttl,
		table: table,
		scope: cacheScopeLabel(usePublic),
	}, true, false
}

// publicScope decides whether one cache entry may be shared across callers.
//
// A public-scoped entry is keyed "global", so every caller shares one response.
// That is only safe when the table's read rule gives every caller the same
// answer. `allow_public` on a table whose rule depends on the caller would
// serve the first requester's rows to everyone until the TTL expires, which is
// a cross-user exposure through a config flag.
//
// Downgraded to per-identity scope rather than refused: the request still
// returns correct data, just with a less-shared cache entry. A misconfiguration
// should not become an outage.
func (d Deps) publicScope(req *http.Request, table string, tableAllowsPublic bool) bool {
	if !ParseClientPublic(req.Header) {
		return false
	}
	if !tableAllowsPublic {
		logrus.WithField("table", table).Debug("restcache: public flag ignored — allow_public false")
		return false
	}
	if !d.publicScopeSafe(req.Context(), table) {
		logrus.WithField("table", table).
			Warn("restcache: allow_public ignored — this table's read rule depends on the caller, " +
				"so a shared cache entry would serve one caller's rows to another")
		return false
	}
	return true
}

// publicScopeSafe reports whether responses for a table may share one entry.
//
// Fails safe: when the classification cannot be read at all, no table is
// treated as publicly cacheable. "We could not check" is not a reason to start
// sharing responses between users.
func (d Deps) publicScopeSafe(ctx context.Context, table string) bool {
	if d.IdentityScoped == nil {
		return false
	}
	tables, ok := d.IdentityScoped(ctx)
	if !ok {
		return false
	}
	identityDependent, known := tables[table]
	if !known {
		// A table the schema does not describe — a view, or something created
		// outside the schema. Its rules are unknown, so it does not get shared
		// caching.
		return false
	}
	return !identityDependent
}

// serveAndStore runs the upstream, relays its answer, and caches it if it may
// be cached.
//
// healthy is whether the cache could be read. It carries into the reported
// status because MISS and BYPASS say different things: MISS means the answer
// was not there, BYPASS means the cache did not work. A read that failed is
// the second even if the write that follows it succeeds.
func (d Deps) serveAndStore(w http.ResponseWriter, req *http.Request, next http.Handler, p plan, healthy bool) {
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, req)

	for name, values := range rec.Header() {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}

	label := "MISS"
	if !healthy {
		label = "BYPASS"
	}
	if storable(rec) {
		entry := Entry{
			StatusCode:  rec.Code,
			ContentType: rec.Header().Get("Content-Type"),
			Body:        rec.Body.Bytes(),
			CachedAt:    time.Now().UTC(),
			Table:       p.table,
			Scope:       p.scope,
			Method:      req.Method,
			Path:        req.URL.Path,
			RawQuery:    req.URL.RawQuery,
		}
		if err := storeEntry(req.Context(), d.Cache, p.key, entry, p.ttl); err != nil {
			logrus.WithError(err).Warn("restcache: valkey SET failed")
			label = "BYPASS"
		}
		w.Header().Set("Vary", VaryHeader(req))
	}

	w.Header().Set(statusHeader, label)
	w.WriteHeader(rec.Code)
	if req.Method != http.MethodHead {
		_, _ = w.Write(rec.Body.Bytes())
	}
}

// storable reports whether a recorded response may be kept.
//
// A Set-Cookie makes the response caller-specific whatever the table config
// says, and a body over the cap is not worth the memory.
func storable(rec *httptest.ResponseRecorder) bool {
	return cacheableStatus(rec.Code) &&
		rec.Header().Get("Set-Cookie") == "" &&
		rec.Body.Len() <= maxCacheBodyBytes
}

// tryServeHit answers from the cache if it can.
//
// served says the response has been written. ok says the cache itself was
// usable: a Valkey that will not answer, or an entry that will not decode, is
// a bypass rather than a miss, because a miss implies the cache is working.
func tryServeHit(ctx context.Context, vk valkey.Client, w http.ResponseWriter, req *http.Request, key string, ttl int) (served, ok bool) {
	raw, err := vk.GetBytes(ctx, key)
	if err != nil {
		logrus.WithError(err).Warn("restcache: valkey GET failed")
		return false, false
	}
	if raw == nil {
		return false, true
	}
	entry, err := DecodeEntry(raw)
	if err != nil {
		logrus.WithError(err).Warn("restcache: corrupt cache entry — bypass")
		return false, false
	}
	age := int(time.Since(entry.CachedAt).Seconds())
	if age >= ttl {
		return false, true
	}

	if entry.ContentType != "" {
		w.Header().Set("Content-Type", entry.ContentType)
	}
	w.Header().Set(statusHeader, "HIT")
	w.Header().Set("Age", strconv.Itoa(age))
	w.Header().Set("Vary", VaryHeader(req))
	w.WriteHeader(entry.StatusCode)
	if req.Method != http.MethodHead && len(entry.Body) > 0 {
		_, _ = w.Write(entry.Body)
	}
	return true, true
}

func storeEntry(ctx context.Context, vk valkey.Client, key string, entry Entry, ttl int) error {
	raw, err := marshalEntry(entry)
	if err != nil {
		return err
	}
	return vk.SetBytes(ctx, key, raw, ttl)
}
