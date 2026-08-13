package modelhooks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func callAgainst(t *testing.T, handler http.HandlerFunc, cfg HookConfigView, event string) Outcome {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	d := NewDispatcher(server.Client(), "")
	return d.Call(context.Background(), server.URL, event, cfg, []byte(`{"table":"posts"}`))
}

func TestCallProceedsOnEmptyVerdicts(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"204 no content", http.StatusNoContent, ""},
		{"200 with empty body", http.StatusOK, ""},
		{"200 with an empty object", http.StatusOK, `{}`},
		{"200 with ok:true", http.StatusOK, `{"ok":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}, HookConfigView{}, EventBeforeChange)

			if got.Kind != OutcomeProceed {
				t.Fatalf("kind = %v, want proceed (%+v)", got.Kind, got)
			}
		})
	}
}

func TestCallReplacesTheBody(t *testing.T) {
	for _, key := range []string{"rows", "patch"} {
		t.Run(key, func(t *testing.T) {
			got := callAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"` + key + `":{"title":"trimmed"}}`))
			}, HookConfigView{}, EventBeforeChange)

			if got.Kind != OutcomeReplace {
				t.Fatalf("kind = %v, want replace (%+v)", got.Kind, got)
			}
			// Only the inner value travels on: it becomes the request body PostgREST receives.
			if string(got.Body) != `{"title":"trimmed"}` {
				t.Fatalf("body = %s, want the unwrapped value", got.Body)
			}
		})
	}
}

func TestCallTreatsAny4xxAsTheHookSayingNo(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity} {
		got := callAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"no"}`))
		}, HookConfigView{}, EventBeforeChange)

		if got.Kind != OutcomeReject {
			t.Fatalf("status %d: kind = %v, want reject", status, got.Kind)
		}
		// The hook chose the status, so the caller sees exactly that rather than a flattened 422.
		if got.Status != status {
			t.Fatalf("status = %d, want %d", got.Status, status)
		}
	}
}

func TestCallTreatsBreakageAsUnavailableNotRefusal(t *testing.T) {
	// The distinction the whole policy rests on: a hook that broke has not said no, so the per-hook
	// onUnavailable decides — collapsing them would let a broken validator pass writes silently.
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }},
		{"502", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }},
		{"200 with an unreadable body", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`not json`))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callAgainst(t, tc.handler, HookConfigView{}, EventBeforeChange)
			if got.Kind != OutcomeUnavailable {
				t.Fatalf("kind = %v, want unavailable (%+v)", got.Kind, got)
			}
			if got.Reason == "" {
				t.Fatal("unavailable outcome carries no reason for the log")
			}
		})
	}
}

func TestCallTimesOutWithoutRetrying(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	d := NewDispatcher(server.Client(), "")
	got := d.Call(
		context.Background(),
		server.URL,
		EventBeforeChange,
		HookConfigView{TimeoutMs: 30},
		[]byte(`{}`),
	)

	if got.Kind != OutcomeUnavailable {
		t.Fatalf("kind = %v, want unavailable", got.Kind)
	}
	// Retrying before a write multiplies the latency the caller waits and re-invokes a handler that
	// may already have acted. One attempt, on purpose.
	if attempts != 1 {
		t.Fatalf("attempts = %d, want exactly 1", attempts)
	}
}

func TestCallSendsTheEventHeaderAndSignsWhenConfigured(t *testing.T) {
	var gotEvent, gotSig, gotID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEvent = r.Header.Get("X-Supatype-Hook")
		gotSig = r.Header.Get("webhook-signature")
		gotID = r.Header.Get("webhook-id")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	d := NewDispatcher(server.Client(), "shhh")
	d.Call(context.Background(), server.URL, EventAfterDelete, HookConfigView{}, []byte(`{}`))

	// The header is how one function serves several hooks, and how a plain HTTP function tells a
	// hook call from a user calling it directly.
	if gotEvent != EventAfterDelete {
		t.Fatalf("event header = %q, want %q", gotEvent, EventAfterDelete)
	}
	if !strings.HasPrefix(gotSig, "v1,") || gotID == "" {
		t.Fatalf("signature = %q id = %q, want a Standard Webhooks pair", gotSig, gotID)
	}
}

func TestCallOmitsSignatureWithoutASecret(t *testing.T) {
	// Unsigned must remain workable: the security that matters is the callback token, not this.
	var gotSig string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("webhook-signature")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	NewDispatcher(server.Client(), "").
		Call(context.Background(), server.URL, EventBeforeChange, HookConfigView{}, []byte(`{}`))

	if gotSig != "" {
		t.Fatalf("signature = %q, want none", gotSig)
	}
}

func TestUnavailablePolicyDefaultsBySide(t *testing.T) {
	cases := []struct {
		event      string
		configured string
		want       bool
		why        string
	}{
		{EventBeforeChange, "", true, "a validation hook that stopped running must not pass writes"},
		{EventBeforeDelete, "", true, "same"},
		{EventAfterChange, "", false, "the write already happened; nothing left to fail"},
		{EventAfterDelete, "", false, "same"},
		{EventBeforeChange, "log", false, "explicit config wins"},
		{EventAfterChange, "reject", true, "explicit config wins the other way too"},
	}
	for _, tc := range cases {
		got := HookConfigView{OnUnavailable: tc.configured}.RejectsWhenUnavailable(tc.event)
		if got != tc.want {
			t.Fatalf("%s/%q = %v, want %v (%s)", tc.event, tc.configured, got, tc.want, tc.why)
		}
	}
}

func TestTimeoutFallsBackToTheDefault(t *testing.T) {
	if got := (HookConfigView{}).Timeout(); got != DefaultTimeout {
		t.Fatalf("timeout = %v, want %v", got, DefaultTimeout)
	}
	if got := (HookConfigView{TimeoutMs: 500}).Timeout(); got != 500*time.Millisecond {
		t.Fatalf("timeout = %v, want 500ms", got)
	}
}
