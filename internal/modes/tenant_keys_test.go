package modes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	vectorMaster = "test-master"
	vectorTenant = "abc"
)

// The values the Cloud side asserts as well. They pin the derivation: a change to either side's
// message prefix or encoding fails here before it fails in a cluster.
func TestTenantKeyTestVector(t *testing.T) {
	const wantKey = "8c1a2dea9dc060d76b030ce410a8f28a0bab31af0e15635e8d097d257d8794ee"
	const wantSig = "ba6930d6e6facfc1f29c1587659566f0316baf365aef0ffe37ddb0a43eb9b088"
	key := TenantKey(vectorMaster, vectorTenant)
	if key != wantKey {
		t.Errorf("TenantKey = %q, want %q", key, wantKey)
	}
	if sig := computeHMAC(vectorTenant, key); sig != wantSig {
		t.Errorf("signature = %q, want %q", sig, wantSig)
	}
}

func TestTenantKeyIsPerTenantAndPerMaster(t *testing.T) {
	a := TenantKey("m", "t1")
	if a == "" || a == TenantKey("m", "t2") || a == TenantKey("m2", "t1") {
		t.Errorf("the key must differ by tenant and by master, got %q", a)
	}
}

func signedFor(tenant, key string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/users", nil)
	req.Header.Set("X-Supatype-Tenant", tenant)
	req.Header.Set("X-Supatype-Tenant-Sig", computeHMAC(tenant, key))
	return req
}

func statusFor(k TenantKeys, req *http.Request) int {
	rec := httptest.NewRecorder()
	TenantMiddlewareKeys(k, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec.Code
}

func TestATenantGatewayAcceptsItsOwnSignedRequests(t *testing.T) {
	k := TenantKeys{TenantKey: TenantKey("m", "t1"), ManagedRef: "t1"}
	if got := statusFor(k, signedFor("t1", k.TenantKey)); got != http.StatusOK {
		t.Errorf("own tenant, own key = %d, want 200", got)
	}
}

func TestATenantGatewayRefusesAnotherTenantEvenWithAValidSignature(t *testing.T) {
	k := TenantKeys{TenantKey: TenantKey("m", "t1"), ManagedRef: "t1"}
	// Signed correctly under the gateway's own key, for a tenant that is not its own.
	if got := statusFor(k, signedFor("t2", k.TenantKey)); got != http.StatusUnauthorized {
		t.Errorf("other tenant, valid signature = %d, want 401", got)
	}
	// Signed with that other tenant's own derived key.
	if got := statusFor(k, signedFor("t2", TenantKey("m", "t2"))); got != http.StatusUnauthorized {
		t.Errorf("other tenant, its own key = %d, want 401", got)
	}
}

func TestATenantKeyWithNoManagedRefAcceptsNothing(t *testing.T) {
	k := TenantKeys{TenantKey: TenantKey("m", "t1")}
	if got := statusFor(k, signedFor("t1", k.TenantKey)); got != http.StatusUnauthorized {
		t.Errorf("a tenant key with no ref = %d, want 401 (fail closed)", got)
	}
}

func TestThePlatformServerDerivesTheKeyFromTheClaimedTenant(t *testing.T) {
	k := TenantKeys{Master: "m"}
	for _, tenant := range []string{"t1", "t2", "abc"} {
		if got := statusFor(k, signedFor(tenant, TenantKey("m", tenant))); got != http.StatusOK {
			t.Errorf("%s signed with its derived key = %d, want 200", tenant, got)
		}
	}
	// Claims t1 but is signed with t2's key.
	if got := statusFor(k, signedFor("t1", TenantKey("m", "t2"))); got != http.StatusUnauthorized {
		t.Errorf("t1 signed with t2's key = %d, want 401", got)
	}
	// The master itself is not a signing key.
	if got := statusFor(k, signedFor("t1", "m")); got != http.StatusUnauthorized {
		t.Errorf("t1 signed with the master = %d, want 401", got)
	}
	if got := statusFor(k, signedFor("t1", TenantKey("other", "t1"))); got != http.StatusUnauthorized {
		t.Errorf("a key from another master = %d, want 401", got)
	}
}

func TestTheSharedSecretIsAcceptedOnlyWhileItIsConfigured(t *testing.T) {
	signedShared := signedFor("t1", "shared")
	with := TenantKeys{Master: "m", LegacySecret: "shared"}
	if got := statusFor(with, signedShared); got != http.StatusOK {
		t.Errorf("shared secret while configured = %d, want 200", got)
	}
	without := TenantKeys{Master: "m"}
	if got := statusFor(without, signedFor("t1", "shared")); got != http.StatusUnauthorized {
		t.Errorf("shared secret once removed = %d, want 401", got)
	}
	// Same for a tenant gateway: the fallback is what lets it keep serving Kong's old signature.
	tenant := TenantKeys{TenantKey: TenantKey("m", "t1"), ManagedRef: "t1", LegacySecret: "shared"}
	if got := statusFor(tenant, signedFor("t1", "shared")); got != http.StatusOK {
		t.Errorf("tenant gateway, shared secret while configured = %d, want 200", got)
	}
	tenant.LegacySecret = ""
	if got := statusFor(tenant, signedFor("t1", "shared")); got != http.StatusUnauthorized {
		t.Errorf("tenant gateway, shared secret once removed = %d, want 401", got)
	}
}

func TestAnUpperCaseSignatureIsStillAccepted(t *testing.T) {
	k := TenantKeys{Master: "m"}
	req := signedFor("t1", TenantKey("m", "t1"))
	req.Header.Set("X-Supatype-Tenant-Sig", upper(req.Header.Get("X-Supatype-Tenant-Sig")))
	if got := statusFor(k, req); got != http.StatusOK {
		t.Errorf("upper-case hex = %d, want 200", got)
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'f' {
			b[i] = c - 32
		}
	}
	return string(b)
}

func TestNoKeysAtAllRefusesEverything(t *testing.T) {
	if got := statusFor(TenantKeys{}, signedFor("t1", "")); got != http.StatusUnauthorized {
		t.Errorf("no keys configured = %d, want 401", got)
	}
}
