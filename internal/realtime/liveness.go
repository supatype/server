package realtime

import "net/http"

// LivenessHandler returns a GET-only handler for HTTP health checks (no WebSocket upgrade).
// Mounted at /realtime/v1/health when the realtime hub is enabled.
func LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service":"realtime","websocket":true}`))
	}
}
