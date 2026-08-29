package valkeytest

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/supatype/server/internal/data/valkey"
)

// Every package that tests against a cache tests against this one, so what it
// gets wrong they all get wrong. Two things already had: a TTL of zero stored a
// value that was already expired, and a scan cursor that counted positions
// skipped keys when the caller deleted as it paged.

func TestStoringAndReadingBack(t *testing.T) {
	c := New()
	ctx := t.Context()

	if got, err := c.GetBytes(ctx, "absent"); err != nil || got != nil {
		t.Errorf("a key that was never set: %q, %v", got, err)
	}
	if err := c.SetBytes(ctx, "k", []byte("v"), 60); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetBytes(ctx, "k")
	if err != nil || string(got) != "v" {
		t.Errorf("GetBytes = %q, %v", got, err)
	}

	// A TTL of zero or less means no expiry, as it does to the real client.
	// Storing now+0 instead made every such value already expired.
	c.Put("forever", []byte("v"), 0)
	if got, _ := c.GetBytes(ctx, "forever"); string(got) != "v" {
		t.Errorf("a value with no expiry read back as %q", got)
	}

	// And one whose expiry has passed is a miss, not a stale hit.
	c.Put("stale", []byte("v"), -1)
	c.values["stale"] = entry{value: []byte("v"), expireAt: time.Now().Add(-time.Second)}
	if got, _ := c.GetBytes(ctx, "stale"); got != nil {
		t.Errorf("an expired value read back as %q", got)
	}

	if err := c.Del(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.GetBytes(ctx, "k"); got != nil {
		t.Errorf("a deleted key read back as %q", got)
	}

	// Keys lists what is still readable, which is how a test asserts what a
	// flush left behind. An expired key is not one of them.
	if got := c.Keys(); !reflect.DeepEqual(got, []string{"forever"}) {
		t.Errorf("Keys = %v, want only the value that has not expired", got)
	}

	if c.Gets == 0 || c.Sets == 0 || c.Dels == 0 {
		t.Errorf("the counters did not count: %d gets, %d sets, %d dels", c.Gets, c.Sets, c.Dels)
	}
}

// A key a scan returns and a read does not find is a key that expired between
// the two. Real Valkey allows it, and a consumer that assumes otherwise is
// wrong in production rather than in its tests.
func TestAKeyThatVanishesBetweenScanAndRead(t *testing.T) {
	c := New()

	c.PutVanishing("gone")
	if keys, _, err := c.ScanPage(t.Context(), 0, "go*", 10); err != nil || len(keys) != 1 {
		t.Fatalf("the scan did not return it: %v, %v", keys, err)
	}
	if got, _ := c.GetBytes(t.Context(), "gone"); got != nil {
		t.Errorf("the read found it: %q", got)
	}
}

// The cursor remembers the last key handed out rather than a position, so a
// caller that deletes as it pages still sees every key that was there
// throughout. The flush endpoint does exactly that.
func TestPagingWhileDeleting(t *testing.T) {
	c := New()
	c.ScanPageSize = 2
	for _, key := range []string{"a", "b", "c", "d", "e"} {
		c.Put("rest:"+key, []byte("v"), 0)
	}

	var seen []string
	var cursor uint64
	for {
		page, next, err := c.ScanPage(t.Context(), cursor, "rest:*", 10)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, page...)
		if err := c.Del(t.Context(), page...); err != nil {
			t.Fatal(err)
		}
		if next == 0 {
			break
		}
		cursor = next
	}

	want := []string{"rest:a", "rest:b", "rest:c", "rest:d", "rest:e"}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("scanned %v, want %v", seen, want)
	}
	if left := c.Keys(); len(left) != 0 {
		t.Errorf("keys left behind: %v", left)
	}
}

// A pattern the scan does not match returns nothing rather than everything.
func TestScanningForNothing(t *testing.T) {
	c := New()
	c.Put("rest:a", []byte("v"), 0)

	keys, cursor, err := c.ScanPage(t.Context(), 0, "graphql:*", 10)
	if err != nil || keys != nil || cursor != 0 {
		t.Errorf("ScanPage = %v, %d, %v", keys, cursor, err)
	}
}

