package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/studiomembers"
)

type AdminTestSuite struct {
	suite.Suite
	User   *models.User
	API    *API
	Config *conf.GlobalConfiguration

	token string
}

func TestAdmin(t *testing.T) {
	api, config, err := setupAPIForTest()
	require.NoError(t, err)

	ts := &AdminTestSuite{
		API:    api,
		Config: config,
	}
	defer api.db.Close()

	suite.Run(t, ts)
}

func (ts *AdminTestSuite) SetupTest() {
	models.TruncateAll(ts.API.db)
	ts.Config.External.Email.Enabled = true
	claims := &AccessTokenClaims{
		Role: "supatype_admin",
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(ts.Config.JWT.Secret))
	require.NoError(ts.T(), err, "Error generating admin jwt")
	ts.token = token
}

// TestAdminUsersUnauthorized tests API /admin/users route without authentication
func (ts *AdminTestSuite) TestAdminUsersUnauthorized() {
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	w := httptest.NewRecorder()

	ts.API.handler.ServeHTTP(w, req)
	assert.Equal(ts.T(), http.StatusUnauthorized, w.Code)
}

// TestAdminUsers tests API /admin/users route
func (ts *AdminTestSuite) TestAdminUsers() {
	// Setup request
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	assert.Equal(ts.T(), "</admin/users?page=0>; rel=\"last\"", w.Header().Get("Link"))
	assert.Equal(ts.T(), "0", w.Header().Get("X-Total-Count"))
}

// TestAdminUsers tests API /admin/users route
func (ts *AdminTestSuite) TestAdminUsers_Pagination() {
	u, err := models.NewUser("12345678", "test1@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	u, err = models.NewUser("987654321", "test2@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	// Setup request
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users?per_page=1", nil)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	assert.Equal(ts.T(), "</admin/users?page=2&per_page=1>; rel=\"next\", </admin/users?page=2&per_page=1>; rel=\"last\"", w.Header().Get("Link"))
	assert.Equal(ts.T(), "2", w.Header().Get("X-Total-Count"))

	data := make(map[string]interface{})
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
	for _, user := range data["users"].([]interface{}) {
		assert.NotEmpty(ts.T(), user)
	}
}

// Studio reads a user's provider from the list response, so a list that
// answered `identities: null` showed every user as having none.
func (ts *AdminTestSuite) TestAdminUsers_IncludesIdentities() {
	withGithub, err := models.NewUser("", "github@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.API.db.Create(withGithub))
	_, err = ts.API.createNewIdentity(ts.API.db, withGithub, "github", map[string]interface{}{
		"sub":   "gh-1",
		"email": "github@example.com",
	})
	require.NoError(ts.T(), err)

	without, err := models.NewUser("", "none@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.API.db.Create(without))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))
	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	var body struct {
		Users []struct {
			Email      string             `json:"email"`
			Identities *[]models.Identity `json:"identities"`
		} `json:"users"`
	}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&body))
	require.Len(ts.T(), body.Users, 2)

	providers := map[string][]string{}
	for _, u := range body.Users {
		require.NotNil(ts.T(), u.Identities, "identities for %s must be a list, not null", u.Email)
		providers[u.Email] = []string{}
		for _, identity := range *u.Identities {
			providers[u.Email] = append(providers[u.Email], identity.Provider)
		}
	}
	assert.Equal(ts.T(), map[string][]string{
		"github@example.com": {"github"},
		"none@example.com":   {},
	}, providers)
}

// TestAdminUsers tests API /admin/users route
func (ts *AdminTestSuite) TestAdminUsers_SortAsc() {
	u, err := models.NewUser("", "test1@example.com", "test", ts.Config.JWT.Aud, nil)
	u.CreatedAt = time.Now().Add(-time.Minute)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	u, err = models.NewUser("", "test2@example.com", "test", ts.Config.JWT.Aud, nil)
	u.CreatedAt = time.Now()
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	// Setup request
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	qv := req.URL.Query()
	qv.Set("sort", "created_at asc")
	req.URL.RawQuery = qv.Encode()

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	data := struct {
		Users []*models.User `json:"users"`
		Aud   string         `json:"aud"`
	}{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))

	require.Len(ts.T(), data.Users, 2)
	assert.Equal(ts.T(), "test1@example.com", data.Users[0].GetEmail())
	assert.Equal(ts.T(), "test2@example.com", data.Users[1].GetEmail())
}

// TestAdminUsers tests API /admin/users route
func (ts *AdminTestSuite) TestAdminUsers_SortDesc() {
	u, err := models.NewUser("12345678", "test1@example.com", "test", ts.Config.JWT.Aud, nil)
	u.CreatedAt = time.Now().Add(-time.Minute)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	u, err = models.NewUser("987654321", "test2@example.com", "test", ts.Config.JWT.Aud, nil)
	u.CreatedAt = time.Now()
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	// Setup request
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	data := struct {
		Users []*models.User `json:"users"`
		Aud   string         `json:"aud"`
	}{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))

	require.Len(ts.T(), data.Users, 2)
	assert.Equal(ts.T(), "test2@example.com", data.Users[0].GetEmail())
	assert.Equal(ts.T(), "test1@example.com", data.Users[1].GetEmail())
}

// TestAdminUsers tests API /admin/users route
func (ts *AdminTestSuite) TestAdminUsers_FilterEmail() {
	u, err := models.NewUser("", "test1@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	// Setup request
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users?filter=test1", nil)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	data := struct {
		Users []*models.User `json:"users"`
		Aud   string         `json:"aud"`
	}{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))

	require.Len(ts.T(), data.Users, 1)
	assert.Equal(ts.T(), "test1@example.com", data.Users[0].GetEmail())
}

