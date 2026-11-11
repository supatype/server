package data

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/supatype/server/internal/config"
)

// fakeKeyspace is a RESP server that refuses every login until it is told to accept them, which
// is a keyspace whose password was wrong and then fixed.
type fakeKeyspace struct {
	addr   string
	accept atomic.Bool
}

func startFakeKeyspace(t *testing.T) *fakeKeyspace {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	f := &fakeKeyspace{addr: l.Addr().String()}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeKeyspace) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	for {
		args, err := readRESPCommand(r)
		if err != nil {
			return
		}
		if _, err := io.WriteString(c, f.reply(args)); err != nil {
			return
		}
	}
}

// reply answers the few commands a valkey-go client sends: the login, a GET, and the rest OK.
func (f *fakeKeyspace) reply(args []string) string {
	switch strings.ToUpper(args[0]) {
	case "HELLO":
		if !f.accept.Load() {
			return "-WRONGPASS invalid username-password pair or user is disabled.\r\n"
		}
		// RESP3 and a version with client-side caching, which valkey-go wants by default.
		return "%3\r\n+server\r\n+valkey\r\n+version\r\n+7.2.0\r\n+proto\r\n:3\r\n"
	case "CLUSTER":
		// What a standalone server answers, so valkey-go does not take it for a cluster.
		return "-ERR This instance has cluster support disabled\r\n"
	case "GET":
		return "_\r\n"
	case "PING":
		return "+PONG\r\n"
	default:
		return "+OK\r\n"
	}
}

// readRESPCommand reads one command, an array of bulk strings.
func readRESPCommand(r *bufio.Reader) ([]string, error) {
	n, err := readRESPLength(r, '*')
	if err != nil {
		return nil, err
	}
	args := make([]string, n)
	for i := range args {
		size, err := readRESPLength(r, '$')
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args[i] = string(buf[:size])
	}
	return args, nil
}

func readRESPLength(r *bufio.Reader, kind byte) (int, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, err
	}
	if len(line) < 3 || line[0] != kind {
		return 0, fmt.Errorf("unexpected RESP line %q", line)
	}
	return strconv.Atoi(strings.TrimSpace(line[1:]))
}

// 0.9.2 release test round 2 (R2-S13): a server whose first keyspace login was refused bypassed
// its caches until it was restarted, though the password had been put right in the keyspace. It
// keeps dialling in the background, and the cache comes on once a login is accepted.
func TestOpenKeepsDiallingAKeyspaceThatRefusedTheLogin(t *testing.T) {
	ks := startFakeKeyspace(t)
	r, err := Open(context.Background(), &config.Config{Mode: "standalone", KeyspaceAddr: ks.addr, KeyspacePassword: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if r.Cache().Available() {
		t.Fatal("a refused login should leave the cache bypassed at first")
	}

	ks.accept.Store(true)
	deadline := time.Now().Add(10 * time.Second)
	for !r.Cache().Available() {
		if time.Now().After(deadline) {
			t.Fatal("the cache never came on after the keyspace accepted the login")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got, err := r.Cache().GetBytes(context.Background(), "k"); err != nil || got != nil {
		t.Errorf("GetBytes once connected = %q, %v; want a miss", got, err)
	}
	if !r.PlatformCache().Available() {
		t.Error("one address is one client: the platform half should be on too")
	}
}
