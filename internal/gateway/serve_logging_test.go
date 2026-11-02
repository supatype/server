package gateway

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/observability"
)

// Logging is configured once per process, so this runs in a child process of its own, where
// nothing has configured it yet: exactly the state "supatype-server serve" starts in.
func TestServeConfiguresLoggingFromTheAuthConfig(t *testing.T) {
	if os.Getenv("N15_SERVE_LOGGING_CHILD") == "1" {
		serveLoggingChild(t)
		return
	}
	// #nosec G204,G702 -- the command is this test binary itself, re-executed, with constant arguments.
	cmd := exec.Command(os.Args[0], "-test.run=^TestServeConfiguresLoggingFromTheAuthConfig$", "-test.count=1")
	cmd.Env = append(os.Environ(), "N15_SERVE_LOGGING_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"service":"serve-test"`) || !strings.Contains(string(out), "after the auth config") {
		t.Errorf("want the line logged after loading the auth config to carry LOG_FIELDS, got:\n%s", out)
	}
}

func serveLoggingChild(t *testing.T) {
	if err := conf.LoadFile(authTestConfig); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUPATYPE_LOG_FIELDS", "service:serve-test")
	logrus.SetOutput(os.Stdout)
	if _, err := loadAuthConfig(observability.ConfigureLogging); err != nil {
		t.Fatal(err)
	}
	logrus.Warn("after the auth config")
}

// Logging that cannot be set up, a LOG_FILE that cannot be opened say, does not stop serve: it
// says why and carries on with the configuration it loaded.
func TestServeCarriesOnWhenLoggingCannotBeSetUp(t *testing.T) {
	loadTestEnvRestored(t)
	hook := globalLogHook(t)
	failing := func(*conf.LoggingConfig) error {
		return errors.New("open /nowhere/server.log: no such file or directory")
	}

	cfg, err := loadAuthConfig(failing)
	if err != nil || cfg == nil {
		t.Fatalf("loadAuthConfig: %v, %v; want the configuration despite the logging failure", cfg, err)
	}
	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.ErrorLevel && entry.Message == "unable to configure logging" && strings.Contains(fmt.Sprint(entry.Data[logrus.ErrorKey]), "/nowhere/server.log") {
			return
		}
	}
	t.Errorf("want an error line naming why logging could not be set up, got %d line(s)", len(hook.AllEntries()))
}

// loadTestEnvRestored loads hack/test.env into the process environment, as the server reads it,
// and puts every variable it sets back as it was when the test ends: conf.LoadFile overwrites them
// for good, and the rest of the package would run with them.
func loadTestEnvRestored(t *testing.T) {
	t.Helper()
	values, err := godotenv.Read(authTestConfig)
	if err != nil {
		t.Fatal(err)
	}
	// t.Setenv puts each back as it was, set or not, when the test ends.
	for key := range values {
		t.Setenv(key, os.Getenv(key))
	}
	if err := conf.LoadFile(authTestConfig); err != nil {
		t.Fatal(err)
	}
}

// globalLogHook is a hook recording the global logger's entries for the test, with the logger's
// own hooks put back when it ends.
func globalLogHook(t *testing.T) *logtest.Hook {
	t.Helper()
	saved := logrus.LevelHooks{}
	for level, hooks := range logrus.StandardLogger().Hooks {
		saved[level] = append([]logrus.Hook(nil), hooks...)
	}
	t.Cleanup(func() { logrus.StandardLogger().ReplaceHooks(saved) })
	return logtest.NewGlobal()
}
