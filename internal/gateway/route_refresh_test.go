package gateway

import (
	"context"
	"errors"
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
