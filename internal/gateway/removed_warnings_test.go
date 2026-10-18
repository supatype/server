package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// A variable that has been removed outright, rather than renamed, is named in a
// warning and the server still starts: ignoring STUDIO_OPEN_DEV leaves the
// deployment locked down, so refusing to start would only break a stack that
// is already safe. config's own tests cover the list; this covers New reading it.
func TestNewWarnsAboutARemovedVariableAndStarts(t *testing.T) {
	requireAuthEnvironment(t)
	t.Setenv("STUDIO_OPEN_DEV", "1")

	logger := logrus.StandardLogger()
	previous := logger.ReplaceHooks(make(logrus.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(previous) })
	hook := logtest.NewLocal(logger)

	ctx, cancel := context.WithCancel(context.Background())
	handler, drain, err := New(ctx)
	cancel()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	drain()
	if handler == nil {
		t.Fatal("no handler")
	}

	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.WarnLevel && strings.Contains(entry.Message, "STUDIO_OPEN_DEV") {
			return
		}
	}
	t.Error("no warning names STUDIO_OPEN_DEV")
}
