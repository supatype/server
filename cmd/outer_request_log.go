package cmd

import (
	"context"
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

type outerLogCtxKey int

const (
	outerLogKeyMode outerLogCtxKey = iota
	outerLogKeyManagedProjectRef
)

// WithOuterAccessLogContext attaches mode and optional managed project ref to the request
// context so JSON access lines can emit `mode` and resolve `tenant` per A21.
func WithOuterAccessLogContext(mode, managedProjectRef string) func(http.Handler) http.Handler {
	mode = strings.TrimSpace(mode)
	ref := strings.TrimSpace(managedProjectRef)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = context.WithValue(ctx, outerLogKeyMode, mode)
			ctx = context.WithValue(ctx, outerLogKeyManagedProjectRef, ref)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func outerLogMode(r *http.Request) string {
	if r == nil {
		return ""
	}
	v, _ := r.Context().Value(outerLogKeyMode).(string)
	return v
}

func outerLogManagedProjectRef(r *http.Request) string {
	if r == nil {
		return ""
	}
	v, _ := r.Context().Value(outerLogKeyManagedProjectRef).(string)
	return v
}

func tenantForAccessLog(r *http.Request) string {
	if r == nil {
		return ""
	}
	if h := strings.TrimSpace(r.Header.Get("X-Supatype-Tenant")); h != "" {
		return h
	}
	if strings.TrimSpace(outerLogMode(r)) == "managed" {
		if ref := strings.TrimSpace(outerLogManagedProjectRef(r)); ref != "" {
			return ref
		}
	}
	return ""
}

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
// outer mux: one JSON line per request with request_id, method, path, optional query,
// status, duration_ms, mode, and tenant (header or managed fixed project ref).
// /health and /health/ready are logged at Debug to keep default Info noise low.
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
	health := p == "/health" || p == "/health/ready"

	fields := logrus.Fields{
		"component":   "outer",
		"request_id":  middleware.GetReqID(e.req.Context()),
		"method":      e.req.Method,
		"path":        p,
		"status":      status,
		"bytes":       bytes,
		"duration_ms": elapsed.Milliseconds(),
	}
	if m := outerLogMode(e.req); m != "" {
		fields["mode"] = m
	}
	if q := strings.TrimSpace(e.req.URL.RawQuery); q != "" {
		fields["query"] = q
	}
	if t := tenantForAccessLog(e.req); t != "" {
		fields["tenant"] = t
	}
	lg := outerAccessLog().WithFields(fields)
	if health {
		lg.Debug("request")
		return
	}
	lg.Info("request")
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
		if m := outerLogMode(e.req); m != "" {
			fields["mode"] = m
		}
		if q := strings.TrimSpace(e.req.URL.RawQuery); q != "" {
			fields["query"] = q
		}
		if t := tenantForAccessLog(e.req); t != "" {
			fields["tenant"] = t
		}
	}
	outerAccessLog().WithFields(fields).Error("request panicked")
}
