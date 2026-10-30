package gateway

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/conf"
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
	if _, err := loadAuthConfig(); err != nil {
		t.Fatal(err)
	}
	logrus.Warn("after the auth config")
}
