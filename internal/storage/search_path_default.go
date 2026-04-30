package storage

import (
	"fmt"
	"strings"

	"github.com/gobuffalo/pop/v6"
	"github.com/lib/pq"
)

// TryAlterDatabaseSearchPathDefault sets ALTER DATABASE ... SET search_path so every new
// session on this database gets a non-empty search_path. That fixes Pop migrations when
// pooled connections do not inherit a prior SET search_path on another connection.
// Fails silently on permission errors (managed Postgres); callers can log and continue.
func TryAlterDatabaseSearchPathDefault(db *pop.Connection, namespace string) error {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return nil
	}
	var dbName string
	if err := db.RawQuery("SELECT current_database()").First(&dbName); err != nil {
		return err
	}
	// Order matches supatype/postgres role defaults; extensions may be absent on vanilla PG.
	sqlFull := fmt.Sprintf(
		"ALTER DATABASE %s SET search_path TO %s, %s, %s",
		pq.QuoteIdentifier(dbName),
		pq.QuoteIdentifier(ns),
		pq.QuoteIdentifier("public"),
		pq.QuoteIdentifier("extensions"),
	)
	if err := db.RawQuery(sqlFull).Exec(); err != nil {
		sqlMin := fmt.Sprintf(
			"ALTER DATABASE %s SET search_path TO %s, %s",
			pq.QuoteIdentifier(dbName),
			pq.QuoteIdentifier(ns),
			pq.QuoteIdentifier("public"),
		)
		return db.RawQuery(sqlMin).Exec()
	}
	return nil
}
