package gateway

import (
	"net/http"
	"testing"
)

// TestFunctionsPathWithDotSegmentsIsRefused locks the bypass of the reserved-route rule. The proxy
// judges the first segment it sees, but the worker resolves "." and ".." in the URL it receives, so
// "x/../_logs" looked like a call to a function named x and was answered by the worker's own _logs.
// Every spelling of a dot segment is refused before any upstream is chosen, on both mounts.
func TestFunctionsPathWithDotSegmentsIsRefused(t *testing.T) {
	targets := []string{
		"/functions/v1/x/../_logs",
		"/functions/v1/x/../_functions",
		"/functions/v1/x/../hooks/moderate",
		"/functions/v1/./_functions",
		"/functions/v1/x/%2e%2e/_logs",
		"/functions/v1/x/%2E%2E/hooks/moderate",
		"/functions/v1/x/.%2e/_logs",
		"/functions/v1/x/%2e./_logs",
		"/functions/v1/%2e/_functions",
		"/functions/v1/x%2f..%2f_logs",
		"/functions/v1/x/%252e%252e/_logs",
		"/functions/v1/x/..",
		"/functions/v1/admin/../_logs",
		"/functions/v1/admin/%2e%2e/_logs",
		"/functions/v1/admin/./x",
	}
	for _, mode := range []string{"dev", "managed"} {
		for _, target := range targets {
			t.Run(mode+" "+target, func(t *testing.T) {
				r := newRig(t, mode)
				for _, upgrade := range []bool{false, true} {
					rec := r.do(http.MethodGet, target, func(req *http.Request) {
						if mode == "managed" {
							withTenant(req)
							req.Header.Set("apikey", signedAPIKey(t, "anon"))
						}
						if upgrade {
							req.Header.Set("Connection", "Upgrade")
							req.Header.Set("Upgrade", "websocket")
						}
					})
					if rec.Code != http.StatusBadRequest {
						t.Errorf("upgrade=%v status = %d, want 400", upgrade, rec.Code)
					}
					if got, ok := r.lastHit(); ok {
						t.Errorf("upgrade=%v reached %s at %q; it must not leave the gateway", upgrade, got.upstream, got.path)
					}
				}
			})
		}
	}
}

// TestFunctionsPathWithoutDotSegmentsStillRoutes keeps the refusal from catching names that merely
// contain dots.
func TestFunctionsPathWithoutDotSegmentsStillRoutes(t *testing.T) {
	r := newRig(t, "dev")
	for _, target := range []string{"/functions/v1/echo", "/functions/v1/echo/a.b", "/functions/v1/echo/...", "/functions/v1/admin/echo"} {
		rec := r.do(http.MethodGet, target, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", target, rec.Code)
		}
	}
}
