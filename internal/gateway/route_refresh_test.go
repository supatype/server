package gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/supatype/server/internal/data/keyspace"
	"github.com/supatype/server/internal/data/keyspace/keyspacetest"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/testutil"
)

const refreshRef = "proj"

// refreshFixture is a gateway merging from a keyspace whose tenant config the control plane can change
// after the gateway started: the case a managed project gateway has, with no manifest file to watch.
type refreshFixture struct {
	ks   *keyspacetest.Client
	live *liveManifests
}

func newRefreshFixture(t *testing.T) *refreshFixture {
	t.Helper()
	ks := keyspacetest.New().WithTenant(refreshRef, &keyspace.TenantConfig{PostgRESTURL: "http://rest-v1"})
	file := &proxy.RouteManifest{Schema: "public"}
	live := newLiveManifests(file)
	live.merge = func(ctx context.Context, f *proxy.RouteManifest) (*proxy.RouteManifest, error) {
		return keyspace.LoadMergedManagedManifest(ctx, ks, refreshRef, f)
	}
	live.Reapply(file)
	if got := live.For(nil).PostgRESTURL; got != "http://rest-v1" {
		t.Fatalf("start state = %q, want the config the gateway started with", got)
	}
	return &refreshFixture{ks: ks, live: live}
}

// publishV2 is the control plane writing a new config after the gateway started.
func (f *refreshFixture) writeV2() {
	f.ks.WithTenant(refreshRef, &keyspace.TenantConfig{PostgRESTURL: "http://rest-v2"})
}

func (f *refreshFixture) start(t *testing.T, timing refreshTiming) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.live.refreshOnChange(ctx, f.ks, keyspace.ChannelConfigChanged, timing)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("refreshOnChange did not stop when its context was cancelled")
		}
	})
}

func (f *refreshFixture) waitSubscribed(t *testing.T) {
	t.Helper()
	testutil.Eventually(t, "the gateway to subscribe", func() bool { return f.ks.Subscribers(keyspace.ChannelConfigChanged) == 1 })
}

func (f *refreshFixture) servesV2() bool { return f.live.For(nil).PostgRESTURL == "http://rest-v2" }

var slowTick = refreshTiming{Every: time.Hour, RetryFirst: 5 * time.Millisecond, RetryMax: 20 * time.Millisecond}

func TestRoutesFollowAChangeMessage(t *testing.T) {
	f := newRefreshFixture(t)
	f.start(t, slowTick)
	f.waitSubscribed(t)

	f.writeV2()
	if f.servesV2() {
		t.Fatal("the gateway saw the change with no message and no tick: the test proves nothing")
	}
	f.ks.Publish(keyspace.ChannelConfigChanged)
	testutil.Eventually(t, "the routes to follow the message", f.servesV2)
}

func TestRoutesFollowThePeriodicTickWhenNoMessageArrives(t *testing.T) {
	f := newRefreshFixture(t)
	f.ks.SubscribeErr = errors.New("pub/sub unavailable")
	f.start(t, refreshTiming{Every: 20 * time.Millisecond, RetryFirst: time.Hour, RetryMax: time.Hour})

	f.writeV2()
	testutil.Eventually(t, "the routes to follow the tick", f.servesV2)
}

func TestRoutesFollowAReconnectThatLostTheMessage(t *testing.T) {
	f := newRefreshFixture(t)
	f.start(t, slowTick)
	f.waitSubscribed(t)

	f.writeV2()
	f.ks.Drop(errors.New("connection reset"))
	testutil.Eventually(t, "the routes to be re-read on resubscribe", f.servesV2)
	testutil.Eventually(t, "the gateway to be subscribed again", func() bool { return f.ks.Subscribers(keyspace.ChannelConfigChanged) == 1 })
}

func TestAFailedMergeKeepsTheRoutesItHas(t *testing.T) {
	f := newRefreshFixture(t)
	f.start(t, slowTick)
	f.waitSubscribed(t)

	f.ks.TenantErr = errors.New("keyspace away")
	f.ks.Publish(keyspace.ChannelConfigChanged)
	time.Sleep(50 * time.Millisecond)
	if got := f.live.For(nil).PostgRESTURL; got != "http://rest-v1" {
		t.Fatalf("routes after a failed merge = %q, want the previous ones", got)
	}
}

// countingTenants records how often the tenant cache is flushed, which Reapply does every time it
// installs a manifest: an unchanged keyspace must not cause one.
type countingTenants struct{ flushes atomic.Int32 }

func (*countingTenants) Get(context.Context, string) (*proxy.RouteManifest, error) { return nil, nil }
func (c *countingTenants) Flush()                                                  { c.flushes.Add(1) }

func TestAnUnchangedKeyspaceIsNotReappliedAndAChangedOneIs(t *testing.T) {
	f := newRefreshFixture(t)
	tenants := &countingTenants{}
	f.live.tenant = tenants
	var reads atomic.Int32
	read := f.live.merge
	f.live.merge = func(ctx context.Context, m *proxy.RouteManifest) (*proxy.RouteManifest, error) {
		reads.Add(1)
		return read(ctx, m)
	}
	f.start(t, refreshTiming{Every: 5 * time.Millisecond, RetryFirst: time.Hour, RetryMax: time.Hour})

	// The fixture applied the starting config before the counter was attached. The keyspace is read on
	// every tick, and none of those reads is applied while it still holds that config.
	testutil.Eventually(t, "several unchanged ticks", func() bool { return reads.Load() >= 5 })
	if got := tenants.flushes.Load(); got != 0 {
		t.Fatalf("applied %d times over unchanged ticks, want none", got)
	}

	f.writeV2()
	testutil.Eventually(t, "the changed keyspace to be applied", f.servesV2)
	if got := tenants.flushes.Load(); got != 1 {
		t.Fatalf("applied %d times after one change, want once", got)
	}
}

