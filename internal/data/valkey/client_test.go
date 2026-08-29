package valkey

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// The client is a wrapper around a real protocol, with a circuit breaker that
// opens on real failures. Faking valkey-go's Client means reimplementing fifteen
// methods and building ValkeyResults by hand, which would test the fake. These
// run against a real Valkey; CI provides one.

// connectedClient returns a client against the test Valkey, or skips.
func connectedClient(t *testing.T) *conn {
	t.Helper()

	addr := strings.TrimSpace(os.Getenv("SUPATYPE_TEST_VALKEY_ADDR"))
	if addr == "" {
		t.Skip("SUPATYPE_TEST_VALKEY_ADDR is not set")
	}

	client, err := New(addr)
	if err != nil {
		t.Fatalf("connecting to %s: %v", addr, err)
	}
	t.Cleanup(client.Close)

	c, ok := client.(*conn)
	if !ok {
		t.Fatalf("New returned %T", client)
	}
	return c
}

// key returns a name unique to this test, so runs do not collide.
func key(t *testing.T, suffix string) string {
	t.Helper()
	return "supatype-test:" + strings.ReplaceAll(t.Name(), "/", ":") + ":" + suffix
}

// ─── The connection ───────────────────────────────────────────────────────────

// An address nothing answers on is a failure to report at startup, not a client
// that fails later on every request.
func TestNewReportsAnAddressItCannotReach(t *testing.T) {
	if _, err := New("127.0.0.1:1"); err == nil {
		t.Error("want an error")
	}
}

func TestAConnectedClientIsAvailable(t *testing.T) {
	if !connectedClient(t).Available() {
		t.Error("a connected client reports unavailable")
	}
}

// ─── Bytes ────────────────────────────────────────────────────────────────────

