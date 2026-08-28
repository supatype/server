package proxy

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// The gaps: what the proxy sends upstream, what it refuses to relay back, and
// what the manifest watcher does when the file it is watching is half-written.

// echo returns a server that reports back what it received.
func echo(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// ─── What reaches the upstream ────────────────────────────────────────────────

func TestNewForwardsToTheTarget(t *testing.T) {
	var gotPath, gotHost, gotQuery string
	upstream := echo(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost, gotQuery = r.URL.Path, r.Host, r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream"))
	})

	target := mustParse(t, upstream.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts?select=id", nil)
	New(target, ProxyOpts{StripPrefix: "/rest/v1"}).ServeHTTP(rec, req)

	if gotPath != "/posts" {
		t.Errorf("upstream path = %q, want the prefix stripped", gotPath)
	}
	if gotQuery != "select=id" {
		t.Errorf("upstream query = %q", gotQuery)
	}
	if gotHost != target.Host {
		t.Errorf("upstream Host = %q, want the target's", gotHost)
	}
	if rec.Body.String() != "upstream" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// Stripping the whole path must leave a path, not an empty one: an empty
// request-target is not a valid HTTP request.
func TestStrippingTheWholePathLeavesRoot(t *testing.T) {
	var gotPath string
	upstream := echo(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rest/v1", nil)
	New(mustParse(t, upstream.URL), ProxyOpts{StripPrefix: "/rest/v1"}).ServeHTTP(rec, req)

	if gotPath != "/" {
		t.Errorf("upstream path = %q, want /", gotPath)
	}
}

// Per-request headers beat the standing ones, which is what lets a tenant's
// schema override a default.
func TestHeaderFuncWinsOverHeaderOverrides(t *testing.T) {
	var got http.Header
	upstream := echo(t, func(w http.ResponseWriter, r *http.Request) { got = r.Header.Clone() })

	rec := httptest.NewRecorder()
	New(mustParse(t, upstream.URL), ProxyOpts{
		HeaderOverrides: map[string]string{"Accept-Profile": "public", "X-Standing": "yes"},
		HeaderFunc: func(*http.Request) map[string]string {
			return map[string]string{"Accept-Profile": "tenant"}
		},
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got.Get("Accept-Profile") != "tenant" {
		t.Errorf("Accept-Profile = %q, want the per-request value", got.Get("Accept-Profile"))
	}
	if got.Get("X-Standing") != "yes" {
		t.Errorf("the standing header was dropped: %q", got.Get("X-Standing"))
	}
}

// The gateway is the only source of CORS truth. An upstream that sets its own
// would otherwise reach the browser alongside, and two Allow-Origin headers is
// a failed request.
func TestUpstreamCORSHeadersAreStripped(t *testing.T) {
	upstream := echo(t, func(w http.ResponseWriter, _ *http.Request) {
		for _, name := range []string{
			"Access-Control-Allow-Origin", "Access-Control-Allow-Methods",
			"Access-Control-Allow-Headers", "Access-Control-Expose-Headers",
			"Access-Control-Allow-Credentials", "Access-Control-Max-Age",
		} {
			w.Header().Set(name, "upstream-value")
		}
		w.Header().Set("X-Kept", "yes")
	})

	rec := httptest.NewRecorder()
	New(mustParse(t, upstream.URL), ProxyOpts{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for name := range rec.Header() {
		if strings.HasPrefix(name, "Access-Control-") {
			t.Errorf("%s survived: %q", name, rec.Header().Get(name))
		}
	}
	if rec.Header().Get("X-Kept") != "yes" {
		t.Error("a header that is not CORS was stripped too")
	}
}

// A timeout is what stops one slow upstream holding a connection open
// indefinitely.
func TestARequestTimeoutIsApplied(t *testing.T) {
	upstream := echo(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	New(mustParse(t, upstream.URL), ProxyOpts{RequestTimeout: 20 * time.Millisecond}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when the upstream did not answer in time", rec.Code)
	}
}

// ─── Forwarded headers ────────────────────────────────────────────────────────

// The upstream needs the client's address and the host the client actually
// asked for, not this proxy's.
func TestForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	augmentForwardedHeaders(req, "api.example.com")

	if got := req.Header.Get("X-Forwarded-For"); got != "203.0.113.7" {
		t.Errorf("X-Forwarded-For = %q", got)
	}
	if got := req.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto = %q", got)
	}
	if got := req.Header.Get("X-Forwarded-Host"); got != "api.example.com" {
		t.Errorf("X-Forwarded-Host = %q", got)
	}
	if got := req.Header.Get("Forwarded"); got != `for=203.0.113.7;proto=http;host="api.example.com"` {
		t.Errorf("Forwarded = %q", got)
	}
}

// A hop appends to the chain rather than replacing it, or the upstream sees
// this proxy as the client.
func TestForwardedForAppendsToAnExistingChain(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:1"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	augmentForwardedHeaders(req, "api.example.com")

	if got := req.Header.Get("X-Forwarded-For"); got != "198.51.100.1, 203.0.113.7" {
		t.Errorf("X-Forwarded-For = %q", got)
	}
}

// Headers the edge already set are its answer, not this hop's to overwrite.
func TestExistingForwardedProtoAndHostAreLeftAlone(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:1"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "edge.example.com")
	augmentForwardedHeaders(req, "inner.example.com")

	if got := req.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Errorf("X-Forwarded-Proto = %q", got)
	}
	if got := req.Header.Get("X-Forwarded-Host"); got != "edge.example.com" {
		t.Errorf("X-Forwarded-Host = %q", got)
	}
	if got := req.Header.Get("Forwarded"); !strings.Contains(got, "proto=https") {
		t.Errorf("Forwarded = %q, want the edge's protocol", got)
	}
}

func TestClientIPFromRemoteAddr(t *testing.T) {
	for name, tc := range map[string]struct{ addr, want string }{
		"host and port":      {"203.0.113.7:54321", "203.0.113.7"},
		"an IPv6 host":       {"[2001:db8::1]:54321", "2001:db8::1"},
		"no port at all":     {"203.0.113.7", "203.0.113.7"},
		"a unix socket path": {"/tmp/app.sock", "/tmp/app.sock"},
		"nothing":            {"", ""},
	} {
		if got := clientIPFromRemoteAddr(tc.addr); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// RFC 7239 wants a bare IPv4, a bracketed and quoted IPv6, and a quoted opaque
// value for anything that is not an address at all.
func TestRFC7239ForParam(t *testing.T) {
	for name, tc := range map[string]struct{ ip, want string }{
		"IPv4":                {"203.0.113.7", "203.0.113.7"},
		"IPv6":                {"2001:db8::1", `"[2001:db8::1]"`},
		"an IPv4-mapped IPv6": {"::ffff:203.0.113.7", "203.0.113.7"},
		"not an address":      {"/tmp/app.sock", `"/tmp/app.sock"`},
		"nothing":             {"", ""},
	} {
		if got := rfc7239ForParam(tc.ip); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// A host containing a quote or a backslash must not be able to end the quoted
// string early and inject another parameter.
func TestRFC7239QuotedString(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":       {"api.example.com", `"api.example.com"`},
		"a quote":     {`a"b`, `"a\"b"`},
		"a backslash": {`a\b`, `"a\\b"`},
		"both":        {`a"\b`, `"a\"\\b"`},
		"empty":       {"", `""`},
	} {
		if got := rfc7239QuotedString(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// With no client address and no host there is nothing to say, so no header is
// added rather than an empty one.
func TestNoForwardedHeaderWhenThereIsNothingToSay(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = ""
	req.Header.Del("X-Forwarded-Proto")
	addRFC7239ForwardedHop(req, "", "")

	// proto is always known, so a hop is always describable.
	if got := req.Header.Get("Forwarded"); got != "proto=http" {
		t.Errorf("Forwarded = %q", got)
	}
	if _, present := req.Header["X-Forwarded-For"]; present {
		t.Error("an X-Forwarded-For was invented for a request with no client address")
	}
}

// ─── WebSocket ────────────────────────────────────────────────────────────────

func upgradeRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.RemoteAddr = "203.0.113.7:1"
	return req
}

func TestIsWebSocketUpgrade(t *testing.T) {
	for name, tc := range map[string]struct {
		upgrade, connection string
		want                bool
	}{
		"a websocket upgrade":         {"websocket", "Upgrade", true},
		"oddly cased":                 {"WebSocket", "upgrade", true},
		"among other tokens":          {"websocket", "keep-alive, Upgrade", true},
		"upgrading to something else": {"h2c", "Upgrade", false},
		"no Connection header":        {"websocket", "", false},
		"no Upgrade header":           {"", "Upgrade", false},
		"an ordinary request":         {"", "", false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.upgrade != "" {
			req.Header.Set("Upgrade", tc.upgrade)
		}
		if tc.connection != "" {
			req.Header.Set("Connection", tc.connection)
		}
		if got := isWebSocketUpgrade(req); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// Anything that is not an upgrade is somebody else's request.
func TestWebSocketProxyPassesOnEverythingElse(t *testing.T) {
	var reached bool
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusTeapot)
	})

	rec := httptest.NewRecorder()
	WebSocketProxy(mustParse(t, "http://127.0.0.1:1"), fallback).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/realtime/v1", nil))

	if !reached || rec.Code != http.StatusTeapot {
		t.Errorf("reached = %v, status = %d", reached, rec.Code)
	}
}

// An upstream that will not accept a connection is a bad gateway, and has to be
// reported before the connection is hijacked, while a status can still be sent.
func TestWebSocketProxyReportsAnUnreachableUpstream(t *testing.T) {
	rec := httptest.NewRecorder()
	WebSocketProxy(mustParse(t, "http://127.0.0.1:1"), nil).ServeHTTP(rec, upgradeRequest("/realtime/v1"))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

// httptest.ResponseRecorder is not a Hijacker, which is the case this branch
// exists for: a middleware that wrapped the writer and lost the capability.
func TestWebSocketProxyRefusesAWriterItCannotHijack(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close() //nolint:errcheck
	go func() {
		conn, err := upstream.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	rec := httptest.NewRecorder()
	WebSocketProxy(mustParse(t, "http://"+upstream.Addr().String()), nil).
		ServeHTTP(rec, upgradeRequest("/realtime/v1"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// The whole point: bytes written by the client come out at the upstream and
// back again.
func TestWebSocketProxySplicesBothDirections(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close() //nolint:errcheck

	var (
		mu      sync.Mutex
		request []byte
	)
	served := make(chan struct{})
	go func() {
		defer close(served)
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close() //nolint:errcheck

		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		mu.Lock()
		request = append(request, buf[:n]...)
		mu.Unlock()
		_, _ = conn.Write([]byte("from-upstream"))
	}()

	// A real server, because hijacking needs one.
	gateway := httptest.NewServer(WebSocketProxy(mustParse(t, "http://"+upstream.Addr().String()), nil))
	defer gateway.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(gateway.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck

	_, err = conn.Write([]byte("GET /realtime/v1 HTTP/1.1\r\nHost: api.example.com\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	answer := make([]byte, 64)
	n, err := conn.Read(answer)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("reading the upstream's answer: %v", err)
	}
	if string(answer[:n]) != "from-upstream" {
		t.Errorf("client received %q", answer[:n])
	}

	<-served
	mu.Lock()
	forwarded := string(request)
	mu.Unlock()

	if !strings.Contains(forwarded, "GET /realtime/v1 HTTP/1.1") {
		t.Errorf("the upstream received:\n%s", forwarded)
	}
	// The client's host, and the client's address, both have to survive the hop.
	if !strings.Contains(forwarded, "X-Forwarded-Host: api.example.com") {
		t.Errorf("the client's host was not forwarded:\n%s", forwarded)
	}
	if !strings.Contains(forwarded, "X-Forwarded-For: 127.0.0.1") {
		t.Errorf("the client's address was not forwarded:\n%s", forwarded)
	}
}

// The request must be rewritten for the upstream before it goes down a raw
// socket: RequestURI wins over URL when both are set, and chi's StripPrefix
// only touches URL.
func TestPrepareUpstreamWebSocketRequest(t *testing.T) {
	target := mustParse(t, "http://upstream:4000")

	req := upgradeRequest("/realtime/v1/websocket")
	req.RequestURI = "/realtime/v1/websocket"
	req.URL.Path = "/websocket"
	prepareUpstreamWebSocketRequest(req, target, "api.example.com")

	if req.RequestURI != "" {
		t.Errorf("RequestURI = %q, want it cleared so URL is used", req.RequestURI)
	}
	if req.URL.Host != "upstream:4000" || req.Host != "upstream:4000" {
		t.Errorf("host = %q / %q", req.URL.Host, req.Host)
	}
	if req.Header.Get("X-Forwarded-Host") != "api.example.com" {
		t.Errorf("X-Forwarded-Host = %q", req.Header.Get("X-Forwarded-Host"))
	}
}

// An empty path is not a valid request-target.
func TestPrepareUpstreamWebSocketRequestFillsAnEmptyPath(t *testing.T) {
	req := upgradeRequest("/")
	req.URL.Path = ""
	prepareUpstreamWebSocketRequest(req, mustParse(t, "http://upstream:4000"), "api.example.com")

	if req.URL.Path != "/" {
		t.Errorf("path = %q, want /", req.URL.Path)
	}
}

func TestPortOrDefault(t *testing.T) {
	for name, tc := range map[string]struct{ raw, fallback, want string }{
		"an explicit port": {"http://h:8080", "80", "8080"},
		"no port, http":    {"http://h", "80", "80"},
		"no port, https":   {"https://h", "443", "443"},
	} {
		parsed, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := portOrDefault(parsed, tc.fallback); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// wss and https both mean 443, and getting that wrong dials the plaintext port.
func TestWebSocketProxyDialsTheSecureDefaultPort(t *testing.T) {
	for _, scheme := range []string{"https", "wss"} {
		rec := httptest.NewRecorder()
		// Loopback with no port named, so the secure default is what gets dialled.
		// Nothing is listening on 443 here, which refuses at once: this asserts
		// which port was chosen, not that a connection succeeded.
		WebSocketProxy(mustParse(t, scheme+"://127.0.0.1"), nil).
			ServeHTTP(rec, upgradeRequest("/realtime/v1"))

		if rec.Code != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502", scheme, rec.Code)
		}
	}
}

// ─── Manifest ─────────────────────────────────────────────────────────────────

func writeManifest(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A project that has not pushed yet has no manifest, and that is the ordinary
// first run rather than a failure.
func TestLoadOnAMissingFileIsTheDefaultManifest(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "manifest.json"))
	if err != nil {
		t.Fatalf("a missing manifest is not an error: %v", err)
	}
	if m.Schema != "public" {
		t.Errorf("schema = %q, want the default", m.Schema)
	}
}

func TestLoadReadsAManifest(t *testing.T) {
	path := writeManifest(t, t.TempDir(), "manifest.json", `{"schema":"app","postgrest_url":"http://rest"}`)

	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Schema != "app" || m.PostgRESTURL != "http://rest" {
		t.Errorf("manifest = %+v", m)
	}
}

// A manifest that names no schema gets the default, whichever way it was read.
func TestTheSchemaDefaultsToPublic(t *testing.T) {
	path := writeManifest(t, t.TempDir(), "manifest.json", `{"postgrest_url":"http://rest"}`)

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRouteManifestJSON([]byte(`{"postgrest_url":"http://rest"}`))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Schema != "public" || parsed.Schema != "public" {
		t.Errorf("schemas = %q and %q", loaded.Schema, parsed.Schema)
	}
	if CloneRouteManifest(&RouteManifest{}).Schema != "public" {
		t.Error("a cloned manifest lost the default")
	}
	if CloneRouteManifest(nil).Schema != "public" {
		t.Error("cloning nothing should still give a usable manifest")
	}
}

// A manifest that will not parse is an error, not an empty manifest: serving
// with no routes would look like a working deployment with nothing in it.
func TestLoadReportsAManifestItCannotParse(t *testing.T) {
	path := writeManifest(t, t.TempDir(), "manifest.json", "{not json")

	if _, err := Load(path); err == nil {
		t.Error("want an error")
	}
	if _, err := ParseRouteManifestJSON([]byte("{not json")); err == nil {
		t.Error("ParseRouteManifestJSON: want an error")
	}
}

// A path whose directory cannot be opened is a real failure, distinct from the
// file simply not being there.
func TestLoadReportsAnUnreadableDirectory(t *testing.T) {
	blocker := writeManifest(t, t.TempDir(), "a-file", "x")

	if _, err := Load(filepath.Join(blocker, "manifest.json")); err == nil {
		t.Error("want an error when the parent is not a directory")
	}
}

// A bare filename resolves against the working directory, which is the form
// the CLI passes.
func TestLoadAcceptsABareFilename(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "manifest.json", `{"schema":"app"}`)

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	m, err := Load("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if m.Schema != "app" {
		t.Errorf("schema = %q", m.Schema)
	}
}

// Every map in a manifest is copied, so one tenant's reload cannot mutate what
// another tenant's in-flight request is reading.
func TestCloneRouteManifestDeepCopiesEveryMap(t *testing.T) {
	original := &RouteManifest{
		Schema:              "app",
		Hooks:               map[string]TableHooks{"posts": {"insert": HookConfig{Function: "check"}}},
		Validators:          map[string]TableValidators{"posts": {"title": HookConfig{Function: "v"}}},
		StaticCachePrefixes: map[string]string{"/docs": "public"},
		FunctionWorkerURLs:  map[string]string{"fn": "http://worker"},
		CorsAllowedOrigins:  []string{"https://app.example"},
	}

	clone := CloneRouteManifest(original)
	clone.Hooks["posts"]["insert"] = HookConfig{Function: "changed"}
	clone.Hooks["other"] = TableHooks{}
	clone.Validators["posts"]["title"] = HookConfig{Function: "changed"}
	clone.Validators["other"] = TableValidators{}
	clone.StaticCachePrefixes["/docs"] = "changed"

	if got := original.Hooks["posts"]["insert"].Function; got != "check" {
		t.Errorf("the hook map is shared: %q", got)
	}
	if _, added := original.Hooks["other"]; added {
		t.Error("the clone added a table to the original")
	}
	if got := original.Validators["posts"]["title"].Function; got != "v" {
		t.Errorf("the validator map is shared: %q", got)
	}
	if got := original.StaticCachePrefixes["/docs"]; got != "public" {
		t.Errorf("the static cache map is shared: %q", got)
	}
}

// Merging is how a tenant's manifest is layered over the deployment's, so what
// an empty overlay does — nothing — matters as much as what a full one does.
func TestMergeRouteManifestFieldByField(t *testing.T) {
	base := &RouteManifest{
		Schema: "base", PostgRESTURL: "http://base-rest", GraphQLURL: "http://base-gql",
		StorageURL: "http://base-store", AppMode: "static", AppStaticDir: "/base",
		AppUpstream: "http://base-app", ViteDevURL: "http://base-vite",
		RealtimeURL: "http://base-rt", FunctionsWorkerURL: "http://base-fn",
		StaticCacheHTML: "base-html", StaticCacheHashedAssets: "base-assets",
	}
	MergeRouteManifest(base, &RouteManifest{})

	if base.Schema != "base" || base.PostgRESTURL != "http://base-rest" ||
		base.GraphQLURL != "http://base-gql" || base.StorageURL != "http://base-store" ||
		base.AppMode != "static" || base.AppStaticDir != "/base" ||
		base.AppUpstream != "http://base-app" || base.ViteDevURL != "http://base-vite" ||
		base.RealtimeURL != "http://base-rt" || base.FunctionsWorkerURL != "http://base-fn" ||
		base.StaticCacheHTML != "base-html" || base.StaticCacheHashedAssets != "base-assets" {
		t.Errorf("an empty overlay changed the base: %+v", base)
	}

	MergeRouteManifest(base, &RouteManifest{
		Schema: "over", PostgRESTURL: "http://over-rest", GraphQLURL: "http://over-gql",
		StorageURL: "http://over-store", AppMode: "proxy", AppStaticDir: "/over",
		AppUpstream: "http://over-app", ViteDevURL: "http://over-vite",
		RealtimeURL: "http://over-rt", FunctionsWorkerURL: "http://over-fn",
		StaticCacheHTML: "over-html", StaticCacheHashedAssets: "over-assets",
	})
	if base.Schema != "over" || base.AppMode != "proxy" || base.StaticCacheHTML != "over-html" {
		t.Errorf("the overlay did not win: %+v", base)
	}
}

// Nothing to merge into, or nothing to merge, is a no-op rather than a panic.
func TestMergeRouteManifestWithNothing(t *testing.T) {
	MergeRouteManifest(nil, &RouteManifest{Schema: "x"})
	base := &RouteManifest{Schema: "base"}
	MergeRouteManifest(base, nil)
	if base.Schema != "base" {
		t.Errorf("schema = %q", base.Schema)
	}
}

// The booleans are taken from the overlay unconditionally, because false is a
// meaningful value: a tenant turning realtime off has to be able to.
func TestMergeTakesBooleansFromTheOverlayEvenWhenFalse(t *testing.T) {
	base := &RouteManifest{RealtimeEnabled: true, FunctionsEnabled: true}
	MergeRouteManifest(base, &RouteManifest{})

	if base.RealtimeEnabled || base.FunctionsEnabled {
		t.Error("an overlay that says off was ignored")
	}
}

// Maps merge per key so a tenant adding one worker does not drop the others,
// except hooks, which are replaced wholesale: a hook removed from the schema
// has to stop firing.
func TestMergeMapsAndSlices(t *testing.T) {
	base := &RouteManifest{
		FunctionWorkerURLs:  map[string]string{"a": "http://a", "b": "http://b"},
		StaticCachePrefixes: map[string]string{"/docs": "base"},
		CorsAllowedOrigins:  []string{"https://base.example"},
		Hooks:               map[string]TableHooks{"posts": {}, "comments": {}},
	}
	MergeRouteManifest(base, &RouteManifest{
		FunctionWorkerURLs:  map[string]string{"b": "http://over-b", "c": "", "d": "http://d"},
		StaticCachePrefixes: map[string]string{"/api": "over"},
		CorsAllowedOrigins:  []string{"https://over.example"},
		Hooks:               map[string]TableHooks{"posts": {}},
	})

	if base.FunctionWorkerURLs["a"] != "http://a" || base.FunctionWorkerURLs["b"] != "http://over-b" ||
		base.FunctionWorkerURLs["d"] != "http://d" {
		t.Errorf("worker URLs = %v", base.FunctionWorkerURLs)
	}
	if _, blanked := base.FunctionWorkerURLs["c"]; blanked {
		t.Error("an empty overlay value was written as a worker URL")
	}
	if base.StaticCachePrefixes["/docs"] != "base" || base.StaticCachePrefixes["/api"] != "over" {
		t.Errorf("static cache prefixes = %v", base.StaticCachePrefixes)
	}
	if len(base.CorsAllowedOrigins) != 1 || base.CorsAllowedOrigins[0] != "https://over.example" {
		t.Errorf("origins = %v, want the overlay's list wholesale", base.CorsAllowedOrigins)
	}
	if _, kept := base.Hooks["comments"]; kept {
		t.Error("a hook the overlay does not list is still registered, so a removed hook keeps firing")
	}
}

// Merging into a base that has no maps at all has to create them rather than
// write into nil.
func TestMergeIntoABaseWithNoMaps(t *testing.T) {
	base := &RouteManifest{}
	MergeRouteManifest(base, &RouteManifest{
		FunctionWorkerURLs:  map[string]string{"a": "http://a"},
		StaticCachePrefixes: map[string]string{"/docs": "over"},
	})

	if base.FunctionWorkerURLs["a"] != "http://a" || base.StaticCachePrefixes["/docs"] != "over" {
		t.Errorf("base = %+v", base)
	}
}

// ─── Watching ─────────────────────────────────────────────────────────────────

// A write reloads; anything else does not.
func TestWatchLoopReloadsOnWriteAndCreate(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, "manifest.json", `{"schema":"app"}`)

	events := make(chan fsnotify.Event, 4)
	errs := make(chan error)
	reloaded := make(chan *RouteManifest, 4)

	done := make(chan struct{})
	go func() {
		defer close(done)
		watchLoop(events, errs, path, func(m *RouteManifest) { reloaded <- m })
	}()

	events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
	events <- fsnotify.Event{Name: path, Op: fsnotify.Create}
	events <- fsnotify.Event{Name: path, Op: fsnotify.Chmod}

	for i := 0; i < 2; i++ {
		select {
		case m := <-reloaded:
			if m.Schema != "app" {
				t.Errorf("reloaded schema = %q", m.Schema)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("reload %d never happened", i)
		}
	}

	close(events)
	close(errs)
	<-done

	select {
	case m := <-reloaded:
		t.Errorf("a chmod triggered a reload: %+v", m)
	default:
	}
}

// A manifest caught mid-write does not parse. Keeping the loaded one is better
// than serving a deployment with no routes.
func TestWatchLoopIgnoresAManifestItCannotParse(t *testing.T) {
	path := writeManifest(t, t.TempDir(), "manifest.json", "{half-writ")

	events := make(chan fsnotify.Event)
	errs := make(chan error)
	reloaded := make(chan *RouteManifest, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		watchLoop(events, errs, path, func(m *RouteManifest) { reloaded <- m })
	}()

	events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
	// Closing after the send is what makes this deterministic: the loop has to
	// finish handling the event before it can observe the close.
	close(events)
	close(errs)
	<-done

	select {
	case m := <-reloaded:
		t.Errorf("an unparseable manifest was handed on: %+v", m)
	default:
	}
}

// And the write that completes is honoured, so one bad event does not end the
// watch.
func TestWatchLoopKeepsGoingAfterAnUnparseableWrite(t *testing.T) {
	dir := t.TempDir()
	bad := writeManifest(t, dir, "bad.json", "{half-writ")
	good := writeManifest(t, dir, "good.json", `{"schema":"app"}`)

	events := make(chan fsnotify.Event)
	errs := make(chan error)
	reloaded := make(chan *RouteManifest, 1)

	// The path is fixed per loop, so two loops over two files stand in for one
	// file in two states, without racing the filesystem.
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchLoop(events, errs, bad, func(m *RouteManifest) { reloaded <- m })
	}()
	events <- fsnotify.Event{Name: bad, Op: fsnotify.Write}
	close(events)
	close(errs)
	<-done

	events2 := make(chan fsnotify.Event)
	errs2 := make(chan error)
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		watchLoop(events2, errs2, good, func(m *RouteManifest) { reloaded <- m })
	}()
	events2 <- fsnotify.Event{Name: good, Op: fsnotify.Write}

	select {
	case m := <-reloaded:
		if m.Schema != "app" {
			t.Errorf("schema = %q", m.Schema)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the completed write was never loaded")
	}

	close(events2)
	close(errs2)
	<-done2
}

// A watcher error is logged and the loop carries on: losing the watch entirely
// because of one inotify hiccup would silently stop reloading.
func TestWatchLoopSurvivesAWatcherError(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, "manifest.json", `{"schema":"app"}`)

	events := make(chan fsnotify.Event)
	errs := make(chan error)
	reloaded := make(chan *RouteManifest, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		watchLoop(events, errs, path, func(m *RouteManifest) { reloaded <- m })
	}()

	errs <- errors.New("inotify hiccup")
	events <- fsnotify.Event{Name: path, Op: fsnotify.Write}

	select {
	case <-reloaded:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop stopped after a watcher error")
	}

	close(events)
	close(errs)
	<-done
}

// One channel closing does not end the loop; both do. Selecting on a closed
// channel otherwise spins.
func TestWatchLoopEndsOnlyWhenBothChannelsClose(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, "manifest.json", `{"schema":"app"}`)

	events := make(chan fsnotify.Event)
	errs := make(chan error)
	reloaded := make(chan *RouteManifest, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		watchLoop(events, errs, path, func(m *RouteManifest) { reloaded <- m })
	}()

	close(errs)
	events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
	select {
	case <-reloaded:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop stopped when only the error channel closed")
	}

	close(events)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop did not end when both channels closed")
	}
}

// Watch itself: a real file, a real watcher, a real write.
func TestWatchReloadsARealFile(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, "manifest.json", `{"schema":"app"}`)

	reloaded := make(chan *RouteManifest, 4)
	if err := Watch(path, func(m *RouteManifest) { reloaded <- m }); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(`{"schema":"changed"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case m := <-reloaded:
			if m.Schema == "changed" {
				return
			}
		case <-deadline:
			t.Fatal("the manifest was never reloaded")
		}
	}
}

// A path with nothing at it cannot be watched, and saying so at startup is
// better than a watcher that silently never fires.
func TestWatchReportsAPathItCannotWatch(t *testing.T) {
	if err := Watch(filepath.Join(t.TempDir(), "absent.json"), func(*RouteManifest) {}); err == nil {
		t.Error("want an error")
	}
}

// Creating a watcher fails only when the process is out of inotify handles, so
// the seam is what proves the failure is reported rather than leaving a nil
// watcher to be used.
func TestWatchReportsAWatcherItCannotCreate(t *testing.T) {
	original := newWatcher
	t.Cleanup(func() { newWatcher = original })
	newWatcher = func() (*fsnotify.Watcher, error) { return nil, errors.New("no handles left") }

	if err := Watch("anything", func(*RouteManifest) {}); err == nil {
		t.Error("want an error")
	}
}

// hijackableWriter is a ResponseWriter a WebSocket proxy can take over, or
// cannot: hijackErr decides which.
type hijackableWriter struct {
	http.ResponseWriter
	conn       net.Conn
	hijackErr  error
	statusCode int
}

func (h *hijackableWriter) WriteHeader(status int) {
	h.statusCode = status
	h.ResponseWriter.WriteHeader(status)
}

func (h *hijackableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h.hijackErr != nil {
		return nil, nil, h.hijackErr
	}
	return h.conn, bufio.NewReadWriter(bufio.NewReader(h.conn), bufio.NewWriter(h.conn)), nil
}

// listening returns a listener that accepts and holds connections until the
// test ends, so a dial succeeds without the upstream saying anything.
func listening(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return listener
}

// A connection that cannot be taken over cannot be spliced. Reporting it is
// the only option: the upgrade has not been answered yet, so a status can
// still be sent.
func TestWebSocketProxyReportsAFailedHijack(t *testing.T) {
	upstream := listening(t)

	rec := httptest.NewRecorder()
	w := &hijackableWriter{ResponseWriter: rec, hijackErr: errors.New("cannot hijack")}
	WebSocketProxy(mustParse(t, "http://"+upstream.Addr().String()), nil).
		ServeHTTP(w, upgradeRequest("/realtime/v1"))

	// Nothing can be said to the client: the handler has given up the
	// connection's protocol without owning it. What matters is that it returns.
	if w.statusCode != 0 {
		t.Errorf("a status was written after a failed hijack: %d", w.statusCode)
	}
}

// errorBody fails on the first read, which is what a request whose body cannot
// be relayed looks like.
type errorBody struct{}

func (errorBody) Read([]byte) (int, error) { return 0, errors.New("body read failed") }
func (errorBody) Close() error             { return nil }

// If the upgrade request cannot be written to the upstream there is nothing to
// splice, and both connections have to be released rather than leaked.
func TestWebSocketProxyGivesUpWhenTheUpgradeCannotBeForwarded(t *testing.T) {
	upstream := listening(t)

	client, server := net.Pipe()
	defer client.Close() //nolint:errcheck
	defer server.Close() //nolint:errcheck

	req := upgradeRequest("/realtime/v1")
	req.Body = errorBody{}
	req.ContentLength = 10

	rec := httptest.NewRecorder()
	w := &hijackableWriter{ResponseWriter: rec, conn: server}

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		WebSocketProxy(mustParse(t, "http://"+upstream.Addr().String()), nil).ServeHTTP(w, req)
	}()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return after the upgrade could not be forwarded")
	}

	// The hijacked connection is closed, so a client waiting on it is not left
	// hanging.
	if _, err := server.Write([]byte("x")); err == nil {
		t.Error("the hijacked connection was left open")
	}
}
