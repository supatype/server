package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	shellWinsKey  = "SUPATYPE_TEST_SHELL_WINS"
	shellWinsOnly = "SUPATYPE_TEST_ONLY_IN_FILE"
)

// A committed fixture rather than t.TempDir: TestLoading clears the whole
// environment, TMP included, so a temp directory cannot be made after it.
var shellWinsFixture = filepath.Join("testdata", "shell_wins.env")

func clearShellWinsOnly(t *testing.T) {
	t.Helper()
	require.NoError(t, os.Unsetenv(shellWinsOnly))
	t.Cleanup(func() { _ = os.Unsetenv(shellWinsOnly) })
}

// The test configuration names a database. A developer or CI job that points
// DATABASE_URL somewhere else has to be believed: when the file replaced the
// environment, a shell value aimed at a private test database was silently
// swapped for the file's one, and the suites truncated whatever listened there.
func TestTestLoaderKeepsAVariableAlreadySet(t *testing.T) {
	t.Setenv(shellWinsKey, "from-shell")
	clearShellWinsOnly(t)

	require.NoError(t, loadEnvironmentKeepingEnv(shellWinsFixture))
	require.Equal(t, "from-shell", os.Getenv(shellWinsKey), "the test loader must not replace a variable the shell set")
	require.Equal(t, "file-only", os.Getenv(shellWinsOnly), "a variable the shell left unset still comes from the file")

	t.Setenv(shellWinsKey, "from-shell-2")
	require.NoError(t, LoadFileKeepingEnv(shellWinsFixture))
	require.Equal(t, "from-shell-2", os.Getenv(shellWinsKey), "LoadFileKeepingEnv must not replace a variable the shell set")
}

// Production reads the server's -c file with the file winning, as it always
// did. Only the test loader lets the environment win.
func TestConfigFileFlagStillOverridesTheEnvironment(t *testing.T) {
	t.Setenv(shellWinsKey, "from-shell")
	clearShellWinsOnly(t)

	require.NoError(t, loadEnvironment(shellWinsFixture))
	require.NotEqual(t, "from-shell", os.Getenv(shellWinsKey), "a -c file must replace the environment")
	require.Equal(t, "file-only", os.Getenv(shellWinsOnly))

	t.Setenv(shellWinsKey, "from-shell-2")
	require.NoError(t, LoadFile(shellWinsFixture))
	require.NotEqual(t, "from-shell-2", os.Getenv(shellWinsKey), "LoadFile with a file must replace the environment")
}