// TestAdminUsers tests API /admin/users route
func (ts *AdminTestSuite) TestAdminUsers_FilterName() {
	u, err := models.NewUser("", "test1@example.com", "test", ts.Config.JWT.Aud, map[string]interface{}{"full_name": "Test User"})
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	u, err = models.NewUser("", "test2@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	// Setup request
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users?filter=User", nil)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	data := struct {
		Users []*models.User `json:"users"`
		Aud   string         `json:"aud"`
	}{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))

	require.Len(ts.T(), data.Users, 1)
	assert.Equal(ts.T(), "test1@example.com", data.Users[0].GetEmail())
}

// filterUserEmails creates one user per email, with the metadata given for it, and returns the
// emails `GET /admin/users?filter=<filter>` answers with.
func (ts *AdminTestSuite) filterUserEmails(users map[string]map[string]interface{}, filter string) []string {
	for email, meta := range users {
		u, err := models.NewUser("", email, "test", ts.Config.JWT.Aud, meta)
		require.NoError(ts.T(), err, "Error making new user")
		require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")
	}

	return ts.listedEmails(ts.getAdminUsers("filter=" + url.QueryEscape(filter)))
}

// Studio searches users by the name an OAuth provider stores, which is `name`, not `full_name`.
func (ts *AdminTestSuite) TestAdminUsers_FilterMetadataName() {
	emails := ts.filterUserEmails(map[string]map[string]interface{}{
		"grace@example.com": {"name": "Grace Hopper"},
		"other@example.com": {"name": "Someone Else"},
	}, "hopper")
	assert.Equal(ts.T(), []string{"grace@example.com"}, emails)
}

// An email stored with capitals, by SQL or an import, is found by a lower case search.
func (ts *AdminTestSuite) TestAdminUsers_FilterEmailIgnoresCase() {
	u, err := models.NewUser("", "mixed@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")
	require.NoError(ts.T(), ts.API.db.RawQuery("UPDATE auth.users SET email = ? WHERE id = ?", "Mixed.Case@Example.com", u.ID).Exec())

	emails := ts.filterUserEmails(nil, "mixed.case@")
	assert.Equal(ts.T(), []string{"Mixed.Case@Example.com"}, emails)
}

// `%` and `_` typed into the search are the characters themselves, not LIKE wildcards.
func (ts *AdminTestSuite) TestAdminUsers_FilterWildcardsAreLiteral() {
	users := map[string]map[string]interface{}{
		"a_b@example.com": nil,
		"axb@example.com": nil,
		"pct@example.com": {"full_name": "100% Done"},
		"num@example.com": {"full_name": "1000 Done"},
	}
	assert.Equal(ts.T(), []string{"a_b@example.com"}, ts.filterUserEmails(users, "a_b"))
	assert.Equal(ts.T(), []string{"pct@example.com"}, ts.filterUserEmails(nil, "100%"))
}

// seedFilterUsers makes three users that differ in role, ban, confirmation and provider:
//
//	alice: role editor, email confirmed, banned for a day, a github identity
//	bob:   default role, unconfirmed, ban already over, an email identity
//	carol: role editor, phone confirmed only, never banned, no identity
func (ts *AdminTestSuite) seedFilterUsers() {
	now := time.Now()
	future, past := now.Add(24*time.Hour), now.Add(-time.Hour)
	alice := ts.createFilterUser("alice@example.com", "editor", "github")
	bob := ts.createFilterUser("bob@example.com", "", "email")
	carol := ts.createFilterUser("carol@example.com", "editor", "")
	ts.execSQL("UPDATE auth.users SET email_confirmed_at = ?, banned_until = ? WHERE id = ?", now, future, alice.ID)
	ts.execSQL("UPDATE auth.users SET email_confirmed_at = NULL, phone_confirmed_at = NULL, banned_until = ? WHERE id = ?", past, bob.ID)
	ts.execSQL("UPDATE auth.users SET email_confirmed_at = NULL, phone = '15550001111', phone_confirmed_at = ?, banned_until = NULL WHERE id = ?", now, carol.ID)
}

// createFilterUser saves a user with the role given (the default when empty) and, when a
// provider is given, one identity from that provider.
func (ts *AdminTestSuite) createFilterUser(email, role, provider string) *models.User {
	u, err := models.NewUser("", email, "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	if role != "" {
		u.Role = role
	}
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")
	if provider != "" {
		i, err := models.NewIdentity(u, provider, map[string]interface{}{"sub": u.ID.String()})
		require.NoError(ts.T(), err)
		require.NoError(ts.T(), ts.API.db.Create(i), "Error creating identity")
	}
	return u
}

func (ts *AdminTestSuite) execSQL(sql string, args ...interface{}) {
	require.NoError(ts.T(), ts.API.db.RawQuery(sql, args...).Exec())
}

// getAdminUsers answers `GET /admin/users?<query>` as the admin.
func (ts *AdminTestSuite) getAdminUsers(query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users?"+query, nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))
	ts.API.handler.ServeHTTP(w, req)
	return w
}

