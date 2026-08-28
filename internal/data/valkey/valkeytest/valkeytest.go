// Package valkeytest provides an in-memory valkey.Client for tests.
//
// Four packages had each grown their own stub of the cache — restcache, admin,
// the gateway and the platform middleware — and each stubbed only the methods
// its own tests happened to reach. A stub that does not really store anything
// cannot tell a hit from a miss, which is exactly what the cache's tests are
// about, and a stub per package means a change to the interface is discovered
// four times.
//
// This one stores. It also lets a test say "this call fails", because the
// interesting behaviour in every consumer is what happens when the cache is
// there but will not answer.
package valkeytest

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/supatype/server/internal/data/valkey"
)

// Client is an in-memory valkey.Client.
//
// Safe for concurrent use, because the consumers are HTTP middlewares and their
// tests run under -race.
type Client struct {
	mu sync.Mutex

	values  map[string]entry
	tenants map[string]*valkey.TenantConfig
	// sets records AddToExpiringSet members per key, which is how the MAU tally
	// is counted.
	sets map[string]map[string]time.Time

	// Failures. Each is returned instead of doing the operation.
	GetErr    error
	SetErr    error
	DelErr    error
	ScanErr   error
	TTLErr    error
	TenantErr error
	SetAddErr error

	// Unavailable makes Available report false, as a deployment with no cache
	// configured does.
	Unavailable bool

	// Counts, for asserting that a cache was consulted rather than bypassed.
	Gets, Sets, Dels int
}

type entry struct {
	value    []byte
	expireAt time.Time
}

// New returns an empty client.
func New() *Client {
	return &Client{
		values:  map[string]entry{},
		tenants: map[string]*valkey.TenantConfig{},
		sets:    map[string]map[string]time.Time{},
	}
}

// WithTenant registers a tenant configuration and returns the client, so a test
// can set one up in a single expression.
func (c *Client) WithTenant(ref string, cfg *valkey.TenantConfig) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tenants[ref] = cfg
	return c
}

// Put stores a value directly, bypassing the counters, so a test can arrange a
// hit without pretending to be the code under test.
func (c *Client) Put(key string, value []byte, ttlSeconds int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = entry{value: value, expireAt: time.Now().Add(time.Duration(ttlSeconds) * time.Second)}
}

// Keys returns every live key, sorted.
func (c *Client) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	keys := make([]string, 0, len(c.values))
	for key := range c.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// SetMembers returns the members added to one expiring set, sorted.
func (c *Client) SetMembers(key string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	members := make([]string, 0, len(c.sets[key]))
	for member := range c.sets[key] {
		members = append(members, member)
	}
	sort.Strings(members)
	return members
}

func (c *Client) Available() bool { return !c.Unavailable }

func (c *Client) GetTenantConfig(_ context.Context, ref string) (*valkey.TenantConfig, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.TenantErr != nil {
		return nil, c.TenantErr
	}
	return c.tenants[ref], nil
}

func (c *Client) GetBytes(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Gets++
	if c.GetErr != nil {
		return nil, c.GetErr
	}
	found, ok := c.values[key]
	if !ok || time.Now().After(found.expireAt) {
		return nil, nil
	}
	return found.value, nil
}

func (c *Client) SetBytes(_ context.Context, key string, value []byte, ttlSeconds int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Sets++
	if c.SetErr != nil {
		return c.SetErr
	}
	c.values[key] = entry{value: value, expireAt: time.Now().Add(time.Duration(ttlSeconds) * time.Second)}
	return nil
}

func (c *Client) Del(_ context.Context, keys ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Dels++
	if c.DelErr != nil {
		return c.DelErr
	}
	for _, key := range keys {
		delete(c.values, key)
	}
	return nil
}

// ScanPage returns every matching key in one page, which is a legitimate SCAN
// implementation: the cursor contract allows any page size.
func (c *Client) ScanPage(_ context.Context, cursor uint64, pattern string, _ int) ([]string, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ScanErr != nil {
		return nil, 0, c.ScanErr
	}
	if cursor != 0 {
		return nil, 0, nil
	}

	prefix := strings.TrimSuffix(pattern, "*")
	var matched []string
	for key := range c.values {
		if strings.HasPrefix(key, prefix) {
			matched = append(matched, key)
		}
	}
	sort.Strings(matched)
	return matched, 0, nil
}

func (c *Client) TTLSeconds(_ context.Context, key string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.TTLErr != nil {
		return 0, c.TTLErr
	}
	found, ok := c.values[key]
	if !ok {
		return -2, nil // Valkey's answer for a key that does not exist.
	}
	return int(time.Until(found.expireAt).Seconds()), nil
}

func (c *Client) AddToExpiringSet(_ context.Context, key, member string, expireAt time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.SetAddErr != nil {
		return c.SetAddErr
	}
	if c.sets[key] == nil {
		c.sets[key] = map[string]time.Time{}
	}
	c.sets[key][member] = expireAt
	return nil
}

func (c *Client) Close() {}

// ErrFailed is a convenient failure for the *Err fields.
var ErrFailed = errors.New("valkeytest: failing as instructed")

var _ valkey.Client = (*Client)(nil)
