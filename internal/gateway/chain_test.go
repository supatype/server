package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// tag returns a middleware that records that it ran, so the order a Chain
// applies can be observed rather than reasoned about.
func tag(order *[]string, name string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*order = append(*order, name)
			next.ServeHTTP(w, r)
		})
	}
}

// The first element of the list must be the outermost middleware, because that
// is how the list reads and the managed stack depends on it.
func TestChainAppliesOutermostFirst(t *testing.T) {
	var order []string
	chain := Chain{tag(&order, "outer"), tag(&order, "middle"), tag(&order, "inner")}

	handler := chain.Then(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if got := strings.Join(order, ","); got != "outer,middle,inner,handler" {
		t.Errorf("order = %s, want outer,middle,inner,handler", got)
	}
}

func TestEmptyChainIsTheIdentity(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	Chain(nil).Then(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("an empty chain must pass through unchanged, got %d", rec.Code)
	}
}

// The managed stack's order is the subtlety most at risk in this refactor, and
// it is now a property of a list rather than of nested constructor calls.
func TestModeChainShape(t *testing.T) {
	manifestFor := func(*http.Request) *proxy.RouteManifest { return &proxy.RouteManifest{} }

	cases := []struct {
		name string
		cfg  *config.Config
		want int
	}{
		{"dev has permissive CORS only", &config.Config{Mode: "dev"}, 1},
		{
			"managed is CORS, tenant, api key",
			&config.Config{Mode: "managed", JWTSecret: "s", TenantHMACSecret: "h"},
			3,
		},
		{
			// Without a tenant secret the HMAC layer is absent, and the warning
			// in ModeChain says so. The API key gate still stands.
			"managed without a tenant secret drops the HMAC layer",
			&config.Config{Mode: "managed", JWTSecret: "s"},
			2,
		},
		{"standalone with no origins adds nothing", &config.Config{Mode: "standalone"}, 0},
		{
			"standalone with origins adds the allowlist",
			&config.Config{Mode: "standalone", CorsAllowOrigins: "https://app.example.com"},
			1,
		},
		{"an unknown mode adds nothing", &config.Config{Mode: "nonsense"}, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := len(ModeChain(c.cfg, manifestFor)); got != c.want {
				t.Errorf("chain length = %d, want %d", got, c.want)
			}
		})
	}
}
