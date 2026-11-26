package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/stff"
)

// throughResolver runs req through the St-Forwarded-For resolver and returns the request the next
// handler saw, which is what the gateway hands a proxy.
func throughResolver(t *testing.T, sec conf.SecurityConfiguration, req *http.Request) *http.Request {
	t.Helper()
	var out *http.Request
	stff.Resolver(stff.Options{Security: &sec, RateLimitHeader: "X-Real-IP"})(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { out = r }),
	).ServeHTTP(httptest.NewRecorder(), req)
	return out
}

func TestProxyCarriesTheClientAddressAndNeverTheSecret(t *testing.T) {
	var gotFor, gotSecret string
	upstream := echo(t, func(_ http.ResponseWriter, r *http.Request) {
		gotFor = r.Header.Get("St-Forwarded-For")
		gotSecret = r.Header.Get("St-Forwarding-Secret")
	})

	req := httptest.NewRequest(http.MethodGet, "http://app.example/fn", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("St-Forwarded-For", "203.0.113.50")
	req.Header.Set("St-Forwarding-Secret", "right")
	req = throughResolver(t, conf.SecurityConfiguration{ForwardingSecret: "right"}, req)
	// Something on the way re-adds them: the proxy must still not pass them on as sent.
	req.Header.Set("St-Forwarded-For", "198.51.100.99")
	req.Header.Set("St-Forwarding-Secret", "right")

	New(mustParse(t, upstream.URL), ProxyOpts{}).ServeHTTP(httptest.NewRecorder(), req)

	if gotSecret != "" {
		t.Fatalf("the forwarding secret reached the upstream: %q", gotSecret)
	}
	if gotFor != "203.0.113.50" {
		t.Fatalf("St-Forwarded-For upstream = %q, want the resolved client 203.0.113.50", gotFor)
	}
}

func TestProxyNamesThePeerWhenNothingWasResolved(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.6:1"
	req.Header.Set("St-Forwarded-For", "198.51.100.1")
	req.Header.Set("St-Forwarding-Secret", "whatever")

	augmentForwardedHeaders(req, "app.example")

	if got := req.Header.Get("St-Forwarded-For"); got != "10.0.0.6" {
		t.Fatalf("St-Forwarded-For = %q, want the peer 10.0.0.6", got)
	}
	if got := req.Header.Get("St-Forwarding-Secret"); got != "" {
		t.Fatalf("St-Forwarding-Secret left on the request: %q", got)
	}
}
