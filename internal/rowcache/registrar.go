// Package rowcache keeps pg_keyspace's row-cache registrations in step with the
// REST cache allowlist.
//
// §12.2 asked whether Mode B should ride the existing `cache_tables` allowlist
// or get a toggle of its own, and chose one switch: two would mean explaining
// the difference between two caches to someone who wants one. So a table with
// `enabled` in `cache_tables` is registered for the row cache, and a table
// without it is not.
//
// The two caches are still different things, and the difference shows up here
// rather than in the UI. The response cache will take any table; the row cache
// needs a primary key, because its key IS the primary key. A table that cannot
// be registered therefore still gets the response cache, and reconciliation
// reports it rather than failing — refusing the whole allowlist because one
// table has no primary key would turn a partial win into an error message.
//
// # Why this runs on a write and not at startup
//
// Reconcile is declarative: give it the intended set and it converges. The
// obvious next step is to call it when the process starts, so a database and a
// config that have drifted line up again without anyone noticing the drift.
//
// That would be destructive here, and not hypothetically. The allowlist lives
// in .supatype/api-config.json (config.ApiConfigPath), and the Cloud
// tenant-gateway manifest mounts no volume for it — so the file is on the
// container's filesystem and an ordinary pod restart brings the process back
// with an empty allowlist. A reconcile at that moment reads "no tables enabled"
// and faithfully unregisters every one of them: the row cache silently turns
// itself off for the whole project, on a restart that changed nothing.
//
// So registration follows an affirmative write and nothing else. A PATCH that
// sets cache_tables is somebody saying what they want; a process that has just
// started knows only what it managed to keep, which is not the same claim.
//
// (The allowlist not surviving a restart is a problem in its own right, and it
// is older and wider than the row cache — it turns the response cache off too.
// Fixing where that config is stored is the fix; a reconcile here would only
// spread the consequences.)
package rowcache

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB is the read path to the project's own Postgres. *pgxpool.Pool satisfies it.
//
// Query rather than Exec because every call here returns something worth
// reading: rowcache_register and rowcache_unregister return a boolean, and a
// false from either is the case this package exists to report.
type DB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ErrUnavailable means this database has no row cache to register anything
// with: pg_keyspace is not installed, or the role cannot read its catalogue.
//
// A distinct error rather than a zero Outcome, because the two readings differ:
// "nothing needed registering" and "there is nowhere to register" produce the
// same empty result and only one of them is worth telling somebody about.
var ErrUnavailable = errors.New("the row cache is not available on this database")

// Skip is a table the allowlist asked for and the row cache would not take.
type Skip struct {
	Table  string `json:"table"`
	Reason string `json:"reason"`
}

// Outcome is what a reconcile did, in the terms a user would describe it.
type Outcome struct {
	Registered   []string `json:"registered,omitempty"`
	Unregistered []string `json:"unregistered,omitempty"`
	Skipped      []Skip   `json:"skipped,omitempty"`
}

// Changed reports whether anything happened, so a caller can leave the field
// out of a response rather than sending three empty arrays.
func (o Outcome) Changed() bool {
	return len(o.Registered) > 0 || len(o.Unregistered) > 0 || len(o.Skipped) > 0
}

const (
	// Normalised by Postgres on both sides. The catalogue stores
	// format('%I.%I', nspname, relname), so building the wanted set any other
	// way would make `orders` and `"orders"` different tables to a string
	// comparison and identical to the server.
	//
	// to_regclass rather than a cast: a cast raises for a table that is not
	// there, and a table named in the allowlist before its migration has run is
	// an ordinary thing to skip, not an error.
	wantedSQL = `SELECT t,
	                    format('%I.%I', $1::text, t) AS qualified,
	                    to_regclass(format('%I.%I', $1::text, t)) IS NOT NULL AS exists
	               FROM unnest($2::text[]) AS t`

	// Scoped to the configured schema on purpose. A registration someone made
	// by hand in another schema is not this allowlist's to remove, and a
	// reconcile that owned every row in the catalogue would silently undo it.
	//
	// The join also drops rows whose table has since been dropped: unregistering
	// one would resolve its regclass and fail, and the catalogue row is dead
	// weight either way.
	currentSQL = `SELECT r.tbl
	                FROM supacache.rowcache_reg r
	                JOIN pg_class c ON c.oid = to_regclass(r.tbl)
	                JOIN pg_namespace n ON n.oid = c.relnamespace
	               WHERE n.nspname = $1`

	registerSQL   = `SELECT supacache.rowcache_register($1)`
	unregisterSQL = `SELECT supacache.rowcache_unregister($1)`
)