func TestARefreshAfterAFailedReadAppliesEvenIfNothingChanged(t *testing.T) {
	f := newRefreshFixture(t)
	tenants := &countingTenants{}
	f.live.tenant = tenants
	f.live.refresh()
	f.ks.TenantErr = errors.New("keyspace away")
	f.live.refresh()
	f.ks.TenantErr = nil
	before := tenants.flushes.Load()
	f.live.refresh()
	if got := tenants.flushes.Load(); got != before+1 {
		t.Fatalf("flushes after recovery = %d, want %d: the first good read after an error must apply", got, before+1)
	}
}

func TestRetryWaitIsSpreadByJitterButNeverShortened(t *testing.T) {
	const wait = 10 * time.Second
	if got := jittered(wait, 0); got != wait {
		t.Fatalf("jittered(wait, 0) = %v, want the wait itself", got)
	}
	if got := jittered(wait, 0.5); got != 12500*time.Millisecond {
		t.Fatalf("jittered(wait, 0.5) = %v, want 12.5s", got)
	}
	if got := jittered(wait, 0.999999); got >= 15*time.Second {
		t.Fatalf("jittered(wait, ~1) = %v, want under 1.5x the wait", got)
	}
}

func TestSubscribeRetryUsesTheInjectedRandomSource(t *testing.T) {
	f := newRefreshFixture(t)
	f.ks.SubscribeErr = errors.New("pub/sub unavailable")
	var draws atomic.Int32
	timing := refreshTiming{Every: time.Hour, RetryFirst: time.Millisecond, RetryMax: 2 * time.Millisecond,
		Rand: func() float64 { draws.Add(1); return 0 }}
	f.start(t, timing)
	testutil.Eventually(t, "the failed subscription to be retried with a drawn wait", func() bool { return draws.Load() >= 2 })
}

// scriptedSource is a routeChangeSource whose every Subscribe call is a step the test wrote, so the
// retry loop is driven to a branch rather than waited on until a goroutine happens to be there.
type scriptedSource struct {
	steps []func(ctx context.Context, onSubscribed func()) error
	calls int
}

func (s *scriptedSource) Subscribe(ctx context.Context, _ string, onSubscribed, _ func()) error {
	step := s.steps[s.calls]
	s.calls++
	return step(ctx, onSubscribed)
}

// runSubscribe runs subscribeUntilDone to its return on this goroutine. The loop can only return
// once ctx is done, so a test that never cancels fails by the deadline instead of hanging.
func runSubscribe(t *testing.T, ctx context.Context, src routeChangeSource, timing refreshTiming) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		subscribeUntilDone(ctx, src, "ch", timing, func() {})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("subscribeUntilDone did not return")
	}
}

func TestTheDefaultJitterSourceStaysInsideTheUnitInterval(t *testing.T) {
	for range 1000 {
		if r := (refreshTiming{}).random(); r < 0 || r >= 1 {
			t.Fatalf("random() = %v, want a value in [0,1)", r)
		}
	}
}

// The wait before subscribing again was reached by cancelling a live goroutine from outside, which
// lands in the wait only if the goroutine got there first. Here the cancel happens inside the draw
// of the jitter, so the wait always starts with the context already done.
func TestACancelDuringTheRetryWaitEndsTheLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &scriptedSource{steps: []func(context.Context, func()) error{
		func(context.Context, func()) error { return errors.New("pub/sub unavailable") },
	}}
	timing := refreshTiming{Every: time.Hour, RetryFirst: time.Hour, RetryMax: time.Hour,
		Rand: func() float64 { cancel(); return 0 }}
	runSubscribe(t, ctx, src, timing)
	if src.calls != 1 {
		t.Fatalf("subscribed %d times, want once: the loop must stop in the wait, not subscribe again", src.calls)
	}
}

// A subscription that was confirmed and then lost is retried after a wait, and
// a cancel that arrives while Subscribe is returning ends the
// loop before any wait at all.
func TestAConfirmedSubscriptionThatIsLostIsSubscribedAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waits []float64
	src := &scriptedSource{steps: []func(context.Context, func()) error{
		func(_ context.Context, onSubscribed func()) error {
			onSubscribed()
			return errors.New("connection reset")
		},
		func(ctx context.Context, _ func()) error { cancel(); return ctx.Err() },
	}}
	timing := refreshTiming{Every: time.Hour, RetryFirst: time.Millisecond, RetryMax: time.Millisecond,
		Rand: func() float64 { waits = append(waits, 0); return 0 }}
	runSubscribe(t, ctx, src, timing)
	if src.calls != 2 || len(waits) != 1 {
		t.Fatalf("subscribed %d times and drew %d waits, want 2 and 1", src.calls, len(waits))
	}
}
