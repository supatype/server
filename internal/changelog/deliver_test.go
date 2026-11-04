package changelog

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/supatype/server/internal/modelhooks"
)

// scriptedCaller answers each delivery id from a script, then proceeds.
type scriptedCaller struct {
	mu       sync.Mutex
	script   map[string][]modelhooks.OutcomeKind
	calls    map[string]int
	inFlight atomic.Int32
	peak     atomic.Int32
	hold     time.Duration
}

func (s *scriptedCaller) Call(ctx context.Context, url, event string, _ modelhooks.HookConfigView, body []byte, depth int) modelhooks.Outcome {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if s.hold > 0 {
		time.Sleep(s.hold)
	}
	id := url
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	attempt := s.calls[id]
	s.calls[id]++
	if steps := s.script[id]; attempt < len(steps) {
		return modelhooks.Outcome{Kind: steps[attempt], Status: 500}
	}
	return modelhooks.Outcome{Kind: modelhooks.OutcomeProceed}
}

func (s *scriptedCaller) count(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[id]
}

func testSender(c caller, workers int) *sender {
	return &sender{
		call:     c,
		resolve:  func(function string) (string, error) { return function, nil },
		workers:  workers,
		attempts: 3,
		backoff:  func(int) time.Duration { return time.Millisecond },
	}
}

func TestSenderRetriesAnUnavailableHookUntilItAnswers(t *testing.T) {
	c := &scriptedCaller{script: map[string][]modelhooks.OutcomeKind{
		"flaky": {modelhooks.OutcomeUnavailable, modelhooks.OutcomeUnavailable},
	}}
	if err := testSender(c, 2).sendAll(context.Background(), []Delivery{{Function: "flaky"}}); err != nil {
		t.Fatal(err)
	}
	if got := c.count("flaky"); got != 3 {
		t.Fatalf("two failures then success is three calls, got %d", got)
	}
}

func TestSenderGivesUpAfterItsAttempts(t *testing.T) {
	c := &scriptedCaller{script: map[string][]modelhooks.OutcomeKind{
		"down": {modelhooks.OutcomeUnavailable, modelhooks.OutcomeUnavailable, modelhooks.OutcomeUnavailable, modelhooks.OutcomeUnavailable},
	}}
	// Giving up is not an error: the slot must still move, or one broken hook holds every other
	// table's hooks back forever.
	if err := testSender(c, 1).sendAll(context.Background(), []Delivery{{Function: "down"}}); err != nil {
		t.Fatal(err)
	}
	if got := c.count("down"); got != 3 {
		t.Fatalf("want exactly the configured 3 attempts, got %d", got)
	}
}

func TestSenderDoesNotRetryARejection(t *testing.T) {
	c := &scriptedCaller{script: map[string][]modelhooks.OutcomeKind{"no": {modelhooks.OutcomeReject}}}
	if err := testSender(c, 1).sendAll(context.Background(), []Delivery{{Function: "no"}}); err != nil {
		t.Fatal(err)
	}
	if got := c.count("no"); got != 1 {
		t.Fatalf("a 4xx is an answer, not an outage; want 1 call, got %d", got)
	}
}

func TestSenderRetriesWhenTheFunctionCannotBeResolved(t *testing.T) {
	c := &scriptedCaller{}
	s := testSender(c, 1)
	var tries atomic.Int32
	s.resolve = func(function string) (string, error) {
		if tries.Add(1) < 2 {
			return "", errors.New("worker not up yet")
		}
		return function, nil
	}
	if err := s.sendAll(context.Background(), []Delivery{{Function: "fn"}}); err != nil {
		t.Fatal(err)
	}
	if c.count("fn") != 1 {
		t.Fatal("the call should go out once the function resolves")
	}
}

func TestSenderCapsConcurrency(t *testing.T) {
	c := &scriptedCaller{hold: 20 * time.Millisecond}
	var ds []Delivery
	for _, f := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		ds = append(ds, Delivery{Function: f})
	}
	if err := testSender(c, 3).sendAll(context.Background(), ds); err != nil {
		t.Fatal(err)
	}
	if p := c.peak.Load(); p > 3 || p < 2 {
		t.Fatalf("want at most 3 calls in flight and some parallelism, saw %d", p)
	}
}

func TestSenderReportsAnAbortSoTheSlotStays(t *testing.T) {
	c := &scriptedCaller{script: map[string][]modelhooks.OutcomeKind{
		"down": {modelhooks.OutcomeUnavailable, modelhooks.OutcomeUnavailable, modelhooks.OutcomeUnavailable},
	}}
	s := testSender(c, 1)
	s.backoff = func(int) time.Duration { return time.Hour }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.sendAll(ctx, []Delivery{{Function: "down"}}); err == nil {
		t.Fatal("a shutdown mid-retry must be reported, so the slot does not move past an undelivered change")
	}
}
