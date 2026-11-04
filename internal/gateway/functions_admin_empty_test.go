package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// With no functions directory the functions admin API still answers, behind the same service-role
// check, so Studio can tell "this project has no functions" from a failure: it was not mounted, and
// the list answered 404. The list is empty; the env routes, which read and write files in that
// directory, refuse with the reason.
func TestWithoutAFunctionsDirTheAdminAPIListsNone(t *testing.T) {
	r := newRig(t, "dev")
	for _, target := range []string{"/admin/v1/functions/list", "/functions/v1/admin/list"} {
		rec := r.do(http.MethodGet, target, asServiceRole)
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"data":[]}` {
			t.Errorf("GET %s: %d %s, want 200 and an empty list", target, rec.Code, rec.Body.String())
		}
		if got, reached := r.lastHit(); reached {
			t.Errorf("GET %s reached %s; the admin API answers it itself", target, got.upstream)
		}
	}
	if rec := r.do(http.MethodGet, "/admin/v1/functions/list", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET the list with no key: %d, want 401 as when a directory is configured", rec.Code)
	}
	rec := r.do(http.MethodGet, "/admin/v1/functions/env", asServiceRole)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no functions directory") {
		t.Errorf("GET env: %d %s, want 409 saying there is no functions directory", rec.Code, rec.Body.String())
	}
}
