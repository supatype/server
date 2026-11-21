package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/stff"
)

func forwardingWarnings(hook *logtest.Hook) int {
	n := 0
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel && strings.Contains(e.Message, "SUPATYPE_SECURITY_FORWARDING_SECRET") {
			n++
		}
	}
	return n
}

func TestForwardingFrontWarnsOnceWithoutASecret(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(hook.Reset)

	front := forwardingFront(&conf.GlobalConfiguration{})
	h := front(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for range 3 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}
	if got := forwardingWarnings(hook); got != 1 {
		t.Fatalf("forwarding secret warnings = %d, want exactly one", got)
	}

	hook.Reset()
	cfg := &conf.GlobalConfiguration{}
	cfg.Security.ForwardingSecret = "right"
	forwardingFront(cfg)
	if got := forwardingWarnings(hook); got != 0 {
		t.Fatalf("warned with a secret configured (%d)", got)
	}
}

func TestForwardingFrontDecidesFromTheRawPeer(t *testing.T) {
	cfg := &conf.GlobalConfiguration{RateLimitHeader: "X-Real-IP"}
	cfg.Security.ForwardingSecret = "right"
	var decided, secretLeft string
	h := forwardingFront(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		decided, _ = stff.DecidedAddress(r)
		secretLeft = r.Header.Get(stff.SecretHeaderName)
	}))

	r := httptest.NewRequest(http.MethodGet, "/auth/v1/otp", nil)
	r.RemoteAddr = "10.4.4.4:9"
	r.Header.Set(stff.HeaderName, "203.0.113.4")
	r.Header.Set("X-Real-IP", "203.0.113.5")
	r.Header.Set(stff.SecretHeaderName, "wrong")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if decided != "10.4.4.4" {
		t.Fatalf("decided = %q, want the raw peer 10.4.4.4", decided)
	}
	if secretLeft != "" {
		t.Fatalf("St-Forwarding-Secret left on the request: %q", secretLeft)
	}
}
