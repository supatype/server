package utilities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStudioURL(t *testing.T) {
	cases := map[string]string{
		// Self-host: the gateway's root.
		"https://api.example.com":  "https://api.example.com/studio/",
		"https://api.example.com/": "https://api.example.com/studio/",
		// Dev: the auth server's own mount under the gateway.
		"http://localhost:18540/auth/v1":  "http://localhost:18540/studio/",
		"http://localhost:18540/auth/v1/": "http://localhost:18540/studio/",
		// A gateway mounted under a prefix keeps it.
		"https://example.com/platform":         "https://example.com/platform/studio/",
		"https://example.com/platform/auth/v1": "https://example.com/platform/studio/",
		// Nothing but the origin and path survives.
		"https://api.example.com/?next=//evil#frag": "https://api.example.com/studio/",
	}
	for external, want := range cases {
		got, err := StudioURL(external)
		require.NoError(t, err, external)
		require.Equal(t, want, got, external)
	}
}

func TestStudioURLRefusesAnAddressWithNoHost(t *testing.T) {
	for _, external := range []string{"", "/auth/v1", "not a url", "javascript:alert(1)"} {
		_, err := StudioURL(external)
		require.Error(t, err, external)
	}
}
