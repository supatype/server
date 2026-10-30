package observability

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/bombsimon/logrusr/v3"
	"github.com/gobuffalo/pop/v6"
	"github.com/gobuffalo/pop/v6/logging"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/conf"
	"go.opentelemetry.io/otel"
)

const (
	LOG_SQL_ALL       = "all"
	LOG_SQL_NONE      = "none"
	LOG_SQL_STATEMENT = "statement"
)

var (
	loggingOnce sync.Once
)

// configureLibraryLoggers points Pop's and OpenTelemetry's logging at logrus. A variable so a
// test can see that it ran.
var configureLibraryLoggers = func(sql string) {
	setPopLogger(sql)
	otel.SetLogger(logrusr.New(logrus.StandardLogger().WithField("component", "otel")))
}

type CustomFormatter struct {
	logrus.JSONFormatter
}

func NewCustomFormatter() *CustomFormatter {
	return &CustomFormatter{
		JSONFormatter: logrus.JSONFormatter{
			DisableTimestamp: false,
			TimestampFormat:  time.RFC3339,
		},
	}
}

func (f *CustomFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	// logrus doesn't support formatting the time in UTC so we need to use a custom formatter
	entry.Time = entry.Time.UTC()
	return f.JSONFormatter.Format(entry)
}

// ConfigureLogging sets up the standard logger once per process. A log file that cannot be
// opened or a level that does not parse is returned as an error, but everything else is still
// configured, so the process logs at the default level instead of with half a setup.
func ConfigureLogging(config *conf.LoggingConfig) error {
	var err error
	loggingOnce.Do(func() {
		logrus.SetFormatter(NewCustomFormatter())
		err = errors.Join(setLogFile(config.File), setLogLevel(config.Level))
		if len(config.Fields) > 0 {
			logrus.AddHook(&fieldsHook{fields: logrus.Fields(config.Fields)})
		}
		configureLibraryLoggers(config.SQL)
	})
	return err
}

// setLogFile sends the log to a file when one is configured.
func setLogFile(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0660) //#nosec G302 -- Log files should be rw-rw-r--
	if err != nil {
		return err
	}
	logrus.SetOutput(f)
	logrus.Infof("Set output file to %s", path)
	return nil
}

// setLogLevel applies the configured level, leaving the default in place when it is empty or
// does not parse.
func setLogLevel(configured string) error {
	if configured == "" {
		return nil
	}
	level, err := logrus.ParseLevel(configured)
	if err != nil {
		return err
	}
	logrus.SetLevel(level)
	logrus.Debug("Set log level to: " + logrus.GetLevel().String())
	return nil
}

// fieldsHook adds the configured LOG_FIELDS to every line, without overwriting a field the
// line already carries.
type fieldsHook struct {
	fields logrus.Fields
}

func (h *fieldsHook) Levels() []logrus.Level { return logrus.AllLevels }

func (h *fieldsHook) Fire(entry *logrus.Entry) error {
	for k, v := range h.fields {
		if _, set := entry.Data[k]; !set {
			entry.Data[k] = v
		}
	}
	return nil
}

func setPopLogger(sql string) {
	popLog := logrus.WithField("component", "pop")
	sqlLog := logrus.WithField("component", "sql")

	shouldLogSQL := sql == LOG_SQL_STATEMENT || sql == LOG_SQL_ALL
	shouldLogSQLArgs := sql == LOG_SQL_ALL

	pop.SetLogger(func(lvl logging.Level, s string, args ...interface{}) {
		// Special case SQL logging since we have 2 extra flags to check
		if lvl == logging.SQL {
			if !shouldLogSQL {
				return
			}

			if shouldLogSQLArgs && len(args) > 0 {
				sqlLog.WithField("args", args).Info(s)
			} else {
				sqlLog.Info(s)
			}
			return
		}

		l := popLog
		if len(args) > 0 {
			l = l.WithField("args", args)
		}

		switch lvl {
		case logging.SQL, logging.Debug:
			l.Debug(s)
		case logging.Info:
			l.Info(s)
		case logging.Warn:
			l.Warn(s)
		case logging.Error:
			l.Error(s)
		}
	})
}
