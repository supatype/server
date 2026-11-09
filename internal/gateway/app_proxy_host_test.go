package gateway

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
)

// 0.9.1 release test: on Docker dev the app upstream is the developer's Vite on
// the host, reached as host.docker.internal:5173. Forwarded with that as its
// Host, Vite 6.0.9 and later answered 403 "Blocked request. This host is not
// allowed." The app proxy names the upstream as localhost on its own port, the
// one host every dev server accepts, and the address the person typed travels
// in X-Forwarded-Host.

func appProxyConfig(upstream string) *config.Config {
	return &config.Config{Mode: "dev", AppMode: "proxy", AppUpstream: upstream}
}

func TestTheAppProxySendsLocalhostAsTheHost(t *testing.T) {
	var gotHost, gotForwardedHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotForwardedHost = r.Host, r.Header.Get("X-Forwarded-Host")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	port := mustURL(t, upstream.URL).Port()

	handler := buildApp(&Deps{Config: appProxyConfig(upstream.URL)})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "myapp.test:8000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if want := "localhost:" + port; gotHost != want {
		t.Errorf("upstream saw Host %q, want %q", gotHost, want)
	}
	if gotForwardedHost != "myapp.test:8000" {
		t.Errorf("X-Forwarded-Host = %q, want the address the client used", gotForwardedHost)
	}
}

// Vite checks the Host of its HMR socket too, so the upgrade carries the same Host.
func TestTheAppProxySendsLocalhostAsTheHostOnAWebSocketUpgrade(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	seen := make(chan *http.Request, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		seen <- r
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	}()

	target := "http://" + ln.Addr().String()
	gateway := httptest.NewServer(buildApp(&Deps{Config: appProxyConfig(target)}))
	defer gateway.Close()

	req, _ := http.NewRequest(http.MethodGet, gateway.URL+"/", nil)
	req.Host = "myapp.test:8000"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	r := <-seen
	if want := "localhost:" + mustURL(t, target).Port(); r.Host != want {
		t.Errorf("upstream saw Host %q, want %q", r.Host, want)
	}
	if got := r.Header.Get("X-Forwarded-Host"); got != "myapp.test:8000" {
		t.Errorf("X-Forwarded-Host = %q, want the address the client used", got)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Vite's HMR proxy at /_vite reaches the same dev server, which checks the
// Host there too, so it names the upstream as localhost as well.
func TestTheViteProxySendsLocalhostAsTheHost(t *testing.T) {
	var gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler := buildVite(&Deps{Config: &config.Config{Mode: "dev", ViteDevURL: upstream.URL}})
	if handler == nil {
		t.Fatal("no /_vite proxy mounted")
	}
	req := httptest.NewRequest(http.MethodGet, "/_vite/client", nil)
	req.Host = "myapp.test:8000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if want := "localhost:" + mustURL(t, upstream.URL).Port(); gotHost != want {
		t.Errorf("upstream saw Host %q, want %q", gotHost, want)
	}
}
