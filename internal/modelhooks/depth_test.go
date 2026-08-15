package modelhooks

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// postAt sends a write carrying an explicit chain depth, as a hook's own write would.
func postAt(t *testing.T, s *stack, depth string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.server.URL+"/posts",
		strings.NewReader(`[{"title":"t"}]`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if depth != "" {
		req.Header.Set(HookDepthHeader, depth)
	}
	res, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func TestAHookCallCarriesTheDepthItIsAt(t *testing.T) {
	// Without this the chain cannot count itself and nothing below works.
	var seen []string
	s := newStack(t, beforeHooks("moderate"), func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get(HookDepthHeader))
		w.WriteHeader(http.StatusNoContent)
	}, nil)

	postAt(t, s, "")
	postAt(t, s, "2")

	if len(seen) != 2 || seen[0] != "1" || seen[1] != "3" {
		t.Fatalf("hook calls carried depths %v, want [1 3]", seen)
	}
}

func TestAWriteAtTheDepthLimitIsRefused(t *testing.T) {
	// The loop this stops: a hook holds the service-role key, so a hook on `posts` that writes to
	// `posts` re-enters this middleware and calls itself again, each hop a fresh request holding a
	// connection and a function slot.
	s := newStack(t, beforeHooks("moderate"), func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the hook was called for a write that should have been refused")
		w.WriteHeader(http.StatusNoContent)
	}, nil)

	res := postAt(t, s, strconv.Itoa(MaxHookDepth))
	if res.StatusCode != http.StatusLoopDetected {
		t.Fatalf("status = %d, want 508", res.StatusCode)
	}
	// Refused, not run without its hooks: a validation hook that stopped running must not let the
	// write through — that would turn a loop into a silent hole in every check the table declares.
	if s.upstreamCalled() {
		t.Fatal("the write reached the database with its hook skipped")
	}
}

func TestFanOutBelowTheLimitStillWorks(t *testing.T) {
	// A `beforeChange` on one table writing a row to another whose own hook then fires is legitimate,
	// so the limit cannot be 1.
	s := newStack(t, beforeHooks("moderate"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, nil)

	res := postAt(t, s, strconv.Itoa(MaxHookDepth-1))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want the write to proceed", res.StatusCode)
	}
	if !s.upstreamCalled() {
		t.Fatal("the write never reached the database")
	}
}

func TestAnUnreadableDepthReadsAsZero(t *testing.T) {
	// A caller cannot talk its way *past* a hook: if a bad value read as "very deep", anyone could
	// disable a table's hooks by sending a header — and refusing is the only thing a header can do.
	for _, raw := range []string{"nonsense", "-3", ""} {
		req, err := http.NewRequest(http.MethodPost, "/posts", nil)
		if err != nil {
			t.Fatal(err)
		}
		if raw != "" {
			req.Header.Set(HookDepthHeader, raw)
		}
		if got := hookDepth(req); got != 0 {
			t.Fatalf("depth %q read as %d, want 0", raw, got)
		}
	}
}

func TestADepthPastTheLimitIsClampedNotWrappedAround(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/posts", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HookDepthHeader, "9999")
	if got := hookDepth(req); got != MaxHookDepth {
		t.Fatalf("depth = %d, want it clamped to %d", got, MaxHookDepth)
	}
}
