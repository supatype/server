package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// clearSupatypeEnv unsets every SUPATYPE_ variable for the test and restores
// them afterwards, so the config under test comes only from the files it names.
func clearSupatypeEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "SUPATYPE_") {
			t.Setenv(name, "")
			require.NoError(t, os.Unsetenv(name))
		}
	}
}

func writeConfigDir(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "site.env"), []byte(body), 0o600))
	return dir
}

const requiredConfig = "SUPATYPE_SITE_URL=http://localhost:8080\n" +
	"SUPATYPE_DB_DRIVER=postgres\n" +
	"SUPATYPE_DB_DATABASE_URL=postgres://u:p@localhost:5432/db\n" +
	"SUPATYPE_OPERATOR_TOKEN=token\n" +
	"SUPATYPE_JWT_SECRET=secret\n" +
	"SUPATYPE_API_EXTERNAL_URL=http://localhost:9999\n"

// Under Supatype Cloud every setting lives in --config-dir. migrate loads its
// config through loadGlobalConfig, so that function must read the directory.
func TestLoadGlobalConfigReadsConfigDir(t *testing.T) {
	clearSupatypeEnv(t)
	prevDir, prevFile := watchDir, configFile
	t.Cleanup(func() { watchDir, configFile = prevDir, prevFile })
	configFile = ""
	watchDir = writeConfigDir(t, requiredConfig)

	cfg := loadGlobalConfig(context.Background())

	require.Equal(t, "http://localhost:8080", cfg.SiteURL)
}
