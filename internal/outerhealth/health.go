package outerhealth

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/utilities"
)

// processStart records when this process started (for uptime in /health).
var processStart = time.Now()

// ProbeConfig lists optional upstreams for /health and /health/ready checks.
// Empty URLs mean that probe is skipped for readiness (except PostgREST, which must be reachable when URL is non-empty).
type ProbeConfig struct {
	PostgRESTURL string
	GraphQLURL   string
	// GraphQLDisabled mirrors SUPATYPE_GRAPHQL_DISABLED: GraphQL is reported as
	// disabled instead of probed, and does not affect readiness.
	GraphQLDisabled  bool
	StorageLocalPath string
	StorageRemoteURL string
	DenoBaseURL      string
	RealtimeEnabled  bool
	// SelfBaseURL is this server's outer base (e.g. http://127.0.0.1:9999 or https://api.example.com)
	// used to GET /realtime/v1/health when RealtimeEnabled. Normally set by serve via SelfBaseURLForRealtimeProbe;
	// override with SUPATYPE_HEALTH_SELF_BASE_URL for exotic layouts.
	SelfBaseURL string
}

// Attach mounts GET /health and GET /health/ready on r (supatype-server outer mux).
// probes is called on each scrape so health reflects dynamic route manifests.
func Attach(r chi.Router, cfg *config.Config, version string, probes func() ProbeConfig) {
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
	return map[string]any{
		"postgrest": postgrestComponent(probes, timeout),
		"graphql":   graphqlComponent(probes, timeout),
		"storage":   storageComponent(probes, timeout),
		"deno":      denoComponent(probes, timeout),
		"realtime":  realtimeComponent(probes, timeout),
	}
}

// postgrestComponent is required when URL is set; an empty URL is not ready (misconfiguration).
func postgrestComponent(probes ProbeConfig, timeout time.Duration) map[string]any {
	prURL := strings.TrimSpace(probes.PostgRESTURL)
	prReady := prURL != "" && probeHTTPGet(joinURL(prURL, "/"), timeout)
	return map[string]any{"url": prURL, "ready": prReady}
}

// graphqlComponent reports GraphQL as disabled, without probing it and without a
// ready value, when SUPATYPE_GRAPHQL_DISABLED is set: the gateway answers
// /graphql/v1 itself with 501, so the upstream says nothing about what clients get.
func graphqlComponent(probes ProbeConfig, timeout time.Duration) map[string]any {
	if probes.GraphQLDisabled {
		return map[string]any{"enabled": false, "skipped": true, "status": "disabled"}
	}
	gqBase := strings.TrimSpace(utilities.FirstNonEmpty(probes.GraphQLURL, probes.PostgRESTURL))
	gqURL := joinURL(gqBase, "/graphql/v1")
	gqReady := gqBase != "" && probeHTTPGet(gqURL, timeout)
	return map[string]any{"url": gqURL, "ready": gqReady}
}

func storageComponent(probes ProbeConfig, timeout time.Duration) map[string]any {
	if p := strings.TrimSpace(probes.StorageLocalPath); p != "" {
		return map[string]any{"mode": "local", "path": p, "ready": isDirReadable(p)}
	}
	if u := strings.TrimSpace(probes.StorageRemoteURL); u != "" {
		return map[string]any{"mode": "remote", "url": u, "ready": probeHTTPGet(joinURL(u, "/"), timeout)}
	}
	return map[string]any{"skipped": true, "ready": true}
}

func denoComponent(probes ProbeConfig, timeout time.Duration) map[string]any {
	deno := strings.TrimSpace(probes.DenoBaseURL)
	if deno == "" {
		return map[string]any{"skipped": true, "ready": true}
	}
	denoURL := joinURL(deno, "/")
	return map[string]any{"url": denoURL, "ready": probeHTTPGet(denoURL, timeout)}
}

func realtimeComponent(probes ProbeConfig, timeout time.Duration) map[string]any {
	if !probes.RealtimeEnabled {
		return map[string]any{"enabled": false, "skipped": true, "ready": true}
	}
	selfBase := strings.TrimSpace(probes.SelfBaseURL)
	if selfBase == "" {
		return map[string]any{
			"enabled": true, "skipped": true, "ready": false,
			"note": "SelfBaseURL unset; set SUPATYPE_HEALTH_SELF_BASE_URL or fix outer listen URL / TLS domain",
		}
	}
	u := joinURL(selfBase, "/realtime/v1/health")
	return map[string]any{"enabled": true, "url": u, "ready": probeHTTPGet(u, timeout)}
}

func aggregateReady(components map[string]any) bool {
	for _, key := range []string{"postgrest", "graphql", "storage", "deno", "realtime"} {
		v, ok := components[key]
		if !ok {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if skip, _ := m["skipped"].(bool); skip {
			// Realtime must be HTTP-probed when enabled; skipped means we cannot reach the hub.
			if key == "realtime" {
				if en, ok := m["enabled"].(bool); ok && en {
					return false
				}
			}
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
