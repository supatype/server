package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/conf"
)

func TestSettings_DefaultProviders(t *testing.T) {
	api, _, err := setupAPIForTest()
	require.NoError(t, err)

	// Setup request
	req := httptest.NewRequest(http.MethodGet, "http://localhost/settings", nil)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	api.handler.ServeHTTP(w, req)
	require.Equal(t, w.Code, http.StatusOK)
	resp := Settings{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	p := resp.ExternalProviders

	require.False(t, p.Phone)
	require.True(t, p.Email)
	require.True(t, p.Azure)
	require.True(t, p.Bitbucket)
	require.True(t, p.Discord)
	require.True(t, p.Facebook)
	require.True(t, p.Snapchat)
	require.True(t, p.Notion)
	require.True(t, p.Spotify)
	require.True(t, p.Slack)
	require.True(t, p.SlackOIDC)
	require.True(t, p.Google)
	require.True(t, p.Kakao)
	require.True(t, p.Keycloak)
	require.True(t, p.Linkedin)
	require.True(t, p.LinkedinOIDC)
	require.True(t, p.GitHub)
	require.True(t, p.GitLab)
	require.True(t, p.Twitch)
	require.True(t, p.WorkOS)
	require.True(t, p.Zoom)

}

func TestSettings_EmailDisabled(t *testing.T) {
	api, config, err := setupAPIForTest()
	require.NoError(t, err)

	config.External.Email.Enabled = false

	// Setup request
	req := httptest.NewRequest(http.MethodGet, "http://localhost/settings", nil)
	req.Header.Set("Content-Type", "application/json")

	ctx := context.Background()
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	api.handler.ServeHTTP(w, req)
	require.Equal(t, w.Code, http.StatusOK)
	resp := Settings{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	p := resp.ExternalProviders
	require.False(t, p.Email)
}

// settingsBody is what GET /settings answers for a server running cfg. It calls the handler
// directly: the endpoint reads nothing but the configuration, so no database is needed.
func settingsBody(t *testing.T, cfg *conf.GlobalConfiguration) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/settings", nil)
	w := httptest.NewRecorder()
	require.NoError(t, (&API{config: cfg}).Settings(w, req))
	require.Equal(t, http.StatusOK, w.Code)
	body := map[string]any{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	return body
}

func TestSettings_ReportsTheSettingsVersionItRuns(t *testing.T) {
	require.EqualValues(t, 7, settingsBody(t, &conf.GlobalConfiguration{AuthSettingsVersion: 7})["settings_version"])
}

func TestSettings_OmitsTheVersionWhenNothingStampsOne(t *testing.T) {
	_, present := settingsBody(t, &conf.GlobalConfiguration{})["settings_version"]
	require.False(t, present)
}

func TestSettings_CaptchaAbsentWhenDisabled(t *testing.T) {
	api, config, err := setupAPIForTest()
	require.NoError(t, err)

	config.Security.Captcha.Enabled = false
	config.Security.Captcha.SiteKey = "should-not-leak"

	w := httptest.NewRecorder()
	api.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/settings", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(w.Body).Decode(&raw))
	require.NotContains(t, raw, "captcha")
}

func TestSettings_CaptchaCarriesProviderAndSiteKeyWhenEnabled(t *testing.T) {
	api, config, err := setupAPIForTest()
	require.NoError(t, err)

	config.Security.Captcha.Enabled = true
	config.Security.Captcha.Provider = "turnstile"
	config.Security.Captcha.Secret = "never-published"
	config.Security.Captcha.SiteKey = "0x4AAAA-site"

	w := httptest.NewRecorder()
	api.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/settings", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.NotContains(t, body, "never-published")

	resp := Settings{}
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	require.NotNil(t, resp.Captcha)
	require.Equal(t, "turnstile", resp.Captcha.Provider)
	require.Equal(t, "0x4AAAA-site", resp.Captcha.SiteKey)
}