// listedEmails is the sorted emails of the users in a `GET /admin/users` answer.
func (ts *AdminTestSuite) listedEmails(w *httptest.ResponseRecorder) []string {
	require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
	data := struct {
		Users []*models.User `json:"users"`
	}{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
	emails := make([]string, 0, len(data.Users))
	for _, u := range data.Users {
		emails = append(emails, u.GetEmail())
	}
	slices.Sort(emails)
	return emails
}

// assertListed checks the users `GET /admin/users?<query>` lists and the total it counts.
func (ts *AdminTestSuite) assertListed(query string, want ...string) {
	w := ts.getAdminUsers(query)
	assert.Equal(ts.T(), append([]string{}, want...), ts.listedEmails(w), query)
	assert.Equal(ts.T(), strconv.Itoa(len(want)), w.Header().Get("X-Total-Count"), query)
}

func (ts *AdminTestSuite) TestAdminUsers_FilterRole() {
	ts.seedFilterUsers()
	ts.assertListed("role=editor", "alice@example.com", "carol@example.com")
	ts.assertListed("role=", "alice@example.com", "bob@example.com", "carol@example.com")
}

func (ts *AdminTestSuite) TestAdminUsers_FilterBanned() {
	ts.seedFilterUsers()
	ts.assertListed("banned=true", "alice@example.com")
	ts.assertListed("banned=false", "bob@example.com", "carol@example.com")
}

func (ts *AdminTestSuite) TestAdminUsers_FilterConfirmed() {
	ts.seedFilterUsers()
	ts.assertListed("confirmed=true", "alice@example.com", "carol@example.com")
	ts.assertListed("confirmed=false", "bob@example.com")
}

func (ts *AdminTestSuite) TestAdminUsers_FilterProvider() {
	ts.seedFilterUsers()
	ts.assertListed("provider=github", "alice@example.com")
	ts.assertListed("provider=email", "bob@example.com")
	ts.assertListed("provider=google")
}

// The filters combine with each other and with the text search, and the count and the Link
// header follow the filtered list.
func (ts *AdminTestSuite) TestAdminUsers_FiltersCombine() {
	ts.seedFilterUsers()
	ts.assertListed("role=editor&confirmed=true&banned=false", "carol@example.com")
	ts.assertListed("filter=alice&role=editor", "alice@example.com")
	ts.assertListed("filter=bob&role=editor")

	w := ts.getAdminUsers("role=editor&per_page=1")
	assert.Equal(ts.T(), "2", w.Header().Get("X-Total-Count"))
	assert.Equal(ts.T(), `</admin/users?page=2&per_page=1&role=editor>; rel="next", </admin/users?page=2&per_page=1&role=editor>; rel="last"`, w.Header().Get("Link"))
}

func (ts *AdminTestSuite) TestAdminUsers_FilterBadBoolean() {
	for _, query := range []string{"banned=maybe", "confirmed=yes please"} {
		w := ts.getAdminUsers(strings.ReplaceAll(query, " ", "+"))
		require.Equal(ts.T(), http.StatusBadRequest, w.Code, query)
		data := map[string]interface{}{}
		require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
		assert.Equal(ts.T(), apierrors.ErrorCodeValidationFailed, data["error_code"], query)
	}
}

// TestAdminUserCreate tests API /admin/user route (POST)
func (ts *AdminTestSuite) TestAdminUserCreate() {
	cases := []struct {
		desc     string
		params   map[string]interface{}
		expected map[string]interface{}
	}{
		{
			desc: "Only phone",
			params: map[string]interface{}{
				"phone":    "123456789",
				"password": "test1",
			},
			expected: map[string]interface{}{
				"email":           "",
				"phone":           "123456789",
				"isAuthenticated": true,
				"provider":        "phone",
				"providers":       []string{"phone"},
				"password":        "test1",
			},
		},
		{
			desc: "With password",
			params: map[string]interface{}{
				"email":    "test1@example.com",
				"phone":    "123456789",
				"password": "test1",
			},
			expected: map[string]interface{}{
				"email":           "test1@example.com",
				"phone":           "123456789",
				"isAuthenticated": true,
				"provider":        "email",
				"providers":       []string{"email", "phone"},
				"password":        "test1",
			},
		},
		{
			desc: "Without password",
			params: map[string]interface{}{
				"email": "test2@example.com",
				"phone": "",
			},
			expected: map[string]interface{}{
				"email":           "test2@example.com",
				"phone":           "",
				"isAuthenticated": false,
				"provider":        "email",
				"providers":       []string{"email"},
			},
		},
		{
			desc: "With empty string password",
			params: map[string]interface{}{
				"email":    "test3@example.com",
				"phone":    "",
				"password": "",
			},
			expected: map[string]interface{}{
				"email":           "test3@example.com",
				"phone":           "",
				"isAuthenticated": false,
				"provider":        "email",
				"providers":       []string{"email"},
				"password":        "",
			},
		},
		{
			desc: "Ban created user",
			params: map[string]interface{}{
				"email":        "test4@example.com",
				"phone":        "",
				"password":     "test1",
				"ban_duration": "24h",
			},
			expected: map[string]interface{}{
				"email":           "test4@example.com",
				"phone":           "",
				"isAuthenticated": true,
				"provider":        "email",
				"providers":       []string{"email"},
				"password":        "test1",
			},
		},
		{
			desc: "With password hash",
			params: map[string]interface{}{
				"email":         "test5@example.com",
				"password_hash": "$2y$10$SXEz2HeT8PUIGQXo9yeUIem8KzNxgG0d7o/.eGj2rj8KbRgAuRVlq",
			},
			expected: map[string]interface{}{
				"email":           "test5@example.com",
				"phone":           "",
				"isAuthenticated": true,
				"provider":        "email",
				"providers":       []string{"email"},
				"password":        "test",
			},
		},
		{
			desc: "With custom id",
			params: map[string]interface{}{
				"id":       "fc56ab41-2010-4870-a9b9-767c1dc573fb",
				"email":    "test6@example.com",
				"password": "test",
			},
			expected: map[string]interface{}{
				"id":              "fc56ab41-2010-4870-a9b9-767c1dc573fb",
				"email":           "test6@example.com",
				"phone":           "",
				"isAuthenticated": true,
				"provider":        "email",
				"providers":       []string{"email"},
				"password":        "test",
			},
		},
	}

	for _, c := range cases {
		ts.Run(c.desc, func() {
			var buffer bytes.Buffer
			require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(c.params))

			// Setup request
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/admin/users", &buffer)

			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))
			ts.Config.External.Phone.Enabled = true

			ts.API.handler.ServeHTTP(w, req)
			require.Equal(ts.T(), http.StatusOK, w.Code)

			data := models.User{}
			require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
			assert.Equal(ts.T(), c.expected["email"], data.GetEmail())
			assert.Equal(ts.T(), c.expected["phone"], data.GetPhone())
			assert.Equal(ts.T(), c.expected["provider"], data.AppMetaData["provider"])
			assert.ElementsMatch(ts.T(), c.expected["providers"], data.AppMetaData["providers"])

			u, err := models.FindUserByID(ts.API.db, data.ID)
			require.NoError(ts.T(), err)

			// verify that the corresponding identities were created
			require.NotEmpty(ts.T(), u.Identities)
			for _, identity := range u.Identities {
				require.Equal(ts.T(), u.ID, identity.UserID)
				if identity.Provider == "email" {
					require.Equal(ts.T(), c.expected["email"], identity.IdentityData["email"])
				}
				if identity.Provider == "phone" {
					require.Equal(ts.T(), c.expected["phone"], identity.IdentityData["phone"])
				}
			}

			if _, ok := c.expected["password"]; ok {
				expectedPassword := fmt.Sprintf("%v", c.expected["password"])
				isAuthenticated, _, err := u.Authenticate(context.Background(), ts.API.db, expectedPassword, ts.API.config.Security.DBEncryption.DecryptionKeys, ts.API.config.Security.DBEncryption.Encrypt, ts.API.config.Security.DBEncryption.EncryptionKeyID)
				require.NoError(ts.T(), err)
				require.Equal(ts.T(), c.expected["isAuthenticated"], isAuthenticated)
			}

			if id, ok := c.expected["id"]; ok {
				uid, err := uuid.FromString(id.(string))
				require.NoError(ts.T(), err)
				require.Equal(ts.T(), uid, data.ID)
			}

			// remove created user after each case
			require.NoError(ts.T(), ts.API.db.Destroy(u))
		})
	}
}

