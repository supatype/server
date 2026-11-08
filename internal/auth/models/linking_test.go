package models

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/supatype/server/internal/auth/provider"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/auth/storage/test"
	"github.com/supatype/server/internal/conf"
)

type AccountLinkingTestSuite struct {
	suite.Suite

	config *conf.GlobalConfiguration
	db     *storage.Connection
}

func (ts *AccountLinkingTestSuite) SetupTest() {
	TruncateAll(ts.db)
}

func TestAccountLinking(t *testing.T) {
	globalConfig, err := conf.LoadGlobal(modelsTestConfig)
	require.NoError(t, err)

	conn, err := test.SetupDBConnection(globalConfig)
	require.NoError(t, err)

	ts := &AccountLinkingTestSuite{
		config: globalConfig,
		db:     conn,
	}
	defer ts.db.Close()

	suite.Run(t, ts)
}

func (ts *AccountLinkingTestSuite) TestCreateAccountDecisionNoAccounts() {
	// when there are no accounts in the system -- conventional provider
	testEmail := provider.Email{
		Email:    "test@example.com",
		Verified: true,
		Primary:  true,
	}
	decision, err := DetermineAccountLinking(ts.db, ts.config, []provider.Email{testEmail}, ts.config.JWT.Aud, "provider", "abcdefgh", false)
	require.NoError(ts.T(), err)

	require.Equal(ts.T(), decision.Decision, CreateAccount)

	// when there are no accounts in the system -- SSO provider
	decision, err = DetermineAccountLinking(ts.db, ts.config, []provider.Email{testEmail}, ts.config.JWT.Aud, "sso:f06f9e3d-ff92-4c47-a179-7acf1fda6387", "abcdefgh", false)
	require.NoError(ts.T(), err)

	require.Equal(ts.T(), decision.Decision, CreateAccount)
}

func (ts *AccountLinkingTestSuite) TestCreateAccountDecisionWithAccounts() {
	userA, err := NewUser("", "test@example.com", "", "authenticated", nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(userA))
	identityA, err := NewIdentity(userA, "provider", map[string]interface{}{
		"sub":   userA.ID.String(),
		"email": "test@example.com",
	})
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(identityA))

	userB, err := NewUser("", "test@samltest.id", "", "authenticated", nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(userB))

	ssoProvider := "sso:f06f9e3d-ff92-4c47-a179-7acf1fda6387"
	identityB, err := NewIdentity(userB, ssoProvider, map[string]interface{}{
		"sub":   userB.ID.String(),
		"email": "test@samltest.id",
	})
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(identityB))

	// when the email doesn't exist in the system -- conventional provider
	decision, err := DetermineAccountLinking(ts.db, ts.config, []provider.Email{
		{
			Email:    "other@example.com",
			Verified: true,
			Primary:  true,
		},
	}, ts.config.JWT.Aud, "provider", "abcdefgh", false)
	require.NoError(ts.T(), err)

	require.Equal(ts.T(), decision.Decision, CreateAccount)
	require.Equal(ts.T(), decision.LinkingDomain, "default")

	// when looking for an email that doesn't exist in the SSO linking domain
	decision, err = DetermineAccountLinking(ts.db, ts.config, []provider.Email{
		{
			Email:    "other@samltest.id",
			Verified: true,
			Primary:  true,
		},
	}, ts.config.JWT.Aud, ssoProvider, "abcdefgh", false)
	require.NoError(ts.T(), err)

	require.Equal(ts.T(), decision.Decision, CreateAccount)
	require.Equal(ts.T(), decision.LinkingDomain, ssoProvider)
}

func (ts *AccountLinkingTestSuite) TestAccountExists() {
	userA, err := NewUser("", "test@example.com", "", "authenticated", nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(userA))
	identityA, err := NewIdentity(userA, "provider", map[string]interface{}{
		"sub":   userA.ID.String(),
		"email": "test@example.com",
	})
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(identityA))

	decision, err := DetermineAccountLinking(ts.db, ts.config, []provider.Email{
		{
			Email:    "test@example.com",
			Verified: true,
			Primary:  true,
		},
	}, ts.config.JWT.Aud, "provider", userA.ID.String(), false)
	require.NoError(ts.T(), err)

	require.Equal(ts.T(), decision.Decision, AccountExists)
	require.Equal(ts.T(), decision.User.ID, userA.ID)
}

