package models

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/auth/storage/test"
	"github.com/supatype/server/internal/conf"
)

type IdentityTestSuite struct {
	suite.Suite
	db *storage.Connection
}

func (ts *IdentityTestSuite) SetupTest() {
	TruncateAll(ts.db)
}

func TestIdentity(t *testing.T) {
	globalConfig, err := conf.LoadGlobal(modelsTestConfig)
	require.NoError(t, err)

	conn, err := test.SetupDBConnection(globalConfig)
	require.NoError(t, err)

	ts := &IdentityTestSuite{
		db: conn,
	}
	defer ts.db.Close()

	suite.Run(t, ts)
}

func (ts *IdentityTestSuite) TestNewIdentity() {
	u := ts.createUserWithEmail("test@supatype.io")
	ts.Run("Test create identity with no provider id", func() {
		identityData := map[string]interface{}{}
		_, err := NewIdentity(u, "email", identityData)
		require.Error(ts.T(), err, "Error missing provider id")
	})

	ts.Run("Test create identity successfully", func() {
		identityData := map[string]interface{}{"sub": uuid.Nil.String()}
		identity, err := NewIdentity(u, "email", identityData)
		require.NoError(ts.T(), err)
		require.Equal(ts.T(), u.ID, identity.UserID)
	})
}

func (ts *IdentityTestSuite) TestFindUserIdentities() {
	u := ts.createUserWithIdentity("test@supatype.io")
	identities, err := FindIdentitiesByUserID(ts.db, u.ID)
	require.NoError(ts.T(), err)

	require.Len(ts.T(), identities, 1)

}

func (ts *IdentityTestSuite) TestUpdateIdentityData() {
	u := ts.createUserWithIdentity("test@supatype.io")

	identities, err := FindIdentitiesByUserID(ts.db, u.ID)
	require.NoError(ts.T(), err)

	updates := map[string]interface{}{
		"sub":   nil,
		"name":  nil,
		"email": nil,
	}
	for _, identity := range identities {
		err := identity.UpdateIdentityData(ts.db, updates)
		require.NoError(ts.T(), err)
	}

	updatedIdentities, err := FindIdentitiesByUserID(ts.db, u.ID)
	require.NoError(ts.T(), err)
	for _, identity := range updatedIdentities {
		require.Empty(ts.T(), identity.IdentityData)
	}
}

func (ts *IdentityTestSuite) createUserWithEmail(email string) *User {
	user, err := NewUser("", email, "secret", "test", nil)
	require.NoError(ts.T(), err)

	err = ts.db.Create(user)
	require.NoError(ts.T(), err)

	return user
}

func (ts *IdentityTestSuite) createUserWithIdentity(email string) *User {
	user, err := NewUser("", email, "secret", "test", nil)
	require.NoError(ts.T(), err)

	err = ts.db.Create(user)
	require.NoError(ts.T(), err)

	identityData := map[string]interface{}{
		"sub":   uuid.Nil.String(),
		"name":  "test",
		"email": email,
	}
	require.NoError(ts.T(), err)

	identity, err := NewIdentity(user, "email", identityData)
	require.NoError(ts.T(), err)

	err = ts.db.Create(identity)
	require.NoError(ts.T(), err)

	return user
}

// createUserWithTwoIdentities gives a user an email identity and then a newer
// github one, and rewrites the email identity last. The rewrite moves its row
// behind github's in the table while leaving it the older of the two, so a
// query that reads rows in table order lists github first.
func (ts *IdentityTestSuite) createUserWithTwoIdentities(email, githubEmail string) *User {
	u := ts.createUserWithIdentity(email)
	github, err := NewIdentity(u, "github", map[string]interface{}{"sub": "github-sub", "email": githubEmail})
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(github))

	backdate := "UPDATE identities SET created_at = now() - interval '1 hour' WHERE user_id = ? AND provider = 'email'"
	require.NoError(ts.T(), ts.db.RawQuery(backdate, u.ID).Exec())
	return u
}

// A user's first provider is the one they signed up with, so the providers list
// must follow created_at, not the order rows happen to sit in the table.
func (ts *IdentityTestSuite) TestFindProvidersByUserOldestFirst() {
	u := ts.createUserWithTwoIdentities("order@supatype.io", "order-github@supatype.io")

	providers, err := FindProvidersByUser(ts.db, u)
	require.NoError(ts.T(), err)
	require.Equal(ts.T(), []string{"email", "github"}, providers)
}

// When the user's email matches none of their identities, it falls back to the
// oldest identity whose email is free, whatever order the rows are stored in.
func (ts *IdentityTestSuite) TestUpdateUserEmailFromIdentitiesPicksTheOldest() {
	u := ts.createUserWithTwoIdentities("first@supatype.io", "second@supatype.io")
	require.NoError(ts.T(), u.SetEmail(ts.db, "gone@supatype.io"))

	require.NoError(ts.T(), u.UpdateUserEmailFromIdentities(ts.db))
	require.Equal(ts.T(), "first@supatype.io", u.GetEmail())
}
