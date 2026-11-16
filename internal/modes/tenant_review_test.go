package modes

import (
	"net/http"
	"testing"
)

// While the old shared secret is still configured, a tenant gateway must still refuse a request
// signed for another tenant. Every namespace held that secret, so accepting any tenant with it
// would let one project's credentials act as another project at its gateway.
func TestATenantGatewayRefusesAnotherTenantSignedWithTheSharedSecret(t *testing.T) {
	k := TenantKeys{TenantKey: TenantKey("m", "t1"), ManagedRef: "t1", LegacySecret: "shared"}
	if got := statusFor(k, signedFor("t2", "shared")); got != http.StatusUnauthorized {
		t.Errorf("tenant gateway t1, t2 signed with the shared secret = %d, want 401", got)
	}
	if got := statusFor(k, signedFor("t1", "shared")); got != http.StatusOK {
		t.Errorf("tenant gateway t1, own tenant signed with the shared secret = %d, want 200", got)
	}
}

// The platform server, which has no managed ref, keeps accepting any tenant under the shared
// secret during the transition.
func TestThePlatformServerStillAcceptsAnyTenantUnderTheSharedSecret(t *testing.T) {
	k := TenantKeys{Master: "m", LegacySecret: "shared"}
	if got := statusFor(k, signedFor("t2", "shared")); got != http.StatusOK {
		t.Errorf("platform server, t2 signed with the shared secret = %d, want 200", got)
	}
}

// Only the inner auth mount itself bypasses tenant verification, not every path that happens to
// start with the same letters.
func TestOnlyTheAuthMountBypassesTenantVerification(t *testing.T) {
	cases := map[string]bool{
		"/auth/v1":        true,
		"/auth/v1/":       true,
		"/auth/v1/token":  true,
		"/health":         true,
		"/health/ready":   true,
		"/auth/v1x":       false,
		"/auth/v1x/rest":  false,
		"/auth/v10/token": false,
		"/rest/v1/users":  false,
	}
	for path, want := range cases {
		if got := tenantBypassPaths(path); got != want {
			t.Errorf("tenantBypassPaths(%q) = %v, want %v", path, got, want)
		}
	}
}
