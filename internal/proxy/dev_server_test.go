package proxy

import "testing"

// An app upstream with no port is reached on its scheme's default, so the Host
// names no port either.
func TestLocalhostOnKeepsTheTargetsPort(t *testing.T) {
	cases := map[string]string{
		"http://host.docker.internal:5173": "localhost:5173",
		"http://app":                       "localhost",
		"https://[::1]:8443":               "localhost:8443",
	}
	for raw, want := range cases {
		if got := localhostOn(mustParse(t, raw)); got != want {
			t.Errorf("localhostOn(%q) = %q, want %q", raw, got, want)
		}
	}
}
