package realtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// accessCacheEntry is a cached positive access decision.
type accessCacheEntry struct {
	expiresAt time.Time
}

// accessCache is a short-lived positive cache for row-level access checks.
// Only positive results are cached — denials fall through to Postgres every time.
var accessCache sync.Map // key: "{role}:{table}:{rowID}" → accessCacheEntry

const accessCacheTTL = 5 * time.Second

// CheckAccess returns true if the authenticated role may read the row identified
// by rowID in table. It executes:
//
//	BEGIN;
//	SET LOCAL role = {role};
//	SELECT 1 FROM {table} WHERE id = $1;
//	ROLLBACK;
//
// The SET LOCAL role applies the caller's JWT role, which activates RLS
// policies defined for that role. A successful SELECT means the row is visible.
//
// Positive results are cached for 5 seconds per (role, table, rowID) triplet
// to avoid hammering Postgres with repeated access checks on broadcast storms.
func CheckAccess(ctx context.Context, db *pgxpool.Pool, role, table, rowID string) bool {
	cacheKey := fmt.Sprintf("%s:%s:%s", role, table, rowID)

	if v, ok := accessCache.Load(cacheKey); ok {
		entry := v.(accessCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return true
		}
		accessCache.Delete(cacheKey)
	}

	conn, err := db.Acquire(ctx)
	if err != nil {
		return false
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return false
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	roleIdent := pgx.Identifier{role}.Sanitize()
	if _, err := tx.Exec(ctx, "SET LOCAL role = "+roleIdent); err != nil {
		return false
	}

	tableIdent := pgx.Identifier{table}.Sanitize()
	var dummy int
	err = tx.QueryRow(ctx,
		fmt.Sprintf("SELECT 1 FROM %s WHERE id = $1", tableIdent),
		rowID,
	).Scan(&dummy)
	if err != nil {
		return false
	}

	// Cache the positive result.
	accessCache.Store(cacheKey, accessCacheEntry{expiresAt: time.Now().Add(accessCacheTTL)})
	return true
}
