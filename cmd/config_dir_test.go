package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Supatype Cloud mounts each project's settings as files in a directory passed
// with --config-dir, so a required key can exist only there.
func TestStartupConfigReadsRequiredKeysFromTheConfigDir(t *testing.T) {
	for _, kv := range [][2]string{
		{"SUPATYPE_DB_DRIVER", "postgres"},
		{"SUPATYPE_DB_DATABASE_URL", "postgres://unused"},
		{"SUPATYPE_JWT_SECRET", "secret"},
		{"SUPATYPE_API_EXTERNAL_URL", "http://localhost:9999"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	require.NoError(t, os.Unsetenv("SUPATYPE_SITE_URL"))

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "10-site.env"), []byte("SUPATYPE_SITE_URL=http://from-dir\n"), 0o600))
	t.Cleanup(func() { _ = os.Unsetenv("SUPATYPE_SITE_URL") })

	cfg, err := loadConfigFromFlags("", dir)
	require.NoError(t, err)
	require.Equal(t, "http://from-dir", cfg.SiteURL)
}
