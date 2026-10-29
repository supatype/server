package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/functions"
)

// withFunctionsDir sets the functions directory, as compose and native dev both do: it is what
// mounts the functions admin API.
func withFunctionsDir(dir string) rigOption {
	return func(cfg *config.Config) { cfg.DenoFunctionsDir = dir }
}

// asServiceRole sends the rig's service-role key, as the CLI and Studio do for administration.
func asServiceRole(req *http.Request) {
	req.Header.Set("Authorization", "Bearer service-role-key")
}

// functionsRig is the dev mux with a functions directory and an external worker, as compose has.
func functionsRig(t *testing.T) *rig {
	t.Helper()
	return newRig(t, "dev", withFunctionsDir("/project/functions"))
}

// expectWorker fails unless the request reached the functions worker, answered 200.
func expectWorker(t *testing.T, r *rig, method, target string, mutate func(*http.Request)) {
	t.Helper()
	rec := r.do(method, target, mutate)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s: status = %d, want 200 from the worker (body %s)", method, target, rec.Code, rec.Body.String())
	}
	if got, ok := r.lastHit(); !ok || got.upstream != "functions" {
		t.Fatalf("%s %s: upstream = %+v, want the functions worker", method, target, got)
	}
}

// expectAdminAPI fails unless the functions admin API answered: its list, never the worker.
func expectAdminAPI(t *testing.T, r *rig, target string) {
	t.Helper()
	rec := r.do(http.MethodGet, target, asServiceRole)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"data"`) {
		t.Fatalf("GET %s: status = %d, body %s, want the admin API's function list", target, rec.Code, rec.Body.String())
	}
	if _, reached := r.lastHit(); reached {
		t.Fatalf("GET %s reached an upstream; the admin API answers it itself", target)
	}
}

// A project function named "admin" is called at /functions/v1/admin like any other. The admin API
// mounted there took the whole prefix, so the function was unreachable in compose and in native
// dev alike, answering 401 "service role key required" to its own callers.
func TestAFunctionNamedAdminReachesTheWorker(t *testing.T) {
	for _, target := range []string{"/functions/v1/admin", "/functions/v1/admin/", "/functions/v1/admin/orders/42", "/functions/v1/admin/list"} {
		t.Run(target, func(t *testing.T) {
			expectWorker(t, functionsRig(t), http.MethodPost, target, nil)
		})
	}
}

// The admin API's own address, under the admin API's prefix.
func TestTheFunctionsAdminAPIIsUnderAdminV1(t *testing.T) {
	expectAdminAPI(t, functionsRig(t), "/admin/v1/functions/list")
	rec := functionsRig(t).do(http.MethodGet, "/admin/v1/functions/list", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without the service-role key", rec.Code)
	}
}

// For a transition the old address still answers the admin API's own routes, to the service role
// only: a Studio or CLI not yet moved keeps working. Every other caller, and every other sub-path,
// is a call to the project's function named admin.
func TestTheOldAdminAddressAnswersOnlyKnownRoutesToTheServiceRole(t *testing.T) {
	expectAdminAPI(t, functionsRig(t), "/functions/v1/admin/list")
	expectWorker(t, functionsRig(t), http.MethodPost, "/functions/v1/admin/orders/42", asServiceRole)
	expectWorker(t, functionsRig(t), http.MethodGet, "/functions/v1/admin/list", func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer not-the-key")
	})
}

// With no invocation proxy built there is no project function to call: the old address answers a
// call to one 404, as /functions/v1 does when it is not mounted, rather than dereferencing nothing.
func TestTheOldAdminAddressWithoutAnInvocationProxyAnswersNotFound(t *testing.T) {
	cfg := &config.Config{ServiceRoleKey: "service-role-key", DenoFunctionsDir: t.TempDir()}
	h := legacyFunctionsAdmin(cfg.ServiceRoleKey, functions.Handler(cfg, cfg.DenoFunctionsDir, nil), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/orders/42", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 with no invocation proxy", rec.Code)
	}
}

// asStudioAdmin sends a Studio admin's session, which the proxy forwards as the service role.
func asStudioAdmin(t *testing.T) func(*http.Request) {
	t.Helper()
	token := signedClaims(t, `{"sub":"admin-user","role":"authenticated","app_metadata":{"role":"admin"},"exp":`+
		itoa(int(time.Now().Add(time.Hour).Unix()))+`}`)
	return func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }
}

// Through the Studio proxy and the real router, an admin reaches the admin API at both addresses
// and the function named admin at the old one: the proxy and the dispatcher ask the same question.
func TestStudioReachesTheAdminAPIAndTheFunctionNamedAdmin(t *testing.T) {
	for _, target := range []string{"/studio/proxy/admin/v1/functions/list", "/studio/proxy/functions/v1/admin/list"} {
		r := functionsRig(t)
		rec := r.do(http.MethodGet, target, asStudioAdmin(t))
		if _, reached := r.lastHit(); rec.Code != http.StatusOK || reached || !strings.Contains(rec.Body.String(), `"data"`) {
			t.Fatalf("GET %s: status %d, body %s, reached an upstream %v; want the admin API", target, rec.Code, rec.Body.String(), reached)
		}
	}
	// A read: an elevated write is refused here, since the rig has no audit log to record it in.
	expectWorker(t, functionsRig(t), http.MethodGet, "/studio/proxy/functions/v1/admin/orders/42", asStudioAdmin(t))
}

// An escaped admin route is not an admin route to the router, so the dispatcher sends it to the
// function named admin even with the service-role key, as the Studio proxy judges it.
func TestAnEscapedAdminRouteIsAFunctionCall(t *testing.T) {
	expectWorker(t, functionsRig(t), http.MethodGet, "/functions/v1/admin/%6cist", asServiceRole)
}
