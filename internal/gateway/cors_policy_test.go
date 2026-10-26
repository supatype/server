package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/corsorigins"
)

// SHb at the level of the per-mode stack: a standalone server with no origins configured had no
// CORS layer at all, so whatever the services behind it answered went out, and the auth service
// answers `*`. Now standalone always has the layer, its default is the site URL plus what is
// configured, and only that.

func preflight(origin string) *http.Request {
	req := httptest.NewRequest(http.MethodOptions, "/auth/v1/user", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	return req
}

func starDownstream() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
	})
}

func TestAStandaloneServerWithNothingConfiguredDoesNotAnswerStar(t *testing.T) {
	rec := httptest.NewRecorder()
	ModeChain(&config.Config{Mode: "standalone"}, nil).Then(starDownstream()).ServeHTTP(rec, preflight("https://evil.example"))

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin = %q for evil.example, want none", got)
	}
}

// memoryStore is a saved list kept in memory, standing in for the database.
type memoryStore struct{ saved *corsorigins.Saved }

func (m *memoryStore) Load(context.Context) (*corsorigins.Saved, error) { return m.saved, nil }
func (m *memoryStore) Save(_ context.Context, origins []string) (*corsorigins.Saved, error) {
	m.saved = &corsorigins.Saved{Origins: origins}
	return m.saved, nil
}
func (m *memoryStore) Delete(context.Context) error { m.saved = nil; return nil }

func allowedBy(chain Chain, origin string) bool {
	rec := httptest.NewRecorder()
	chain.Then(starDownstream()).ServeHTTP(rec, preflight(origin))
	return rec.Header().Get("Access-Control-Allow-Origin") == origin
}

func TestTheStandaloneDefaultIsTheSiteURLPlusWhatIsConfigured(t *testing.T) {
	cfg := &config.Config{Mode: "standalone", CorsAllowOrigins: "https://admin.example.com/"}
	policy := corsorigins.NewPolicy(&memoryStore{}, corsorigins.Defaults{
		SiteURL:    "https://app.example.com/some/path",
		Configured: []string{"https://admin.example.com/"},
	})
	chain := ModeChainWithPolicy(cfg, nil, policy)

	for origin, want := range map[string]bool{
		"https://app.example.com":   true,
		"https://admin.example.com": true,
		"https://evil.example":      false,
	} {
		if got := allowedBy(chain, origin); got != want {
			t.Errorf("%s: allowed = %v, want %v", origin, got, want)
		}
	}
}

func TestASavedListReplacesTheDefaultUntilReset(t *testing.T) {
	store := &memoryStore{}
	policy := corsorigins.NewPolicy(store, corsorigins.Defaults{
		SiteURL:    "https://app.example.com",
		Configured: []string{"https://admin.example.com"},
		Platform:   []string{"https://studio.supatype.com"},
	})
	chain := ModeChainWithPolicy(&config.Config{Mode: "standalone"}, nil, policy)

	if _, err := policy.Save(context.Background(), []string{"https://partner.example"}); err != nil {
		t.Fatal(err)
	}
	if !allowedBy(chain, "https://partner.example") || allowedBy(chain, "https://admin.example.com") {
		t.Error("a saved list should replace the configured default")
	}
	// SHc: the site URL stays allowed whatever is saved.
	if !allowedBy(chain, "https://app.example.com") {
		t.Error("the site URL must stay allowed beside a saved list")
	}
	if !allowedBy(chain, "https://studio.supatype.com") {
		t.Error("platform origins stay allowed whatever is saved")
	}

	if err := policy.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if allowedBy(chain, "https://partner.example") || !allowedBy(chain, "https://admin.example.com") {
		t.Error("a reset should return to the default")
	}
}

func TestDevModeStillReflectsAnyOrigin(t *testing.T) {
	policy := corsorigins.NewPolicy(nil, corsorigins.Defaults{SiteURL: "https://app.example.com"})
	if !allowedBy(ModeChainWithPolicy(&config.Config{Mode: "dev"}, nil, policy), "https://anything.example") {
		t.Error("dev mode keeps its permissive CORS")
	}
}

// The origins API is mounted and answers from the policy the CORS layer reads, with the request's
// manifest origins (Cloud's tenant configuration) in the default.
func TestTheOriginsAPIIsMountedOnTheGateway(t *testing.T) {
	r := newRig(t, "standalone", withCORSOrigins("https://admin.example.com"))
	rec := r.do(http.MethodGet, "/admin/cors-origins", func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer service-role-key")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"configured":["https://admin.example.com"]`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}
