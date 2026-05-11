package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
)

func TestOuterAccessLogFormatter_JSONFields(t *testing.T) {
	log := logrus.StandardLogger()
	prevOut, prevFmt, prevLevel := log.Out, log.Formatter, log.Level
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFormatter(prevFmt)
		log.SetLevel(prevLevel)
	})

	var buf bytes.Buffer
	logrus.SetOutput(&buf)
	logrus.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339})
	logrus.SetLevel(logrus.InfoLevel)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
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

func TestOuterAccessLogFormatter_SkipsHealthPaths(t *testing.T) {
	log := logrus.StandardLogger()
	prevOut, prevFmt, prevLevel := log.Out, log.Formatter, log.Level
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFormatter(prevFmt)
		log.SetLevel(prevLevel)
	})

	var buf bytes.Buffer
	logrus.SetOutput(&buf)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.SetLevel(logrus.InfoLevel)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RequestLogger(outerAccessLogFormatter{}))
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if buf.Len() != 0 {
		t.Fatalf("expected no log for /health, got: %s", buf.String())
	}
}
