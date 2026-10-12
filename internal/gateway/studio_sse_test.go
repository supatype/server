package gateway

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
)

// The Realtime Inspector's stream is Server-Sent Events from the control plane,
// read by Studio through `/studio/proxy`. An event has to reach the browser
// when it is written, not when the upstream response ends: a stream that only
// arrives at its end is no stream. This runs the whole gateway, so a buffering
// layer anywhere between the two proxies fails it.
func TestStudioProxyStreamsServerSentEvents(t *testing.T) {
	clearAmbientEnv(t)
	release := make(chan struct{})

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: ready\ndata: {}\n\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-req.Context().Done():
		}
	}))
	defer controlPlane.Close()

	cfg := &config.Config{
		Mode:               "standalone",
		JWTSecret:          testJWTSecret,
		ServiceRoleKey:     "service-role-key",
		ControlPlaneURL:    controlPlane.URL,
		ControlPlaneSecret: "control-plane-secret",
	}
	manifest := &proxy.RouteManifest{Schema: "public"}
	gateway := httptest.NewServer(buildOuterMux(
		cfg,
		func(*http.Request) *proxy.RouteManifest { return manifest },
		func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") },
		http.NotFoundHandler(),
		nil, "sse-test", nil, nil,
	))
	defer gateway.Close()
	// Deferred after both Close calls, so it runs before them: they wait for the
	// open stream, which waits for this.
	defer close(release)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":          "admin-user",
		"app_metadata": map[string]any{"role": "admin"},
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodGet, gateway.URL+"/studio/proxy/platform/v1/projects/ref/realtime/stream?view=none", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("the stream did not open before the upstream finished: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}

	line := make(chan string, 1)
	go func() {
		text, _ := bufio.NewReader(res.Body).ReadString('\n')
		line <- text
	}()
	select {
	case got := <-line:
		if !strings.HasPrefix(got, "event: ready") {
			t.Fatalf("first line %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the event did not arrive while the upstream was still open")
	}
}
