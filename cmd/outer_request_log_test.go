package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func TestOuterAccessLogFormatter_JSONFields(t *testing.T) {
	var buf bytes.Buffer
	configureOuterAccessLoggingWriter(&buf, "info")
	t.Cleanup(func() { configureOuterAccessLogging("info") })

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(WithOuterAccessLogContext("dev", ""))
	r.Use(middleware.RequestLogger(outerAccessLogFormatter{}))
	r.Get("/api/x", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("X-Supatype-Tenant", "proj-ref-1")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("status %d", rr.Code)
	}

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line: %v\n%s", err, buf.String())
	}
	if line["component"] != "outer" {
		t.Fatalf("component: %#v", line["component"])
	}
	if line["mode"] != "dev" {
		t.Fatalf("mode: %#v", line["mode"])
	}
	if line["tenant"] != "proj-ref-1" {
		t.Fatalf("tenant: %#v", line["tenant"])
	}
	if line["path"] != "/api/x" {
		t.Fatalf("path: %#v", line["path"])
	}
	if line["method"] != "GET" {
		t.Fatalf("method: %#v", line["method"])
	}
	if int(line["status"].(float64)) != http.StatusTeapot {
		t.Fatalf("status: %#v", line["status"])
	}
	if line["request_id"] == nil || line["request_id"] == "" {
		t.Fatalf("missing request_id: %#v", line["request_id"])
	}
	ms, ok := line["duration_ms"].(float64)
	if !ok || ms < 0 {
		t.Fatalf("duration_ms: %#v", line["duration_ms"])
	}
}

func TestOuterAccessLogFormatter_healthAtInfoQuietAtDebugLogged(t *testing.T) {
	var buf bytes.Buffer
	configureOuterAccessLoggingWriter(&buf, "info")
	t.Cleanup(func() { configureOuterAccessLogging("info") })

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(WithOuterAccessLogContext("dev", ""))
	r.Use(middleware.RequestLogger(outerAccessLogFormatter{}))
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	if buf.Len() != 0 {
		t.Fatalf("expected no Info log for /health at info level, got: %s", buf.String())
	}

	configureOuterAccessLoggingWriter(&buf, "debug")
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line: %v\n%s", err, buf.String())
	}
	if line["path"] != "/health/ready" {
		t.Fatalf("path: %#v", line["path"])
	}
	if line["level"] != "debug" {
		t.Fatalf("level: %#v", line["level"])
	}
	if line["msg"] != "request" {
		t.Fatalf("msg: %#v", line["msg"])
	}
}

func TestOuterAccessLogFormatter_queryField(t *testing.T) {
	var buf bytes.Buffer
	configureOuterAccessLoggingWriter(&buf, "info")
	t.Cleanup(func() { configureOuterAccessLogging("info") })

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(WithOuterAccessLogContext("standalone", ""))
	r.Use(middleware.RequestLogger(outerAccessLogFormatter{}))
	r.Get("/rest/v1/x", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/rest/v1/x?select=id", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line: %v\n%s", err, buf.String())
	}
	if line["query"] != "select=id" {
		t.Fatalf("query: %#v", line["query"])
	}
	if line["mode"] != "standalone" {
		t.Fatalf("mode: %#v", line["mode"])
	}
}

func TestOuterAccessLogFormatter_managedTenantFromProjectRef(t *testing.T) {
	var buf bytes.Buffer
	configureOuterAccessLoggingWriter(&buf, "info")
	t.Cleanup(func() { configureOuterAccessLogging("info") })

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(WithOuterAccessLogContext("managed", "fixed-ref"))
	r.Use(middleware.RequestLogger(outerAccessLogFormatter{}))
	r.Get("/api/z", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/z", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line: %v\n%s", err, buf.String())
	}
	if line["tenant"] != "fixed-ref" {
		t.Fatalf("tenant: %#v", line["tenant"])
	}
	if line["mode"] != "managed" {
		t.Fatalf("mode: %#v", line["mode"])
	}
}

func TestOuterAccessLogFormatter_levelFiltersInfo(t *testing.T) {
	var buf bytes.Buffer
	configureOuterAccessLoggingWriter(&buf, "error")
	t.Cleanup(func() { configureOuterAccessLogging("info") })

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(WithOuterAccessLogContext("dev", ""))
	r.Use(middleware.RequestLogger(outerAccessLogFormatter{}))
	r.Get("/api/y", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/y", nil))
	if buf.Len() != 0 {
		t.Fatalf("expected no log at error level for Info access line, got: %s", buf.String())
	}
}
