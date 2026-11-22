package conf

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Users are stored with the audience the server runs with, and a sign-in, a
// lookup by email or phone and the admin check all compare it. A host that never
// set SUPATYPE_JWT_AUD stored users with an empty one, so the unset case must
// land on the same value every host that did set it uses.
func TestJWTAudienceDefaultsToAuthenticated(t *testing.T) {
	setMinimalEnv(t)
	require.NoError(t, os.Unsetenv("SUPATYPE_JWT_AUD"))

	cfg, err := LoadGlobalFromEnv()
	require.NoError(t, err)
	require.Equal(t, "authenticated", cfg.JWT.Aud)
}

func TestAnExplicitJWTAudienceIsKept(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("SUPATYPE_JWT_AUD", "members")

	cfg, err := LoadGlobalFromEnv()
	require.NoError(t, err)
	require.Equal(t, "members", cfg.JWT.Aud)
}
