package observability

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp  string                 `json:"timestamp"`
	Level      string                 `json:"level"`
	Service    string                 `json:"service"`
	ProjectRef string                 `json:"project_ref,omitempty"`
	RequestID  string                 `json:"request_id,omitempty"`
	Message    string                 `json:"message"`
	DurationMs int64                  `json:"duration_ms,omitempty"`
	Method     string                 `json:"method,omitempty"`
	Path       string                 `json:"path,omitempty"`
	StatusCode int                    `json:"status_code,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// logLevel filters structured log entries. It defaults to info and is set once
// at startup by Configure.
//
// This used to read LOG_LEVEL at package initialisation, which made the level a
// hidden dependency on the process environment and left it unsettable from
// configuration. Note that LOG_LEVEL is a third, separate control: the auth
// service's own logrus level is GOTRUE_LOG_LEVEL and the gateway access log is
// SUPATYPE_OUTER_LOG_LEVEL. Collapsing the three is a behaviour change and
// belongs with the rename.
var logLevel = levelInfo

const (
	levelDebug = 0
	levelInfo  = 1
	levelWarn  = 2
	levelError = 3
)

// SetStructuredLevel sets the structured log level. An unrecognised or empty level
// leaves it at info, which is what reading an unset LOG_LEVEL always did.
func SetStructuredLevel(level string) {
	logLevel = ParseLevel(level)
}

// ParseLevel maps a level name to its ordering, defaulting to info.
func ParseLevel(level string) int {
	switch level {
	case "debug":
		return levelDebug
	case "info":
		return levelInfo
	case "warn":
		return levelWarn
	case "error":
		return levelError
	default:
		return levelInfo
	}
}

func levelOrder(level string) int {
	switch level {
	case "debug":
		return 0
	case "info":
		return 1
	case "warn":
		return 2
	case "error":
		return 3
	default:
		return 1
	}
}

// Log writes a structured JSON log entry to stdout
func Log(entry LogEntry) {
	if levelOrder(entry.Level) < logLevel {
		return
	}

	entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	entry.Service = "auth"

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal log entry: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stdout, string(data))
}

// Info logs an info-level message
func Info(msg string, projectRef string, metadata map[string]interface{}) {
	Log(LogEntry{Level: "info", Message: msg, ProjectRef: projectRef, Metadata: metadata})
}

// Warn logs a warn-level message
func Warn(msg string, projectRef string, metadata map[string]interface{}) {
	Log(LogEntry{Level: "warn", Message: msg, ProjectRef: projectRef, Metadata: metadata})
}

// Error logs an error-level message
func Error(msg string, projectRef string, metadata map[string]interface{}) {
	Log(LogEntry{Level: "error", Message: msg, ProjectRef: projectRef, Metadata: metadata})
}

// Debug logs a debug-level message
func Debug(msg string, projectRef string, metadata map[string]interface{}) {
	Log(LogEntry{Level: "debug", Message: msg, ProjectRef: projectRef, Metadata: metadata})
}
