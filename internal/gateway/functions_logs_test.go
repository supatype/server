package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// A worker whose log stream says which worker it is, and which counts the tails it served.
func labelledWorker(t *testing.T, label string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var tails atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/functions/v1/_logs" {
			http.NotFound(w, r)
			return
		}
		tails.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"function\":\"hello\",\"message\":\"from " + label + "\"}\n\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, &tails
}

func tailOf(t *testing.T, d *Deps, function string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/logs/tail"
	if function != "" {
		path += "?function=" + function
	}
	return adminGet(t, d, path)
}

func TestTheLogTailFollowsAFunctionToItsOwnWorker(t *testing.T) {
	own, ownTails := labelledWorker(t, "own")
	project, projectTails := labelledWorker(t, "project")
	d := adminDeps(&config.Config{}, &proxy.RouteManifest{
		FunctionWorkerURLs: map[string]string{"hello": own.URL},
		FunctionsWorkerURL: project.URL,
	})

	rec := tailOf(t, d, "hello")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from own") {
		t.Fatalf("tail = %d %q, want the function's own worker", rec.Code, rec.Body.String())
	}
	if ownTails.Load() != 1 || projectTails.Load() != 0 {
		t.Fatalf("own=%d project=%d, want 1 and 0", ownTails.Load(), projectTails.Load())
	}

	// A function the manifest gives no worker goes to the project's.
	rec = tailOf(t, d, "other")
	if !strings.Contains(rec.Body.String(), "from project") {
		t.Fatalf("tail = %d %q, want the project worker", rec.Code, rec.Body.String())
	}
}

func TestTheLogTailReachesTheProjectWorker(t *testing.T) {
	project, _ := labelledWorker(t, "project")
	d := adminDeps(&config.Config{}, &proxy.RouteManifest{FunctionsWorkerURL: project.URL})

	for _, fn := range []string{"hello", ""} {
		rec := tailOf(t, d, fn)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from project") {
			t.Fatalf("tail(%q) = %d %q", fn, rec.Code, rec.Body.String())
		}
	}
}

func TestTheLogTailReachesTheConfiguredWorker(t *testing.T) {
	static, _ := labelledWorker(t, "static")
	d := adminDeps(&config.Config{FunctionsWorkerURL: static.URL}, &proxy.RouteManifest{})

	rec := tailOf(t, d, "hello")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from static") {
		t.Fatalf("tail = %d %q", rec.Code, rec.Body.String())
	}
}

func TestTheLogTailWithNoWorkerAnswersAsBefore(t *testing.T) {
	d := adminDeps(&config.Config{}, &proxy.RouteManifest{})

	rec := tailOf(t, d, "hello")
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "in-process") {
		t.Fatalf("tail = %d %q, want 501 in-process", rec.Code, rec.Body.String())
	}
}

func TestTheLogTailDoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("data: {\"message\":\"stolen\"}\n\n"))
	}))
	defer target.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/functions/v1/_logs", http.StatusFound)
	}))
	defer redirecting.Close()

	d := adminDeps(&config.Config{}, &proxy.RouteManifest{FunctionWorkerURLs: map[string]string{"hello": redirecting.URL}})
	rec := tailOf(t, d, "hello")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (%s)", rec.Code, rec.Body.String())
	}
	if targetHits.Load() != 0 || strings.Contains(rec.Body.String(), "stolen") {
		t.Errorf("a redirect was followed: hits=%d body=%q", targetHits.Load(), rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), redirecting.URL) {
		t.Errorf("the worker address is in the answer: %s", rec.Body.String())
	}
}
