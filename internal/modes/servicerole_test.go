package modes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceRoleBearer(t *testing.T) {
	cases := []struct {
		name   string
		header string
		key    string
		want   bool
	}{
		{"exact match", "Bearer secret", "secret", true},
		{"surrounding space is trimmed", "  Bearer   secret  ", "secret", true},
		{"key is trimmed too", "Bearer secret", "  secret  ", true},
		{"wrong token", "Bearer other", "secret", false},
		{"no bearer prefix", "secret", "secret", false},
		{"wrong scheme", "Basic secret", "secret", false},
		{"prefix is case sensitive", "bearer secret", "secret", false},
		{"no header", "", "secret", false},

		// An unconfigured key must never match. Comparing an empty key against a
		// trimmed empty token would otherwise let `Authorization: Bearer `
		// through as the service role.
		{"empty key with empty token", "Bearer ", "", false},
		{"empty key with any token", "Bearer anything", "", false},
		{"empty key with no header", "", "", false},
		{"whitespace key", "Bearer ", "   ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			if got := ServiceRoleBearer(r, c.key); got != c.want {
				t.Errorf("ServiceRoleBearer(%q, %q) = %v, want %v", c.header, c.key, got, c.want)
			}
		})
	}
}