func (ts *AccountLinkingTestSuite) TestLinkingScenarios() {
	userA, err := NewUser("", "test@example.com", "", "authenticated", nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(userA))
	identityA, err := NewIdentity(userA, "provider", map[string]interface{}{
		"sub":   userA.ID.String(),
		"email": "test@example.com",
	})
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(identityA))

	userB, err := NewUser("", "test@samltest.id", "", "authenticated", nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(userB))

	identityB, err := NewIdentity(userB, "sso:f06f9e3d-ff92-4c47-a179-7acf1fda6387", map[string]interface{}{
		"sub":   userB.ID.String(),
		"email": "test@samltest.id",
	})
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(identityB))

	cases := []struct {
		desc     string
		email    provider.Email
		sub      string
		provider string
		decision AccountLinkingResult
	}{
		{
			// link decision because the below described identity is in the default linking domain but uses "other-provider" instead of "provder"
			desc: "same email address",
			email: provider.Email{
				Email:    "test@example.com",
				Verified: true,
				Primary:  true,
			},
			sub:      userA.ID.String(),
			provider: "other-provider",
			decision: AccountLinkingResult{
				Decision:      LinkAccount,
				User:          userA,
				LinkingDomain: "default",
				CandidateEmail: provider.Email{
					Email:    "test@example.com",
					Verified: true,
					Primary:  true,
				},
			},
		},
		{
			desc: "same email address in uppercase",
			email: provider.Email{
				Email:    "TEST@example.com",
				Verified: true,
				Primary:  true,
			},
			sub:      userA.ID.String(),
			provider: "other-provider",
			decision: AccountLinkingResult{
				Decision:      LinkAccount,
				User:          userA,
				LinkingDomain: "default",
				CandidateEmail: provider.Email{
					// expected email should be case insensitive
					Email:    "test@example.com",
					Verified: true,
					Primary:  true,
				},
			},
		},
		{
			desc: "no link decision because the SSO linking domain is scoped to the provider unique ID",
			email: provider.Email{
				Email:    "test@samltest.id",
				Verified: true,
				Primary:  true,
			},
			sub:      userB.ID.String(),
			provider: "sso:f06f9e3d-ff92-4c47-a179-7acf1fda6387",
			// decision: AccountExists,
			decision: AccountLinkingResult{
				Decision:      AccountExists,
				User:          userB,
				LinkingDomain: "sso:f06f9e3d-ff92-4c47-a179-7acf1fda6387",
				CandidateEmail: provider.Email{
					Email:    "test@samltest.id",
					Verified: true,
					Primary:  true,
				},
			},
		},
		{
			desc: "create account with empty email because email is unverified and user exists",
			email: provider.Email{
				Email:    "test@example.com",
				Verified: false,
				Primary:  true,
			},
			sub:      userA.ID.String(),
			provider: "other-provider",
			decision: AccountLinkingResult{
				Decision:      CreateAccount,
				LinkingDomain: "default",
				CandidateEmail: provider.Email{
					Email:    "",
					Verified: false,
					Primary:  true,
				},
			},
		},
		{
			desc: "create account because email is unverified and user doesn't exist",
			email: provider.Email{
				Email:    "other@example.com",
				Verified: false,
				Primary:  true,
			},
			sub:      "000000000",
			provider: "other-provider",
			decision: AccountLinkingResult{
				Decision:      CreateAccount,
				LinkingDomain: "default",
				CandidateEmail: provider.Email{
					Email:    "other@example.com",
					Verified: false,
					Primary:  true,
				},
			},
		},
	}

	for _, c := range cases {
		ts.Run(c.desc, func() {
			decision, err := DetermineAccountLinking(ts.db, ts.config, []provider.Email{c.email}, ts.config.JWT.Aud, c.provider, c.sub, false)
			require.NoError(ts.T(), err)
			require.Equal(ts.T(), c.decision.Decision, decision.Decision)
			require.Equal(ts.T(), c.decision.LinkingDomain, decision.LinkingDomain)
			require.Equal(ts.T(), c.decision.CandidateEmail.Email, decision.CandidateEmail.Email)
			require.Equal(ts.T(), c.decision.CandidateEmail.Verified, decision.CandidateEmail.Verified)
			require.Equal(ts.T(), c.decision.CandidateEmail.Primary, decision.CandidateEmail.Primary)
		})
	}

}