// TestAdminUserGet tests API /admin/user route (GET)
func (ts *AdminTestSuite) TestAdminUserGet() {
	u, err := models.NewUser("12345678", "test1@example.com", "test", ts.Config.JWT.Aud, map[string]interface{}{"full_name": "Test Get User"})
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	// Setup request
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/users/%s", u.ID), nil)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	data := make(map[string]interface{})
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))

	assert.Equal(ts.T(), data["email"], "test1@example.com")
	assert.NotNil(ts.T(), data["app_metadata"])
	assert.NotNil(ts.T(), data["user_metadata"])
	md := data["user_metadata"].(map[string]interface{})
	assert.Len(ts.T(), md, 1)
	assert.Equal(ts.T(), "Test Get User", md["full_name"])
}

// TestAdminUserUpdate tests API /admin/user route (UPDATE)
func (ts *AdminTestSuite) TestAdminUserUpdate() {
	u, err := models.NewUser("12345678", "test1@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	var buffer bytes.Buffer
	newEmail := "test2@example.com"
	newPhone := "234567890"
	require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(map[string]interface{}{
		"role": "testing",
		"app_metadata": map[string]interface{}{
			"roles": []string{"writer", "editor"},
		},
		"user_metadata": map[string]interface{}{
			"name": "David",
		},
		"ban_duration": "24h",
		"email":        newEmail,
		"phone":        newPhone,
	}))

	// Setup request
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/admin/users/%s", u.ID), &buffer)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	data := models.User{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))

	assert.Equal(ts.T(), "testing", data.Role)
	assert.NotNil(ts.T(), data.UserMetaData)
	assert.Equal(ts.T(), "David", data.UserMetaData["name"])
	assert.Equal(ts.T(), newEmail, data.GetEmail())
	assert.Equal(ts.T(), newPhone, data.GetPhone())

	assert.NotNil(ts.T(), data.AppMetaData)
	assert.Len(ts.T(), data.AppMetaData["roles"], 2)
	assert.Contains(ts.T(), data.AppMetaData["roles"], "writer")
	assert.Contains(ts.T(), data.AppMetaData["roles"], "editor")
	assert.NotNil(ts.T(), data.BannedUntil)

	u, err = models.FindUserByID(ts.API.db, data.ID)
	require.NoError(ts.T(), err)

	// check if the corresponding identities were successfully created
	require.NotEmpty(ts.T(), u.Identities)

	for _, identity := range u.Identities {
		// for email & phone identities, the providerId is the same as the userId
		require.Equal(ts.T(), u.ID.String(), identity.ProviderID)
		require.Equal(ts.T(), u.ID, identity.UserID)
		if identity.Provider == "email" {
			require.Equal(ts.T(), newEmail, identity.IdentityData["email"])
		}
		if identity.Provider == "phone" {
			require.Equal(ts.T(), newPhone, identity.IdentityData["phone"])

		}
	}
}

func (ts *AdminTestSuite) TestAdminUserUpdatePasswordFailed() {
	u, err := models.NewUser("12345678", "test1@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	var updateEndpoint = fmt.Sprintf("/admin/users/%s", u.ID)
	ts.Config.Password.MinLength = 6
	ts.Run("Password doesn't meet minimum length", func() {
		var buffer bytes.Buffer
		require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(map[string]interface{}{
			"password": "",
		}))

		// Setup request
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, updateEndpoint, &buffer)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

		ts.API.handler.ServeHTTP(w, req)
		require.Equal(ts.T(), http.StatusUnprocessableEntity, w.Code)
	})
}

func (ts *AdminTestSuite) TestAdminUserUpdateBannedUntilFailed() {
	u, err := models.NewUser("", "test1@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	var updateEndpoint = fmt.Sprintf("/admin/users/%s", u.ID)
	ts.Config.Password.MinLength = 6
	ts.Run("Incorrect format for ban_duration", func() {
		var buffer bytes.Buffer
		require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(map[string]interface{}{
			"ban_duration": "24",
		}))

		// Setup request
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, updateEndpoint, &buffer)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

		ts.API.handler.ServeHTTP(w, req)
		require.Equal(ts.T(), http.StatusBadRequest, w.Code)
	})
}

