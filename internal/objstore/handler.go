// Package objstore implements a Supabase-compatible object storage HTTP handler
// backed by the local filesystem. It is used by supatype-server in dev mode
// (SUPATYPE_STORAGE_PROVIDER=local) so that storage works out of the box with
// no MinIO or external service required.
//
// The HTTP API surface is identical to the supatype/storage Node.js service,
// so the @supatype/client SDK works without any changes.
package objstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/utilities"
)

// Handler returns an http.Handler that implements the Supabase-compatible
// object storage API backed by local disk at storageRoot.
//
// jwtSecret is the HS256 secret used to validate Bearer tokens — the same
// value as SUPATYPE_JWT_SECRET set by the CLI (local dev JWT secret).
//
// The handler is designed to be mounted with http.StripPrefix("/storage/v1")
// so all routes below are relative to that prefix.
func Handler(storageRoot, jwtSecret string) http.Handler {
	h := &store{
		root:      storageRoot,
		jwtSecret: []byte(jwtSecret),
		mu:        &sync.RWMutex{},
	}

	metaDir := filepath.Join(storageRoot, ".supatype")
	if err := mkdirAll(metaDir, 0o700); err != nil {
		logrus.WithError(err).Warn("objstore: failed to create metadata directory")
	}

	r := chi.NewRouter()

	// Who may reach a route is stated once, at the mount, rather than as the
	// first four lines of every handler. Ten handlers each opened with their own
	// copy of the check, which is ten chances to write the wrong one.

	// ── Bucket administration ─────────────────────────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(h.serviceRoleOnly)
		r.Get("/bucket", h.listBuckets)
		r.Post("/bucket", h.createBucket)
		r.Get("/bucket/{id}", h.getBucket)
		r.Put("/bucket/{id}", h.updateBucket)
		r.Delete("/bucket/{id}", h.deleteBucket)
		r.Post("/bucket/{id}/empty", h.emptyBucket)
	})

	// ── Objects, for any authenticated caller ─────────────────────────────────
	// More specific patterns first.
	r.Group(func(r chi.Router) {
		r.Use(h.authenticated)
		r.Post("/object/list/{bucket}", h.listObjects)
		r.Post("/object/sign/{bucket}/*", h.createSignedURL)
		r.Get("/object/authenticated/{bucket}/*", h.downloadAuthenticated)
		r.Post("/object/{bucket}/*", h.uploadObject)
		r.Delete("/object/{bucket}", h.removeObjects)
	})

	// ── Objects reachable without a token ─────────────────────────────────────
	// The signed URL carries its own credential; the public bucket is public.
	r.Get("/object/sign/{bucket}/*", h.serveSignedURL) // ?token=...
	r.Get("/object/public/{bucket}/*", h.downloadPublic)

	return r
}

// ─── store ────────────────────────────────────────────────────────────────────

type store struct {
	root      string
	jwtSecret []byte
	mu        *sync.RWMutex
}

// ─── JSON helpers ─────────────────────────────────────────────────────────────

func writeError(w http.ResponseWriter, status int, msg string) {
	utilities.WriteJSON(w, status, map[string]string{"message": msg})
}

// ─── JWT auth ─────────────────────────────────────────────────────────────────

type jwtClaims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	Exp  int64  `json:"exp"`
}

// claimsKey carries the verified claims from the middleware to the handler, so
// the token is parsed once per request rather than once per place that wants it.
type claimsKey struct{}

// claimsFrom returns the claims the middleware verified, or nil on a route that
// has none.
func claimsFrom(ctx context.Context) *jwtClaims {
	claims, _ := ctx.Value(claimsKey{}).(*jwtClaims)
	return claims
}

// authenticated refuses a request that carries no usable token.
func (s *store) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := s.extractClaims(r)
		if claims == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)))
	})
}

// serviceRoleOnly refuses anyone but the service role. Bucket administration is
// not something an end user's token may do.
func (s *store) serviceRoleOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := s.extractClaims(r)
		if !isServiceRole(claims) {
			writeError(w, http.StatusUnauthorized, "service role required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)))
	})
}

// extractClaims parses and validates an HS256 JWT from the request.
// Returns nil if no token is present or the token is invalid/expired.
func (s *store) extractClaims(r *http.Request) *jwtClaims {
	token := ""
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	} else if api := r.Header.Get("apikey"); api != "" {
		token = api
	}
	if token == "" {
		return nil
	}
	return s.parseJWT(token)
}

func (s *store) parseJWT(token string) *jwtClaims {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}

	// Verify HMAC-SHA256 signature.
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil
	}
	mac := hmac.New(sha256.New, s.jwtSecret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(mac.Sum(nil), sigBytes) {
		return nil
	}

	// Decode payload.
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

func isServiceRole(c *jwtClaims) bool {
	return c != nil && c.Role == "service_role"
}
