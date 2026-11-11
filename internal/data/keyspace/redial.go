package keyspace

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// ErrNotConnected is returned by every operation on a client whose first connection failed and
// whose redial has not yet succeeded. It is not ErrUnavailable: a keyspace is configured, and the
// start log said why it could not be reached.
var ErrNotConnected = errors.New("keyspace: not connected yet, redialling in the background")

// Down is whether err is a client with no connection at all, configured or not. The server says
// so once, at start, so a caller that treats the cache as optional need not log it per request.
func Down(err error) bool {
	return errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotConnected)
}

// The backoff between redials: the first soon, as a keyspace in a Postgres that is still starting
// is the usual case, then doubling to a ceiling a fixed password is never long waiting for.
const (
	redialFirst = time.Second
	redialMax   = 30 * time.Second
)

// Dial connects to addr as New does. When that first dial fails it returns the error, for the
// caller to log once, together with a client that answers ErrNotConnected and keeps dialling in
// the background with backoff until it connects or is closed.
//
// 0.9.2 release test round 2 (R2-S13): a server whose first login was refused bypassed its caches
// until it was restarted, though the password had been put right in the keyspace, and a keyspace
// restarted under a running server was the same.
func Dial(addr string, auth Auth) (Client, error) {
	dial := func() (Client, error) { return New(addr, auth) }
	c, err := dial()
	if err == nil {
		return c, nil
	}
	return newRedialing(dial, redialFirst, redialMax, func() {
		logrus.WithField("addr", addr).Info("keyspace: connected after the first connection failed; caches are back on")
	}), err
}

// redialing is a Client whose connection is not there yet. Until its background dial succeeds
// every operation answers ErrNotConnected; after, every operation is the connected client's.
type redialing struct {
	live    atomic.Pointer[liveClient]
	stop    chan struct{}
	done    chan struct{}
	stopped sync.Once
}

// liveClient boxes the connected client, as an atomic pointer needs a concrete type.
type liveClient struct{ Client }

func newRedialing(dial func() (Client, error), first, ceiling time.Duration, connected func()) *redialing {
	r := &redialing{stop: make(chan struct{}), done: make(chan struct{})}
	go r.redial(dial, first, ceiling, connected)
	return r
}

// redial dials after each wait, doubling it up to ceiling, until a dial succeeds or Close.
func (r *redialing) redial(dial func() (Client, error), wait, ceiling time.Duration, connected func()) {
	defer close(r.done)
	for {
		timer := time.NewTimer(wait)
		select {
		case <-r.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
		if c, err := dial(); err == nil {
			r.live.Store(&liveClient{c})
			connected()
			return
		}
		wait = min(wait*2, ceiling)
	}
}

// client is the connected client, or one that answers ErrNotConnected.
func (r *redialing) client() Client {
	if l := r.live.Load(); l != nil {
		return l.Client
	}
	return notConnected
}

// notConnected answers every operation as the redialing client does before it connects.
var notConnected Client = unavailableClient{err: ErrNotConnected}

func (r *redialing) Available() bool { return r.client().Available() }

func (r *redialing) GetTenantConfig(ctx context.Context, ref string) (*TenantConfig, error) {
	return r.client().GetTenantConfig(ctx, ref)
}

func (r *redialing) GetBytes(ctx context.Context, key string) ([]byte, error) {
	return r.client().GetBytes(ctx, key)
}

func (r *redialing) SetBytes(ctx context.Context, key string, value []byte, ttlSeconds int) error {
	return r.client().SetBytes(ctx, key, value, ttlSeconds)
}

func (r *redialing) Del(ctx context.Context, keys ...string) error {
	return r.client().Del(ctx, keys...)
}

func (r *redialing) ScanPage(ctx context.Context, cursor uint64, pattern string, count int) ([]string, uint64, error) {
	return r.client().ScanPage(ctx, cursor, pattern, count)
}

func (r *redialing) TTLSeconds(ctx context.Context, key string) (int, error) {
	return r.client().TTLSeconds(ctx, key)
}

func (r *redialing) AddToExpiringSet(ctx context.Context, key, member string, expireAt time.Time) error {
	return r.client().AddToExpiringSet(ctx, key, member, expireAt)
}

// Close stops the redial, waits for a dial in progress to finish, and closes what it connected.
func (r *redialing) Close() {
	r.stopped.Do(func() {
		close(r.stop)
		<-r.done
		r.client().Close()
	})
}
