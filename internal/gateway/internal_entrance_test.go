package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/modes"
)

// doInternal sends a request in by the internal entrance of the rig's own project, as a function in
// the project's namespace does.
func (r *rig) doInternal(method, target string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if mutate != nil {
		mutate(req)
	}
	srv := modes.NewInternalEntranceServer("", testTenant, r.handler, context.Background())
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req.WithContext(srv.ConnContext(req.Context(), nil)))
	return rec
}

func withAnonKey(t *testing.T) func(*http.Request) {
	return func(req *http.Request) { req.Header.Set("apikey", signedAPIKey(t, "anon")) }
}

// A function calls its own project's REST API by the internal entrance with only its anon key, and
// reaches PostgREST as its own tenant.
func TestTheInternalEntranceReachesTheProjectsAPIWithItsKey(t *testing.T) {
	key := modes.TenantKey(signingMaster, testTenant)
	r := newRig(t, "managed", tenantGatewayOpt(key, testTenant))
	rec := r.doInternal(http.MethodGet, "/rest/v1/posts", withAnonKey(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("internal entrance with the anon key = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got, ok := r.lastHit()
	if !ok || got.upstream != "postgrest" {
		t.Fatalf("upstream = %+v, want postgrest", got)
	}
	if tenant := got.header.Get(tenantHeader); tenant != "" && tenant != testTenant {
		t.Errorf("tenant passed on = %q, want %q or none", tenant, testTenant)
	}
}

// The internal entrance stands in for the Kong signature only. A caller with no API key is still
// refused by the key check behind it.
func TestTheInternalEntranceStillNeedsAnAPIKey(t *testing.T) {
	key := modes.TenantKey(signingMaster, testTenant)
	r := newRig(t, "managed", tenantGatewayOpt(key, testTenant))
	if rec := r.doInternal(http.MethodGet, "/rest/v1/posts", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("internal entrance with no API key = %d, want 401", rec.Code)
	}
}

// The public entrance is unchanged: the same request without Kong's signature is refused.
func TestThePublicEntranceStillNeedsKongsSignature(t *testing.T) {
	key := modes.TenantKey(signingMaster, testTenant)
	r := newRig(t, "managed", tenantGatewayOpt(key, testTenant))
	if rec := r.do(http.MethodGet, "/rest/v1/posts", withAnonKey(t)); rec.Code != http.StatusUnauthorized {
		t.Errorf("public entrance, anon key and no signature = %d, want 401", rec.Code)
	}
}

func TestValidateInternalEntrance(t *testing.T) {
	cases := map[string]struct {
		cfg     config.Config
		wantErr bool
	}{
		"not configured":                  {config.Config{Mode: "standalone"}, false},
		"a managed tenant gateway":        {config.Config{Mode: "managed", ManagedProjectRef: "p", InternalAddr: ":9990"}, false},
		"the platform server, no project": {config.Config{Mode: "managed", InternalAddr: ":9990"}, true},
		"a blank project":                 {config.Config{Mode: "managed", ManagedProjectRef: " ", InternalAddr: ":9990"}, true},
		"standalone mode":                 {config.Config{Mode: "standalone", ManagedProjectRef: "p", InternalAddr: ":9990"}, true},
		"dev mode":                        {config.Config{Mode: "dev", ManagedProjectRef: "p", InternalAddr: ":9990"}, true},
	}
	for name, c := range cases {
		cfg := c.cfg
		if err := ValidateInternalEntrance(&cfg); (err != nil) != c.wantErr {
			t.Errorf("%s: error = %v, want error %v", name, err, c.wantErr)
		}
	}
}
