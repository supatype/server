package keyspace

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// connectedFake is a Client that is there: available, every read a miss, and it counts its closes.
type connectedFake struct {
	unavailableClient
	closed atomic.Int32
}

func (*connectedFake) Available() bool                                  { return true }
func (*connectedFake) GetBytes(context.Context, string) ([]byte, error) { return nil, nil }
func (c *connectedFake) Close()                                         { c.closed.Add(1) }

// gatedDialer refuses every dial until open is set, then hands back live. It counts the dials.
type gatedDialer struct {
	open  atomic.Bool
	calls atomic.Int32
	live  Client
}

func (g *gatedDialer) dial() (Client, error) {
	g.calls.Add(1)
	if !g.open.Load() {
		return nil, fmt.Errorf("keyspace: connect: %w", ErrAuthFailed)
	}
	return g.live, nil
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// Until a dial succeeds the client answers as not connected, which callers tell apart from no
// keyspace configured, and keeps dialling; once one does, it is that connection, and says so once.
func TestRedialingConnectsOnceADialSucceeds(t *testing.T) {
	g := &gatedDialer{live: &connectedFake{}}
	var announced atomic.Int32
	r := newRedialing(g.dial, time.Millisecond, 4*time.Millisecond, func() { announced.Add(1) })
	t.Cleanup(r.Close)

	waitFor(t, "a few refused dials", func() bool { return g.calls.Load() >= 3 })
	if r.Available() {
		t.Error("available before any dial succeeded")
	}
	if _, err := r.GetBytes(context.Background(), "k"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("GetBytes before connecting = %v, want ErrNotConnected", err)
	}

	g.open.Store(true)
	waitFor(t, "the redial to connect", r.Available)
	if got, err := r.GetBytes(context.Background(), "k"); err != nil || got != nil {
		t.Errorf("GetBytes once connected = %q, %v; want the live client's miss", got, err)
	}
	if announced.Load() != 1 {
		t.Errorf("announced %d times, want 1", announced.Load())
	}
}

// Close stops the redial for good.
func TestRedialingCloseStopsDialling(t *testing.T) {
	g := &gatedDialer{}
	r := newRedialing(g.dial, time.Millisecond, time.Millisecond, func() {})
	waitFor(t, "a few dials", func() bool { return g.calls.Load() >= 3 })
	r.Close()
	after := g.calls.Load()
	time.Sleep(20 * time.Millisecond)
	if g.calls.Load() != after {
		t.Errorf("still dialling after Close: %d then %d", after, g.calls.Load())
	}
}

// Close closes the connection the redial made, once however often it is called.
func TestRedialingCloseClosesTheConnectionOnce(t *testing.T) {
	live := &connectedFake{}
	g := &gatedDialer{live: live}
	g.open.Store(true)
	r := newRedialing(g.dial, time.Millisecond, time.Millisecond, func() {})
	waitFor(t, "the redial to connect", r.Available)
	r.Close()
	r.Close()
	if live.closed.Load() != 1 {
		t.Errorf("the connection was closed %d times, want 1", live.closed.Load())
	}
}

// Down is no connection at all, configured or not, and nothing else: a keyspace that is there and
// failing is still worth a log line per request.
func TestDownIsNoConnectionOnly(t *testing.T) {
	for err, want := range map[error]bool{
		ErrUnavailable:                           true,
		ErrNotConnected:                          true,
		fmt.Errorf("GET k: %w", ErrNotConnected): true,
		ErrCircuitOpen:                           false,
		errors.New("WRONGPASS"):                  false,
	} {
		if got := Down(err); got != want {
			t.Errorf("Down(%v) = %v, want %v", err, got, want)
		}
	}
}

// Every operation, not only the read the other tests use, answers as not connected before a dial
// succeeds, so a caller never mistakes the gap for a miss or a write that landed.
func TestRedialingAnswersEveryOperationAsNotConnected(t *testing.T) {
	g := &gatedDialer{live: &connectedFake{}}
	r := newRedialing(g.dial, time.Hour, time.Hour, func() {})
	t.Cleanup(r.Close)
	ctx := context.Background()

	_, tenantErr := r.GetTenantConfig(ctx, "ref")
	_, scanErr := func() (uint64, error) { _, next, err := r.ScanPage(ctx, 0, "*", 10); return next, err }()
	_, ttlErr := r.TTLSeconds(ctx, "k")
	for name, err := range map[string]error{
		"GetTenantConfig":  tenantErr,
		"SetBytes":         r.SetBytes(ctx, "k", []byte("v"), 60),
		"Del":              r.Del(ctx, "k"),
		"ScanPage":         scanErr,
		"TTLSeconds":       ttlErr,
		"AddToExpiringSet": r.AddToExpiringSet(ctx, "s", "m", time.Now().Add(time.Hour)),
	} {
		if !errors.Is(err, ErrNotConnected) {
			t.Errorf("%s before connecting = %v, want ErrNotConnected", name, err)
		}
	}
}
