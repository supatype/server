package restcache

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type jwtClaims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	Exp  int64  `json:"exp"`
}

// ParseClientPublic reads `public` from X-Supatype-Cache (e.g. max-age=60, public).
func ParseClientPublic(h http.Header) bool {
	raw := strings.TrimSpace(h.Get("X-Supatype-Cache"))
	if raw == "" {
		return false
	}
	for _, part := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(part), "public") {
			return true
		}
	}
	return false
}

// RestTableFromPath returns the first path segment for /table or /table/... (not /rpc).
func RestTableFromPath(path string) string {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return ""
	}
	seg := path
	if i := strings.IndexByte(path, '/'); i >= 0 {
		seg = path[:i]
	}
	seg = strings.TrimSpace(seg)
	if seg == "" || seg == "rpc" {
		return ""
	}
	return seg
}

// IdentityForCache returns the auth component of a cache key.
// usePublic → shared "global" scope; otherwise role:sub from verified JWT.
func IdentityForCache(req *http.Request, jwtSecret []byte, usePublic bool) string {
	if usePublic {
		return "global"
	}
	token := bearerOrAPIKey(req)
	if token == "" {
		return "anon"
	}
	if claims := parseVerifiedJWT(token, jwtSecret); claims != nil {
		role := strings.TrimSpace(claims.Role)
		if role == "" {
			role = "authenticated"
		}
		sub := strings.TrimSpace(claims.Sub)
		if sub != "" {
			return role + ":" + sub
		}
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:16])
}

func bearerOrAPIKey(req *http.Request) string {
	if auth := strings.TrimSpace(req.Header.Get("Authorization")); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return strings.TrimSpace(req.Header.Get("apikey"))
}

func parseVerifiedJWT(token string, secret []byte) *jwtClaims {
	if len(secret) == 0 {
		return parseJWTUnsafe(token)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(mac.Sum(nil), sigBytes) {
		return nil
	}
	return parseJWTUnsafe(token)
}

func parseJWTUnsafe(token string) *jwtClaims {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return nil
	}
	return &claims
}
