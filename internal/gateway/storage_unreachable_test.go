package gateway

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// closedPortURL is an http URL on a loopback port nothing listens on, so a dial refuses.
func closedPortURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}

// 0.9.2 release test round 2 (R2-S1): a storage service that crashed while dev ran left the
// gateway answering a bare 502 until the supervisor gave up. A storage upstream that refuses the
// connection is storage being down, so the answer is the one storage switched off gives.
func TestStorageThatRefusesTheConnectionAnswers503(t *testing.T) {
	cfg := config.Config{Mode: "dev", StorageURL: closedPortURL(t)}
	d := depsFor(t, &cfg, &proxy.RouteManifest{Schema: "public"})

	rec := httptest.NewRecorder()
	buildStorage(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/object/public/avatars/a.txt", nil))

	off := httptest.NewRecorder()
	storageUnavailable(off, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d %q, want 503", rec.Code, rec.Body.String())
	}
	var got, want map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	if err := json.Unmarshal(off.Body.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	if got["code"] != "storage_unavailable" || got["message"] != want["message"] {
		t.Errorf("body = %v, want the switched-off body %v", got, want)
	}
}

// A storage upstream that connects and then fails is not storage being off: that stays a 502.
func TestStorageThatFailsAfterConnectingStays502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijacker")
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(upstream.Close)
	cfg := config.Config{Mode: "dev", StorageURL: upstream.URL}
	d := depsFor(t, &cfg, &proxy.RouteManifest{Schema: "public"})

	rec := httptest.NewRecorder()
	buildStorage(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/object/public/avatars/a.txt", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d %q, want 502", rec.Code, rec.Body.String())
	}
}