// TestAdminUserDelete tests API /admin/users route (DELETE)
func (ts *AdminTestSuite) TestAdminUserDelete() {
	type expected struct {
		code int
		err  error
	}
	signupParams := &SignupParams{
		Email:    "test-delete@example.com",
		Password: "test",
		Data:     map[string]interface{}{"name": "test"},
		Provider: "email",
		Aud:      ts.Config.JWT.Aud,
	}
	cases := []struct {
		desc         string
		body         map[string]interface{}
		isSoftDelete string
		isSSOUser    bool
		expected     expected
	}{
		{
			desc:         "Test admin delete user (default)",
			isSoftDelete: "",
			isSSOUser:    false,
			expected:     expected{code: http.StatusOK, err: models.UserNotFoundError{}},
			body:         nil,
		},
		{
			desc:         "Test admin delete user (hard deletion)",
			isSoftDelete: "?is_soft_delete=false",
			isSSOUser:    false,
			expected:     expected{code: http.StatusOK, err: models.UserNotFoundError{}},
			body: map[string]interface{}{
				"should_soft_delete": false,
			},
		},
		{
			desc:         "Test admin delete user (soft deletion)",
			isSoftDelete: "?is_soft_delete=true",
			isSSOUser:    false,
			expected:     expected{code: http.StatusOK, err: models.UserNotFoundError{}},
			body: map[string]interface{}{
				"should_soft_delete": true,
			},
		},
		{
			desc:         "Test admin delete user (soft deletion & sso user)",
			isSoftDelete: "?is_soft_delete=true",
			isSSOUser:    true,
			expected:     expected{code: http.StatusOK, err: nil},
			body: map[string]interface{}{
				"should_soft_delete": true,
			},
		},
	}

	for _, c := range cases {
		ts.Run(c.desc, func() {
			var buffer bytes.Buffer
			require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(c.body))
			u, err := signupParams.ToUserModel(false /* <- isSSOUser */)
			require.NoError(ts.T(), err)
			u, err = ts.API.signupNewUser(ts.API.db, u)
			require.NoError(ts.T(), err)

			// Setup request
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/admin/users/%s", u.ID), &buffer)

			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

			ts.API.handler.ServeHTTP(w, req)
			require.Equal(ts.T(), c.expected.code, w.Code)

			if c.isSSOUser {
				u, err = models.FindUserByID(ts.API.db, u.ID)
				require.NotNil(ts.T(), u)
			} else {
				_, err = models.FindUserByEmailAndAudience(ts.API.db, signupParams.Email, ts.Config.JWT.Aud)
			}
			require.Equal(ts.T(), c.expected.err, err)
		})
	}
}

// studioTables are the engine's `_supatype` tables a user delete touches, as
// the engine creates them.
var studioTables = []struct{ name, ddl string }{
	{"_supatype.studio_members", `CREATE TABLE _supatype.studio_members (
		id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id          UUID UNIQUE,
		platform_user_id UUID UNIQUE,
		role             TEXT NOT NULL,
		created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		CONSTRAINT studio_members_one_identity
			CHECK (num_nonnulls(user_id, platform_user_id) = 1)
	)`},
	{"_supatype.studio_audit", `CREATE TABLE _supatype.studio_audit (
		id         BIGSERIAL PRIMARY KEY,
		actor_id   UUID,
		target_id  UUID,
		action     TEXT NOT NULL,
		role       TEXT,
		detail     JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`},
}

// withStudioMembersTable creates the engine's `_supatype.studio_members` and
// `_supatype.studio_audit` for the length of a test, each unless the database
// already has it.
func (ts *AdminTestSuite) withStudioMembersTable() {
	var schema struct {
		Present bool `db:"present"`
	}
	require.NoError(ts.T(), ts.API.db.RawQuery(
		`SELECT to_regnamespace('_supatype') IS NOT NULL AS present`).First(&schema))
	if !schema.Present {
		require.NoError(ts.T(), ts.API.db.RawQuery(`CREATE SCHEMA _supatype`).Exec())
		ts.T().Cleanup(func() {
			require.NoError(ts.T(), ts.API.db.RawQuery(`DROP SCHEMA _supatype`).Exec())
		})
	}
	for _, table := range studioTables {
		var row struct {
			Present bool `db:"present"`
		}
		require.NoError(ts.T(), ts.API.db.RawQuery(
			`SELECT to_regclass(?) IS NOT NULL AS present`, table.name).First(&row))
		if row.Present {
			continue
		}
		require.NoError(ts.T(), ts.API.db.RawQuery(table.ddl).Exec())
		name := table.name
		ts.T().Cleanup(func() {
			require.NoError(ts.T(), ts.API.db.RawQuery(`DROP TABLE `+name).Exec())
		})
	}
}

func (ts *AdminTestSuite) studioMemberCount(userID uuid.UUID) int {
	var row struct {
		N int `db:"n"`
	}
	require.NoError(ts.T(), ts.API.db.RawQuery(
		`SELECT count(*) AS n FROM _supatype.studio_members WHERE user_id = ?`, userID).First(&row))
	return row.N
}

// addStudioMember gives a new project user a Studio role and returns them.
func (ts *AdminTestSuite) addStudioMember(email, role string) *models.User {
	u, err := models.NewUser("", email, "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.API.db.Create(u))
	require.NoError(ts.T(), ts.API.db.RawQuery(
		`INSERT INTO _supatype.studio_members (user_id, role) VALUES (?, ?)`, u.ID, role).Exec())
	return u
}

// studioAuditFor returns the membership audit rows about `target`, each as
// "action:role".
func (ts *AdminTestSuite) studioAuditFor(target uuid.UUID) []string {
	var rows []struct {
		Entry string `db:"entry"`
	}
	require.NoError(ts.T(), ts.API.db.RawQuery(
		`SELECT action || ':' || COALESCE(role, '') AS entry
		   FROM _supatype.studio_audit WHERE target_id = ? ORDER BY id`, target).All(&rows))
	entries := make([]string, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, r.Entry)
	}
	return entries
}

func (ts *AdminTestSuite) deleteUserRequest(u *models.User, soft bool) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, ts.newDeleteUserRequest(u, soft, ts.token))
	return w
}

// newDeleteUserRequest builds DELETE /admin/users/{id} for `u`, sent with
// `token`.
func (ts *AdminTestSuite) newDeleteUserRequest(u *models.User, soft bool, token string) *http.Request {
	var buffer bytes.Buffer
	require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(map[string]interface{}{"should_soft_delete": soft}))
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/admin/users/%s", u.ID), &buffer)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	return req
}

