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

var logLevel = getLogLevel()

func getLogLevel() int {
	level := os.Getenv("LOG_LEVEL")
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
		return 1 // info
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
