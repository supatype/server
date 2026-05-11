package outerhealth

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/auth/internal/serverconf"
)

// processStart records when this process started (for uptime in /health).
var processStart = time.Now()

// ProbeConfig lists optional upstreams for /health and /health/ready checks.
// Empty URLs mean that probe is skipped for readiness (except PostgREST, which must be reachable when URL is non-empty).
type ProbeConfig struct {
	PostgRESTURL     string
	GraphQLURL       string
	StorageLocalPath string
	StorageRemoteURL string
	DenoBaseURL      string
	// RealtimeEnabled is informational only (WebSocket hub is in-process; no separate HTTP probe here).
	RealtimeEnabled bool
}

// Attach mounts GET /health and GET /health/ready on r (supatype-server outer mux).
// probes is called on each scrape so health reflects dynamic route manifests.
func Attach(r chi.Router, cfg *serverconf.ServerConfig, version string, probes func() ProbeConfig) {
	timeout := 2 * time.Second

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		components := collectComponents(probes(), timeout)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":         overallStatus(components),
			"version":        version,
			"mode":           cfg.Mode,
			"uptime_seconds": int(time.Since(processStart).Seconds()),
			"components":     components,
		})
	})

	r.Get("/health/ready", func(w http.ResponseWriter, _ *http.Request) {
		components := collectComponents(probes(), timeout)
		ready := aggregateReady(components)
		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":      ready,
			"status":     overallStatus(components),
			"components": components,
			"checked_at": time.Now().UTC().Format(time.RFC3339),
		})
	})
}

func collectComponents(probes ProbeConfig, timeout time.Duration) map[string]any {
	out := make(map[string]any)

	// PostgREST — required when URL is set; empty URL = not ready (misconfiguration).
	prURL := strings.TrimSpace(probes.PostgRESTURL)
	prReady := prURL != "" && probeHTTPGet(joinURL(prURL, "/"), timeout)
	out["postgrest"] = map[string]any{"url": prURL, "ready": prReady}

	gqBase := strings.TrimSpace(firstNonEmpty(probes.GraphQLURL, probes.PostgRESTURL))
	gqURL := joinURL(gqBase, "/graphql/v1")
	gqReady := gqBase != "" && probeHTTPGet(gqURL, timeout)
	out["graphql"] = map[string]any{"url": gqURL, "ready": gqReady}

	switch {
	case strings.TrimSpace(probes.StorageLocalPath) != "":
		p := strings.TrimSpace(probes.StorageLocalPath)
		stReady := isDirReadable(p)
		out["storage"] = map[string]any{"mode": "local", "path": p, "ready": stReady}
	case strings.TrimSpace(probes.StorageRemoteURL) != "":
		u := strings.TrimSpace(probes.StorageRemoteURL)
		stReady := probeHTTPGet(joinURL(u, "/"), timeout)
		out["storage"] = map[string]any{"mode": "remote", "url": u, "ready": stReady}
	default:
		out["storage"] = map[string]any{"skipped": true, "ready": true}
	}

	deno := strings.TrimSpace(probes.DenoBaseURL)
	if deno != "" {
		denoURL := joinURL(deno, "/")
		dReady := probeHTTPGet(denoURL, timeout)
		out["deno"] = map[string]any{"url": denoURL, "ready": dReady}
	} else {
		out["deno"] = map[string]any{"skipped": true, "ready": true}
	}

	out["realtime"] = map[string]any{
		"enabled": probes.RealtimeEnabled,
		"note":    "in-process WebSocket hub; not HTTP-probed from /health/ready",
	}

	return out
}

func aggregateReady(components map[string]any) bool {
	for _, key := range []string{"postgrest", "graphql", "storage", "deno"} {
		v, ok := components[key]
		if !ok {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if skip, _ := m["skipped"].(bool); skip {
			continue
		}
		r, ok := m["ready"].(bool)
		if !ok || !r {
			return false
		}
	}
	return true
}

func overallStatus(components map[string]any) string {
	if aggregateReady(components) {
		return "ok"
	}
	return "degraded"
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func joinURL(base, path string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		return ""
	}
	p := strings.TrimSpace(path)
	if p == "" || p == "/" {
		return b + "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return b + p
}

func probeHTTPGet(url string, timeout time.Duration) bool {
	if strings.TrimSpace(url) == "" {
		return false
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// Upstream alive: accept any non–5xx (incl. 401/404) as reachable.
	return resp.StatusCode < 500
}

func isDirReadable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.IsDir()
}

// probePostgREST is kept for tests that exercised the old helper name.
func probePostgREST(baseURL string, timeout time.Duration) bool {
	if strings.TrimSpace(baseURL) == "" {
		return false
	}
	return probeHTTPGet(joinURL(baseURL, "/"), timeout)
}