// adminTokenFor signs an admin-role token whose `sub` is `sub`, as a signed-in
// admin's is.
func (ts *AdminTestSuite) adminTokenFor(sub string) string {
	claims := &AccessTokenClaims{Role: "supatype_admin"}
	claims.Subject = sub
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(ts.Config.JWT.Secret))
	require.NoError(ts.T(), err)
	return token
}

// studioAuditActorsFor returns the actor of each membership audit row about
// `target`, "" for a row with none.
func (ts *AdminTestSuite) studioAuditActorsFor(target uuid.UUID) []string {
	var rows []struct {
		Actor string `db:"actor"`
	}
	require.NoError(ts.T(), ts.API.db.RawQuery(
		`SELECT COALESCE(actor_id::text, '') AS actor
		   FROM _supatype.studio_audit WHERE target_id = ? ORDER BY id`, target).All(&rows))
	actors := make([]string, 0, len(rows))
	for _, r := range rows {
		actors = append(actors, r.Actor)
	}
	return actors
}

// holdStudioAdminRows locks every admin membership in a transaction of its own
// and returns its release, so concurrent deletes queue behind it.
func (ts *AdminTestSuite) holdStudioAdminRows() func() {
	blocker, err := ts.API.db.Connection.NewTransaction()
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), blocker.RawQuery(
		`SELECT 1 FROM _supatype.studio_members WHERE role = 'admin' FOR UPDATE`).Exec())
	return func() { _ = blocker.TX.Rollback() }
}

// waitForLockWaiters waits until `n` sessions in this database are blocked on
// a lock.
func (ts *AdminTestSuite) waitForLockWaiters(n int) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		var row struct {
			Waiting int `db:"waiting"`
		}
		require.NoError(ts.T(), ts.API.db.RawQuery(`
			SELECT count(*) AS waiting FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'`).First(&row))
		if row.Waiting >= n {
			return
		}
		require.False(ts.T(), time.Now().After(deadline),
			"only %d of %d deletes reached the lock", row.Waiting, n)
		time.Sleep(20 * time.Millisecond)
	}
}

// A deleted user kept its Studio membership, so the members list showed a
// grant for nobody, and a soft-deleted user's id still carried a role. The
// removal is audited in the members API's audit shape, so the trail says why
// the grant went.
func (ts *AdminTestSuite) TestAdminUserDeleteRemovesStudioMembership() {
	ts.withStudioMembersTable()
	ts.addStudioMember("other-admin@example.com", "admin")

	for _, soft := range []bool{false, true} {
		ts.Run(fmt.Sprintf("soft=%v", soft), func() {
			u := ts.addStudioMember(fmt.Sprintf("member-%v@example.com", soft), "admin")
			require.Equal(ts.T(), 1, ts.studioMemberCount(u.ID))

			w := ts.deleteUserRequest(u, soft)
			require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())

			assert.Equal(ts.T(), 0, ts.studioMemberCount(u.ID))
			assert.Equal(ts.T(), []string{"user_deleted:admin"}, ts.studioAuditFor(u.ID))
		})
	}
}

// Deleting the project's only Studio admin would leave nobody able to grant
// access, which the members API already refuses for a revoke. Deleting the
// user is the same revoke by another route, so it is refused the same way and
// leaves the user and the grant in place.
func (ts *AdminTestSuite) TestAdminUserDeleteRefusesTheLastStudioAdmin() {
	ts.withStudioMembersTable()
	editor := ts.addStudioMember("editor@example.com", "editor")
	admin := ts.addStudioMember("only-admin@example.com", "admin")

	for _, soft := range []bool{false, true} {
		ts.Run(fmt.Sprintf("soft=%v", soft), func() {
			w := ts.deleteUserRequest(admin, soft)
			require.Equal(ts.T(), http.StatusConflict, w.Code, w.Body.String())

			var body struct {
				ErrorCode string `json:"error_code"`
				Message   string `json:"msg"`
			}
			require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&body))
			assert.Equal(ts.T(), "conflict", body.ErrorCode)
			assert.Equal(ts.T(), fmt.Sprintf("cannot delete user %s: %s", admin.ID, studiomembers.ErrLastAdmin), body.Message)

			reloaded, err := models.FindUserByID(ts.API.db, admin.ID)
			require.NoError(ts.T(), err, "the refused delete leaves the user")
			assert.Nil(ts.T(), reloaded.DeletedAt, "and does not soft delete them either")
			assert.Equal(ts.T(), 1, ts.studioMemberCount(admin.ID))
			assert.Empty(ts.T(), ts.studioAuditFor(admin.ID))
		})
	}

	// A member who is not the last admin is still deleted.
	w := ts.deleteUserRequest(editor, false)
	require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
	assert.Equal(ts.T(), []string{"user_deleted:editor"}, ts.studioAuditFor(editor.ID))
}

// Two deletes of the last two Studio admins at once both read "two admins"
// unless the guard locks what it counts, and the project is left with nobody
// able to grant access. Both deletes are held at the admin rows until each has
// started, so neither commits before the other reaches the guard. Exactly one
// may win; the other sees it and is refused.
func (ts *AdminTestSuite) TestAdminUserDeleteConcurrentLastTwoStudioAdmins() {
	ts.withStudioMembersTable()
	first := ts.addStudioMember("first-admin@example.com", "admin")
	second := ts.addStudioMember("second-admin@example.com", "admin")

	requests := []*http.Request{
		ts.newDeleteUserRequest(first, false, ts.token),
		ts.newDeleteUserRequest(second, true, ts.token),
	}
	release := ts.holdStudioAdminRows()
	codes := make(chan int, len(requests))
	for _, req := range requests {
		go func() {
			w := httptest.NewRecorder()
			ts.API.handler.ServeHTTP(w, req)
			codes <- w.Code
		}()
	}
	ts.waitForLockWaiters(len(requests))
	release()

	got := []int{<-codes, <-codes}
	assert.ElementsMatch(ts.T(), []int{http.StatusOK, http.StatusConflict}, got)

	var row struct {
		Admins int `db:"admins"`
	}
	require.NoError(ts.T(), ts.API.db.RawQuery(
		`SELECT count(*) AS admins FROM _supatype.studio_members WHERE role = 'admin'`).First(&row))
	assert.Equal(ts.T(), 1, row.Admins, "one Studio admin is left")
}

