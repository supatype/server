package templatemailer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/conf"
)

// captureClient keeps the last email it was asked to send.
type captureClient struct {
	to, subject, body string
}

func (c *captureClient) Mail(_ context.Context, to, subject, body string, _ map[string][]string, _ string) error {
	c.to, c.subject, c.body = to, subject, body
	return nil
}

func sendSSOLinked(t *testing.T, provider string) *captureClient {
	t.Helper()
	client := &captureClient{}
	m := New(&conf.GlobalConfiguration{}, client, NewCache())
	user := &models.User{Email: "member@example.com"}
	r := httptest.NewRequest(http.MethodPost, "http://localhost/sso/saml/acs", nil)
	require.NoError(t, m.SSOLinkedNotificationMail(r, user, provider))
	return client
}

// 0.9.1 release test (RB1): the first SSO link removes every other way in, so
// the account's address is told plainly what changed and who to ask.
func TestSSOLinkedNotificationDefaultTemplate(t *testing.T) {
	sent := sendSSOLinked(t, "example.com")
	require.Equal(t, "member@example.com", sent.to)
	require.Equal(t, "Single sign-on is now how you sign in", sent.subject)
	require.Contains(t, sent.body, "The account for member@example.com now signs in with your company's single sign-on (example.com).")
	require.Contains(t, sent.body, "Its password, two-factor methods, passkeys and other sign-in methods were removed.")
	require.Contains(t, sent.body, "If you did not expect this, contact your administrator.")
	require.NotContains(t, sent.body, "\u2014")
	require.NotContains(t, sent.subject, "\u2014")
}

// Without a provider name the sentence leaves the brackets out rather than
// showing them empty.
func TestSSOLinkedNotificationWithoutProviderName(t *testing.T) {
	sent := sendSSOLinked(t, "")
	require.Contains(t, sent.body, "now signs in with your company's single sign-on.")
	require.NotContains(t, sent.body, "()")
}
