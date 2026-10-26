package studioauth

import (
	"context"
	"errors"
	"sync"

	"github.com/supatype/server/internal/studiomembers"
)

// fakeAuditor records what the proxy audits, in memory, so proxy tests run
// without a database. fail makes every insert fail, as a broken audit table
// would. failStatus makes recording the upstream status fail the same way.
type fakeAuditor struct {
	mu         sync.Mutex
	fail       bool
	failStatus bool
	records    []studiomembers.ElevatedRequest
	statuses   []int
}

var errAuditDown = errors.New("audit table unavailable")

func (f *fakeAuditor) AuditElevated(_ context.Context, r studiomembers.ElevatedRequest) (studiomembers.AuditRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return studiomembers.AuditRef{}, errAuditDown
	}
	f.records = append(f.records, r)
	return studiomembers.AuditRef{}, nil
}

func (f *fakeAuditor) AuditElevatedStatus(_ context.Context, _ studiomembers.AuditRef, status int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, status)
	if f.failStatus {
		return errAuditDown
	}
	return nil
}
