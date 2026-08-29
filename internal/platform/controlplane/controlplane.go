// Package controlplane talks to the Supatype control plane from a tenant
// gateway: it reports that a project is being used, and asks which organisation
// a project belongs to.
//
// Both calls sit next to a request being served, so both are strictly
// best-effort and tightly bounded. Neither may delay or fail the request that
// prompted it.
package controlplane

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sign produces the timestamp and signature proving a request came from inside
// the platform.
//
// The timestamp is part of the signed payload so a captured header cannot be
// replayed indefinitely; the control plane decides how much skew it will accept.
func Sign(secret, method, path string, now time.Time) (timestamp, signature string) {
	timestamp = strconv.FormatInt(now.Unix(), 10)
	payload := timestamp + "\n" + strings.ToUpper(method) + "\n" + path
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return timestamp, "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// Client calls the control plane's internal API.
type Client struct {
	// BaseURL is the control plane, without a trailing slash.
	BaseURL string
	// Secret signs internal calls. Empty means the calls go unsigned, which the
	// control plane may refuse; that is its decision, not ours.
	Secret string
	// HTTP is the client used. Its timeout is the only thing bounding these
	// calls, so it must have one.
	HTTP *http.Client
	// Now is overridable for tests.
	Now func() time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// request builds a signed internal request.
func (c *Client) request(ctx context.Context, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.Secret != "" {
		timestamp, signature := Sign(c.Secret, method, path, c.now())
		req.Header.Set("X-Supatype-Internal-Ts", timestamp)
		req.Header.Set("X-Supatype-Internal-Sig", signature)
	}
	return req, nil
}

// TouchActivity reports that a tenant is being used, which is what keeps a
// project from being paused as idle.
func (c *Client) TouchActivity(ctx context.Context, tenantID string) error {
	req, err := c.request(ctx, http.MethodPost, "/internal/activity/"+tenantID)
	if err != nil {
		return err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer drain(res)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("controlplane: activity touch returned %d", res.StatusCode)
	}
	return nil
}

// OrgFor returns the organisation a project belongs to.
func (c *Client) OrgFor(ctx context.Context, projectRef string) (string, error) {
	req, err := c.request(ctx, http.MethodGet, "/internal/projects/"+projectRef+"/org")
	if err != nil {
		return "", err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer drain(res)
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("controlplane: org lookup returned %d", res.StatusCode)
	}
	var body struct {
		OrgID string `json:"org_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.OrgID == "" {
		return "", fmt.Errorf("controlplane: org lookup returned no org for %q", projectRef)
	}
	return body.OrgID, nil
}

// drain reads and closes a response body so the connection can be reused.
func drain(res *http.Response) {
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
}

// OrgLookup is the part of Client an OrgCache needs.
type OrgLookup interface {
	OrgFor(ctx context.Context, projectRef string) (string, error)
}

// OrgCache remembers which organisation a project belongs to.
//
// It caches failures as well as successes, and that is the point. The previous
// version only remembered answers, so a project the control plane did not know
// about was looked up again on every single authentication, each time waiting on
// a network call from the request path. A project that will not resolve is
// exactly the one that must not be asked about repeatedly.
//
// The two lifetimes differ deliberately: a known organisation is stable, while a
// failure may be a deploy racing a provision, so it is retried sooner.
type OrgCache struct {
	Lookup OrgLookup
	// TTL is how long a resolved organisation is trusted.
	TTL time.Duration
	// NegativeTTL is how long a failure is remembered.
	NegativeTTL time.Duration
	// Now is overridable for tests.
	Now func() time.Time

	mu      sync.RWMutex
	entries map[string]orgEntry
}

type orgEntry struct {
	orgID string
	ok    bool
	until time.Time
}

// Default cache lifetimes.
const (
	DefaultTTL         = 5 * time.Minute
	DefaultNegativeTTL = 30 * time.Second
)

func (c *OrgCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *OrgCache) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return DefaultTTL
}

func (c *OrgCache) negativeTTL() time.Duration {
	if c.NegativeTTL > 0 {
		return c.NegativeTTL
	}
	return DefaultNegativeTTL
}

// Resolve returns the organisation for a project, consulting the cache first.
func (c *OrgCache) Resolve(ctx context.Context, projectRef string) (string, bool) {
	if entry, found := c.cached(projectRef); found {
		return entry.orgID, entry.ok
	}

	orgID, err := c.Lookup.OrgFor(ctx, projectRef)
	c.remember(projectRef, orgID, err == nil)
	if err != nil {
		return "", false
	}
	return orgID, true
}

func (c *OrgCache) cached(projectRef string) (orgEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[projectRef]
	if !ok || !c.now().Before(entry.until) {
		return orgEntry{}, false
	}
	return entry, true
}

func (c *OrgCache) remember(projectRef, orgID string, ok bool) {
	lifetime := c.negativeTTL()
	if ok {
		lifetime = c.ttl()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]orgEntry)
	}
	c.entries[projectRef] = orgEntry{orgID: orgID, ok: ok, until: c.now().Add(lifetime)}
}
