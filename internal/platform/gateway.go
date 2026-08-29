// Package platform is the cloud tenant gateway: the layer a hosted project's
// traffic passes through so the platform knows the project is alive and who to
// bill for it.
//
// Everything here is best-effort and must fail open. A customer's API call is
// not allowed to slow down, fail, or leak information because the platform's own
// bookkeeping is having a bad day.
package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/platform/async"
	"github.com/supatype/server/internal/platform/controlplane"
	"github.com/supatype/server/internal/platform/mau"
	"github.com/supatype/server/internal/platform/robots"
)

const (
	// activityTimeout bounds the activity report. It sits beside a request being
	// served, so it gets a fraction of a request budget and no more.
	activityTimeout = 200 * time.Millisecond
	// orgLookupTimeout bounds the organisation lookup.
	orgLookupTimeout = 150 * time.Millisecond
	// tallyTimeout bounds the write to Valkey.
	tallyTimeout = 50 * time.Millisecond

	// asyncWorkers caps how much of the control plane this process can occupy at
	// once, and asyncDepth is the burst it absorbs before shedding. Both exist
	// because this work used to be an unbounded goroutine per request: when the
	// control plane slowed down, every arrival started another one.
	asyncWorkers = 4
	asyncDepth   = 256

	// captureLimit bounds how much of an auth response is buffered to find the
	// user object. The whole body used to be held in memory with no ceiling, so
	// a large response was a per-request allocation an attacker could choose.
	captureLimit = 64 << 10
)

// Gateway is the tenant metering middleware.
type Gateway struct {
	tenantID string
	salt     string
	policy   robots.Policy
	control  *controlplane.Client
	orgs     *controlplane.OrgCache
	recorder mau.Recorder
	queue    *async.Queue
}

// New builds the gateway from configuration, or returns nil when metering is
// switched off, which it is everywhere except a hosted tenant pod.
func New(cfg *config.Config, store mau.Store) *Gateway {
	if !cfg.CloudActivityEnabled.Bool() {
		return nil
	}

	base := strings.TrimRight(strings.TrimSpace(cfg.CloudActivityURL), "/")
	if base == "" {
		base = strings.TrimRight(config.DefaultCloudActivityURL, "/")
	}
	client := &controlplane.Client{
		BaseURL: base,
		Secret:  cfg.InternalHMACSecret,
		HTTP:    &http.Client{Timeout: orgLookupTimeout},
	}

	return &Gateway{
		tenantID: strings.TrimSpace(cfg.ManagedProjectRef),
		salt:     cfg.MAUEmailSalt,
		policy:   robots.Policy{NonProd: cfg.NonProd.Bool(), BlockBots: cfg.BlockBotUA.Bool()},
		control:  client,
		orgs:     &controlplane.OrgCache{Lookup: client},
		recorder: mau.Recorder{Store: store, Salt: cfg.MAUEmailSalt},
		queue:    async.New(asyncWorkers, asyncDepth),
	}
}

// Wrap puts the gateway in front of next. A nil Gateway is the disabled case and
// returns next untouched, so the caller does not need to ask.
func (g *Gateway) Wrap(next http.Handler) http.Handler {
	if g == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.policy.Handle(w, r) {
			return
		}
		g.reportActivity(r)

		if !strings.HasPrefix(r.URL.Path, "/auth/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		g.serveAuthAndTally(w, r, next)
	})
}

// Close stops the background workers and waits for queued work.
func (g *Gateway) Close() {
	if g == nil {
		return
	}
	g.queue.Close()
}

// Dropped is how much background work has been shed. Non-zero means the
// telemetry is incomplete, not that anything is broken for the customer.
func (g *Gateway) Dropped() int64 {
	if g == nil {
		return 0
	}
	return g.queue.Dropped()
}