// The membership a user delete removes is audited with the admin who asked for
// it, as the members API records its actor. A service-role call carries no
// user, so its row has no actor.
func (ts *AdminTestSuite) TestAdminUserDeleteAuditsTheCallingAdmin() {
	ts.withStudioMembersTable()
	caller := ts.addStudioMember("caller-admin@example.com", "admin")

	for _, soft := range []bool{false, true} {
		ts.Run(fmt.Sprintf("soft=%v", soft), func() {
			signedIn := ts.addStudioMember(fmt.Sprintf("by-admin-%v@example.com", soft), "editor")
			w := httptest.NewRecorder()
			ts.API.handler.ServeHTTP(w, ts.newDeleteUserRequest(signedIn, soft, ts.adminTokenFor(caller.ID.String())))
			require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
			assert.Equal(ts.T(), []string{caller.ID.String()}, ts.studioAuditActorsFor(signedIn.ID))

			byService := ts.addStudioMember(fmt.Sprintf("by-service-%v@example.com", soft), "editor")
			w = httptest.NewRecorder()
			ts.API.handler.ServeHTTP(w, ts.newDeleteUserRequest(byService, soft, ts.adminTokenFor("")))
			require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
			assert.Equal(ts.T(), []string{""}, ts.studioAuditActorsFor(byService.ID))
		})
	}
}

func (ts *AdminTestSuite) TestAdminUserSoftDeletion() {
	// create user
	u, err := models.NewUser("123456789", "test@example.com", "secret", ts.Config.JWT.Aud, map[string]interface{}{"name": "test"})
	require.NoError(ts.T(), err)
	u.ConfirmationToken = "some_token"
	u.RecoveryToken = "some_token"
	u.EmailChangeTokenCurrent = "some_token"
	u.EmailChangeTokenNew = "some_token"
	u.PhoneChangeToken = "some_token"
	u.AppMetaData = map[string]interface{}{
		"provider": "email",
	}
	require.NoError(ts.T(), ts.API.db.Create(u))
	require.NoError(ts.T(), models.CreateOneTimeToken(ts.API.db, u.ID, u.GetEmail(), u.ConfirmationToken, models.ConfirmationToken))
	require.NoError(ts.T(), models.CreateOneTimeToken(ts.API.db, u.ID, u.GetEmail(), u.RecoveryToken, models.RecoveryToken))
	require.NoError(ts.T(), models.CreateOneTimeToken(ts.API.db, u.ID, u.GetEmail(), u.EmailChangeTokenCurrent, models.EmailChangeTokenCurrent))
	require.NoError(ts.T(), models.CreateOneTimeToken(ts.API.db, u.ID, u.GetEmail(), u.EmailChangeTokenNew, models.EmailChangeTokenNew))
	require.NoError(ts.T(), models.CreateOneTimeToken(ts.API.db, u.ID, u.GetPhone(), u.PhoneChangeToken, models.PhoneChangeToken))

	// create user identities
	_, err = ts.API.createNewIdentity(ts.API.db, u, "email", map[string]interface{}{
		"sub":   "123456",
		"email": "test@example.com",
	})
	require.NoError(ts.T(), err)
	_, err = ts.API.createNewIdentity(ts.API.db, u, "github", map[string]interface{}{
		"sub":   "234567",
		"email": "test@example.com",
	})
	require.NoError(ts.T(), err)

	var buffer bytes.Buffer
	require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(map[string]interface{}{
		"should_soft_delete": true,
	}))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/admin/users/%s", u.ID), &buffer)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	// get soft-deleted user from db
	deletedUser, err := models.FindUserByID(ts.API.db, u.ID)
	require.NoError(ts.T(), err)

	require.Empty(ts.T(), deletedUser.ConfirmationToken)
	require.Empty(ts.T(), deletedUser.RecoveryToken)
	require.Empty(ts.T(), deletedUser.EmailChangeTokenCurrent)
	require.Empty(ts.T(), deletedUser.EmailChangeTokenNew)
	require.Empty(ts.T(), deletedUser.EncryptedPassword)
	require.Empty(ts.T(), deletedUser.PhoneChangeToken)
	require.Empty(ts.T(), deletedUser.UserMetaData)
	require.Empty(ts.T(), deletedUser.AppMetaData)
	require.NotEmpty(ts.T(), deletedUser.DeletedAt)
	require.NotEmpty(ts.T(), deletedUser.GetEmail())

	// get soft-deleted user's identity from db
	deletedIdentities, err := models.FindIdentitiesByUserID(ts.API.db, deletedUser.ID)
	require.NoError(ts.T(), err)

	for _, identity := range deletedIdentities {
		require.Empty(ts.T(), identity.IdentityData)
	}
}

