package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"
)

// The gateway builds a proxy per request. Each one had a transport of its own, so no connection to
// an upstream was ever reused, and every idle one kept two goroutines and a file descriptor until
// the upstream closed it (0.9.2 release test round 2 review).
func TestProxiesWithTheSameTimeoutShareOneTransport(t *testing.T) {
	target, _ := url.Parse("http://127.0.0.1:1")
	first := New(target, ProxyOpts{RequestTimeout: 7 * time.Second}).(*httputil.ReverseProxy)
	second := New(target, ProxyOpts{RequestTimeout: 7 * time.Second}).(*httputil.ReverseProxy)
	if first.Transport == nil || first.Transport != second.Transport {
		t.Fatalf("each proxy has a transport of its own: %p and %p", first.Transport, second.Transport)
	}
	tr, ok := first.Transport.(*http.Transport)
	if !ok || tr.ResponseHeaderTimeout != 7*time.Second || tr.IdleConnTimeout <= 0 {
		t.Fatalf("transport %+v: want the request timeout and an idle timeout", first.Transport)
	}
}
