package observability

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/conf"
)

// freshLogging lets a test run ConfigureLogging again and puts the standard logger back.
func freshLogging(t *testing.T) *bytes.Buffer {
	t.Helper()
	std := logrus.StandardLogger()
	level, hooks, libraries := std.GetLevel(), std.ReplaceHooks(make(logrus.LevelHooks)), configureLibraryLoggers
	working := make(logrus.LevelHooks)
	for lvl, hs := range hooks {
		working[lvl] = append([]logrus.Hook(nil), hs...)
	}
	std.ReplaceHooks(working)
	var out bytes.Buffer
	logrus.SetOutput(&out)
	loggingOnce = sync.Once{}
	t.Cleanup(func() {
		std.SetLevel(level)
		std.ReplaceHooks(hooks)
		logrus.SetOutput(io.Writer(os.Stderr))
		configureLibraryLoggers = libraries
		loggingOnce = sync.Once{}
	})
	return &out
}

// A misspelled level used to pass unnoticed: the parse error was checked against the wrong
// variable, so the zero level it returned, panic, was applied. Every warning and fatal
// message below panic was then dropped, and a bad config exited 1 without a word.
func TestConfigureLoggingRefusesAnUnknownLevel(t *testing.T) {
	freshLogging(t)
	logrus.SetLevel(logrus.InfoLevel)
	var librarySQL []string
	configureLibraryLoggers = func(sql string) { librarySQL = append(librarySQL, sql) }

	err := ConfigureLogging(&conf.LoggingConfig{Level: "bogus", SQL: LOG_SQL_ALL})
	if err == nil {
		t.Error("want an error for an unknown log level")
	}
	if logrus.GetLevel() != logrus.InfoLevel {
		t.Errorf("an unknown level changed the level to %s, want it left at info", logrus.GetLevel())
	}
	if len(librarySQL) != 1 || librarySQL[0] != LOG_SQL_ALL {
		t.Errorf("the Pop and OTel loggers were not configured after a bad level: %v", librarySQL)
	}
}

// LOG_FIELDS used to be built into an entry that was thrown away, so no line carried them.
func TestConfigureLoggingAddsTheConfiguredFieldsToEveryLine(t *testing.T) {
	out := freshLogging(t)
	configureLibraryLoggers = func(string) {}

	err := ConfigureLogging(&conf.LoggingConfig{Fields: map[string]interface{}{"service": "auth"}})
	if err != nil {
		t.Fatal(err)
	}
	logrus.Warn("plain")
	logrus.WithField("service", "mine").Warn("own")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"service":"auth"`) {
		t.Errorf("want the configured field on a plain line, got %q", out.String())
	}
	if len(lines) == 2 && !strings.Contains(lines[1], `"service":"mine"`) {
		t.Errorf("a field the call site set was overwritten: %q", lines[1])
	}
}

// The fields go on before anything is logged, so even the line that reports the log file
// carries them.
func TestConfigureLoggingAddsTheFieldsBeforeItsOwnFirstLine(t *testing.T) {
	freshLogging(t)
	configureLibraryLoggers = func(string) {}
	t.Cleanup(func() { SetLogFields(nil) })
	path := t.TempDir() + "/auth.log"

	err := ConfigureLogging(&conf.LoggingConfig{File: path, Fields: map[string]interface{}{"service": "auth"}})
	if err != nil {
		t.Fatal(err)
	}
	// ConfigureLogging keeps the file open for the life of the process; close it so the temp
	// dir can go.
	if f, ok := logrus.StandardLogger().Out.(*os.File); ok {
		_ = f.Close()
	}
	logrus.SetOutput(io.Discard)
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "Set output file to") || !strings.Contains(string(written), `"service":"auth"`) {
		t.Errorf("want the log file line to carry the configured field, got %q", written)
	}
}