// Reconcile makes the row cache's registrations match `enabled`, which is the
// set of table names the REST allowlist has switched on.
//
// Declarative, not a delta: it is given the whole intended set and works out
// what to add and what to remove. That is what lets it be the recovery path as
// well as the update path — a database restored from a backup, or a config file
// restored without one, converges on the next call rather than needing somebody
// to notice the drift.
func Reconcile(ctx context.Context, db DB, schema string, enabled []string) (Outcome, error) {
	var out Outcome
	if db == nil {
		return out, ErrUnavailable
	}

	wanted, skipped, err := resolveWanted(ctx, db, schema, enabled)
	if err != nil {
		return out, err
	}
	out.Skipped = skipped

	current, err := currentRegistrations(ctx, db, schema)
	if err != nil {
		return out, err
	}

	for table, qualified := range wanted {
		if _, already := current[qualified]; already {
			continue
		}
		ok, err := callBool(ctx, db, registerSQL, qualified)
		if err != nil {
			return out, fmt.Errorf("registering %s: %w", qualified, err)
		}
		if !ok {
			// The commonest reason by far, and the only one a user can act on.
			// rowcache_register returns false rather than raising for a table
			// with no primary key, and the row cache keys on the primary key, so
			// there is nothing to key on.
			out.Skipped = append(out.Skipped, Skip{
				Table:  table,
				Reason: "no primary key, so there is no row key to cache by; the response cache still applies",
			})
			continue
		}
		out.Registered = append(out.Registered, table)
	}

	for qualified, table := range current {
		if _, keep := wanted[table]; keep {
			continue
		}
		if _, err := callBool(ctx, db, unregisterSQL, qualified); err != nil {
			return out, fmt.Errorf("unregistering %s: %w", qualified, err)
		}
		// Not checked for false. Unregistering something already gone is the
		// outcome asked for, and reporting it would make a converged reconcile
		// look like it did work.
		out.Unregistered = append(out.Unregistered, table)
	}

	return out, nil
}

// resolveWanted maps each allowlisted table to its qualified catalogue name,
// and collects the ones that are not there to be registered.
func resolveWanted(ctx context.Context, db DB, schema string, enabled []string) (map[string]string, []Skip, error) {
	wanted := map[string]string{}
	var skipped []Skip
	if len(enabled) == 0 {
		return wanted, nil, nil
	}

	rows, err := db.Query(ctx, wantedSQL, schema, enabled)
	if err != nil {
		return nil, nil, classify(err)
	}
	defer rows.Close()

	for rows.Next() {
		var table, qualified string
		var exists bool
		if err := rows.Scan(&table, &qualified, &exists); err != nil {
			return nil, nil, err
		}
		if !exists {
			// A config pushed before the migration that creates the table. The
			// next reconcile picks it up, so this is a note rather than a fault.
			skipped = append(skipped, Skip{Table: table, Reason: "no such table in " + schema})
			continue
		}
		wanted[table] = qualified
	}
	return wanted, skipped, rows.Err()
}

// currentRegistrations returns qualified name → the bare table name, so both
// directions of the comparison above have the key they need.
func currentRegistrations(ctx context.Context, db DB, schema string) (map[string]string, error) {
	rows, err := db.Query(ctx, currentSQL, schema)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	current := map[string]string{}
	for rows.Next() {
		var qualified string
		if err := rows.Scan(&qualified); err != nil {
			return nil, err
		}
		current[qualified] = bareName(qualified, schema)
	}
	return current, rows.Err()
}

// bareName strips the schema that currentSQL already matched, and the quoting
// format('%I.%I') may have added, so a catalogue row lines up with the plain
// table name the allowlist uses.
func bareName(qualified, schema string) string {
	for _, prefix := range []string{schema + ".", `"` + schema + `".`} {
		if len(qualified) > len(prefix) && qualified[:len(prefix)] == prefix {
			rest := qualified[len(prefix):]
			if len(rest) > 1 && rest[0] == '"' && rest[len(rest)-1] == '"' {
				return rest[1 : len(rest)-1]
			}
			return rest
		}
	}
	return qualified
}

func callBool(ctx context.Context, db DB, sql, arg string) (bool, error) {
	rows, err := db.Query(ctx, sql, arg)
	if err != nil {
		return false, classify(err)
	}
	defer rows.Close()
	if !rows.Next() {
		return false, rows.Err()
	}
	var ok bool
	if err := rows.Scan(&ok); err != nil {
		return false, err
	}
	return ok, rows.Err()
}

// classify turns "there is no row cache here" into ErrUnavailable and leaves
// every other failure alone.
//
// 42P01 is the catalogue table absent, 42501 is it present and this role not a
// pg_monitor member, 3F000 is no supacache schema at all. All three describe a
// deployment without the extension, which is a supported deployment.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "42P01", "42501", "3F000", "42883":
			return ErrUnavailable
		}
	}
	return err
}
