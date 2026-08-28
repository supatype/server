package admin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/supatype/server/internal/data/valkey"
	"github.com/supatype/server/internal/restcache"
	"github.com/supatype/server/internal/serverconf"
)

const cacheBodyPreviewMax = 4096

type cacheEntrySummary struct {
	Key        string `json:"key"`
	Table      string `json:"table,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
	RawQuery   string `json:"raw_query,omitempty"`
	TTLSeconds int    `json:"ttl_seconds"`
	SizeBytes  int    `json:"size_bytes"`
	CachedAt   string `json:"cached_at,omitempty"`
}

type cacheEntryDetail struct {
	cacheEntrySummary
	StatusCode  int             `json:"status_code"`
	ContentType string          `json:"content_type,omitempty"`
	BodyPreview string          `json:"body_preview,omitempty"`
	BodyJSON    json.RawMessage `json:"body_json,omitempty"`
}

func mountCacheRoutes(mux *http.ServeMux, cfg *serverconf.ServerConfig, vc *valkey.Client) {
	if vc == nil {
		mux.HandleFunc("/cache", cacheUnavailable)
		mux.HandleFunc("/cache/", cacheUnavailable)
		return
	}

	mux.HandleFunc("/cache", func(w http.ResponseWriter, r *http.Request) {
		if !restcache.ServerCacheOffered(r.Context(), cfg, vc, r) {
			cacheNotOffered(w, r)
			return
		}
		prefix := tenantCachePrefix(cfg, r)
		switch r.Method {
		case http.MethodGet:
			listCacheEntries(w, r, vc, prefix)
		case http.MethodDelete:
			flushCache(w, r, vc, prefix, "")
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/cache/entries/", func(w http.ResponseWriter, r *http.Request) {
		if !restcache.ServerCacheOffered(r.Context(), cfg, vc, r) {
			cacheNotOffered(w, r)
			return
		}
		prefix := tenantCachePrefix(cfg, r)
		keyEnc := strings.TrimPrefix(r.URL.Path, "/cache/entries/")
		keyEnc = strings.TrimSpace(keyEnc)
		if keyEnc == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key required"})
			return
		}
		keyEnc, err := url.PathUnescape(keyEnc)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key encoding"})
			return
		}
		keyBytes, err := base64.RawURLEncoding.DecodeString(keyEnc)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key encoding"})
			return
		}
		key := string(keyBytes)
		if !strings.HasPrefix(key, prefix) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key out of tenant scope"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			getCacheEntry(w, r, vc, key)
		case http.MethodDelete:
			if err := vc.Del(r.Context(), key); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"deleted": key})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})
}

func cacheUnavailable(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "valkey not configured"})
}

func cacheNotOffered(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":   "rest_cache_not_available",
		"message": "Server-side REST caching is included on paid Cloud plans and self-host.",
	})
}

func tenantCachePrefix(cfg *serverconf.ServerConfig, r *http.Request) string {
	ref := restcache.TenantRef(r, cfg.ManagedProjectRef)
	if ref == "" {
		ref = "local"
	}
	return restcache.RestKeyPrefix(ref)
}

func listCacheEntries(w http.ResponseWriter, r *http.Request, vc *valkey.Client, prefix string) {
	tableFilter := strings.TrimSpace(r.URL.Query().Get("table"))
	cursor, _ := strconv.ParseUint(r.URL.Query().Get("cursor"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	pattern := prefix + "*"
	var entries = make([]cacheEntrySummary, 0)
	var next uint64
	scanned := 0
	const maxScanPages = 20

	for page := 0; page < maxScanPages && len(entries) < limit; page++ {
		keys, nextCursor, err := vc.ScanPage(r.Context(), cursor, pattern, limit*2)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		cursor = nextCursor
		for _, key := range keys {
			scanned++
			summary, ok, err := summarizeKey(r, vc, key, tableFilter)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			if !ok {
				continue
			}
			entries = append(entries, summary)
			if len(entries) >= limit {
				break
			}
		}
		if cursor == 0 {
			next = 0
			break
		}
		next = cursor
		if len(entries) >= limit {
			break
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"cursor":  strconv.FormatUint(next, 10),
	})
}

func summarizeKey(r *http.Request, vc *valkey.Client, key, tableFilter string) (cacheEntrySummary, bool, error) {
	raw, err := vc.GetBytes(r.Context(), key)
	if err != nil {
		return cacheEntrySummary{}, false, err
	}
	if raw == nil {
		return cacheEntrySummary{}, false, nil
	}
	entry, err := restcache.DecodeEntryForAdmin(raw)
	if err != nil {
		return cacheEntrySummary{}, false, nil
	}
	if tableFilter != "" && entry.Table != tableFilter {
		return cacheEntrySummary{}, false, nil
	}
	ttl, err := vc.TTLSeconds(r.Context(), key)
	if err != nil {
		return cacheEntrySummary{}, false, err
	}
	return cacheEntrySummary{
		Key:        key,
		Table:      entry.Table,
		Scope:      entry.Scope,
		Method:     entry.Method,
		Path:       entry.Path,
		RawQuery:   entry.RawQuery,
		TTLSeconds: ttl,
		SizeBytes:  len(raw),
		CachedAt:   entry.CachedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}, true, nil
}

func getCacheEntry(w http.ResponseWriter, r *http.Request, vc *valkey.Client, key string) {
	raw, err := vc.GetBytes(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if raw == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	entry, err := restcache.DecodeEntryForAdmin(raw)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "corrupt entry"})
		return
	}
	ttl, _ := vc.TTLSeconds(r.Context(), key)
	detail := cacheEntryDetail{
		cacheEntrySummary: cacheEntrySummary{
			Key:        key,
			Table:      entry.Table,
			Scope:      entry.Scope,
			Method:     entry.Method,
			Path:       entry.Path,
			RawQuery:   entry.RawQuery,
			TTLSeconds: ttl,
			SizeBytes:  len(raw),
			CachedAt:   entry.CachedAt.UTC().Format("2006-01-02T15:04:05Z"),
		},
		StatusCode:  entry.StatusCode,
		ContentType: entry.ContentType,
	}
	preview := entry.Body
	if len(preview) > cacheBodyPreviewMax {
		preview = preview[:cacheBodyPreviewMax]
	}
	detail.BodyPreview = string(preview)
	if json.Valid(entry.Body) {
		detail.BodyJSON = json.RawMessage(entry.Body)
	}
	writeJSON(w, http.StatusOK, detail)
}

func flushCache(w http.ResponseWriter, r *http.Request, vc *valkey.Client, prefix, _ string) {
	tableFilter := strings.TrimSpace(r.URL.Query().Get("table"))
	var cursor uint64
	for {
		keys, next, err := vc.ScanPage(r.Context(), cursor, prefix+"*", 100)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		var toDelete []string
		for _, key := range keys {
			if tableFilter == "" {
				toDelete = append(toDelete, key)
				continue
			}
			raw, err := vc.GetBytes(r.Context(), key)
			if err != nil || raw == nil {
				continue
			}
			entry, err := restcache.DecodeEntryForAdmin(raw)
			if err != nil {
				continue
			}
			if entry.Table == tableFilter {
				toDelete = append(toDelete, key)
			}
		}
		if len(toDelete) > 0 {
			if err := vc.Del(r.Context(), toDelete...); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	writeJSON(w, http.StatusOK, map[string]string{"flushed": "ok"})
}

func encodeCacheKey(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

// CacheKeyParam returns URL-safe admin path segment for a Valkey key.
func CacheKeyParam(key string) string {
	return url.PathEscape(encodeCacheKey(key))
}
