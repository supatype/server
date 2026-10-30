package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// setMinimalEnv sets the variables a global configuration needs to validate.
func setMinimalEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"SUPATYPE_SITE_URL":         "http://localhost:8080",
		"SUPATYPE_DB_DRIVER":        "postgres",
		"SUPATYPE_DB_DATABASE_URL":  "fake",
		"SUPATYPE_JWT_SECRET":       "secret",
		"SUPATYPE_API_EXTERNAL_URL": "http://localhost:9999",
		"SUPATYPE_MODE":             "dev",
	} {
		t.Setenv(k, v)
	}
}

// A blank SUPATYPE_SMTP_PORT is what a project's .env holds when SMTP is not
// configured. Compose reads it as ${SUPATYPE_SMTP_PORT:-587}; the server must
// too, rather than refusing to parse "" as an int.
func TestBlankTypedSettingFromEnvTakesItsDefault(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("SUPATYPE_SMTP_PORT", "")

	cfg, err := LoadGlobalFromEnv()
	require.NoError(t, err)
	require.Equal(t, 587, cfg.SMTP.Port)
}

func TestBlankTypedSettingFromDotEnvTakesItsDefault(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("SUPATYPE_SMTP_PORT", "x")
	require.NoError(t, os.Unsetenv("SUPATYPE_SMTP_PORT"))
	envFile := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("SUPATYPE_SMTP_PORT=\n"), 0o600))

	cfg, err := LoadGlobal(envFile)
	require.NoError(t, err)
	require.Equal(t, 587, cfg.SMTP.Port)
}
