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
	"github.com/supatype/server/internal/studiobootstrap"
)

// Middleware caches opt-in GET/HEAD /rest/v1 responses in Valkey.
func Middleware(
	store apiconfig.Store,
	vk valkey.Client,
	cfg *config.Config,
	schemaFor func(*http.Request) string,
	maxRowsFor func(*http.Request) string,
	next http.Handler,
) http.Handler {
	jwtSecret := []byte(cfg.JWTSecret)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !cacheableMethod(req.Method) {
			next.ServeHTTP(w, req)
			return
		}

		if !ServerCacheOffered(req.Context(), cfg, vk, req) {
			if ParseClientMaxAge(req.Header) > 0 {
				w.Header().Set("X-Supatype-Cache-Status", "BYPASS")
			}
			next.ServeHTTP(w, req)
			return
		}

		restCfg, err := store.Get(req.Context())
		if err != nil {
			logrus.WithError(err).Warn("restcache: api config read failed — bypass")
			next.ServeHTTP(w, req)
			return
		}

		clientMaxAge := ParseClientMaxAge(req.Header)
		table := RestTableFromPath(req.URL.Path)
		tableCfg, tableAllowed := restCfg.Rest.TableCacheAllowed(table)
		ttl := EffectiveTTL(restCfg.Rest.CacheMaxTTL, clientMaxAge, tableAllowed)
		if ttl <= 0 || vk == nil {
			if clientMaxAge > 0 && vk == nil {
				w.Header().Set("X-Supatype-Cache-Status", "BYPASS")
			}
			next.ServeHTTP(w, req)
			return
		}

		clientPublic := ParseClientPublic(req.Header)
		usePublic := clientPublic && tableCfg.AllowPublic
		if clientPublic && !tableCfg.AllowPublic {
			logrus.WithField("table", table).Debug("restcache: public flag ignored — allow_public false")
		}
		// A public-scoped entry is keyed "global", so every caller shares one
		// response. That is only safe when the table's read rule gives every caller
		// the same answer. `allow_public` on a table whose rule depends on the
		// caller would serve the first requester's rows to everyone until the TTL
		// expires, which is a cross-user exposure through a config flag.
		//
		// Downgraded to per-identity scope rather than refused: the request still
		// returns correct data, just with a less-shared cache entry. A
		// misconfiguration should not become an outage.
		if usePublic && !publicScopeSafe(req.Context(), table) {
			usePublic = false
			logrus.WithField("table", table).
				Warn("restcache: allow_public ignored — this table's read rule depends on the caller, " +
					"so a shared cache entry would serve one caller's rows to another")
		}
		identity := IdentityForCache(req, jwtSecret, usePublic)
		scope := cacheScopeLabel(usePublic)

		key := BuildKey(keyParts{
			Tenant:   TenantRef(req, cfg.ManagedProjectRef),
			Schema:   schemaFor(req),
			Method:   req.Method,
			Path:     req.URL.Path,
			RawQuery: req.URL.RawQuery,
			AuthHash: identity,
			Accept:   req.Header.Get("Accept"),
			Range:    req.Header.Get("Range"),
			Language: req.Header.Get("Accept-Language"),
			MaxRows:  maxRowsFor(req),
		})

		served, cacheOK := tryServeHit(req.Context(), vk, w, req, key, ttl)
		if cacheOK && served {
			return
		}
		if !cacheOK {
			w.Header().Set("X-Supatype-Cache-Status", "BYPASS")
		}

		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, req)

		for k, vals := range rec.Header() {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}

		status := rec.Code
		if !cacheableStatus(status) {
			w.Header().Set("X-Supatype-Cache-Status", "MISS")
			w.WriteHeader(status)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		if rec.Header().Get("Set-Cookie") != "" {
			w.Header().Set("X-Supatype-Cache-Status", "MISS")
			w.WriteHeader(status)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		body := rec.Body.Bytes()
		if len(body) > maxCacheBodyBytes {
			w.Header().Set("X-Supatype-Cache-Status", "MISS")
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}

		entry := Entry{
			StatusCode:  status,
			ContentType: rec.Header().Get("Content-Type"),
			Body:        append([]byte(nil), body...),
			CachedAt:    time.Now().UTC(),
			Table:       table,
			Scope:       scope,
			Method:      req.Method,
			Path:        req.URL.Path,
			RawQuery:    req.URL.RawQuery,
		}
		if err := storeEntry(req.Context(), vk, key, entry, ttl); err != nil {
			logrus.WithError(err).Warn("restcache: valkey SET failed")
			w.Header().Set("X-Supatype-Cache-Status", "BYPASS")
		} else {
			w.Header().Set("X-Supatype-Cache-Status", "MISS")
		}
		w.Header().Set("Vary", VaryHeader(req))
		w.WriteHeader(status)
		if req.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	})
}

func tryServeHit(ctx context.Context, vk valkey.Client, w http.ResponseWriter, req *http.Request, key string, ttl int) (served bool, ok bool) {
	raw, err := vk.GetBytes(ctx, key)
	if err != nil {
		logrus.WithError(err).Warn("restcache: valkey GET failed")
		return false, false
	}
	if raw == nil {
		return false, true
	}
	entry, err := decodeEntry(raw)
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
	w.Header().Set("X-Supatype-Cache-Status", "HIT")
	w.Header().Set("Age", strconv.Itoa(age))
	w.Header().Set("Vary", VaryHeader(req))
	w.WriteHeader(entry.StatusCode)
	if req.Method != http.MethodHead && len(entry.Body) > 0 {
		_, _ = w.Write(entry.Body)
	}
	return true, true
}

func storeEntry(ctx context.Context, vk valkey.Client, key string, entry Entry, ttl int) error {
	raw, err := encodeEntry(entry)
	if err != nil {
		return err
	}
	return vk.SetBytes(ctx, key, raw, ttl)
}

// publicScopeSafe reports whether responses for a table may share one cache entry
// across callers.
//
// Fails safe: when the schema classification cannot be read at all, no table is
// treated as publicly cacheable. "We could not check" is not a reason to start
// sharing responses between users.
func publicScopeSafe(ctx context.Context, table string) bool {
	tables, ok := studiobootstrap.IdentityScopedTables(ctx)
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
