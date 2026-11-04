package changelog

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/modelhooks"
)

// caller is the part of modelhooks.Dispatcher the sender uses.
type caller interface {
	Call(ctx context.Context, url, event string, cfg modelhooks.HookConfigView, payload []byte, depth int) modelhooks.Outcome
}

// sender delivers hook calls on its own capped pool, apart from request handling.
type sender struct {
	call    caller
	resolve func(function string) (string, error)
	// workers caps calls in flight, so a burst of changes cannot take every function slot.
	workers int
	// attempts is how many times a hook that does not answer is called before giving up.
	attempts int
	backoff  func(attempt int) time.Duration
}

// defaultBackoff doubles from half a second, capped at ten.
func defaultBackoff(attempt int) time.Duration {
	d := 500 * time.Millisecond << min(attempt, 5)
	return min(d, 10*time.Second)
}

// sendAll delivers every call and returns once each has answered or been given up on.
//
// An error means the context ended first, so some call may not have been made, and the caller must
// not move the slot past these changes.
func (s *sender) sendAll(ctx context.Context, ds []Delivery) error {
	slots := make(chan struct{}, max(s.workers, 1))
	var wg sync.WaitGroup
	for _, d := range ds {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
		wg.Add(1)
		go func(d Delivery) {
			defer wg.Done()
			defer func() { <-slots }()
			s.sendOne(ctx, d)
		}(d)
	}
	wg.Wait()
	return ctx.Err()
}

// sendOne calls one hook, retrying while it does not answer.
func (s *sender) sendOne(ctx context.Context, d Delivery) {
	log := Log().WithFields(logrus.Fields{
		"event":       d.Event,
		"function":    d.Function,
		"delivery_id": d.DeliveryID,
	})
	for attempt := 0; attempt < s.attempts; attempt++ {
		if attempt > 0 && !sleep(ctx, s.backoff(attempt-1)) {
			return
		}
		reason, done := s.try(ctx, d, log)
		if done {
			return
		}
		log.WithFields(logrus.Fields{"attempt": attempt + 1, "reason": reason}).Warn("after hook did not answer")
	}
	if ctx.Err() == nil {
		log.WithField("attempts", s.attempts).Error("after hook gave up; this change was not delivered")
	}
}

// try makes one call. done is true when the hook answered, whatever it said.
func (s *sender) try(ctx context.Context, d Delivery, log *logrus.Entry) (string, bool) {
	url, err := s.resolve(d.Function)
	if err != nil {
		return fmt.Sprintf("resolving the function: %v", err), false
	}
	outcome := s.call.Call(ctx, url, d.Event, d.View, d.Body, d.Depth)
	switch outcome.Kind {
	case modelhooks.OutcomeUnavailable:
		return outcome.Reason, false
	case modelhooks.OutcomeReject:
		// The write is committed and cannot be undone, so a 4xx is only worth a line.
		log.WithField("status", outcome.Status).Warn("after hook rejected, but the write already happened")
	}
	return "", true
}

// sleep waits d, or reports false if the context ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
