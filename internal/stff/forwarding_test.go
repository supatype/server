package stff

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/conf"
)

const (
	testSecret = "s3cret-forwarding-value"
	testPeer   = "10.1.2.3"
	forgedSTFF = "203.0.113.7"
	forgedReal = "203.0.113.8"
	forgedXFF  = "203.0.113.9"
)

// seen is what a handler behind the resolver observed.
type seen struct {
	decided     string
	decidedOK   bool
	client      string
	clientOK    bool
	legacySTFF  string
	secretLeft  string
	forwardLeft string
}

// resolveWith runs one request through Resolver and reports what the next handler saw.
func resolveWith(t *testing.T, sec conf.SecurityConfiguration, prepare func(*http.Request)) seen {
	t.Helper()
	var got seen
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got.decided, got.decidedOK = DecidedAddress(r)
		got.client, got.clientOK = ClientAddress(r.Context())
		got.legacySTFF, _ = GetIPAddress(r)
		got.secretLeft = r.Header.Get(SecretHeaderName)
		got.forwardLeft = r.Header.Get(HeaderName)
	})
	handler := Resolver(Options{Security: &sec, RateLimitHeader: "X-Real-IP", OnInvalid: func(*http.Request, error) {}})(next)
	r := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	r.RemoteAddr = testPeer + ":51234"
	prepare(r)
	handler.ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func forgeAll(r *http.Request) {
	r.Header.Set(HeaderName, forgedSTFF)
	r.Header.Set("X-Real-IP", forgedReal)
	r.Header.Set("X-Forwarded-For", forgedXFF)
}

func configured() conf.SecurityConfiguration {
	return conf.SecurityConfiguration{ForwardingSecret: testSecret, StForwardedForEnabled: true}
}

func TestResolverIgnoresForwardedAddressesWithoutTheSecret(t *testing.T) {
	got := resolveWith(t, configured(), forgeAll)
	if !got.decidedOK || got.decided != testPeer {
		t.Fatalf("decided = %q (%v), want the raw peer %q", got.decided, got.decidedOK, testPeer)
	}
	if got.legacySTFF != "" {
		t.Fatalf("an untrusted St-Forwarded-For reached the legacy context value: %q", got.legacySTFF)
	}
}

func TestResolverIgnoresForwardedAddressesWithAWrongSecret(t *testing.T) {
	got := resolveWith(t, configured(), func(r *http.Request) {
		forgeAll(r)
		r.Header.Set(SecretHeaderName, testSecret+"x")
	})
	if !got.decidedOK || got.decided != testPeer {
		t.Fatalf("decided = %q (%v), want the raw peer %q", got.decided, got.decidedOK, testPeer)
	}
}

func TestResolverBelievesStForwardedForWithTheSecret(t *testing.T) {
	got := resolveWith(t, configured(), func(r *http.Request) {
		forgeAll(r)
		r.Header.Set(SecretHeaderName, testSecret)
	})
	if !got.decidedOK || got.decided != forgedSTFF {
		t.Fatalf("decided = %q (%v), want St-Forwarded-For %q", got.decided, got.decidedOK, forgedSTFF)
	}
	if got.client != forgedSTFF {
		t.Fatalf("client = %q, want %q", got.client, forgedSTFF)
	}
}

func TestResolverFallsBackToTheRateLimitHeaderWithTheSecret(t *testing.T) {
	got := resolveWith(t, configured(), func(r *http.Request) {
		r.Header.Set(HeaderName, "not-an-ip")
		r.Header.Set("X-Real-IP", forgedReal+", 198.51.100.1")
		r.Header.Set(SecretHeaderName, testSecret)
	})
	if got.decided != forgedReal {
		t.Fatalf("decided = %q, want the first X-Real-IP value %q", got.decided, forgedReal)
	}
}

func TestResolverFallsBackToThePeerWithTheSecret(t *testing.T) {
	got := resolveWith(t, configured(), func(r *http.Request) {
		r.Header.Set(SecretHeaderName, testSecret)
	})
	if got.decided != testPeer {
		t.Fatalf("decided = %q, want the raw peer %q", got.decided, testPeer)
	}
}

func TestResolverStripsBothHeaders(t *testing.T) {
	for name, sec := range map[string]conf.SecurityConfiguration{
		"configured":     configured(),
		"not configured": {StForwardedForEnabled: true},
	} {
		t.Run(name, func(t *testing.T) {
			got := resolveWith(t, sec, func(r *http.Request) {
				forgeAll(r)
				r.Header.Set(SecretHeaderName, testSecret)
			})
			if got.secretLeft != "" || got.forwardLeft != "" {
				t.Fatalf("headers left on the request: secret %q, St-Forwarded-For %q", got.secretLeft, got.forwardLeft)
			}
		})
	}
}

func TestResolverWithoutASecretKeepsTheOlderBehaviour(t *testing.T) {
	got := resolveWith(t, conf.SecurityConfiguration{StForwardedForEnabled: true}, forgeAll)
	if got.decidedOK {
		t.Fatalf("an address was decided with no secret configured: %q", got.decided)
	}
	if got.legacySTFF != forgedSTFF {
		t.Fatalf("legacy St-Forwarded-For = %q, want %q", got.legacySTFF, forgedSTFF)
	}
	if got.client != forgedSTFF {
		t.Fatalf("client = %q, want %q", got.client, forgedSTFF)
	}
}

func TestResolverWithoutASecretFallsBackToTheHeaderThenThePeer(t *testing.T) {
	got := resolveWith(t, conf.SecurityConfiguration{}, forgeAll)
	if got.client != forgedReal {
		t.Fatalf("client = %q, want X-Real-IP %q (St-Forwarded-For is off)", got.client, forgedReal)
	}
	got = resolveWith(t, conf.SecurityConfiguration{}, func(*http.Request) {})
	if got.client != testPeer {
		t.Fatalf("client = %q, want the peer %q", got.client, testPeer)
	}
}

func TestResolverDecidesOnceForTheOuterLayer(t *testing.T) {
	sec := configured()
	var decided string
	inner := Resolver(Options{Security: &conf.SecurityConfiguration{}})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		decided, _ = DecidedAddress(r)
	}))
	outer := Resolver(Options{Security: &sec})(inner)
	r := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	r.RemoteAddr = testPeer + ":1"
	forgeAll(r)
	outer.ServeHTTP(httptest.NewRecorder(), r)
	if decided != testPeer {
		t.Fatalf("decided = %q, want the outer layer's answer %q", decided, testPeer)
	}
}

func TestWarnUnconfiguredOnlyWhenNoSecret(t *testing.T) {
	var lines []string
	warn := func(msg string) { lines = append(lines, msg) }
	WarnIfUnconfigured(&conf.SecurityConfiguration{ForwardingSecret: testSecret}, warn)
	if len(lines) != 0 {
		t.Fatalf("warned with a secret configured: %v", lines)
	}
	WarnIfUnconfigured(&conf.SecurityConfiguration{}, warn)
	if len(lines) != 1 {
		t.Fatalf("warnings = %d, want exactly one", len(lines))
	}
}
