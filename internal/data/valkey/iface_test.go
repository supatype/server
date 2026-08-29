package valkey

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The unavailable client exists so that "no Valkey configured" is a value rather
// than a nil pointer every consumer has to remember to check. Each operation
// must therefore be callable and must say why it did nothing.
func TestUnavailableClientReportsRatherThanPanics(t *testing.T) {
	c := Unavailable()
	ctx := context.Background()

	if c.Available() {
		t.Error("Available() must be false")
	}

	if _, err := c.GetTenantConfig(ctx, "ref"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("GetTenantConfig: %v", err)
	}
	if _, err := c.GetBytes(ctx, "k"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("GetBytes: %v", err)
	}
	if err := c.SetBytes(ctx, "k", []byte("v"), 60); !errors.Is(err, ErrUnavailable) {
		t.Errorf("SetBytes: %v", err)
	}
	if err := c.Del(ctx, "k"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Del: %v", err)
	}
	if _, _, err := c.ScanPage(ctx, 0, "*", 10); !errors.Is(err, ErrUnavailable) {
		t.Errorf("ScanPage: %v", err)
	}
	if _, err := c.TTLSeconds(ctx, "k"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("TTLSeconds: %v", err)
	}
	if err := c.AddToExpiringSet(ctx, "k", "m", time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("AddToExpiringSet: %v", err)
	}

	// Close must be safe, because both shutdown paths call it unconditionally.
	c.Close()
	c.Close()
}

// A read must not look like a cache miss. A miss invites the caller to compute
// the value and write it back, and the write would fail too.
func TestUnavailableReadIsNotAMiss(t *testing.T) {
	got, err := Unavailable().GetBytes(context.Background(), "k")
	if got != nil {
		t.Errorf("want nil bytes, got %q", got)
	}
	if err == nil {
		t.Fatal("a read on an unconfigured cache must not report a plain miss")
	}
	if errors.Is(err, ErrCircuitOpen) {
		t.Error("not configured must be distinguishable from a tripped circuit")
	}
}

// The real client and the unavailable one must stay interchangeable. Adding a
// method to the interface without implementing it here would otherwise only
// surface wherever a caller happened to construct the unavailable one.
func TestBothImplementationsSatisfyTheInterface(t *testing.T) {
	var _ Client = Unavailable()
	var _ Client = (*conn)(nil)
}

func TestRealClientReportsAvailable(t *testing.T) {
	var c Client = &conn{}
	if !c.Available() {
		t.Error("a real client must report itself available")
	}
}
