package storage

import (
	"github.com/gobuffalo/pop/v6"
)

// TightenPoolForMigration sets the underlying sql.DB pool to a single connection so
// session SET search_path (and Pop migrator queries) stay on the same connection.
func TightenPoolForMigration(db *pop.Connection) {
	sqldb, ok := popConnToStd(db)
	if !ok || sqldb == nil {
		return
	}
	sqldb.SetMaxOpenConns(1)
	sqldb.SetMaxIdleConns(1)
}
