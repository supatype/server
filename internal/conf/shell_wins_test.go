package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The test configuration names a database. A developer or CI job that points
// DATABASE_URL somewhere else has to be believed: when the file replaced the
// environment, a shell value aimed at a private test database was silently
// swapped for the file's one, and the suites truncated whatever listened there.
func TestAVariableAlreadySetBeatsTheConfigFile(t *testing.T) {
	const key = "SUPATYPE_TEST_SHELL_WINS"
	// A committed fixture rather than t.TempDir: TestLoading clears the whole
	// environment, TMP included, so a temp directory cannot be made after it.
	path := filepath.Join("testdata", "shell_wins.env")

	t.Setenv(key, "from-shell")
	require.NoError(t, os.Unsetenv("SUPATYPE_TEST_ONLY_IN_FILE"))
	t.Cleanup(func() { _ = os.Unsetenv("SUPATYPE_TEST_ONLY_IN_FILE") })

	require.NoError(t, loadEnvironment(path))
	require.Equal(t, "from-shell", os.Getenv(key), "loadEnvironment must not replace a variable the shell set")
	require.Equal(t, "file-only", os.Getenv("SUPATYPE_TEST_ONLY_IN_FILE"), "a variable the shell left unset still comes from the file")

	t.Setenv(key, "from-shell-2")
	require.NoError(t, LoadFile(path))
	require.Equal(t, "from-shell-2", os.Getenv(key), "LoadFile must not replace a variable the shell set")
}
