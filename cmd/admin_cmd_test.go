package cmd

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/studiomembers"
)

// cliTestDB connects to the migrated test database with a Studio membership
// table and audit table of the engine's shape, removed again after the test.
func cliTestDB(t *testing.T) (*storage.Connection, *conf.GlobalConfiguration) {
	t.Helper()
	config, err := conf.LoadGlobal("../hack/test.env")
	require.NoError(t, err)
	db, err := storage.Dial(config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, models.TruncateAll(db))

	require.NoError(t, db.RawQuery(`CREATE SCHEMA IF NOT EXISTS _supatype`).Exec())
	require.NoError(t, db.RawQuery(`CREATE TABLE _supatype.studio_members (
		id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id          UUID UNIQUE,
		platform_user_id UUID UNIQUE,
		role             TEXT NOT NULL,
		created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		CONSTRAINT studio_members_one_identity
			CHECK (num_nonnulls(user_id, platform_user_id) = 1))`).Exec())
	require.NoError(t, db.RawQuery(`CREATE TABLE _supatype.studio_audit (
		id         BIGSERIAL PRIMARY KEY,
		actor_id   UUID,
		target_id  UUID,
		action     TEXT NOT NULL,
		role       TEXT,
		detail     JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`).Exec())
	t.Cleanup(func() {
		require.NoError(t, db.RawQuery(`DROP SCHEMA _supatype CASCADE`).Exec())
	})
	return db, config
}

func cliMember(t *testing.T, db *storage.Connection, aud, email, role string) *models.User {
	t.Helper()
	u, err := models.NewUser("", email, "test", aud, nil)
	require.NoError(t, err)
	require.NoError(t, db.Create(u))
	require.NoError(t, db.RawQuery(
		`INSERT INTO _supatype.studio_members (user_id, role) VALUES (?, ?)`, u.ID, role).Exec())
	return u
}

func cliAuditRemovals(t *testing.T, db *storage.Connection, u *models.User) int {
	t.Helper()
	var row struct {
		N int `db:"n"`
	}
	require.NoError(t, db.RawQuery(
		`SELECT count(*) AS n FROM _supatype.studio_audit
		  WHERE target_id = ? AND action = 'user_deleted' AND actor_id IS NULL`, u.ID).First(&row))
	return row.N
}

// `admin deleteuser` is a hard delete with nobody signed in, so it is held to
// the same last-admin rule as the admin API and audited with no actor.
func TestDeleteUserKeepsTheLastStudioAdmin(t *testing.T) {
	db, config := cliTestDB(t)
	aud := config.JWT.Aud
	admin := cliMember(t, db, aud, "only-admin@example.com", "admin")
	editor := cliMember(t, db, aud, "editor@example.com", "editor")

	err := deleteUser(db, aud, admin.GetEmail())
	require.Error(t, err)
	assert.True(t, errors.Is(err, studiomembers.ErrLastAdmin), "got: %v", err)
	_, err = models.FindUserByID(db, admin.ID)
	require.NoError(t, err, "the refused delete leaves the user")
	assert.Equal(t, 0, cliAuditRemovals(t, db, admin))

	require.NoError(t, deleteUser(db, aud, editor.ID.String()))
	assert.Equal(t, 1, cliAuditRemovals(t, db, editor))
}

func TestDeleteUserRejectsAnArgumentThatIsNeitherEmailNorID(t *testing.T) {
	db, config := cliTestDB(t)
	assert.Error(t, deleteUser(db, config.JWT.Aud, "nobody"))
}
