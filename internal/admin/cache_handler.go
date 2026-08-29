package admin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/valkey"
	"github.com/supatype/server/internal/restcache"
	"github.com/supatype/server/internal/utilities"
)

const (
	cacheBodyPreviewMax = 4096
	// maxScanPages bounds one listing request. A tenant with a very large cache
	// is paged through by the cursor rather than held in one response.
	maxScanPages = 20
)

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

func mountCacheRoutes(mux *http.ServeMux, cfg *config.Config, vc valkey.Client) {
	if !vc.Available() {
		mux.HandleFunc("/cache", cacheUnavailable)
		mux.HandleFunc("/cache/", cacheUnavailable)
		return
	}

	mux.HandleFunc("/cache", offeredOnly(cfg, vc, func(w http.ResponseWriter, r *http.Request, prefix string) {
		switch r.Method {
		case http.MethodGet:
			listCacheEntries(w, r, vc, prefix)
		case http.MethodDelete:
			flushCache(w, r, vc, prefix)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}))

	mux.HandleFunc("/cache/entries/", offeredOnly(cfg, vc, func(w http.ResponseWriter, r *http.Request, prefix string) {
		key, ok := entryKey(w, r, prefix)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			getCacheEntry(w, r, vc, key)
		case http.MethodDelete:
			if err := vc.Del(r.Context(), key); err != nil {
				writeErr(w, http.StatusBadGateway, err.Error())
				return
			}
			utilities.WriteJSON(w, http.StatusOK, map[string]string{"deleted": key})
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}))
}

// offeredOnly refuses a tenant that does not have the cache, and resolves the
// key prefix its entries live under.
//
// Both cache routes opened with the same two steps; the prefix is what keeps
// one tenant's admin API from reaching another's entries.
func offeredOnly(cfg *config.Config, vc valkey.Client, next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !restcache.ServerCacheOffered(r.Context(), cfg, vc, r) {
			cacheNotOffered(w, r)
			return
		}
		next(w, r, tenantCachePrefix(cfg, r))
	}
}

// entryKey decodes the key from the path and refuses one outside this tenant.
func entryKey(w http.ResponseWriter, r *http.Request, prefix string) (string, bool) {
	// r.URL.Path is already decoded by net/http, so the segment is the base64
	// the client sent. Unescaping it again would decode a second time: a key
	// containing the literal characters %41 would silently become A.
	encoded := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/cache/entries/"))
	if encoded == "" {
		writeErr(w, http.StatusBadRequest, "key required")
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid key encoding")
		return "", false
	}
	key := string(raw)
	if !strings.HasPrefix(key, prefix) {
		writeErr(w, http.StatusBadRequest, "key out of tenant scope")
		return "", false
	}
	return key, true
}

func cacheUnavailable(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusServiceUnavailable, "valkey not configured")
}

func cacheNotOffered(w http.ResponseWriter, _ *http.Request) {
	utilities.WriteJSON(w, http.StatusForbidden, map[string]string{
		"error":   "rest_cache_not_available",
		"message": "Server-side REST caching is included on paid Cloud plans and self-host.",
	})
}

func tenantCachePrefix(cfg *config.Config, r *http.Request) string {
	// TenantRef falls back to "local" itself, so there is nothing left to
	// default here.
	return restcache.RestKeyPrefix(restcache.TenantRef(r, cfg.ManagedProjectRef))
}

func listCacheEntries(w http.ResponseWriter, r *http.Request, vc valkey.Client, prefix string) {
	tableFilter := strings.TrimSpace(r.URL.Query().Get("table"))
	cursor, _ := strconv.ParseUint(r.URL.Query().Get("cursor"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	entries := make([]cacheEntrySummary, 0)
	for page := 0; page < maxScanPages && len(entries) < limit; page++ {
		keys, next, err := vc.ScanPage(r.Context(), cursor, prefix+"*", limit*2)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		cursor = next

		for _, key := range keys {
			summary, ok, err := summarizeKey(r, vc, key, tableFilter)
			if err != nil {
				writeErr(w, http.StatusBadGateway, err.Error())
				return
			}
			if ok {
				entries = append(entries, summary)
			}
			if len(entries) >= limit {
				break
			}
		}
		// A zero cursor means the scan is complete, and it is also what the
		// caller is handed to mean "no more pages".
		if cursor == 0 {
			break
		}
	}

	utilities.WriteJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"cursor":  strconv.FormatUint(cursor, 10),
	})
}

