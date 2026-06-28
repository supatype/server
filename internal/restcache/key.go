package restcache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const maxCacheBodyBytes = 1 << 20 // 1 MiB

// ParseClientMaxAge reads `X-Supatype-Cache: max-age=N`. Returns 0 when absent or invalid.
func ParseClientMaxAge(h http.Header) int {
	raw := strings.TrimSpace(h.Get("X-Supatype-Cache"))
	if raw == "" {
		return 0
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "max-age=") {
			n, err := strconv.Atoi(strings.TrimSpace(part[8:]))
			if err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// EffectiveTTL returns min(clientMaxAge, serverCap) when table caching is allowed.
func EffectiveTTL(serverCap, clientMaxAge int, tableAllowed bool) int {
	if !tableAllowed || serverCap <= 0 || clientMaxAge <= 0 {
		return 0
	}
	if clientMaxAge < serverCap {
		return clientMaxAge
	}
	return serverCap
}

// TenantRef resolves cache namespace for a request.
func TenantRef(req *http.Request, managedProjectRef string) string {
	if t := strings.TrimSpace(req.Header.Get("X-Supatype-Tenant")); t != "" {
		return t
	}
	if r := strings.TrimSpace(managedProjectRef); r != "" {
		return r
	}
	return "local"
}

// RestKeyPrefix returns the Valkey SCAN prefix for a tenant's REST cache keys.
func RestKeyPrefix(tenant string) string {
	return fmt.Sprintf("tenant:%s:rest:", tenant)
}

type keyParts struct {
	Tenant     string
	Schema     string
	Method     string
	Path       string
	RawQuery   string
	AuthHash   string
	Accept     string
	Range      string
	Language   string
	MaxRows    string
}

// BuildKey returns a Valkey key tenant:{ref}:rest:{hash}.
func BuildKey(p keyParts) string {
	var b strings.Builder
	b.WriteString(p.Tenant)
	b.WriteByte(0)
	b.WriteString(p.Schema)
	b.WriteByte(0)
	b.WriteString(p.Method)
	b.WriteByte(0)
	b.WriteString(p.Path)
	b.WriteByte(0)
	b.WriteString(p.RawQuery)
	b.WriteByte(0)
	b.WriteString(p.AuthHash)
	b.WriteByte(0)
	b.WriteString(p.Accept)
	b.WriteByte(0)
	b.WriteString(p.Range)
	b.WriteByte(0)
	b.WriteString(p.Language)
	b.WriteByte(0)
	b.WriteString(p.MaxRows)
	sum := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("tenant:%s:rest:%s", p.Tenant, hex.EncodeToString(sum[:]))
}

// VaryHeader builds a stable Vary value for cacheable REST GET responses.
func VaryHeader(req *http.Request) string {
	parts := []string{"Authorization", "apikey", "Accept", "Range", "Accept-Language"}
	if strings.TrimSpace(req.Header.Get("X-Supatype-Cache")) != "" {
		parts = append(parts, "X-Supatype-Cache")
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func cacheableMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func cacheableStatus(code int) bool {
	return code == http.StatusOK || code == http.StatusPartialContent
}

func cacheScopeLabel(usePublic bool) string {
	if usePublic {
		return "public"
	}
	return "user"
}
