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
// /auth/v1 is the inner auth mount (platform login + control-plane proxy); tenant
// routing applies to /rest/v1, /storage/v1, etc.
func tenantBypassPaths(path string) bool {
	if path == "/health" || path == "/health/ready" {
		return true
	}
	return path == "/auth/v1" || strings.HasPrefix(path, "/auth/v1/")
}

func TenantMiddleware(secret string, next http.Handler) http.Handler {
	return TenantMiddlewareKeys(TenantKeys{LegacySecret: secret}, next)
}

// computeHMAC returns the lowercase hex-encoded HMAC-SHA256 of message using key.
func computeHMAC(message, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(message)) //nolint:errcheck
	return hex.EncodeToString(mac.Sum(nil))
}

// tenantKeyContext is the domain separator of the per-tenant signing key. It keeps a key derived
// here from being a signature over anything else, and it is part of the contract with the Cloud
// side, which derives the same key: change it on both together.
const tenantKeyContext = "supatype-tenant-signing-v1|"

// TenantKey is the signing key of one tenant: the lowercase hex HMAC-SHA256, under master, of the
// context string followed by the tenant ID. A tenant gateway is given only this, so a leak from one
// project namespace does not let anyone sign for another tenant.
func TenantKey(master, tenantID string) string {
	return computeHMAC(tenantKeyContext+tenantID, master)
}

// TenantKeys is what a gateway holds to check a tenant signature. Every field is optional; a field
// left empty turns its way of verifying off, and with all of them empty nothing verifies.
type TenantKeys struct {
	// Master derives a tenant's key per request, from the tenant ID the request claims. Only the
	// platform server, which serves every tenant, holds it. It refuses a tenant other than
	// ManagedRef when ManagedRef is set.
	Master string
	// TenantKey is this gateway's own key (see TenantKey). It verifies only requests signed for
	// ManagedRef, and verifies nothing while ManagedRef is empty.
	TenantKey string
	// LegacySecret is the platform-wide secret the signature used to be made with directly. While it
	// is configured it is accepted, so a release can move Kong and the gateways to per-tenant keys
	// one step at a time; on a tenant gateway only for its own tenant. Removing it turns it off.
	LegacySecret string
	// ManagedRef is the tenant this gateway serves, empty on the platform server.
	ManagedRef string
}

// verify reports whether sig, lowercase or not, is a valid signature of tenantID under any way
// this gateway is configured to check it. Every comparison runs, in constant time, whatever an
// earlier one concluded.
func (k TenantKeys) verify(tenantID, sig string) bool {
	sig = strings.ToLower(sig)
	ok := false
	// A gateway that serves one tenant accepts only that tenant, under every key it holds.
	ownTenant := k.ManagedRef == "" || tenantID == k.ManagedRef
	if IsSet(k.LegacySecret) && ownTenant {
		ok = signatureMatches(sig, tenantID, k.LegacySecret) || ok
	}
	if IsSet(k.TenantKey) && k.ManagedRef != "" && tenantID == k.ManagedRef {
		ok = signatureMatches(sig, tenantID, k.TenantKey) || ok
	}
	if IsSet(k.Master) && ownTenant {
		ok = signatureMatches(sig, tenantID, TenantKey(k.Master, tenantID)) || ok
	}
	return ok
}

func signatureMatches(sig, tenantID, key string) bool {
	return hmac.Equal([]byte(computeHMAC(tenantID, key)), []byte(sig))
}

// TenantMiddlewareKeys is TenantMiddleware for the per-tenant scheme: it verifies the signature
// against whichever of the keys k holds.
func TenantMiddlewareKeys(k TenantKeys, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenantBypassPaths(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		tenantID := r.Header.Get("X-Supatype-Tenant")
		sig := r.Header.Get("X-Supatype-Tenant-Sig")
		if tenantID == "" || sig == "" || !k.verify(tenantID, sig) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Configured reports whether k holds anything to verify with.
func (k TenantKeys) Configured() bool {
	return IsSet(k.Master) || IsSet(k.TenantKey) || IsSet(k.LegacySecret)
}

// IsSet reports whether a key holds anything but whitespace. Whitespace decides only whether a key
// counts as set: the key itself is used exactly as configured, because Kong and the Cloud control
// plane sign with it untrimmed.
func IsSet(key string) bool {
	return strings.TrimSpace(key) != ""
}
