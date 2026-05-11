package modes

import (
	"net/http"
	"slices"
	"strings"

	"github.com/supatype/auth/internal/proxy"
)

const (
	defaultCORSMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD"
	defaultCORSHeaders = "Authorization, Content-Type, X-Client-Info, Apikey, Prefer, Range, X-Supatype-Tenant, X-Supatype-Tenant-Sig"
)

// ParseCSV splits a comma-separated string into trimmed non-empty fields.
func ParseCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// unionCORSOrigins returns env origins first, then manifest origins, de-duplicated in order.
func unionCORSOrigins(env []string, manifest []string) []string {
	seen := make(map[string]struct{}, len(env)+len(manifest))
	out := make([]string, 0, len(env)+len(manifest))
	for _, list := range [][]string{env, manifest} {
		for _, v := range list {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

// ManagedCORSMiddleware wraps next with an Origin allowlist built from corsEnv (SUPATYPE_CORS_ALLOW_ORIGINS)
// and the per-request route manifest (cors_allowed_origins). Runs outside TenantMiddleware so browsers
// can complete OPTIONS preflight without HMAC headers.
func ManagedCORSMiddleware(corsEnv string, manifestFor func(*http.Request) *proxy.RouteManifest, next http.Handler) http.Handler {
	envOrigins := ParseCSV(corsEnv)
	return AllowlistCORSMiddleware(func(req *http.Request) []string {
		var extra []string
		if manifestFor != nil {
			if m := manifestFor(req); m != nil {
				extra = m.CorsAllowedOrigins
			}
		}
		return unionCORSOrigins(envOrigins, extra)
	}, next)
}

// AllowlistCORSMiddleware reflects Access-Control-Allow-Origin only when the request Origin is in
// the list returned by getOrigins (non-empty). If getOrigins returns nil/empty, next is invoked
// with no CORS headers (same-origin only from the browser's perspective).
func AllowlistCORSMiddleware(getOrigins func(*http.Request) []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed := getOrigins(r)
		if len(allowed) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !slices.Contains(allowed, origin) {
			next.ServeHTTP(w, r)
			return
		}

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", defaultCORSMethods)
			if reqHdr := r.Header.Get("Access-Control-Request-Headers"); reqHdr != "" {
				h.Set("Access-Control-Allow-Headers", reqHdr)
			} else {
				h.Set("Access-Control-Allow-Headers", defaultCORSHeaders)
			}
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(&corsResponseWriter{ResponseWriter: w, origin: origin}, r)
	})
}

type corsResponseWriter struct {
	http.ResponseWriter
	origin   string
	acHeader bool
}

func (cw *corsResponseWriter) WriteHeader(code int) {
	cw.ensureCORSHeader()
	cw.ResponseWriter.WriteHeader(code)
}

func (cw *corsResponseWriter) Write(b []byte) (int, error) {
	cw.ensureCORSHeader()
	return cw.ResponseWriter.Write(b)
}

func (cw *corsResponseWriter) ensureCORSHeader() {
	if cw.acHeader {
		return
	}
	cw.ResponseWriter.Header().Set("Access-Control-Allow-Origin", cw.origin)
	cw.ResponseWriter.Header().Add("Vary", "Origin")
	cw.acHeader = true
}