// What TTLSeconds answers, in the three shapes the real client answers them.
func TestTheTTLAnswers(t *testing.T) {
	c := New()

	if got, _ := c.TTLSeconds(t.Context(), "absent"); got != -2 {
		t.Errorf("a key that does not exist: %d, want -2", got)
	}
	c.Put("forever", []byte("v"), 0)
	if got, _ := c.TTLSeconds(t.Context(), "forever"); got != -1 {
		t.Errorf("a key with no expiry: %d, want -1", got)
	}
	c.Put("soon", []byte("v"), 60)
	if got, _ := c.TTLSeconds(t.Context(), "soon"); got <= 0 || got > 60 {
		t.Errorf("a key with an expiry: %d, want a positive count under a minute", got)
	}
}

// Tenant configuration, and the sets the MAU tally is counted in.
func TestTenantsAndSets(t *testing.T) {
	c := New().WithTenant("proj-a", &valkey.TenantConfig{Schema: "app"})
	ctx := t.Context()

	cfg, err := c.GetTenantConfig(ctx, "proj-a")
	if err != nil || cfg == nil || cfg.Schema != "app" {
		t.Errorf("GetTenantConfig = %+v, %v", cfg, err)
	}
	if cfg, err := c.GetTenantConfig(ctx, "proj-unknown"); err != nil || cfg != nil {
		t.Errorf("an unknown tenant: %+v, %v", cfg, err)
	}

	if err := c.AddToExpiringSet(ctx, "mau:2026-08", "user-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := c.AddToExpiringSet(ctx, "mau:2026-08", "user-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := c.SetMembers("mau:2026-08"); !reflect.DeepEqual(got, []string{"user-1"}) {
		t.Errorf("members = %v, want the one user counted once", got)
	}
	if got := c.SetMembers("mau:never"); len(got) != 0 {
		t.Errorf("an empty set = %v", got)
	}
}

// Availability, and the failures a test asks for. Every consumer's interesting
// behaviour is what it does when the cache is there but will not answer, so a
// double that cannot be told to fail is not much of one.
func TestTheFailuresATestCanAskFor(t *testing.T) {
	ctx := t.Context()

	if !New().Available() {
		t.Error("a client with no cache configured reported unavailable")
	}
	if (&Client{Unavailable: true}).Available() {
		t.Error("Unavailable did not report unavailable")
	}

	c := New()
	c.GetErr, c.SetErr, c.DelErr = ErrFailed, ErrFailed, ErrFailed
	c.ScanErr, c.TTLErr, c.TenantErr, c.SetAddErr = ErrFailed, ErrFailed, ErrFailed, ErrFailed

	if _, err := c.GetBytes(ctx, "k"); !errors.Is(err, ErrFailed) {
		t.Errorf("GetBytes: %v", err)
	}
	if err := c.SetBytes(ctx, "k", nil, 0); !errors.Is(err, ErrFailed) {
		t.Errorf("SetBytes: %v", err)
	}
	if err := c.Del(ctx, "k"); !errors.Is(err, ErrFailed) {
		t.Errorf("Del: %v", err)
	}
	if _, _, err := c.ScanPage(ctx, 0, "*", 10); !errors.Is(err, ErrFailed) {
		t.Errorf("ScanPage: %v", err)
	}
	if _, err := c.TTLSeconds(ctx, "k"); !errors.Is(err, ErrFailed) {
		t.Errorf("TTLSeconds: %v", err)
	}
	if _, err := c.GetTenantConfig(ctx, "proj-a"); !errors.Is(err, ErrFailed) {
		t.Errorf("GetTenantConfig: %v", err)
	}
	if err := c.AddToExpiringSet(ctx, "k", "m", time.Now()); !errors.Is(err, ErrFailed) {
		t.Errorf("AddToExpiringSet: %v", err)
	}

	// SetErrAfter is how a write that fails partway through a multi-step
	// operation is arranged: the first succeeds, the next does not.
	after := New()
	after.SetErrAfter = 1
	if err := after.SetBytes(ctx, "a", nil, 0); err != nil {
		t.Errorf("the first write: %v", err)
	}
	if err := after.SetBytes(ctx, "b", nil, 0); !errors.Is(err, ErrFailed) {
		t.Errorf("the second write: %v", err)
	}

	// Close is there for the interface; it holds nothing to release.
	New().Close()
}