func (ts *AdminTestSuite) TestAdminUserCreateWithDisabledLogin() {
	var cases = []struct {
		desc         string
		customConfig *conf.GlobalConfiguration
		userData     map[string]interface{}
		expected     int
	}{
		{
			desc: "Email Signups Disabled",
			customConfig: &conf.GlobalConfiguration{
				JWT: ts.Config.JWT,
				External: conf.ProviderConfiguration{
					Email: conf.EmailProviderConfiguration{
						Enabled: false,
					},
				},
			},
			userData: map[string]interface{}{
				"email":    "test1@example.com",
				"password": "test1",
			},
			expected: http.StatusOK,
		},
		{
			desc: "Phone Signups Disabled",
			customConfig: &conf.GlobalConfiguration{
				JWT: ts.Config.JWT,
				External: conf.ProviderConfiguration{
					Phone: conf.PhoneProviderConfiguration{
						Enabled: false,
					},
				},
			},
			userData: map[string]interface{}{
				"phone":    "123456789",
				"password": "test1",
			},
			expected: http.StatusOK,
		},
		{
			desc: "All Signups Disabled",
			customConfig: &conf.GlobalConfiguration{
				JWT:           ts.Config.JWT,
				DisableSignup: true,
			},
			userData: map[string]interface{}{
				"email":    "test2@example.com",
				"password": "test2",
			},
			expected: http.StatusOK,
		},
	}

	for _, c := range cases {
		ts.Run(c.desc, func() {
			// Initialize user data
			var buffer bytes.Buffer
			require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(c.userData))

			// Setup request
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/admin/users", &buffer)
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

			ts.Config.JWT = c.customConfig.JWT
			ts.Config.External = c.customConfig.External
			ts.API.handler.ServeHTTP(w, req)
			require.Equal(ts.T(), c.expected, w.Code)
		})
	}
}

// TestAdminUserDeleteFactor tests API /admin/users/<user_id>/factors/<factor_id>/
func (ts *AdminTestSuite) TestAdminUserDeleteFactor() {
	u, err := models.NewUser("123456789", "test-delete@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	f := models.NewTOTPFactor(u, "testSimpleName")
	require.NoError(ts.T(), f.UpdateStatus(ts.API.db, models.FactorStateVerified))
	require.NoError(ts.T(), f.SetSecret("secretkey", ts.Config.Security.DBEncryption.Encrypt, ts.Config.Security.DBEncryption.EncryptionKeyID, ts.Config.Security.DBEncryption.EncryptionKey))
	require.NoError(ts.T(), ts.API.db.Create(f), "Error saving new test factor")

	// Setup request
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/admin/users/%s/factors/%s/", u.ID, f.ID), nil)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)

	_, err = models.FindFactorByFactorID(ts.API.db, f.ID)
	require.EqualError(ts.T(), err, models.FactorNotFoundError{}.Error())

}

// TestAdminUserGetFactor tests API /admin/user/<user_id>/factors/
func (ts *AdminTestSuite) TestAdminUserGetFactors() {
	u, err := models.NewUser("123456789", "test-delete@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	f := models.NewTOTPFactor(u, "testSimpleName")
	require.NoError(ts.T(), f.SetSecret("secretkey", ts.Config.Security.DBEncryption.Encrypt, ts.Config.Security.DBEncryption.EncryptionKeyID, ts.Config.Security.DBEncryption.EncryptionKey))
	require.NoError(ts.T(), ts.API.db.Create(f), "Error saving new test factor")

	// Setup request
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/users/%s/factors/", u.ID), nil)

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))

	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code)
	getFactorsResp := []*models.Factor{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&getFactorsResp))
	require.Equal(ts.T(), getFactorsResp[0].Secret, "")
}

func (ts *AdminTestSuite) TestAdminUserUpdateFactor() {
	u, err := models.NewUser("123456789", "test-delete@example.com", "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err, "Error making new user")
	require.NoError(ts.T(), ts.API.db.Create(u), "Error creating user")

	f := models.NewPhoneFactor(u, "123456789", "testSimpleName")
	require.NoError(ts.T(), f.SetSecret("secretkey", ts.Config.Security.DBEncryption.Encrypt, ts.Config.Security.DBEncryption.EncryptionKeyID, ts.Config.Security.DBEncryption.EncryptionKey))
	require.NoError(ts.T(), ts.API.db.Create(f), "Error saving new test factor")

	var cases = []struct {
		Desc         string
		FactorData   map[string]interface{}
		ExpectedCode int
	}{
		{
			Desc: "Update Factor friendly name",
			FactorData: map[string]interface{}{
				"friendly_name": "john",
			},
			ExpectedCode: http.StatusOK,
		},
		{
			Desc: "Update Factor phone number",
			FactorData: map[string]interface{}{
				"phone": "+1976154321",
			},
			ExpectedCode: http.StatusOK,
		},
	}

	// Initialize factor data
	for _, c := range cases {
		ts.Run(c.Desc, func() {
			var buffer bytes.Buffer
			require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(c.FactorData))
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/admin/users/%s/factors/%s/", u.ID, f.ID), &buffer)
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))
			ts.API.handler.ServeHTTP(w, req)
			require.Equal(ts.T(), c.ExpectedCode, w.Code)
		})
	}
}

func (ts *AdminTestSuite) TestAdminUserCreateValidationErrors() {
	cases := []struct {
		desc   string
		params map[string]interface{}
	}{
		{
			desc: "create user without email and phone",
			params: map[string]interface{}{
				"password": "test_password",
			},
		},
		{
			desc: "create user with password and password hash",
			params: map[string]interface{}{
				"email":         "test@example.com",
				"password":      "test_password",
				"password_hash": "$2y$10$Tk6yEdmTbb/eQ/haDMaCsuCsmtPVprjHMcij1RqiJdLGPDXnL3L1a",
			},
		},
		{
			desc: "invalid ban duration",
			params: map[string]interface{}{
				"email":        "test@example.com",
				"ban_duration": "never",
			},
		},
		{
			desc: "custom id is nil",
			params: map[string]interface{}{
				"id":    "00000000-0000-0000-0000-000000000000",
				"email": "test@example.com",
			},
		},
		{
			desc: "bad id format",
			params: map[string]interface{}{
				"id":    "bad_uuid_format",
				"email": "test@example.com",
			},
		},
	}
	for _, c := range cases {
		ts.Run(c.desc, func() {
			var buffer bytes.Buffer
			require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(c.params))
			req := httptest.NewRequest(http.MethodPost, "/admin/users", &buffer)
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))
			w := httptest.NewRecorder()
			ts.API.handler.ServeHTTP(w, req)
			require.Equal(ts.T(), http.StatusBadRequest, w.Code, w)

			data := map[string]interface{}{}
			require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
			require.Equal(ts.T(), data["error_code"], apierrors.ErrorCodeValidationFailed)
		})

	}
}
