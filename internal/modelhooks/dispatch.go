package modelhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// DefaultTimeout is used when a hook declares none. Well below the edge-function ceiling, so a hung
// hook fails fast instead of occupying an invocation slot for ten seconds.
const DefaultTimeout = 2 * time.Second

// maxVerdictBytes caps what we will read back from a hook. A verdict is a small JSON object; a hook
// returning something enormous is a bug we should not follow into memory.
const maxVerdictBytes = 1 << 20 // 1 MiB

// OutcomeKind is what the server should do next.
type OutcomeKind int

const (
	// OutcomeProceed — carry on with the request unchanged.
	OutcomeProceed OutcomeKind = iota
	// OutcomeReplace — carry on, using Body as the request body.
	OutcomeReplace
	// OutcomeReject — the hook said no. Status and Body go to the caller.
	OutcomeReject
	// OutcomeUnavailable — the hook did not answer. The per-hook policy decides.
	OutcomeUnavailable
)

// Outcome is the result of calling one hook.
type Outcome struct {
	Kind   OutcomeKind
	Status int
	Body   []byte
	// Reason explains an OutcomeUnavailable, for the log line. Never sent to the caller: it may name
	// internal hosts, and a caller cannot act on it.
	Reason string
}

// Doer is the HTTP surface the dispatcher needs, so tests can supply their own.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Dispatcher calls hook functions and turns their answers into outcomes.
//
// Deliberately **not** `internal/hooks/hookshttp`: that dispatcher retries, which is right for an
// auth webhook and wrong here. A retry before a write multiplies the latency the caller waits, and
// re-invokes a handler that may already have acted. It also folds every non-2xx into an error, which
// would lose the distinction this design turns on — a 4xx is the hook saying no, a 5xx is the hook
// being broken.
type Dispatcher struct {
	client Doer
	// Secret signs the payload so a handler can tell a real hook call from a public one. Optional:
	// unsigned calls still work, because the security that matters does not rest here — the callback
	// URL for previous() carries its own token, and a hook's verdict only affects the request that
	// invoked it.
	secret []byte
}

// NewDispatcher builds a dispatcher. A nil client means the default HTTP client.
func NewDispatcher(client Doer, secret string) *Dispatcher {
	if client == nil {
		client = &http.Client{}
	}
	return &Dispatcher{client: client, secret: []byte(secret)}
}

// Call invokes one hook and classifies the answer.
//
// The **status is the outcome**, which is why this reads as a switch on it rather than a hunt through
// a body envelope: the transport already has a vocabulary for "no", and a hook written in another
// language should not have to learn ours.
func (d *Dispatcher) Call(
	ctx context.Context,
	url string,
	event string,
	cfg HookConfigView,
	payload []byte,
	// depth is how many hooks deep this invocation is. Stamped on the call so the chain can count
	// itself: a handler's own writes carry it onward, and this middleware refuses past MaxHookDepth.
	depth int,
) Outcome {
	timeout := cfg.Timeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return Outcome{Kind: OutcomeUnavailable, Reason: fmt.Sprintf("building request: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supatype-Hook", event)
	req.Header.Set(HookDepthHeader, strconv.Itoa(depth))
	// Identity encoding for the same reason hookshttp sets it: a gzipped response carries no length.
	req.Header.Set("Accept-Encoding", "identity")
	d.sign(req, payload)

	res, err := d.client.Do(req)
	if err != nil {
		// A timeout arrives here as a context error. Either way the hook did not answer, which is a
		// different thing from answering "no" — the caller's policy decides.
		return Outcome{Kind: OutcomeUnavailable, Reason: fmt.Sprintf("calling hook: %v", err)}
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, maxVerdictBytes))
	if err != nil {
		return Outcome{Kind: OutcomeUnavailable, Reason: fmt.Sprintf("reading verdict: %v", err)}
	}

	switch {
	case res.StatusCode == http.StatusNoContent:
		return Outcome{Kind: OutcomeProceed}

	case res.StatusCode == http.StatusOK:
		return verdictFromBody(body)

	case res.StatusCode >= 400 && res.StatusCode < 500:
		// The hook's considered no. Its status and message are the caller's answer.
		return Outcome{Kind: OutcomeReject, Status: res.StatusCode, Body: body}

	default:
		return Outcome{
			Kind:   OutcomeUnavailable,
			Reason: fmt.Sprintf("hook answered %d", res.StatusCode),
		}
	}
}

// verdictFromBody reads a 200 body: empty or `{}` proceeds, `rows`/`patch` replaces.
func verdictFromBody(body []byte) Outcome {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return Outcome{Kind: OutcomeProceed}
	}

	var verdict struct {
		Rows  json.RawMessage `json:"rows"`
		Patch json.RawMessage `json:"patch"`
	}
	if err := json.Unmarshal(trimmed, &verdict); err != nil {
		// A 200 we cannot read is the hook being broken, not the hook saying yes. Proceeding on it
		// would apply a write the hook may have meant to change.
		return Outcome{Kind: OutcomeUnavailable, Reason: fmt.Sprintf("verdict was not JSON: %v", err)}
	}

	switch {
	case len(verdict.Rows) > 0:
		return Outcome{Kind: OutcomeReplace, Body: verdict.Rows}
	case len(verdict.Patch) > 0:
		return Outcome{Kind: OutcomeReplace, Body: verdict.Patch}
	default:
		return Outcome{Kind: OutcomeProceed}
	}
}

// sign adds Standard Webhooks headers so a handler can verify the call came from this server.
func (d *Dispatcher) sign(req *http.Request, payload []byte) {
	if len(d.secret) == 0 {
		return
	}
	id := req.Header.Get("X-Request-Id")
	if id == "" {
		id = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	mac := hmac.New(sha256.New, d.secret)
	// Standard Webhooks signs `id.timestamp.payload`.
	mac.Write([]byte(id + "." + ts + "."))
	mac.Write(payload)

	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

// HookConfigView is the part of a hook's config the dispatcher needs.
type HookConfigView struct {
	TimeoutMs     int
	OnUnavailable string
}

// Timeout is the configured timeout, or the default.
func (h HookConfigView) Timeout() time.Duration {
	if h.TimeoutMs > 0 {
		return time.Duration(h.TimeoutMs) * time.Millisecond
	}
	return DefaultTimeout
}

// RejectsWhenUnavailable reports whether an unreachable hook should fail the write.
//
// The default is decided by the CLI and written into the manifest, so both sides agree. An empty
// value here means the manifest predates that, and the safe reading for a *before* hook is to
// reject: a validation hook that stopped running must not quietly pass writes through.
func (h HookConfigView) RejectsWhenUnavailable(event string) bool {
	switch h.OnUnavailable {
	case "reject":
		return true
	case "log":
		return false
	default:
		return event == EventBeforeChange ||
			event == EventBeforeDelete ||
			event == EventBeforeValidate
	}
}
