package valkey

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valkey-io/valkey-go"
)

// ErrCircuitOpen is returned when the circuit breaker is open and requests
// are being shed to protect Valkey from cascading failures.
var ErrCircuitOpen = errors.New("valkey: circuit breaker open")

// TenantConfig holds routing and configuration for a single tenant,
// as written by the cloud provisioner and read by supatype-server in managed mode.
type TenantConfig struct {
	PostgRESTURL string `json:"postgrest_url"`
	GraphQLURL   string `json:"graphql_url,omitempty"`
	StorageURL   string `json:"storage_url,omitempty"`
	AppMode      string `json:"app_mode,omitempty"`
	AppUpstream  string `json:"app_upstream,omitempty"`
	AppStaticDir string `json:"app_static_dir,omitempty"`
	Schema       string `json:"schema"`
}

const (
	readTimeout     = 100 * time.Millisecond
	cbFailThreshold = 3
	cbOpenDuration  = 30 * time.Second
)

// Client wraps a valkey-go client with a simple circuit breaker.
// After cbFailThreshold consecutive errors the circuit opens and all
// requests return ErrCircuitOpen immediately. After cbOpenDuration the
// circuit enters half-open state: one probe request is allowed through.
type Client struct {
	vc valkey.Client

	mu          sync.Mutex
	failures    int
	circuitOpen bool
	openAt      time.Time

	// probeInFlight prevents concurrent half-open probes.
	probeInFlight atomic.Bool
}

// New creates a Client connected to addr (e.g. "127.0.0.1:6379").
func New(addr string) (*Client, error) {
	vc, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{addr},
	})
	if err != nil {
		return nil, fmt.Errorf("valkey: connect %s: %w", addr, err)
	}
	return &Client{vc: vc}, nil
}

// GetTenantConfig fetches the TenantConfig for ref from Valkey.
// Key pattern: tenant:{ref}:config
//
// Returns ErrCircuitOpen if the circuit breaker is open.
// Returns a nil pointer and an error if the key is not found or the
// value cannot be unmarshalled.
func (c *Client) GetTenantConfig(ctx context.Context, ref string) (*TenantConfig, error) {
	if err := c.checkCircuit(); err != nil {
		return nil, err
	}

	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	key := fmt.Sprintf("tenant:%s:config", ref)
	cmd := c.vc.B().Get().Key(key).Build()
	result := c.vc.Do(rctx, cmd)

	if err := result.Error(); err != nil {
		c.recordFailure()
		return nil, fmt.Errorf("valkey: GET %s: %w", key, err)
	}

	data, err := result.AsBytes()
	if err != nil {
		c.recordFailure()
		return nil, fmt.Errorf("valkey: decode %s: %w", key, err)
	}

	var cfg TenantConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("valkey: unmarshal %s: %w", key, err)
	}

	c.recordSuccess()
	return &cfg, nil
}

// GetBytes fetches a raw byte value by key.
func (c *Client) GetBytes(ctx context.Context, key string) ([]byte, error) {
	if err := c.checkCircuit(); err != nil {
		return nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	result := c.vc.Do(rctx, c.vc.B().Get().Key(key).Build())
	if err := result.Error(); err != nil {
		c.recordFailure()
		return nil, fmt.Errorf("valkey: GET %s: %w", key, err)
	}
	data, err := result.AsBytes()
	if err != nil {
		c.recordFailure()
		return nil, fmt.Errorf("valkey: decode %s: %w", key, err)
	}
	c.recordSuccess()
	return data, nil
}

// SetBytes stores raw bytes with an optional TTL (seconds). ttlSeconds <= 0 means no expiry.
func (c *Client) SetBytes(ctx context.Context, key string, value []byte, ttlSeconds int) error {
	if err := c.checkCircuit(); err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	var result valkey.ValkeyResult
	if ttlSeconds > 0 {
		result = c.vc.Do(rctx, c.vc.B().Set().Key(key).Value(string(value)).Ex(time.Duration(ttlSeconds)*time.Second).Build())
	} else {
		result = c.vc.Do(rctx, c.vc.B().Set().Key(key).Value(string(value)).Build())
	}
	if err := result.Error(); err != nil {
		c.recordFailure()
		return fmt.Errorf("valkey: SET %s: %w", key, err)
	}
	c.recordSuccess()
	return nil
}

// Del deletes one or more keys.
func (c *Client) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := c.checkCircuit(); err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	result := c.vc.Do(rctx, c.vc.B().Del().Key(keys...).Build())
	if err := result.Error(); err != nil {
		c.recordFailure()
		return fmt.Errorf("valkey: DEL %v: %w", keys, err)
	}
	c.recordSuccess()
	return nil
}

// Close shuts down the underlying Valkey client.
func (c *Client) Close() {
	c.vc.Close()
}

func (c *Client) checkCircuit() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.circuitOpen {
		return nil
	}

	if time.Since(c.openAt) >= cbOpenDuration {
		// Half-open: allow one probe if none is in flight.
		if c.probeInFlight.CompareAndSwap(false, true) {
			return nil
		}
		return ErrCircuitOpen
	}

	return ErrCircuitOpen
}

func (c *Client) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.probeInFlight.Store(false)
	c.failures++
	if c.failures >= cbFailThreshold {
		c.circuitOpen = true
		c.openAt = time.Now()
	}
}

func (c *Client) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.failures = 0
	c.circuitOpen = false
	c.probeInFlight.Store(false)
}
