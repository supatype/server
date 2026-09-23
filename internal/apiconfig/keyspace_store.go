package apiconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// KeyspaceStore persists the API configuration in the platform keyspace.
//
// # Why this exists
//
// FileStore writes `.supatype/api-config.json`. On a managed pod that path is on the container's
// own filesystem — the tenant-gateway manifest mounts no volume for it — so every restart brings
// the process back with `DefaultApiConfig()`. The REST response cache is configured here, which
// means **an ordinary pod restart silently turned server-side caching off for the whole project**,
// and nothing anywhere reported it. A developer who enabled caching on a table found it off again
// with no event to point at.
//
// # Why the keyspace and not a volume or a table
//
// A PVC would work today — the gateway runs one replica — and would stop working the moment it
// runs two, because a ReadWriteOnce volume cannot be shared. The failure would arrive as two pods
// disagreeing about what is cached.
//
// A table in `_supatype` would be the most orthodox home, but the server owns no DDL there: every
// table in that schema is created by the schema engine, and adding one from here would either move
// that boundary or need a cross-repo migration and an engine floor bump.
//
// The platform keyspace needs neither. It already holds `tenant:{ref}:config` and
// `tenant:{ref}:manifest`, it is reachable from every replica, and everything under the `tenant:`
// prefix is durable by construction — that is the whole point of plan decision D4, which moved the
// response cache keys *out* from under `tenant:` precisely so `tenant:=durable` could be written as
// one rule and be correct.
type KeyspaceStore struct {
	kv  KV
	key string
}

// KV is the slice of the keyspace client this store needs.
//
// Declared here rather than imported so that `apiconfig` keeps depending on nothing: it is read by
// the CLI-facing admin handlers and by tests that have no business constructing a keyspace client.
type KV interface {
	Available() bool
	GetBytes(ctx context.Context, key string) ([]byte, error)
	SetBytes(ctx context.Context, key string, value []byte, ttlSeconds int) error
}

// NewKeyspaceStore returns a store writing to `tenant:{ref}:api_config`.
func NewKeyspaceStore(kv KV, ref string) *KeyspaceStore {
	return &KeyspaceStore{kv: kv, key: fmt.Sprintf("tenant:%s:api_config", ref)}
}

// ErrUnavailable means the keyspace this store was built on is not reachable.
var ErrUnavailable = errors.New("the platform keyspace is not available")

func (s *KeyspaceStore) Get(ctx context.Context) (ApiConfig, error) {
	if s == nil || s.kv == nil || !s.kv.Available() {
		return DefaultApiConfig(), ErrUnavailable
	}
	raw, err := s.kv.GetBytes(ctx, s.key)
	if err != nil {
		return DefaultApiConfig(), err
	}
	// A project that has never been configured has no key, which is the default rather than an
	// error — exactly as an absent file is to FileStore.
	if len(raw) == 0 {
		return DefaultApiConfig(), nil
	}
	var cfg ApiConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultApiConfig(), err
	}
	return cfg, nil
}

func (s *KeyspaceStore) Set(ctx context.Context, cfg ApiConfig) error {
	if s == nil || s.kv == nil || !s.kv.Available() {
		return ErrUnavailable
	}
	data, err := marshalConfig(cfg)
	if err != nil {
		return err
	}
	// No TTL. This is configuration, not cache: an expiring API config would turn caching off on a
	// timer, which is the bug this type exists to fix wearing a different hat.
	return s.kv.SetBytes(ctx, s.key, data, 0)
}
