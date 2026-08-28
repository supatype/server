package studiobootstrap

import (
	"context"
	"encoding/json"
	"github.com/supatype/server/internal/data"
	"sync"
	"time"
)

// IsIdentityDependent reports whether a rule's outcome varies by *who* is asking.
//
// A shared cache entry for such a table serves one caller's rows to everyone, so
// this is what decides whether a response may be cached under a scope that is not
// keyed by identity.
//
// Row-dependence is not the same question. `Lte<"published_at", Now>` varies by row
// and by time but not by caller, so a shared entry is fine for it — the whole point
// of allowing public caching on a published-content table.
func IsIdentityDependent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var r rule
	if err := json.Unmarshal(raw, &r); err != nil {
		// An unreadable rule is assumed to depend on identity. Guessing the other
		// way would share a cache entry across callers on the strength of a parse
		// failure.
		return true
	}
	return ruleIsIdentityDependent(&r)
}

func ruleIsIdentityDependent(r *rule) bool {
	switch r.Type {
	case "public", "private":
		return false

	// "Any authenticated user" distinguishes signed-in from anonymous, so a shared
	// entry could serve a member's view to a stranger.
	case "authenticated", "role", "owner":
		return true

	// Membership is per caller by construction.
	case "in", "exists":
		return true

	case "compare":
		return operandIsIdentityDependent(r.Left) || operandIsIdentityDependent(r.Right)

	case "nullCheck":
		return operandIsIdentityDependent(r.Operand)

	case "any", "all":
		for i := range r.Rules {
			if ruleIsIdentityDependent(&r.Rules[i]) {
				return true
			}
		}
		return false

	case "not":
		return r.Rule != nil && ruleIsIdentityDependent(r.Rule)

	default:
		// Unrecognised rules are treated as identity-dependent for the same reason
		// an unreadable one is.
		return true
	}
}

func operandIsIdentityDependent(o *operand) bool {
	if o == nil {
		return false
	}
	switch o.Kind {
	case "authUid", "authRole", "claim":
		return true
	default:
		// Columns, literals and the temporal operands are the same for every caller.
		return false
	}
}

// identityScopeTTL bounds how stale the table classification may be.
//
// Short, because it gates a cache-sharing decision: a schema push that makes a
// table identity-dependent should stop shared caching promptly. Not zero, because
// this is consulted on a request path and the answer changes only on push.
const identityScopeTTL = 30 * time.Second

var identityScope struct {
	sync.Mutex
	tables   map[string]bool
	loadedAt time.Time
	loaded   bool
}

// IdentityScopedTables returns the tables whose read rule varies by caller.
//
// The second result is false when the classification could not be determined — the
// caller must then treat every table as identity-scoped, because "we could not
// check" is not a reason to start sharing responses between users.
func IdentityScopedTables(ctx context.Context, resources *data.Resources) (map[string]bool, bool) {
	identityScope.Lock()
	defer identityScope.Unlock()

	if identityScope.loaded && time.Since(identityScope.loadedAt) < identityScopeTTL {
		return identityScope.tables, true
	}

	snapshot, err := LoadSnapshot(ctx, resources)
	if err != nil {
		// Keep serving a previous answer rather than flapping to "unknown" on a
		// transient database blip; the TTL still bounds how old it can be.
		if identityScope.loaded {
			return identityScope.tables, true
		}
		return nil, false
	}

	var shape astShape
	if err := json.Unmarshal(snapshot.AST, &shape); err != nil {
		return nil, false
	}

	tables := make(map[string]bool, len(shape.Models))
	for _, m := range shape.Models {
		table := m.Annotations.DB.TableName
		if table == "" {
			table = m.Name
		}
		tables[table] = IsIdentityDependent(m.Annotations.Platform.Access["read"])
	}

	identityScope.tables = tables
	identityScope.loadedAt = time.Now()
	identityScope.loaded = true
	return tables, true
}

// ResetIdentityScopeCache clears the memoised classification. For tests.
func ResetIdentityScopeCache() {
	identityScope.Lock()
	defer identityScope.Unlock()
	identityScope.tables = nil
	identityScope.loaded = false
	identityScope.loadedAt = time.Time{}
}
