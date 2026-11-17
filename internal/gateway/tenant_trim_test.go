package gateway

import (
	"testing"

	"github.com/supatype/server/internal/config"
)

// Kong and the Cloud control plane sign with the configured secret exactly as written. Trimming
// it here would turn a secret that ends in a newline into a different key, and every request
// signed by Kong would then answer 401. Whitespace only decides whether a field counts as set.
func TestTenantKeysAreUsedAsConfiguredNotTrimmed(t *testing.T) {
	cfg := &config.Config{
		TenantMasterSecret: "master\n",
		TenantSigningKey:   " key ",
		TenantHMACSecret:   "shared\n",
		ManagedProjectRef:  "t1",
	}
	k := tenantKeysOf(cfg)
	if k.Master != "master\n" || k.TenantKey != " key " || k.LegacySecret != "shared\n" {
		t.Errorf("tenantKeysOf trimmed the keys: %q %q %q", k.Master, k.TenantKey, k.LegacySecret)
	}
	blank := tenantKeysOf(&config.Config{TenantMasterSecret: "  ", TenantSigningKey: "\n", TenantHMACSecret: "\t"})
	if blank.Configured() {
		t.Errorf("whitespace-only keys count as configured: %+v", blank)
	}
}
