package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/config"
)

// precedenceChildEnv marks the re-executed test binary that reports what the
// configuration resolved to.
const precedenceChildEnv = "SUPATYPE_TEST_DOTENV_PRECEDENCE_CHILD"

// A dev stack runs the server in /project, next to the project's .env, with
// compose setting the container environment. Package initialisers run before
// any test can change directory, and one of them (gobuffalo/envy, imported by
// pop) used to load ./.env over the environment, so the binary is re-executed
// from a directory holding a .env: that is the only way to put the file in
// front of every initialiser, as it is in front of the real server.
func TestTheContainerEnvironmentWinsOverTheProjectDotEnv(t *testing.T) {
	if os.Getenv(precedenceChildEnv) == "1" {
		reportResolvedPrecedence(t)
		return
	}

	dir := t.TempDir()
	dotenv := "SUPATYPE_MAILER_AUTOCONFIRM=true\nSUPATYPE_SITE_URL=http://from-dotenv\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv), 0o600))

	// os.Executable rather than os.Args[0]: argv[0] is whatever the caller chose
	// to pass, while this is the binary that is actually running.
	exe, err := os.Executable()
	require.NoError(t, err)
	// #nosec G204 -- the command is this test binary itself, re-executed, and every argument is a constant.
	child := exec.Command(exe, "-test.run=^TestTheContainerEnvironmentWinsOverTheProjectDotEnv$", "-test.count=1")
	child.Dir = dir
	child.Env = append(withoutKeys(os.Environ(), "SUPATYPE_"),
		precedenceChildEnv+"=1",
		"SUPATYPE_MAILER_AUTOCONFIRM=false",
		"SUPATYPE_DB_DRIVER=postgres",
		"SUPATYPE_DB_DATABASE_URL=postgres://unused",
		"SUPATYPE_JWT_SECRET=secret",
		"SUPATYPE_API_EXTERNAL_URL=http://localhost:9999",
	)
	out, err := child.CombinedOutput()
	require.NoError(t, err, "child failed:\n%s", out)

	require.Contains(t, string(out), "autoconfirm=false\n", "the container's SUPATYPE_MAILER_AUTOCONFIRM=false must beat the .env's true:\n%s", out)
	require.Contains(t, string(out), "site_url=http://from-dotenv\n", "a key the container leaves unset still comes from the .env:\n%s", out)
}

// reportResolvedPrecedence loads configuration the way gateway.New does and
// prints the two values the parent asserts on.
func reportResolvedPrecedence(t *testing.T) {
	require.NoError(t, conf.LoadFile(""))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, config.LoadDotEnvForServe(cwd, ""))
	cfg, err := conf.LoadGlobalFromEnv()
	require.NoError(t, err)
	fmt.Printf("autoconfirm=%t\nsite_url=%s\n", cfg.Mailer.Autoconfirm, cfg.SiteURL)
}

// withoutKeys drops the variables whose names start with prefix, so the
// developer's own SUPATYPE_ settings cannot leak into the child.
func withoutKeys(env []string, prefix string) []string {
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			kept = append(kept, kv)
		}
	}
	return kept
}
