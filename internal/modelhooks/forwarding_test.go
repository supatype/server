package modelhooks

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/stff"
)

func TestCallPassesTheTriggeringClientAddress(t *testing.T) {
	var gotFor, gotSecret string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFor = r.Header.Get("St-Forwarded-For")
		gotSecret = r.Header.Get("St-Forwarding-Secret")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	sec := conf.SecurityConfiguration{ForwardingSecret: "right"}
	trigger := httptest.NewRequest(http.MethodPost, "http://localhost/rest/v1/posts", nil)
	trigger.RemoteAddr = "10.0.0.7:1"
	trigger.Header.Set("St-Forwarded-For", "203.0.113.70")
	trigger.Header.Set("St-Forwarding-Secret", "right")

	d := NewDispatcher(server.Client(), "")
	stff.Resolver(stff.Options{Security: &sec})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		d.Call(r.Context(), server.URL, EventBeforeChange, HookConfigView{}, []byte(`{}`), 1)
	})).ServeHTTP(httptest.NewRecorder(), trigger)

	if gotFor != "203.0.113.70" {
		t.Fatalf("St-Forwarded-For to the hook = %q, want 203.0.113.70", gotFor)
	}
	if gotSecret != "" {
		t.Fatalf("the forwarding secret reached the hook: %q", gotSecret)
	}
}
