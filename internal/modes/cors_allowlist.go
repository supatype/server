package modes

import (
	"net/http"
	"slices"
	"strings"

	"github.com/supatype/server/internal/corsorigins"
	"github.com/supatype/server/internal/proxy"
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

// ManagedCORSMiddleware wraps next with an Origin allowlist built from corsEnv (SUPATYPE_CORS_ALLOW_ORIGINS)
// and the per-request route manifest (cors_allowed_origins), with nothing saved. Runs outside
// TenantMiddleware so browsers can complete OPTIONS preflight without HMAC headers.
func ManagedCORSMiddleware(corsEnv string, manifestFor func(*http.Request) *proxy.RouteManifest, next http.Handler) http.Handler {
	policy := corsorigins.NewPolicy(nil, corsorigins.Defaults{Configured: ParseCSV(corsEnv)})
	return PolicyCORSMiddleware(policy, manifestFor, next)
}

// PolicyCORSMiddleware allows the origins policy has in force for each request: its saved list or
// its default, with the per-request manifest's origins (Cloud's tenant configuration) added to the
// default. The one CORS layer outside dev mode, standalone and managed alike.
func PolicyCORSMiddleware(policy *corsorigins.Policy, manifestFor func(*http.Request) *proxy.RouteManifest, next http.Handler) http.Handler {
	return AllowlistCORSMiddleware(func(req *http.Request) []string {
		return policy.InForce(req.Context(), ManifestOrigins(manifestFor, req)).Origins
	}, next)
}

// ManifestOrigins is the per-request manifest's configured origins, or nil.
func ManifestOrigins(manifestFor func(*http.Request) *proxy.RouteManifest, req *http.Request) []string {
	if manifestFor == nil {
		return nil
	}
	if m := manifestFor(req); m != nil {
		return m.CorsAllowedOrigins
	}
	return nil
}

// AllowlistCORSMiddleware makes the gateway the one place that decides CORS for every request
// behind it. An Origin on the list returned by getOrigins gets Access-Control-Allow-Origin (the
// origin itself, or `*` when the list says `*`); a preflight from it is answered here. Any other
// Origin gets no CORS headers at all: a preflight is refused with 403 before it reaches a handler,
// and every Access-Control-* header a downstream service set is removed from the answer. The auth
// service's own CORS handler answers `*` when nothing is configured, and letting an unlisted origin
// through to it is how a production server came to allow every origin (SHb).
func AllowlistCORSMiddleware(getOrigins func(*http.Request) []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		allowed := getOrigins(r)
		isPreflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""

		if origin == "" || !originAllowed(allowed, origin) {
			if origin != "" && isPreflight {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(&corsResponseWriter{ResponseWriter: w}, r)
			return
		}

		value := origin
		if !slices.Contains(allowed, origin) {
			value = "*"
		}
		if isPreflight {
			answerPreflight(w, r, value)
			return
		}
		next.ServeHTTP(&corsResponseWriter{ResponseWriter: w, origin: value}, r)
	})
}

// originAllowed reports whether origin is listed, or the list holds the explicit wildcard.
func originAllowed(allowed []string, origin string) bool {
	return slices.Contains(allowed, origin) || slices.Contains(allowed, "*")
}

func answerPreflight(w http.ResponseWriter, r *http.Request, value string) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", value)
	h.Add("Vary", "Origin")
	h.Set("Access-Control-Allow-Methods", defaultCORSMethods)
	if reqHdr := r.Header.Get("Access-Control-Request-Headers"); reqHdr != "" {
		h.Set("Access-Control-Allow-Headers", reqHdr)
	} else {
		h.Set("Access-Control-Allow-Headers", defaultCORSHeaders)
	}
	h.Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// corsHeaders are the response headers that grant a browser origin something. Whatever a
// downstream service set is removed; only the gateway's answer goes out.
var corsHeaders = []string{
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Credentials",
	"Access-Control-Allow-Methods",
	"Access-Control-Allow-Headers",
	"Access-Control-Expose-Headers",
	"Access-Control-Max-Age",
}

// corsResponseWriter replaces a downstream service's CORS headers with the gateway's decision
// just before the status line goes out: origin set means that origin (or `*`) is allowed, empty
// means it is not and no CORS header is sent.
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

// Unwrap lets http.ResponseController reach Flush and Hijack on the writer underneath, which
// streaming responses and the realtime WebSocket upgrade need.
func (cw *corsResponseWriter) Unwrap() http.ResponseWriter {
	return cw.ResponseWriter
}

func (cw *corsResponseWriter) ensureCORSHeader() {
	if cw.acHeader {
		return
	}
	cw.acHeader = true
	h := cw.ResponseWriter.Header()
	credentials := h.Get("Access-Control-Allow-Credentials")
	exposed := h.Get("Access-Control-Expose-Headers")
	for _, name := range corsHeaders {
		h.Del(name)
	}
	if cw.origin == "" {
		return
	}
	h.Set("Access-Control-Allow-Origin", cw.origin)
	h.Add("Vary", "Origin")
	if exposed != "" {
		h.Set("Access-Control-Expose-Headers", exposed)
	}
	// A wildcard with credentials is a combination browsers refuse, so the wildcard goes alone.
	if credentials != "" && cw.origin != "*" {
		h.Set("Access-Control-Allow-Credentials", credentials)
	}
}
