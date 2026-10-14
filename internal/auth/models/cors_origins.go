package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/gobuffalo/pop/v6"
	"github.com/pkg/errors"
	"github.com/supatype/server/internal/auth/storage"
)

// CorsOrigins is the browser origin list saved from Studio. At most one row exists; while it does,
// it replaces the configured default.
type CorsOrigins struct {
	Singleton bool      `db:"singleton"`
	Origins   string    `db:"origins"`
	UpdatedAt time.Time `db:"updated_at"`
}

// TableName is the table the saved list lives in.
func (CorsOrigins) TableName() string {
	return "cors_origins"
}

func corsOriginsTable() string {
	return (&pop.Model{Value: CorsOrigins{}}).TableName()
}

// FindCorsOrigins returns the saved list and when it was saved, or nil when none is saved.
func FindCorsOrigins(tx *storage.Connection) ([]string, *time.Time, error) {
	var row CorsOrigins
	err := tx.RawQuery(`select singleton, origins::text as origins, updated_at from ` + corsOriginsTable()).First(&row)
	if err != nil {
		if errors.Cause(err) == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, errors.Wrap(err, "error finding saved CORS origins")
	}
	var origins []string
	if err := json.Unmarshal([]byte(row.Origins), &origins); err != nil {
		return nil, nil, errors.Wrap(err, "saved CORS origins are not a list")
	}
	return origins, &row.UpdatedAt, nil
}

// SaveCorsOrigins replaces the saved list and returns when it was saved.
func SaveCorsOrigins(tx *storage.Connection, origins []string) (time.Time, error) {
	// Microseconds, as Postgres stores it, so the answer to a save matches a later read.
	at := time.Now().UTC().Truncate(time.Microsecond)
	body, err := json.Marshal(origins)
	if err != nil {
		return at, errors.Wrap(err, "error encoding CORS origins")
	}
	err = tx.RawQuery(
		`insert into `+corsOriginsTable()+` (singleton, origins, updated_at) values (true, ?::jsonb, ?)
		 on conflict (singleton) do update set origins = excluded.origins, updated_at = excluded.updated_at`,
		string(body), at,
	).Exec()
	return at, errors.Wrap(err, "error saving CORS origins")
}

// DeleteCorsOrigins removes the saved list, if there is one.
func DeleteCorsOrigins(tx *storage.Connection) error {
	// With a WHERE: Supatype's Postgres refuses a DELETE without one (its safe-update guard), and
	// the reset failed with 503 on a real stack while every test on plain Postgres passed.
	err := tx.RawQuery(`delete from ` + corsOriginsTable() + ` where singleton`).Exec()
	return errors.Wrap(err, "error deleting saved CORS origins")
}