func TestBytesRoundTrip(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	k := key(t, "bytes")
	t.Cleanup(func() { _ = c.Del(ctx, k) })

	// A key nobody has written is a miss, not an error: the caller's next move
	// is to compute the value, not to give up.
	got, err := c.GetBytes(ctx, k)
	if err != nil {
		t.Fatalf("a missing key is not an error: %v", err)
	}
	if got != nil {
		t.Errorf("got %q, want nothing", got)
	}

	if err := c.SetBytes(ctx, k, []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	got, err = c.GetBytes(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "value" {
		t.Errorf("got %q", got)
	}

	if err := c.Del(ctx, k); err != nil {
		t.Fatal(err)
	}
	if got, err := c.GetBytes(ctx, k); err != nil || got != nil {
		t.Errorf("after delete: %q %v", got, err)
	}
}

// A TTL is what stops a cache growing without bound, so a value written with
// one has to actually carry it.
func TestSetWithATTL(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	k := key(t, "ttl")
	t.Cleanup(func() { _ = c.Del(ctx, k) })

	if err := c.SetBytes(ctx, k, []byte("value"), 60); err != nil {
		t.Fatal(err)
	}
	ttl, err := c.TTLSeconds(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 || ttl > 60 {
		t.Errorf("ttl = %d, want it between 1 and 60", ttl)
	}
}

// Valkey's own answers: -1 for a key with no expiry, -2 for one that is not
// there. Both are values, not errors.
func TestTTLOfAKeyWithNoExpiryAndOneThatIsGone(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	k := key(t, "no-expiry")
	t.Cleanup(func() { _ = c.Del(ctx, k) })

	if err := c.SetBytes(ctx, k, []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	if ttl, err := c.TTLSeconds(ctx, k); err != nil || ttl != -1 {
		t.Errorf("a key with no expiry: ttl = %d, err = %v", ttl, err)
	}
	if ttl, err := c.TTLSeconds(ctx, key(t, "absent")); err != nil || ttl != -2 {
		t.Errorf("a key that is not there: ttl = %d, err = %v", ttl, err)
	}
}

// Deleting nothing is not a command worth sending.
func TestDeletingNoKeysDoesNothing(t *testing.T) {
	c := connectedClient(t)
	if err := c.Del(context.Background()); err != nil {
		t.Errorf("Del with no keys: %v", err)
	}
}

func TestDeletingSeveralKeys(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	first, second := key(t, "a"), key(t, "b")

	for _, k := range []string{first, second} {
		if err := c.SetBytes(ctx, k, []byte("x"), 60); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Del(ctx, first, second); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{first, second} {
		if got, _ := c.GetBytes(ctx, k); got != nil {
			t.Errorf("%s survived", k)
		}
	}
}

// ─── Tenant config ────────────────────────────────────────────────────────────

func TestGetTenantConfig(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	ref := "supatype-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	k := "tenant:" + ref + ":config"
	t.Cleanup(func() { _ = c.Del(ctx, k) })

	// A tenant nobody has configured is a miss.
	got, err := c.GetTenantConfig(ctx, "supatype-test-nobody")
	if err != nil || got != nil {
		t.Fatalf("a missing tenant: %+v %v", got, err)
	}

	enabled := true
	want := TenantConfig{PostgRESTURL: "http://rest", Schema: "app", RestCacheEnabled: &enabled}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetBytes(ctx, k, raw, 60); err != nil {
		t.Fatal(err)
	}

	got, err = c.GetTenantConfig(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.PostgRESTURL != "http://rest" || got.Schema != "app" || !got.RestCacheOffered() {
		t.Errorf("config = %+v", got)
	}
}

// A tenant config that will not parse is reported. Treating it as absent would
// silently route a tenant at the defaults.
func TestATenantConfigThatWillNotParse(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	ref := "supatype-test-bad-json"
	k := "tenant:" + ref + ":config"
	t.Cleanup(func() { _ = c.Del(ctx, k) })

	if err := c.SetBytes(ctx, k, []byte("{not json"), 60); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetTenantConfig(ctx, ref); err == nil {
		t.Error("want an error")
	}
}

func TestRestCacheOffered(t *testing.T) {
	enabled, disabled := true, false

	if (*TenantConfig)(nil).RestCacheOffered() {
		t.Error("no config does not offer the cache")
	}
	if (&TenantConfig{}).RestCacheOffered() {
		t.Error("a config that says nothing does not offer the cache")
	}
	if (&TenantConfig{RestCacheEnabled: &disabled}).RestCacheOffered() {
		t.Error("a config that says no does not offer the cache")
	}
	if !(&TenantConfig{RestCacheEnabled: &enabled}).RestCacheOffered() {
		t.Error("a config that says yes does")
	}
}

// ─── Sets ─────────────────────────────────────────────────────────────────────

// The monthly-active-user tally: one set per organisation per day, whose
// members are dedupe keys.
func TestAddToExpiringSet(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	k := key(t, "mau")
	t.Cleanup(func() { _ = c.Del(ctx, k) })

	expireAt := time.Now().Add(time.Hour)
	for _, member := range []string{"user-1", "user-2", "user-1"} {
		if err := c.AddToExpiringSet(ctx, k, member, expireAt); err != nil {
			t.Fatal(err)
		}
	}

	// The expiry is reapplied on every add, so a set can never be left without
	// one because the process died between the two commands.
	ttl, err := c.TTLSeconds(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 {
		t.Errorf("ttl = %d, want the set to expire", ttl)
	}
}

// ─── Scanning ─────────────────────────────────────────────────────────────────

// Scanning is how the admin API lists and purges a tenant's cache entries, so
// the cursor has to be usable across pages.
func TestScanPage(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	prefix := key(t, "scan") + ":"

	var written []string
	for i := 0; i < 20; i++ {
		k := prefix + string(rune('a'+i))
		written = append(written, k)
		if err := c.SetBytes(ctx, k, []byte("x"), 60); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = c.Del(ctx, written...) })

	var found []string
	var cursor uint64
	for i := 0; i < 100; i++ {
		keys, next, err := c.ScanPage(ctx, cursor, prefix+"*", 5)
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, keys...)
		cursor = next
		if cursor == 0 {
			break
		}
	}

	sort.Strings(found)
	sort.Strings(written)
	if len(found) != len(written) {
		t.Fatalf("scanned %d keys, wrote %d", len(found), len(written))
	}
	for i := range found {
		if found[i] != written[i] {
			t.Errorf("scanned %v", found)
			break
		}
	}
}

// A count of zero is a hint, not a request for nothing.
func TestScanPageDefaultsItsCount(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	k := key(t, "count")
	if err := c.SetBytes(ctx, k, []byte("x"), 60); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Del(ctx, k) })

	keys, _, err := c.ScanPage(ctx, 0, k, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Errorf("keys = %v", keys)
	}
}

// ─── Failure ──────────────────────────────────────────────────────────────────

// A client whose connection has gone reports on every operation rather than
// answering with a miss, which a caller would write back into a cache that is
// not there.
func TestEveryOperationReportsALostConnection(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	c.vc.Close()

	// A fresh breaker each time, so this is the operation failing rather than
	// the circuit that a previous one opened.
	reset := func() { c.recordSuccess() }

	reset()
	if _, err := c.GetTenantConfig(ctx, "ref"); err == nil {
		t.Error("GetTenantConfig")
	}
	reset()
	if _, err := c.GetBytes(ctx, "k"); err == nil {
		t.Error("GetBytes")
	}
	reset()
	if err := c.SetBytes(ctx, "k", []byte("v"), 0); err == nil {
		t.Error("SetBytes without a TTL")
	}
	reset()
	if err := c.SetBytes(ctx, "k", []byte("v"), 60); err == nil {
		t.Error("SetBytes with a TTL")
	}
	reset()
	if err := c.Del(ctx, "k"); err == nil {
		t.Error("Del")
	}
	reset()
	if err := c.AddToExpiringSet(ctx, "k", "m", time.Now().Add(time.Hour)); err == nil {
		t.Error("AddToExpiringSet")
	}
	reset()
	if _, _, err := c.ScanPage(ctx, 0, "*", 10); err == nil {
		t.Error("ScanPage")
	}
	reset()
	if _, err := c.TTLSeconds(ctx, "k"); err == nil {
		t.Error("TTLSeconds")
	}
}

// The SADD lands and the EXPIREAT does not: reported, because a set with no
// expiry is one that grows for ever.
func TestAddToExpiringSetReportsAFailedExpiry(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	k := key(t, "bad-expiry")
	t.Cleanup(func() { _ = c.Del(ctx, k) })

	// A timestamp Valkey will not accept as an expiry.
	if err := c.AddToExpiringSet(ctx, k, "m", time.Unix(-1<<62, 0)); err == nil {
		t.Error("want an error")
	}
}

// ─── The circuit breaker ──────────────────────────────────────────────────────

// The breaker is what stops a failing Valkey turning every request into a
// 100ms wait. It is pure state, so it is tested as such.
func TestTheCircuitOpensAfterRepeatedFailures(t *testing.T) {
	c := &conn{}

	for i := 0; i < cbFailThreshold-1; i++ {
		c.recordFailure()
		if err := c.checkCircuit(); err != nil {
			t.Fatalf("the circuit opened after %d failures, want %d", i+1, cbFailThreshold)
		}
	}

	c.recordFailure()
	if err := c.checkCircuit(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("the circuit did not open: %v", err)
	}
}

// One success is enough to close it, because the point is to stop hammering a
// service that is down, not to punish it once it is back.
func TestASuccessClosesTheCircuit(t *testing.T) {
	c := &conn{}
	for i := 0; i < cbFailThreshold; i++ {
		c.recordFailure()
	}
	c.recordSuccess()

	if err := c.checkCircuit(); err != nil {
		t.Errorf("the circuit stayed open after a success: %v", err)
	}
}

// A success before the threshold clears the count, so intermittent failures do
// not accumulate into an open circuit.
func TestFailuresDoNotAccumulateAcrossSuccesses(t *testing.T) {
	c := &conn{}
	for i := 0; i < cbFailThreshold*3; i++ {
		c.recordFailure()
		c.recordSuccess()
	}
	if err := c.checkCircuit(); err != nil {
		t.Errorf("the circuit opened on alternating results: %v", err)
	}
}

// After the open period one request is let through to find out whether the
// service is back. Exactly one: a thundering herd against a recovering Valkey
// is what the breaker exists to prevent.
func TestOnlyOneProbeIsAllowedThrough(t *testing.T) {
	c := &conn{}
	for i := 0; i < cbFailThreshold; i++ {
		c.recordFailure()
	}
	c.openAt = time.Now().Add(-cbOpenDuration - time.Second)

	if err := c.checkCircuit(); err != nil {
		t.Fatalf("the probe was refused: %v", err)
	}
	if err := c.checkCircuit(); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("a second probe was allowed through: %v", err)
	}
}

// A probe that fails puts the circuit back to fully open and lets the next
// window start over, rather than leaving the probe flag set for ever.
func TestAFailedProbeAllowsAnotherLater(t *testing.T) {
	c := &conn{}
	for i := 0; i < cbFailThreshold; i++ {
		c.recordFailure()
	}
	c.openAt = time.Now().Add(-cbOpenDuration - time.Second)

	if err := c.checkCircuit(); err != nil {
		t.Fatal(err)
	}
	c.recordFailure() // the probe failed

	c.mu.Lock()
	c.openAt = time.Now().Add(-cbOpenDuration - time.Second)
	c.mu.Unlock()

	if err := c.checkCircuit(); err != nil {
		t.Errorf("no further probe was allowed: %v", err)
	}
}

// While the circuit is open no operation reaches the network, which is the
// whole point: an open circuit answers at once.
func TestAnOpenCircuitRefusesEveryOperation(t *testing.T) {
	c := connectedClient(t)
	ctx := context.Background()
	for i := 0; i < cbFailThreshold; i++ {
		c.recordFailure()
	}

	if _, err := c.GetTenantConfig(ctx, "ref"); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("GetTenantConfig: %v", err)
	}
	if _, err := c.GetBytes(ctx, "k"); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("GetBytes: %v", err)
	}
	if err := c.SetBytes(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("SetBytes: %v", err)
	}
	if err := c.Del(ctx, "k"); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Del: %v", err)
	}
	if err := c.AddToExpiringSet(ctx, "k", "m", time.Now()); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("AddToExpiringSet: %v", err)
	}
	if _, _, err := c.ScanPage(ctx, 0, "*", 10); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("ScanPage: %v", err)
	}
	if _, err := c.TTLSeconds(ctx, "k"); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("TTLSeconds: %v", err)
	}
}