func (ts *AccountLinkingTestSuite) TestMultipleAccounts() {
	userA, err := NewUser("", "test@example.com", "", "authenticated", nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(userA))
	identityA, err := NewIdentity(userA, "provider", map[string]interface{}{
		"sub":   userA.ID.String(),
		"email": "test@example.com",
	})
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(identityA))

	userB, err := NewUser("", "test-b@example.com", "", "authenticated", nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(userB))
	identityB, err := NewIdentity(userB, "provider", map[string]interface{}{
		"sub":   userB.ID.String(),
		"email": "test@example.com", // intentionally same as userA
	})
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.db.Create(identityB))

	// decision is multiple accounts because there are two distinct
	// identities in the same "default" linking domain with the same email
	// address pointing to two different user accounts
	decision, err := DetermineAccountLinking(ts.db, ts.config, []provider.Email{
		{
			Email:    "test@example.com",
			Verified: true,
			Primary:  true,
		},
	}, ts.config.JWT.Aud, "provider", "abcdefgh", false)
	require.NoError(ts.T(), err)

	require.Equal(ts.T(), decision.Decision, MultipleAccounts)
}

// firstSSOLogin decides a first login of email through an SSO provider, on a
// verified domain of it.
func (ts *AccountLinkingTestSuite) firstSSOLogin(email string) AccountLinkingResult {
	decision, err := DetermineAccountLinking(ts.db, ts.config, []provider.Email{
		{Email: email, Verified: true, Primary: true},
	}, ts.config.JWT.Aud, "sso:f06f9e3d-ff92-4c47-a179-7acf1fda6387", "first-login", true)
	require.NoError(ts.T(), err)
	return decision
}

// createUser saves an account for email, an SSO account when ssoUser is set.
// The email is stored as given: NewUser lowercases it, but rows written by
// other means can differ from each other in case alone.
func (ts *AccountLinkingTestSuite) createUser(email string, ssoUser bool) *User {
	user, err := NewUser("", email, "", ts.config.JWT.Aud, nil)
	require.NoError(ts.T(), err)
	user.Email = storage.NullString(email)
	user.IsSSOUser = ssoUser
	require.NoError(ts.T(), ts.db.Create(user))
	return user
}

// A first SSO login on a verified domain links only to a regular account:
// an SSO account of another provider with the same email is not one.
func (ts *AccountLinkingTestSuite) TestSSOLinkSkipsSSOAccounts() {
	ts.createUser("sso@example.com", true)

	decision := ts.firstSSOLogin("sso@example.com")
	require.Equal(ts.T(), CreateAccount, decision.Decision)
	require.Nil(ts.T(), decision.User)
}

// When more than one regular account has the email in some case and none has
// it exactly as the SSO login gives it, the login links to none of them.
func (ts *AccountLinkingTestSuite) TestSSOLinkWithSeveralAccountsIsMultipleAccounts() {
	ts.createUser("Dup@example.com", false)
	ts.createUser("DUP@example.com", false)

	decision := ts.firstSSOLogin("dup@example.com")
	require.Equal(ts.T(), MultipleAccounts, decision.Decision)
	require.Nil(ts.T(), decision.User)
}

// When several regular accounts have the email in some case, the one whose
// stored email is exactly the SSO email is the account linked.
func (ts *AccountLinkingTestSuite) TestSSOLinkToTheExactCaseAccount() {
	ts.createUser("dup@example.com", false)
	exact := ts.createUser("Dup@Example.com", false)
	ts.createUser("DUP@EXAMPLE.COM", false)

	decision := ts.firstSSOLogin("Dup@Example.com")
	require.Equal(ts.T(), LinkAccount, decision.Decision)
	require.Equal(ts.T(), exact.ID, decision.User.ID)
}

// One regular account on a verified domain is linked, whatever the case of
// its stored email.
func (ts *AccountLinkingTestSuite) TestSSOLinkToTheOneAccount() {
	user := ts.createUser("One@example.com", false)

	decision := ts.firstSSOLogin("one@example.com")
	require.Equal(ts.T(), LinkAccount, decision.Decision)
	require.Equal(ts.T(), user.ID, decision.User.ID)
}
