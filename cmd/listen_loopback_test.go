package cmd

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// ipv6Loopback reports whether this machine can listen on ::1, so the tests that need it can skip
// on one that cannot rather than fail.
func ipv6Loopback(t *testing.T) bool {
	t.Helper()
	l, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// serveHello serves "hello" on l until the test ends.
func serveHello(t *testing.T, l net.Listener) {
	t.Helper()
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "hello") }),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// get fetches http://addr/ with a short dial timeout, so a refused address fails the test quickly.
func get(addr string) (string, error) {
	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext},
	}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func portOf(l net.Listener) string {
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

func noWarning(t *testing.T) func(error) {
	return func(err error) { t.Errorf("unexpected IPv6 warning: %v", err) }
}

// 0.9.2 release test (N-FF1): a server told to listen on localhost answered on 127.0.0.1 only, so a
// browser's first try, ::1, was refused and every WebSocket waited 300 ms before falling back.
func TestListenAPILocalhostAnswersOnBothLoopbacks(t *testing.T) {
	if !ipv6Loopback(t) {
		t.Skip("this machine has no ::1")
	}
	skipUnlessBothLoopbacks(t, "localhost")
	l, err := listenAPI(context.Background(), net.ListenConfig{}, "localhost", "0", noWarning(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveHello(t, l)
	port := portOf(l)

	for _, host := range []string{"127.0.0.1", "::1"} {
		body, err := get(net.JoinHostPort(host, port))
		if err != nil || body != "hello" {
			t.Errorf("dial %s: body %q, err %v", net.JoinHostPort(host, port), body, err)
		}
	}
}

func TestListenAPIOtherHostsListenAsGiven(t *testing.T) {
	l, err := listenAPI(context.Background(), net.ListenConfig{}, "127.0.0.1", "0", noWarning(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	if _, multi := l.(*multiListener); multi {
		t.Fatal("an explicit address must get exactly that address")
	}
	if got := l.Addr().(*net.TCPAddr).IP.String(); got != "127.0.0.1" {
		t.Fatalf("bound %s", got)
	}
}

func TestListenAPIWithoutIPv6CarriesOnWithIPv4AndSaysSo(t *testing.T) {
	if !ipv6Loopback(t) {
		t.Skip("this machine has no ::1 to occupy")
	}
	skipUnlessBothLoopbacks(t, "LocalHost")
	// Hold [::1]:port, so the IPv6 half cannot be had while 127.0.0.1:port still can.
	held, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Fatalf("hold ::1: %v", err)
	}
	defer func() { _ = held.Close() }()

	var warnings []error
	l, err := listenAPI(context.Background(), net.ListenConfig{}, "LocalHost", portOf(held), func(err error) { warnings = append(warnings, err) })
	if err != nil {
		t.Skipf("127.0.0.1 on the same port is taken too: %v", err)
	}
	serveHello(t, l)
	if len(warnings) != 1 {
		t.Fatalf("want one warning, got %v", warnings)
	}
	if _, multi := l.(*multiListener); multi {
		t.Fatal("expected the IPv4 listener alone")
	}
	if body, err := get(net.JoinHostPort("127.0.0.1", portOf(l))); err != nil || body != "hello" {
		t.Fatalf("IPv4: body %q, err %v", body, err)
	}
}

func TestListenAPIFailsWhenIPv4IsTaken(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold 127.0.0.1: %v", err)
	}
	defer func() { _ = held.Close() }()
	if _, err := listenAPI(context.Background(), net.ListenConfig{}, "localhost", portOf(held), noWarning(t)); err == nil {
		t.Fatal("expected the taken IPv4 port to fail the listen")
	}
}

func TestMultiListenerCloseStopsAcceptAndIsIdempotent(t *testing.T) {
	a, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := newMultiListener(a, b)
	if m.Addr().String() != a.Addr().String() {
		t.Fatalf("Addr %s, want %s", m.Addr(), a.Addr())
	}

	// A connection to the second listener comes out of the one Accept.
	go func() {
		if c, err := net.Dial("tcp", b.Addr().String()); err == nil {
			_ = c.Close()
		}
	}()
	conn, err := m.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	_ = conn.Close()

	first := m.Close()
	if first != nil {
		t.Fatalf("close: %v", first)
	}
	if again := m.Close(); again != first {
		t.Fatalf("second close %v, want %v", again, first)
	}
	if _, err := m.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("accept after close: %v", err)
	}
}

func TestMultiListenerDropsAConnectionArrivingAsItCloses(t *testing.T) {
	a, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := newMultiListener(a)
	// Nobody calls Accept: the accepted connection waits in acceptFrom until Close lets it go.
	c, err := net.Dial("tcp", a.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	time.Sleep(50 * time.Millisecond)
	_ = m.Close()

	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected the dropped connection to be closed")
	}
}

// skipUnlessBothLoopbacks skips a test that needs host to resolve to 127.0.0.1 and ::1, which a
// machine whose hosts file names only one of them does not.
func skipUnlessBothLoopbacks(t *testing.T, host string) {
	t.Helper()
	if len(loopbackIPs(context.Background(), host)) < 2 {
		t.Skipf("%s does not resolve to several loopback addresses here", host)
	}
}

// Not only "localhost": any name whose every address is loopback is served on each of them. The
// names a hosts file gives loopback vary by platform, so the test takes the first that resolves to
// several and skips where none does.
func TestListenAPIAnyAllLoopbackNameAnswersOnEachAddress(t *testing.T) {
	// A name the hosts file lacks goes to DNS, which can be slow to say no.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var host string
	var ips []net.IP
	for _, name := range []string{"localhost.localdomain", "localhost6", "ip6-localhost", "ip6-loopback"} {
		if ips = loopbackIPs(ctx, name); ips != nil {
			host = name
			break
		}
	}
	if host == "" {
		t.Skip("no name other than localhost resolves to several loopback addresses here")
	}
	l, err := listenAPI(context.Background(), net.ListenConfig{}, host, "0", noWarning(t))
	if err != nil {
		t.Fatalf("listen on %s: %v", host, err)
	}
	serveHello(t, l)
	port := portOf(l)
	for _, ip := range ips {
		addr := net.JoinHostPort(ip.String(), port)
		if body, err := get(addr); err != nil || body != "hello" {
			t.Errorf("%s: dial %s: body %q, err %v", host, addr, body, err)
		}
	}
}

// A name that reaches beyond this machine, or does not resolve, is not split up.
func TestLoopbackIPsLeavesOtherHostsAlone(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0", "127.0.0.1", "::1", "no-such-host.invalid"} {
		if ips := loopbackIPs(context.Background(), host); ips != nil {
			t.Errorf("%q: got %v, want nil", host, ips)
		}
	}
}