// summarizeKey describes one entry, or reports that it is not one to list.
//
// An entry that vanished between the scan and the read, and one that will not
// decode, are both skipped rather than failing the listing: neither is a reason
// the operator cannot see the rest of their cache.
func summarizeKey(r *http.Request, vc valkey.Client, key, tableFilter string) (cacheEntrySummary, bool, error) {
	raw, err := vc.GetBytes(r.Context(), key)
	if err != nil {
		return cacheEntrySummary{}, false, err
	}
	if raw == nil {
		return cacheEntrySummary{}, false, nil
	}
	entry, err := restcache.DecodeEntry(raw)
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
	return summaryOf(key, entry, ttl, len(raw)), true, nil
}

func summaryOf(key string, entry restcache.Entry, ttl, size int) cacheEntrySummary {
	return cacheEntrySummary{
		Key:        key,
		Table:      entry.Table,
		Scope:      entry.Scope,
		Method:     entry.Method,
		Path:       entry.Path,
		RawQuery:   entry.RawQuery,
		TTLSeconds: ttl,
		SizeBytes:  size,
		CachedAt:   entry.CachedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func getCacheEntry(w http.ResponseWriter, r *http.Request, vc valkey.Client, key string) {
	raw, err := vc.GetBytes(r.Context(), key)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if raw == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	entry, err := restcache.DecodeEntry(raw)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "corrupt entry")
		return
	}

	// The TTL is decoration on a detail view: an entry that is readable but
	// whose expiry is not is still worth showing.
	ttl, _ := vc.TTLSeconds(r.Context(), key)
	detail := cacheEntryDetail{
		cacheEntrySummary: summaryOf(key, entry, ttl, len(raw)),
		StatusCode:        entry.StatusCode,
		ContentType:       entry.ContentType,
	}

	preview := entry.Body
	if len(preview) > cacheBodyPreviewMax {
		preview = preview[:cacheBodyPreviewMax]
	}
	detail.BodyPreview = string(preview)
	if json.Valid(entry.Body) {
		detail.BodyJSON = json.RawMessage(entry.Body)
	}
	utilities.WriteJSON(w, http.StatusOK, detail)
}

func flushCache(w http.ResponseWriter, r *http.Request, vc valkey.Client, prefix string) {
	tableFilter := strings.TrimSpace(r.URL.Query().Get("table"))
	var cursor uint64
	for {
		keys, next, err := vc.ScanPage(r.Context(), cursor, prefix+"*", 100)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}

		toDelete := keys
		if tableFilter != "" {
			toDelete = keysForTable(r, vc, keys, tableFilter)
		}
		if len(toDelete) > 0 {
			if err := vc.Del(r.Context(), toDelete...); err != nil {
				writeErr(w, http.StatusBadGateway, err.Error())
				return
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	utilities.WriteJSON(w, http.StatusOK, map[string]string{"flushed": "ok"})
}

// keysForTable narrows a page of keys to the entries for one table.
//
// A key that cannot be read or decoded is left alone: deleting an entry without
// knowing which table it belongs to would flush more than was asked for.
func keysForTable(r *http.Request, vc valkey.Client, keys []string, table string) []string {
	var matched []string
	for _, key := range keys {
		raw, err := vc.GetBytes(r.Context(), key)
		if err != nil || raw == nil {
			continue
		}
		entry, err := restcache.DecodeEntry(raw)
		if err != nil {
			continue
		}
		if entry.Table == table {
			matched = append(matched, key)
		}
	}
	return matched
}

// CacheKeyParam returns a URL-safe admin path segment for a Valkey key.
func CacheKeyParam(key string) string {
	return url.PathEscape(base64.RawURLEncoding.EncodeToString([]byte(key)))
}
