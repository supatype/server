package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
)

var (
	outerAccessMu     sync.Mutex
	outerAccessLogger *logrus.Logger
)

// configureOuterAccessLogging sets the dedicated logger used by outerAccessLogFormatter (stderr, JSON).
func configureOuterAccessLogging(level string) {
	configureOuterAccessLoggingWriter(os.Stderr, level)
}

func configureOuterAccessLoggingWriter(w io.Writer, level string) {
	outerAccessMu.Lock()
	defer outerAccessMu.Unlock()
	outerAccessLogger = newOuterAccessLogger(w, level)
}

// newOuterAccessLogger builds a logger; does not take outerAccessMu (callers must serialize).
func newOuterAccessLogger(w io.Writer, level string) *logrus.Logger {
	lg := logrus.New()
	lg.SetOutput(w)
	lg.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	lv := strings.TrimSpace(level)
	if lv == "" {
		lv = "info"
	}
	l, err := logrus.ParseLevel(lv)
	if err != nil {
		l = logrus.InfoLevel
	}
	lg.SetLevel(l)
	return lg
}

func outerAccessLog() *logrus.Logger {
	outerAccessMu.Lock()
	defer outerAccessMu.Unlock()
	if outerAccessLogger == nil {
		// Do not call configureOuterAccessLogging here — it would re-enter this mutex and deadlock.
		outerAccessLogger = newOuterAccessLogger(os.Stderr, "info")
	}
	return outerAccessLogger
}

// outerAccessLogFormatter implements chi middleware.LogFormatter for supatype-server's
// outer mux: one JSON line per request with request_id, duration_ms, and optional tenant.
type outerAccessLogFormatter struct{}

func (outerAccessLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	return &outerLogEntry{req: r}
}

type outerLogEntry struct {
	req *http.Request
}

func (e *outerLogEntry) Write(status, bytes int, _ http.Header, elapsed time.Duration, _ interface{}) {
	if e.req == nil {
		return
	}
	p := e.req.URL.Path
	if p == "/health" || p == "/health/ready" {
		return
	}

	fields := logrus.Fields{
		"component":   "outer",
		"request_id":  middleware.GetReqID(e.req.Context()),
		"method":      e.req.Method,
		"path":        p,
		"status":      status,
		"bytes":       bytes,
		"duration_ms": elapsed.Milliseconds(),
	}
	if t := strings.TrimSpace(e.req.Header.Get("X-Supatype-Tenant")); t != "" {
		fields["tenant"] = t
	}
	outerAccessLog().WithFields(fields).Info("request")
}

func (e *outerLogEntry) Panic(v interface{}, stack []byte) {
	reqID := ""
	if e.req != nil {
		reqID = middleware.GetReqID(e.req.Context())
	}
	fields := logrus.Fields{
		"component":  "outer",
		"request_id": reqID,
		"panic":      fmt.Sprintf("%v", v),
		"stack":      string(stack),
	}
	if e.req != nil {
		fields["method"] = e.req.Method
		fields["path"] = e.req.URL.Path
		if t := strings.TrimSpace(e.req.Header.Get("X-Supatype-Tenant")); t != "" {
			fields["tenant"] = t
		}
	}
	outerAccessLog().WithFields(fields).Error("request panicked")
}
