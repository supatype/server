package cmd

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
)

// listenAPI opens the API listener on host:port.
//
// A host that resolves to several addresses, every one of them loopback, is listened on at each,
// on the same port: localhost is 127.0.0.1 and ::1. Listening on "localhost:port" binds only the
// first address the name resolves to, 127.0.0.1, while a browser resolves localhost to ::1 first.
// On Windows a refused loopback connection is not reported at once, so every new connection waited
// out the browser's 300 ms fallback to IPv4. Pages reuse their HTTP connections and barely notice;
// a WebSocket opens a new one each time, and the realtime join arrived late enough to miss a change
// made right after a page opened (0.9.2 release test N-FF1).
//
// The first address, IPv4 when there is one, must be had. Any other that cannot be had, ::1 on a
// machine without IPv6, is left out, and skipped is told why, once. Every other host is listened
// on as given.
func listenAPI(ctx context.Context, lc net.ListenConfig, host, port string, skipped func(error)) (net.Listener, error) {
	ips := loopbackIPs(ctx, host)
	if ips == nil {
		return lc.Listen(ctx, "tcp", net.JoinHostPort(host, port))
	}
	first, err := lc.Listen(ctx, "tcp", net.JoinHostPort(ips[0].String(), port))
	if err != nil {
		return nil, err
	}
	// The port the first address got, so that port 0 gives every address one port.
	bound := strconv.Itoa(first.Addr().(*net.TCPAddr).Port)
	listeners := []net.Listener{first}
	var errs []error
	for _, ip := range ips[1:] {
		l, err := lc.Listen(ctx, "tcp", net.JoinHostPort(ip.String(), bound))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		listeners = append(listeners, l)
	}
	if len(errs) > 0 {
		skipped(errors.Join(errs...))
	}
	if len(listeners) == 1 {
		return first, nil
	}
	return newMultiListener(listeners...), nil
}

// loopbackIPs returns the distinct addresses host resolves to, IPv4 first, when there are several
// and every one is loopback. Otherwise it returns nil: a host that does not resolve, resolves to
// one address, or reaches beyond this machine is listened on as given.
func loopbackIPs(ctx context.Context, host string) []net.IP {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool, len(addrs))
	var v4, v6 []net.IP
	for _, a := range addrs {
		if !a.IP.IsLoopback() {
			return nil
		}
		if key := a.IP.String(); !seen[key] {
			seen[key] = true
			if a.IP.To4() != nil {
				v4 = append(v4, a.IP)
			} else {
				v6 = append(v6, a.IP)
			}
		}
	}
	if len(v4)+len(v6) < 2 {
		return nil
	}
	return append(v4, v6...)
}

// multiListener accepts from several listeners as one, for one http.Server.
type multiListener struct {
	listeners []net.Listener
	accepted  chan acceptResult
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

type acceptResult struct {
	conn net.Conn
	err  error
}

func newMultiListener(listeners ...net.Listener) *multiListener {
	m := &multiListener{
		listeners: listeners,
		accepted:  make(chan acceptResult),
		done:      make(chan struct{}),
	}
	for _, l := range listeners {
		go m.acceptFrom(l)
	}
	return m
}

// acceptFrom hands each connection, or error, from l to Accept until the listener is closed.
func (m *multiListener) acceptFrom(l net.Listener) {
	for {
		conn, err := l.Accept()
		select {
		case m.accepted <- acceptResult{conn: conn, err: err}:
		case <-m.done:
			if conn != nil {
				_ = conn.Close()
			}
			return
		}
		if errors.Is(err, net.ErrClosed) {
			return
		}
	}
}

// Accept returns the next connection from any of the listeners.
func (m *multiListener) Accept() (net.Conn, error) {
	select {
	case r := <-m.accepted:
		return r.conn, r.err
	case <-m.done:
		return nil, net.ErrClosed
	}
}

// Close closes every listener; closing again returns the first close's result.
func (m *multiListener) Close() error {
	m.closeOnce.Do(func() {
		close(m.done)
		errs := make([]error, 0, len(m.listeners))
		for _, l := range m.listeners {
			errs = append(errs, l.Close())
		}
		m.closeErr = errors.Join(errs...)
	})
	return m.closeErr
}

// Addr is the first listener's address, 127.0.0.1:port for localhost.
func (m *multiListener) Addr() net.Addr {
	return m.listeners[0].Addr()
}
