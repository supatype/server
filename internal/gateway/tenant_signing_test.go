package gateway

import (
	"net/http"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/modes"
)

const signingMaster = "test-master"

func tenantGatewayOpt(key, ref string) rigOption {
	return func(cfg *config.Config) {
		cfg.TenantHMACSecret = ""
		cfg.TenantSigningKey = key
		cfg.ManagedProjectRef = ref
	}
}

func platformOpt(cfg *config.Config) {
	cfg.TenantHMACSecret = ""
	cfg.TenantMasterSecret = signingMaster
	cfg.ManagedProjectRef = ""
}

func signedTo(t *testing.T, tenant, key string) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("X-Supatype-Tenant", tenant)
		req.Header.Set("X-Supatype-Tenant-Sig", tenantSig(tenant, key))
		req.Header.Set("apikey", signedAPIKey(t, "anon"))
	}
}

func TestATenantGatewayServesOnlyItsOwnTenantThroughTheMux(t *testing.T) {
	key := modes.TenantKey(signingMaster, testTenant)
	r := newRig(t, "managed", tenantGatewayOpt(key, testTenant))
	if rec := r.do(http.MethodGet, "/rest/v1/posts", signedTo(t, testTenant, key)); rec.Code != http.StatusOK {
		t.Errorf("own tenant = %d, want 200", rec.Code)
	}
	if rec := r.do(http.MethodGet, "/rest/v1/posts", signedTo(t, "someone-else", key)); rec.Code != http.StatusUnauthorized {
		t.Errorf("another tenant signed with this gateway's key = %d, want 401", rec.Code)
	}
	if rec := r.do(http.MethodGet, "/rest/v1/posts", signedTo(t, testTenant, "behaviour-lock-hmac-secret")); rec.Code != http.StatusUnauthorized {
		t.Errorf("the old shared secret with no fallback configured = %d, want 401", rec.Code)
	}
}

func TestThePlatformServerServesEveryTenantItCanDeriveAKeyFor(t *testing.T) {
	r := newRig(t, "managed", platformOpt)
	for _, tenant := range []string{"proj-a", "proj-b"} {
		if rec := r.do(http.MethodGet, "/rest/v1/posts", signedTo(t, tenant, modes.TenantKey(signingMaster, tenant))); rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", tenant, rec.Code)
		}
	}
	if rec := r.do(http.MethodGet, "/rest/v1/posts", signedTo(t, "proj-a", modes.TenantKey(signingMaster, "proj-b"))); rec.Code != http.StatusUnauthorized {
		t.Errorf("proj-a signed with proj-b's key = %d, want 401", rec.Code)
	}
}

func TestValidateTenantSigning(t *testing.T) {
	cases := map[string]struct {
		cfg     config.Config
		wantErr bool
	}{
		"nothing":                       {config.Config{}, true},
		"only the old shared secret":    {config.Config{TenantHMACSecret: "s"}, false},
		"the platform master":           {config.Config{TenantMasterSecret: "m"}, false},
		"a tenant key with its ref":     {config.Config{TenantSigningKey: "k", ManagedProjectRef: "p"}, false},
		"a tenant key with no ref":      {config.Config{TenantSigningKey: "k"}, true},
		"blank values are not a secret": {config.Config{TenantHMACSecret: " ", TenantMasterSecret: " "}, true},
	}
	for name, c := range cases {
		cfg := c.cfg
		if err := validateTenantSigning(&cfg); (err != nil) != c.wantErr {
			t.Errorf("%s: error = %v, want error %v", name, err, c.wantErr)
		}
	}
}
