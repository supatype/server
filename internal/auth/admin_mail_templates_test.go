package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/conf"
)

const (
	savedSubject      = "Persisted subject 42"
	savedHTML         = "<p>Persisted body 42 {{ .ConfirmationURL }}</p>"
	configuredSubject = "Configured subject"
	configuredHTML    = "<p>Configured body {{ .ConfirmationURL }}</p>"
)

// templateCall sends one admin template request and decodes the answer.
func templateCall(t *testing.T, api *API, method, body string, wantStatus int) mailTemplateResponse {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &AccessTokenClaims{Role: "supatype_admin"}).
		SignedString([]byte(api.config.JWT.Secret))
	require.NoError(t, err)

	req := httptest.NewRequest(method, "/admin/template/recovery", bytes.NewBufferString(body))
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.handler.ServeHTTP(w, req)
	require.Equal(t, wantStatus, w.Code, w.Body.String())

	var out mailTemplateResponse
	if wantStatus == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	}
	return out
}

func saveBody(subject, html string) string {
	return fmt.Sprintf(`{"subject":%q,"content":%q}`, subject, html)
}

// templateAPI is one server instance on the suite's database. configured, when
// true, sets a recovery template through configuration, as an operator would
// with SUPATYPE_MAILER_TEMPLATES_RECOVERY and SUPATYPE_MAILER_SUBJECTS_RECOVERY.
func templateAPI(t *testing.T, configured bool) *API {
	t.Helper()
	api, _, err := setupAPIForTestWithCallback(func(cfg *conf.GlobalConfiguration, _ *storage.Connection) {
		if cfg != nil && configured {
			cfg.Mailer.Subjects.Recovery = configuredSubject
			cfg.Mailer.Templates.Recovery = configuredHTML
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = api.db.Close() })
	return api
}

func freshTemplateAPI(t *testing.T, configured bool) *API {
	t.Helper()
	api := templateAPI(t, configured)
	require.NoError(t, models.TruncateAll(api.db))
	return api
}

// TestAdminTemplateEditSurvivesARestart locks what Studio's Email templates
// view promises: a saved template is the template. The edit used to live only
// in the running process's configuration, so the next start of the server, on
// the same database, served the default again and the operator's change was
// gone without a word.
func TestAdminTemplateEditSurvivesARestart(t *testing.T) {
	before := freshTemplateAPI(t, false)
	put := templateCall(t, before, http.MethodPut, saveBody(savedSubject, savedHTML), http.StatusOK)

	// A new API on the same database is what a restarted server is.
	after := templateAPI(t, false)
	got := templateCall(t, after, http.MethodGet, "", http.StatusOK)
	require.Equal(t, savedSubject, got.Subject)
	require.Equal(t, savedHTML, got.Content)
	require.True(t, got.Saved)
	require.NotNil(t, got.SavedAt)
	require.NotNil(t, put.SavedAt)
	require.True(t, put.SavedAt.Equal(*got.SavedAt), "save answered %v, the database holds %v", put.SavedAt, got.SavedAt)
}

// Two replicas on one database: an edit saved through one is what the other
// shows, and a reset through the other is what the first shows.
func TestAdminTemplateEditIsSharedByReplicas(t *testing.T) {
	a := freshTemplateAPI(t, false)
	b := templateAPI(t, false)
	require.False(t, templateCall(t, b, http.MethodGet, "", http.StatusOK).Saved)

	templateCall(t, a, http.MethodPut, saveBody(savedSubject, savedHTML), http.StatusOK)
	got := templateCall(t, b, http.MethodGet, "", http.StatusOK)
	require.Equal(t, savedSubject, got.Subject)
	require.True(t, got.Saved)

	templateCall(t, b, http.MethodDelete, "", http.StatusOK)
	require.False(t, templateCall(t, a, http.MethodGet, "", http.StatusOK).Saved)
}

// A saved edit wins over the configured template, and DELETE goes back to the
// configured one rather than to the built-in default.
func TestASavedTemplateWinsOverConfigurationUntilDeleted(t *testing.T) {
	api := freshTemplateAPI(t, true)

	got := templateCall(t, api, http.MethodGet, "", http.StatusOK)
	require.Equal(t, configuredSubject, got.Subject)
	require.Equal(t, configuredHTML, got.Content)
	require.False(t, got.Saved)
	require.Nil(t, got.SavedAt)

	got = templateCall(t, api, http.MethodPut, saveBody(savedSubject, savedHTML), http.StatusOK)
	require.True(t, got.Saved)
	got = templateCall(t, api, http.MethodGet, "", http.StatusOK)
	require.Equal(t, savedSubject, got.Subject)
	require.Equal(t, savedHTML, got.Content)

	got = templateCall(t, api, http.MethodDelete, "", http.StatusOK)
	require.Equal(t, configuredSubject, got.Subject)
	require.Equal(t, configuredHTML, got.Content)
	require.False(t, got.Saved)
}

// With nothing configured, DELETE goes back to the built-in default, and
// deleting when nothing is saved is not an error.
func TestDeletingGoesBackToTheBuiltInDefault(t *testing.T) {
	api := freshTemplateAPI(t, false)
	original := templateCall(t, api, http.MethodGet, "", http.StatusOK)

	templateCall(t, api, http.MethodPut, saveBody(savedSubject, savedHTML), http.StatusOK)
	got := templateCall(t, api, http.MethodDelete, "", http.StatusOK)
	require.Equal(t, original, got)
	require.Equal(t, original, templateCall(t, api, http.MethodDelete, "", http.StatusOK))
}

func TestAdminTemplatePutRefusesWhatCannotBeSent(t *testing.T) {
	api := freshTemplateAPI(t, false)
	templateCall(t, api, http.MethodPut, saveBody("Fine", "<p>{{ .Broken </p>"), http.StatusBadRequest)
	templateCall(t, api, http.MethodPut, saveBody("", savedHTML), http.StatusBadRequest)
	templateCall(t, api, http.MethodPut, saveBody(savedSubject, "  "), http.StatusBadRequest)
	require.False(t, templateCall(t, api, http.MethodGet, "", http.StatusOK).Saved)
}
