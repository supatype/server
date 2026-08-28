// Package studiomembers resolves Studio capability from the project database.
//
// Studio access lives in `_supatype.studio_members`, not in a JWT claim.
// `app_metadata` is the developer's namespace for their own application roles,
// so granting an app role must never hand out admin UI access; and `_supatype`
// is absent from PGRST_DB_SCHEMAS, so nothing reachable through the data plane
// can read or write the membership table.
package studiomembers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sirupsen/logrus"
)

// lookupTimeout bounds a single membership query. Studio access checks sit in
// front of every proxied admin request, so a wedged database must fail closed
// quickly rather than hang the request.
const lookupTimeout = 3 * time.Second

const lookupSQL = `SELECT role FROM _supatype.studio_members WHERE user_id = $1::uuid`

// Lookup returns the Studio role recorded for a verified user id.
//
// The second result is false whenever access cannot be positively established —
// no membership row, no database, an unreadable table, a malformed id. Every
// such case is a denial: an admin UI that opens up when its authority is
// unreachable is worse than one that is briefly unavailable.
func (s Store) Lookup(userID string) (string, bool) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()

	pool, err := s.pool()
	if err != nil {
		logrus.WithError(err).Warn("studiomembers: no database available — denying Studio access")
		return "", false
	}

	var role string
	if err := pool.QueryRow(ctx, lookupSQL, userID).Scan(&role); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			logrus.WithError(err).Warn("studiomembers: membership lookup failed — denying Studio access")
		}
		return "", false
	}

	role = strings.TrimSpace(role)
	if role == "" {
		return "", false
	}
	return role, true
}

// Available reports whether membership lookups can be performed at all. Used to
// decide between the membership path and the legacy claim path, so a deployment
// with no DSN configured is not locked out of its own Studio.
func (s Store) Available() bool {
	_, err := s.pool()
	return err == nil
}
