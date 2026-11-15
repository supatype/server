package gateway

import (
	"context"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// A project gateway has no manifest file to watch, so the only thing that tells it the control plane
// wrote a new config (a function deployed, a tier changed) is the keyspace. It merged that config once
// at start and served what it found until it was restarted: a Free project's first function deploy
// answered 502 for ever, because the worker URL was written after the gateway came up.
//
// So the gateway re-reads on three triggers. A pub/sub message from the control plane is the prompt
// one. Every confirmed subscription is the second, because a message published while the connection
// was down is gone. A periodic tick is the third, because pub/sub is fire and forget and nothing
// else would notice a message that never arrived.

// routeChangeSource is the keyspace's pub/sub: a keyspace.Client in a running server.
type routeChangeSource interface {
	Subscribe(ctx context.Context, channel string, onSubscribed, onMessage func()) error
}

// Production pacing: the safety-net re-merge, and the wait before subscribing again after a failure.
var defaultRefreshTiming = refreshTiming{Every: time.Minute, RetryFirst: time.Second, RetryMax: 30 * time.Second}

// refreshTiming is how a route refresh paces itself.
type refreshTiming struct {
	// Every is the safety-net re-merge, for a message that was never sent or never arrived.
	Every time.Duration
	// RetryFirst and RetryMax bound the wait before subscribing again, which doubles up to RetryMax.
	RetryFirst, RetryMax time.Duration
	// Rand returns a value in [0,1) used to spread the wait, so project gateways that lost the same
	// keyspace do not all subscribe again at the same instant. Nil means math/rand.
	Rand func() float64
}

// maxJitter is the most a wait grows by, as a fraction of the wait.
const maxJitter = 0.5

// jittered lengthens wait by up to maxJitter of itself, by r in [0,1).
func jittered(wait time.Duration, r float64) time.Duration {
	return wait + time.Duration(float64(wait)*maxJitter*r)
}

func (t refreshTiming) random() float64 {
	if t.Rand != nil {
		return t.Rand()
	}
	return rand.Float64()
}

// refreshOnChange re-merges the routes from the keyspace until ctx is done.
func (l *liveManifests) refreshOnChange(ctx context.Context, src routeChangeSource, channel string, t refreshTiming) {
	// One slot: a burst of changes is one re-merge, and a trigger never blocks the keyspace's reader.
	wake := make(chan struct{}, 1)
	poke := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	go subscribeUntilDone(ctx, src, channel, t, poke)

	tick := time.NewTicker(t.Every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-tick.C:
		}
		l.refresh()
	}
}

// subscribeUntilDone keeps a subscription on channel, calling poke on every message and every
// confirmed subscription, and subscribing again after a failure.
func subscribeUntilDone(ctx context.Context, src routeChangeSource, channel string, t refreshTiming, poke func()) {
	wait := t.RetryFirst
	for ctx.Err() == nil {
		var confirmed atomic.Bool
		err := src.Subscribe(ctx, channel, func() { confirmed.Store(true); poke() }, poke)
		if ctx.Err() != nil {
			return
		}
		if confirmed.Load() {
			wait = t.RetryFirst
		}
		sleep := jittered(wait, t.random())
		logrus.WithError(err).WithField("retry_in", sleep).Warn("serve: route change subscription lost, the periodic refresh still applies")
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}
		wait = min(wait*2, t.RetryMax)
	}
}
