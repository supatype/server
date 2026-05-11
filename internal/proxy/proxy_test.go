package proxy

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestAugmentForwardedHeaders_IPProtoHost(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "203.0.113.7:44444"
	req.Host = "app.example"

	augmentForwardedHeaders(req, "app.example")

	if got := req.Header.Get("X-Forwarded-For"); got != "203.0.113.7" {
		t.Fatalf("X-Forwarded-For = %q want %q", got, "203.0.113.7")
	}
	if got := req.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Fatalf("X-Forwarded-Proto = %q want http", got)
	}
	if got := req.Header.Get("X-Forwarded-Host"); got != "app.example" {
		t.Fatalf("X-Forwarded-Host = %q", got)
	}
	if got := req.Header.Get("Forwarded"); got != `for=203.0.113.7;proto=http;host="app.example"` {
		t.Fatalf("Forwarded = %q", got)
	}
}

func TestAugmentForwardedHeaders_TLSProto(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "[2001:db8::1]:8080"
	req.Host = "secure.example"
	req.TLS = &tls.ConnectionState{}

	augmentForwardedHeaders(req, "secure.example")

	if got := req.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Fatalf("X-Forwarded-Proto = %q want https", got)
	}
	if got := req.Header.Get("X-Forwarded-For"); got != "2001:db8::1" {
		t.Fatalf("X-Forwarded-For = %q", got)
	}
	if got := req.Header.Get("Forwarded"); got != `for="[2001:db8::1]";proto=https;host="secure.example"` {
		t.Fatalf("Forwarded = %q", got)
	}
}

func TestAugmentForwardedHeaders_preservesExistingForwardedProto(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "198.51.100.2:1"
	req.Host = "edge.example"
	req.Header.Set("X-Forwarded-Proto", "https")

	augmentForwardedHeaders(req, "edge.example")

	if got := req.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Fatalf("X-Forwarded-Proto = %q", got)
	}
	if got := req.Header.Get("Forwarded"); got != `for=198.51.100.2;proto=https;host="edge.example"` {
		t.Fatalf("Forwarded = %q", got)
	}
}

func TestAugmentForwardedHeaders_appendsXForwardedForChain(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "198.51.100.9:2"
	req.Header.Set("X-Forwarded-For", "203.0.113.1")

	augmentForwardedHeaders(req, "hop.example")

	if got := req.Header.Get("X-Forwarded-For"); got != "203.0.113.1, 198.51.100.9" {
		t.Fatalf("X-Forwarded-For = %q", got)
	}
	if got := req.Header.Get("Forwarded"); got != `for=198.51.100.9;proto=http;host="hop.example"` {
		t.Fatalf("Forwarded = %q", got)
	}
}

func TestAugmentForwardedHeaders_preservesIncomingForwardedLine(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "198.51.100.1:9"
	req.Host = "app.internal"
	req.Header.Set("Forwarded", `for=192.0.2.1;proto=https`)

	augmentForwardedHeaders(req, "app.internal")

	vals := req.Header.Values("Forwarded")
	if len(vals) != 2 || vals[0] != `for=192.0.2.1;proto=https` {
		t.Fatalf("Forwarded values = %#v", vals)
	}
	if vals[1] != `for=198.51.100.1;proto=http;host="app.internal"` {
		t.Fatalf("appended Forwarded = %q", vals[1])
	}
}
