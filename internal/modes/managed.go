package modes

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// TenantMiddleware verifies the X-Supatype-Tenant-Sig header on every request.
// Kong signs the header as HMAC-SHA256(tenant_id, secret) and sets it alongside
// X-Supatype-Tenant: {tenant_id}. This middleware verifies the signature and
// returns 401 if the header is missing or the signature is invalid.
//
// secret must be a non-empty shared secret configured identically on Kong and
// supatype-server. Timing-safe comparison is used (hmac.Equal).
// tenantBypassPaths are reachable without Kong tenant headers.
// /auth/v1 is the inner GoTrue mount (platform login + control-plane proxy); tenant
// routing applies to /rest/v1, /storage/v1, etc.
func tenantBypassPaths(path string) bool {
	if path == "/health" || path == "/health/ready" {
		return true
	}
	return strings.HasPrefix(path, "/auth/v1")
}

func TenantMiddleware(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenantBypassPaths(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		tenantID := r.Header.Get("X-Supatype-Tenant")
		sig := r.Header.Get("X-Supatype-Tenant-Sig")

		if tenantID == "" || sig == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		expected := computeHMAC(tenantID, secret)
		if !hmac.Equal([]byte(expected), []byte(strings.ToLower(sig))) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// computeHMAC returns the lowercase hex-encoded HMAC-SHA256 of message using key.
func computeHMAC(message, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(message)) //nolint:errcheck
	return hex.EncodeToString(mac.Sum(nil))
}
