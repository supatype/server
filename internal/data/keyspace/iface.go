// Package keyspace is the service's RESP keyspace client.
//
// The name is the protocol's, not a product's: what this package talks to is a
// RESP keyspace, which is pg_keyspace running inside the project's Postgres, or
// a Valkey or Redis server where a deployment points it at one. It was called
// valkey when only one of those existed, and consumers had to read the import
// path as if it named the backend.
package keyspace

import (
	"context"
	"errors"
	"time"
)

// ErrUnavailable is returned by every operation on the unavailable client.
//
// It is a distinct error from ErrCircuitOpen: the circuit opens when a
// configured keyspace is failing, whereas this means no keyspace was configured
// at all. Callers that treat a cache as optional check Available and skip; callers
// that require it should refuse to start rather than discover this per request.
var ErrUnavailable = errors.New("keyspace: not configured")

// Client is the keyspace surface this service uses.
//
// It is an interface, and there is an explicit Unavailable implementation, so
// that "no keyspace configured" is a value rather than a nil pointer. Every
// consumer used to guard with `if vc != nil`, and each guard was a chance to
// forget one and panic on a deployment that simply had no cache.
type Client interface {
	// Available reports whether this client talks to a real keyspace. It is the
	// honest replacement for a nil check, and the only thing callers should
	// branch on when a cache is optional.
	Available() bool

	GetTenantConfig(ctx context.Context, ref string) (*TenantConfig, error)
	GetBytes(ctx context.Context, key string) ([]byte, error)
	SetBytes(ctx context.Context, key string, value []byte, ttlSeconds int) error
	Del(ctx context.Context, keys ...string) error
	ScanPage(ctx context.Context, cursor uint64, pattern string, count int) (keys []string, next uint64, err error)
	TTLSeconds(ctx context.Context, key string) (int, error)
	AddToExpiringSet(ctx context.Context, key, member string, expireAt time.Time) error
	Close()
}

// Unavailable returns a Client for a deployment with no keyspace configured.
//
// Reads report ErrUnavailable rather than a cache miss. A miss would be a lie:
// it invites the caller to write the value back, and the write would fail too.
func Unavailable() Client { return unavailableClient{} }

type unavailableClient struct{}

func (unavailableClient) Available() bool { return false }

func (unavailableClient) GetTenantConfig(context.Context, string) (*TenantConfig, error) {
	return nil, ErrUnavailable
}

func (unavailableClient) GetBytes(context.Context, string) ([]byte, error) {
	return nil, ErrUnavailable
}

func (unavailableClient) SetBytes(context.Context, string, []byte, int) error {
	return ErrUnavailable
}

func (unavailableClient) Del(context.Context, ...string) error { return ErrUnavailable }

func (unavailableClient) ScanPage(context.Context, uint64, string, int) ([]string, uint64, error) {
	return nil, 0, ErrUnavailable
}

func (unavailableClient) TTLSeconds(context.Context, string) (int, error) {
	return 0, ErrUnavailable
}

func (unavailableClient) AddToExpiringSet(context.Context, string, string, time.Time) error {
	return ErrUnavailable
}

func (unavailableClient) Close() {}