// countsAsActivity reports whether a request is somebody using the project.
//
// Crawlers and speculative prefetches are not people, and health probes are the
// platform talking to itself. Counting any of them would keep an idle project
// awake and bill for it.
func countsAsActivity(r *http.Request) bool {
	switch r.URL.Path {
	case "/health", "/healthz":
		return false
	}
	return !robots.IsBot(r.UserAgent()) && !robots.IsPrefetch(r)
}

// reportActivity tells the control plane the project is being used, off the
// request path.
func (g *Gateway) reportActivity(r *http.Request) {
	if g.tenantID == "" || !countsAsActivity(r) {
		return
	}
	tenantID := g.tenantID
	g.queue.Submit(func() {
		ctx, cancel := context.WithTimeout(context.Background(), activityTimeout)
		defer cancel()
		if err := g.control.TouchActivity(ctx, tenantID); err != nil {
			logrus.WithError(err).Debug("platform: activity touch failed")
		}
	})
}

// serveAuthAndTally serves an auth request and, if it was a successful sign-in,
// counts the user.
//
// The response is streamed to the caller as it is written; the copy kept here is
// only so the user object can be read afterwards. The customer never waits for
// the tally.
func (g *Gateway) serveAuthAndTally(w http.ResponseWriter, r *http.Request, next http.Handler) {
	grantType := r.URL.Query().Get("grant_type")
	recorder := &boundedRecorder{ResponseWriter: w, status: http.StatusOK, limit: captureLimit}
	next.ServeHTTP(recorder, r)

	if recorder.status < 200 || recorder.status >= 300 {
		return
	}
	if !mau.Eligible(r.Method, r.URL.Path, grantType) {
		return
	}
	user, ok := userFromAuthResponse(recorder)
	if !ok {
		return
	}
	g.tally(user)
}

// userFromAuthResponse pulls the user object out of a captured auth response.
func userFromAuthResponse(recorder *boundedRecorder) (map[string]any, bool) {
	if recorder.truncated {
		// Parsing a truncated body would fail anyway, and guessing would be worse
		// than missing one tally.
		logrus.Debug("platform: auth response exceeded the capture limit, tally skipped")
		return nil, false
	}
	var payload map[string]any
	if json.Unmarshal(recorder.body.Bytes(), &payload) != nil {
		return nil, false
	}
	user, _ := payload["user"].(map[string]any)
	return user, user != nil
}

// tally resolves the organisation and records the user, off the request path.
func (g *Gateway) tally(user map[string]any) {
	if g.tenantID == "" {
		return
	}
	projectRef := mau.ProjectRef(g.tenantID)
	g.queue.Submit(func() {
		ctx, cancel := context.WithTimeout(context.Background(), orgLookupTimeout+tallyTimeout)
		defer cancel()

		orgID, ok := g.orgs.Resolve(ctx, projectRef)
		if !ok {
			return
		}
		if err := g.recorder.Record(ctx, orgID, projectRef, user); err != nil {
			logrus.WithError(err).Debug("platform: MAU tally not recorded")
		}
	})
}

// boundedRecorder passes a response through to the client while keeping a capped
// copy of the body.
//
// The cap is the point. The previous version buffered the whole body with no
// ceiling, on every successful auth request, to read one small object out of it.
type boundedRecorder struct {
	http.ResponseWriter
	status    int
	body      bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedRecorder) WriteHeader(code int) {
	b.status = code
	b.ResponseWriter.WriteHeader(code)
}

func (b *boundedRecorder) Write(p []byte) (int, error) {
	if remaining := b.limit - b.body.Len(); remaining > 0 {
		if len(p) > remaining {
			b.body.Write(p[:remaining])
			b.truncated = true
		} else {
			b.body.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return b.ResponseWriter.Write(p)
}

// Flush forwards to the underlying writer when it supports it. Without this, a
// streaming auth response would be buffered by the wrapper rather than reaching
// the client as it is produced.
func (b *boundedRecorder) Flush() {
	if flusher, ok := b.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
