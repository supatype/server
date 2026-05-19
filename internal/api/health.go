package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

var authStartTime = time.Now()

// HealthResponse represents a health check response
type HealthResponse struct {
	Status        string            `json:"status"`
	Service       string            `json:"service"`
	Version       string            `json:"version,omitempty"`
	UptimeSeconds int64             `json:"uptime_seconds,omitempty"`
	Checks        map[string]string `json:"checks,omitempty"`
	Error         string            `json:"error,omitempty"`
}

// HandleHealth returns basic health status
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:        "healthy",
		Service:       "auth",
		Version:       "0.1.0",
		UptimeSeconds: int64(time.Since(authStartTime).Seconds()),
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleLiveness returns liveness status (is the process running?)
func HandleLiveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

// HandleReadiness returns readiness status (can we serve requests?)
// Checks database connectivity and probes optional upstream URLs (2s timeout each).
// upstreamURLs is a map of label → URL probed with a HEAD request; a non-2xx
// response marks that check as degraded but does not make the overall status 503.
func HandleReadiness(db interface{ Ping() error }, upstreamURLs map[string]string) http.HandlerFunc {
	probeClient := &http.Client{Timeout: 2 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		checks := map[string]string{}
		overallOK := true

		// Check database — hard failure.
		if err := db.Ping(); err != nil {
			checks["database"] = "failed"
			writeJSON(w, http.StatusServiceUnavailable, HealthResponse{
				Status:  "not_ready",
				Service: "auth",
				Checks:  checks,
				Error:   err.Error(),
			})
			return
		}
		checks["database"] = "ok"

		// Probe upstream services — soft failure (logged, not 503).
		for label, rawURL := range upstreamURLs {
			if rawURL == "" {
				continue
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
			if err != nil {
				cancel()
				checks[label] = "error"
				continue
			}
			resp, err := probeClient.Do(req)
			cancel()
			if err != nil {
				checks[label] = "unreachable"
				overallOK = false
			} else {
				if err := resp.Body.Close(); err != nil {
					checks[label] = "error"
					overallOK = false
					continue
				}
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					checks[label] = "ok"
				} else {
					checks[label] = "degraded"
					overallOK = false
				}
			}
		}

		status := "ready"
		code := http.StatusOK
		if !overallOK {
			status = "degraded"
		}
		writeJSON(w, code, HealthResponse{
			Status:  status,
			Service: "auth",
			Checks:  checks,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logrus.WithError(err).Debug("health: write response failed")
	}
}
