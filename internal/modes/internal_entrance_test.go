package modes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// seen is what the handler behind the tenant check was given, or nil when the check refused.
type seen struct {
	tenant string
	sig    string
}

// entranceRig is the internal entrance of a gateway that serves entranceRef and verifies with k,
// and what the handler behind the tenant check last saw.
type entranceRig struct {
	srv  *http.Server
	last *seen
}

func newEntranceRig(entranceRef string, k TenantKeys) *entranceRig {
	rig := &entranceRig{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rig.last = &seen{tenant: r.Header.Get(TenantHeader), sig: r.Header.Get(TenantSigHeader)}
		w.WriteHeader(http.StatusOK)
	})
	rig.srv = NewInternalEntranceServer("", entranceRef, TenantMiddlewareKeys(k, inner), context.Background())
	return rig
}

// accepted sends req as a connection the entrance's listener accepted: through its ConnContext.
func (rig *entranceRig) accepted(req *http.Request) int {
	rec := httptest.NewRecorder()
	rig.srv.Handler.ServeHTTP(rec, req.WithContext(rig.srv.ConnContext(req.Context(), nil)))
	return rec.Code
}

func unsigned() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/rest/v1/post", nil)
}

var ownGateway = TenantKeys{TenantKey: TenantKey("m", "t1"), ManagedRef: "t1"}

// A project's own function calls its API by the internal entrance, which no Kong signature reaches.
func TestTheInternalEntranceServesItsOwnProjectWithoutASignature(t *testing.T) {
	rig := newEntranceRig("t1", ownGateway)
	if code := rig.accepted(unsigned()); code != http.StatusOK || rig.last == nil {
		t.Fatalf("unsigned request by the internal entrance = %d, want 200", code)
	}
	if rig.last.tenant != "t1" {
		t.Errorf("tenant behind the check = %q, want the gateway's own t1", rig.last.tenant)
	}
}

// The caller does not choose the project: whatever tenant it names, the entrance serves its own.
func TestTheInternalEntranceReplacesATenantTheCallerNames(t *testing.T) {
	rig := newEntranceRig("t1", ownGateway)
	req := unsigned()
	req.Header.Set(TenantHeader, "t2")
	req.Header.Set(TenantSigHeader, "forged")
	if code := rig.accepted(req); code != http.StatusOK || rig.last == nil {
		t.Fatalf("request naming t2 by the internal entrance = %d, want 200 as t1", code)
	}
	if rig.last.tenant != "t1" || rig.last.sig != "" {
		t.Errorf("behind the check: tenant %q sig %q, want t1 and no signature", rig.last.tenant, rig.last.sig)
	}
}

// The trust is the listener's, not the handler's: the same handler reached without the entrance's
// connection, as from any other listener, is refused.
func TestTheInternalEntrancesHandlerAloneTrustsNothing(t *testing.T) {
	rig := newEntranceRig("t1", ownGateway)
	rec := httptest.NewRecorder()
	rig.srv.Handler.ServeHTTP(rec, unsigned())
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("entrance handler without its listener's connection = %d, want 401", rec.Code)
	}
}

// An entrance configured for another project than the gateway verifies for is refused, so a
// mismatched deployment fails closed instead of serving the wrong tenant.
func TestTheInternalEntranceIsRefusedForAnotherProject(t *testing.T) {
	if code := newEntranceRig("t2", ownGateway).accepted(unsigned()); code != http.StatusUnauthorized {
		t.Errorf("entrance for t2 on gateway t1 = %d, want 401", code)
	}
}

// The platform server serves every tenant and has no project of its own, so it has no internal
// entrance to trust.
func TestThePlatformServerDoesNotTrustAnInternalEntrance(t *testing.T) {
	if code := newEntranceRig("t1", TenantKeys{Master: "m"}).accepted(unsigned()); code != http.StatusUnauthorized {
		t.Errorf("internal entrance on the platform server = %d, want 401", code)
	}
}

type baseKey struct{}

// Requests on the entrance inherit the context the server was built with, so they end with the
// process that serves them rather than outliving it.
func TestTheInternalEntrancesRequestsInheritTheBaseContext(t *testing.T) {
	base := context.WithValue(context.Background(), baseKey{}, "the server")
	srv := NewInternalEntranceServer("", "t1", http.NotFoundHandler(), base)
	if got := srv.BaseContext(nil); got != base {
		t.Errorf("BaseContext = %v, want the context the server was built with", got)
	}
}
