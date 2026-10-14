package corsorigins

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/auth/storage/test"
	"github.com/supatype/server/internal/conf"
)

// The saved list against the auth schema on Postgres, as the server keeps it. Uses the auth
// suite's database (hack/test.env), migrated with the auth migrations.

func testDB(t *testing.T) *storage.Connection {
	t.Helper()
	config, err := conf.LoadGlobal("../../hack/test.env")
	require.NoError(t, err)
	conn, err := test.SetupDBConnection(config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, models.TruncateAll(conn))
	return conn
}

// A list saved through one server is what the next server on the same database serves: the table
// is the setting, not the process.
func TestASavedListSurvivesARestartAndIsSharedByReplicas(t *testing.T) {
	db := testDB(t)
	defaults := Defaults{SiteURL: "https://app.example.com"}
	first := NewPolicy(DBStore{DB: db}, defaults)

	saved, err := first.Save(context.Background(), []string{"https://partner.example/", "https://app.example.com"})
	require.NoError(t, err)
	require.Equal(t, []string{"https://partner.example", "https://app.example.com"}, saved.Origins)

	restarted := NewPolicy(DBStore{DB: db}, defaults)
	got := restarted.InForce(context.Background(), nil)
	require.Equal(t, "saved", got.Source)
	// The site URL first: it is always allowed (SHc), and the saved list follows it.
	require.Equal(t, []string{"https://app.example.com", "https://partner.example"}, got.Origins)
	require.True(t, got.Saved.UpdatedAt.Equal(saved.UpdatedAt), "the read matches the save to the microsecond")
}

func TestAResetGoesBackToTheDefaultEverywhere(t *testing.T) {
	db := testDB(t)
	defaults := Defaults{SiteURL: "https://app.example.com", Configured: []string{"https://admin.example.com"}}
	p := NewPolicy(DBStore{DB: db}, defaults)
	_, err := p.Save(context.Background(), []string{"https://partner.example"})
	require.NoError(t, err)

	require.NoError(t, p.Reset(context.Background()))

	other := NewPolicy(DBStore{DB: db}, defaults)
	got := other.InForce(context.Background(), nil)
	require.Equal(t, "default", got.Source)
	require.Equal(t, []string{"https://app.example.com", "https://admin.example.com"}, got.Origins)
}

func TestSavingAgainReplacesTheList(t *testing.T) {
	db := testDB(t)
	p := NewPolicy(DBStore{DB: db}, Defaults{})
	_, err := p.Save(context.Background(), []string{"https://one.example"})
	require.NoError(t, err)
	_, err = p.Save(context.Background(), []string{"https://two.example", "*"})
	require.NoError(t, err)

	got, err := DBStore{DB: db}.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"https://two.example", "*"}, got.Origins)
}

func TestTheStoreReportsADatabaseItCannotReach(t *testing.T) {
	store := DBStore{DB: testDB(t)}
	// A cancelled request stands in for a database that stopped answering: every call fails.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.Save(ctx, []string{"https://x.example"})
	require.Error(t, err)
	_, err = store.Load(ctx)
	require.Error(t, err)
	require.Error(t, store.Delete(ctx))
}
