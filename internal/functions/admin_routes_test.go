package functions

import (
	"net/http"
	"testing"
)

// IsAdminRoute names the admin API's own routes and nothing else: below its old address, every
// other path is a call to a project function named admin.
func TestIsAdminRouteNamesOnlyTheAPIsRoutes(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/list", true},
		{http.MethodGet, "/hello/logs", true},
		{http.MethodGet, "/logs/tail", true},
		{http.MethodPost, "/env", true},
		{http.MethodDelete, "/hello/env/KEY", true},
		{http.MethodPost, "/list", false},
		{http.MethodGet, "", false},
		{http.MethodGet, "/", false},
		{http.MethodPost, "/orders/42", false},
		{http.MethodGet, "/%6cist", false},
		{http.MethodGet, "/list/", false},
	}
	for _, c := range cases {
		if got := IsAdminRoute(c.method, c.path); got != c.want {
			t.Errorf("IsAdminRoute(%s, %q) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

// The old address serves the admin API only when the request carries the service role and the
// path is one of the API's routes; anything short of both is the function.
func TestServesAdminAPINeedsCredentialAndRoute(t *testing.T) {
	if !ServesAdminAPI(true, http.MethodGet, "/env") {
		t.Error("a service-role GET /env must be the admin API")
	}
	if ServesAdminAPI(false, http.MethodGet, "/env") {
		t.Error("the caller's own token reached the admin API")
	}
	if ServesAdminAPI(true, http.MethodPost, "/orders/42") {
		t.Error("a function call reached the admin API")
	}
}
