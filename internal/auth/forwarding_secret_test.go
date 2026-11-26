package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sebest/xff"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/stff"
	"github.com/supatype/server/internal/utilities"
)

// keysBehindRouterFront runs one request through the St-Forwarded-For resolver and then the
// X-Forwarded-For rewrite, in the order the auth router mounts them, and reports the rate-limit key
// and the audit address the handler would use.
func keysBehindRouterFront(t *testing.T, cfg *conf.GlobalConfiguration, prepare func(*http.Request)) (limitKey, auditIP string) {
	t.Helper()
	api := &API{config: cfg}
	xffmw, err := xff.Default()
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		limitKey = api.rateLimitKey(r)
		auditIP = utilities.GetIPAddress(r)
	})
	front := stff.Resolver(stff.Options{
		Security:        &cfg.Security,
		RateLimitHeader: cfg.RateLimitHeader,
		OnInvalid:       func(*http.Request, error) {},
	})(xffmw.Handler(handler))

	r := httptest.NewRequest(http.MethodPost, "http://localhost/otp", nil)
	r.RemoteAddr = "10.9.8.7:40000"
	prepare(r)
	front.ServeHTTP(httptest.NewRecorder(), r)
	return limitKey, auditIP
}

func forwardingConfig(secret string) *conf.GlobalConfiguration {
	cfg := &conf.GlobalConfiguration{RateLimitHeader: "X-Real-IP"}
	cfg.Security.StForwardedForEnabled = true
	cfg.Security.ForwardingSecret = secret
	return cfg
}

func forgeEveryAddress(r *http.Request) {
	r.Header.Set(stff.HeaderName, "203.0.113.1")
	r.Header.Set("X-Real-IP", "203.0.113.2")
	r.Header.Set("X-Forwarded-For", "203.0.113.3")
}

func TestForgedAddressesWithoutTheForwardingSecretDoNotMoveTheKey(t *testing.T) {
	for name, prepare := range map[string]func(*http.Request){
		"no secret": forgeEveryAddress,
		"wrong secret": func(r *http.Request) {
			forgeEveryAddress(r)
			r.Header.Set(stff.SecretHeaderName, "guess")
		},
	} {
		t.Run(name, func(t *testing.T) {
			key, ip := keysBehindRouterFront(t, forwardingConfig("right"), prepare)
			if key != "10.9.8.7" || ip != "10.9.8.7" {
				t.Fatalf("rate-limit key %q, audit IP %q, want both the peer 10.9.8.7", key, ip)
			}
		})
	}
}

func TestTheForwardingSecretLetsStForwardedForNameTheClient(t *testing.T) {
	key, ip := keysBehindRouterFront(t, forwardingConfig("right"), func(r *http.Request) {
		forgeEveryAddress(r)
		r.Header.Set(stff.SecretHeaderName, "right")
	})
	if key != "203.0.113.1" || ip != "203.0.113.1" {
		t.Fatalf("rate-limit key %q, audit IP %q, want both St-Forwarded-For 203.0.113.1", key, ip)
	}
}

func TestNoForwardingSecretKeepsTheOlderKeys(t *testing.T) {
	key, ip := keysBehindRouterFront(t, forwardingConfig(""), forgeEveryAddress)
	if key != "203.0.113.1" || ip != "203.0.113.1" {
		t.Fatalf("rate-limit key %q, audit IP %q, want St-Forwarded-For as before", key, ip)
	}

	cfg := forwardingConfig("")
	cfg.Security.StForwardedForEnabled = false
	key, ip = keysBehindRouterFront(t, cfg, forgeEveryAddress)
	if key != "203.0.113.2" || ip != "203.0.113.3" {
		t.Fatalf("rate-limit key %q, audit IP %q, want X-Real-IP and X-Forwarded-For as before", key, ip)
	}
}
