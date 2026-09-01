package studiomembers

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/supatype/server/internal/data"
)

// Store reads and writes Studio membership in `_supatype.studio_members`.
//
// It holds the resources rather than a pool so that a deployment with no
// database still produces a usable Store: every method then reports
// data.ErrNoDatabase and the caller denies, which is the behaviour Studio needs.
// These were package-level functions reaching for a process-wide singleton,
// which made them impossible to point at a second database and impossible to
// test without one.
type Store struct {
	resources *data.Resources
}

// NewStore returns a Store backed by the process resources.
func NewStore(resources *data.Resources) Store {
	return Store{resources: resources}
}

// pool returns the admin pool, or an error when no database is configured.
func (s Store) pool() (*pgxpool.Pool, error) {
	if s.resources == nil {
		return nil, data.ErrNoDatabase
	}
	return s.resources.AdminPool()
}
