package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/auth/models"
)

// Studio's Users view lists the project's active sessions and revokes one.
// It used to read and delete auth.sessions through the SQL editor's route,
// which is for a different set of roles than user administration and cannot
// touch tables the auth service owns once the editor is not a superuser.

func (ts *AdminTestSuite) adminSessionsRequest(method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))
	ts.API.handler.ServeHTTP(w, req)
	return w
}

// sessionFor makes a session for a new user, expired when notAfter is in the past.
func (ts *AdminTestSuite) sessionFor(email string, notAfter *time.Time) *models.Session {
	u, err := models.NewUser("", email, "test", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.API.db.Create(u))
	s, err := models.NewSession(u.ID, nil)
	require.NoError(ts.T(), err)
	agent, ip := "test-agent", "203.0.113.7"
	s.UserAgent, s.IP, s.NotAfter = &agent, &ip, notAfter
	require.NoError(ts.T(), ts.API.db.Create(s))
	return s
}

func (ts *AdminTestSuite) TestAdminSessionsListsActiveSessions() {
	active := ts.sessionFor("active@example.com", nil)
	past := time.Now().Add(-time.Hour)
	ts.sessionFor("expired@example.com", &past)

	w := ts.adminSessionsRequest(http.MethodGet, "/admin/sessions")
	require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
	var got struct {
		Sessions []struct {
			ID        uuid.UUID `json:"id"`
			UserID    uuid.UUID `json:"user_id"`
			IP        string    `json:"ip"`
			UserAgent string    `json:"user_agent"`
		} `json:"sessions"`
	}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&got))
	require.Len(ts.T(), got.Sessions, 1)
	require.Equal(ts.T(), active.ID, got.Sessions[0].ID)
	require.Equal(ts.T(), active.UserID, got.Sessions[0].UserID)
	require.Equal(ts.T(), "203.0.113.7", got.Sessions[0].IP)
	require.Equal(ts.T(), "test-agent", got.Sessions[0].UserAgent)
}

func (ts *AdminTestSuite) TestAdminSessionRevoke() {
	s := ts.sessionFor("revoked@example.com", nil)

	w := ts.adminSessionsRequest(http.MethodDelete, "/admin/sessions/"+s.ID.String())
	require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
	_, err := models.FindSessionByID(ts.API.db, s.ID, false)
	require.True(ts.T(), models.IsNotFoundError(err), "the session must be gone")

	missing := ts.adminSessionsRequest(http.MethodDelete, "/admin/sessions/"+s.ID.String())
	require.Equal(ts.T(), http.StatusNotFound, missing.Code)
}

func (ts *AdminTestSuite) TestAdminSessionsNeedAdminCredentials() {
	req := httptest.NewRequest(http.MethodGet, "/admin/sessions", nil)
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusUnauthorized, w.Code)
}

// sessionForUser makes a session for an existing user, last used at `usedAt`.
func (ts *AdminTestSuite) sessionForUser(userID uuid.UUID, notAfter *time.Time, usedAt time.Time) *models.Session {
	s, err := models.NewSession(userID, nil)
	require.NoError(ts.T(), err)
	s.NotAfter = notAfter
	require.NoError(ts.T(), ts.API.db.Create(s))
	require.NoError(ts.T(), ts.API.db.RawQuery("UPDATE auth.sessions SET updated_at = ? WHERE id = ?", usedAt, s.ID).Exec())
	return s
}

// listedSessionIDs is the session ids a `{"sessions": [...]}` answer lists, in order.
func (ts *AdminTestSuite) listedSessionIDs(w *httptest.ResponseRecorder) []uuid.UUID {
	require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
	var got struct {
		Sessions []struct {
			ID     uuid.UUID `json:"id"`
			UserID uuid.UUID `json:"user_id"`
		} `json:"sessions"`
	}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&got))
	require.NotNil(ts.T(), got.Sessions, "sessions must be a list, not null")
	ids := make([]uuid.UUID, 0, len(got.Sessions))
	for _, s := range got.Sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

// A user's page in Studio lists only that user's sessions that have not run out, most recently
// used first.
func (ts *AdminTestSuite) TestAdminUserSessionsListsOnlyThatUsersActiveSessions() {
	other := ts.sessionFor("other@example.com", nil)
	mine := ts.sessionFor("mine@example.com", nil)
	now, past := time.Now(), time.Now().Add(-time.Hour)
	ts.sessionForUser(mine.UserID, &past, now)
	newest := ts.sessionForUser(mine.UserID, nil, now.Add(time.Minute))
	ts.execSQL("UPDATE auth.sessions SET updated_at = ? WHERE id IN (?, ?)", now.Add(-time.Minute), mine.ID, other.ID)

	w := ts.adminSessionsRequest(http.MethodGet, "/admin/users/"+mine.UserID.String()+"/sessions")
	require.Equal(ts.T(), []uuid.UUID{newest.ID, mine.ID}, ts.listedSessionIDs(w))

	u := ts.createFilterUser("nosessions@example.com", "", "")
	w = ts.adminSessionsRequest(http.MethodGet, "/admin/users/"+u.ID.String()+"/sessions")
	require.Empty(ts.T(), ts.listedSessionIDs(w))
}

// An unknown user is the user lookup's 404, not a missing route's.
func (ts *AdminTestSuite) TestAdminUserSessionsUnknownUser() {
	w := ts.adminSessionsRequest(http.MethodGet, "/admin/users/"+uuid.Must(uuid.NewV4()).String()+"/sessions")
	require.Equal(ts.T(), http.StatusNotFound, w.Code, w.Body.String())
	data := map[string]interface{}{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
	require.Equal(ts.T(), string(apierrors.ErrorCodeUserNotFound), data["error_code"])
}
