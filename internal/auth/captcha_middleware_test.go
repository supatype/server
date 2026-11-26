package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/security"
)

// captchaTestAPI builds an API that needs no database: with no bearer token the
// admin bypass never reaches storage.
func captchaTestAPI() *API {
	cfg := &conf.GlobalConfiguration{}
	cfg.Security.Captcha = conf.CaptchaConfiguration{
		Enabled:  true,
		Provider: "turnstile",
		Secret:   TurnstileCaptchaSecret,
	}
	return &API{config: cfg}
}

func captchaRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://localhost/signup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestVerifyCaptchaMissingTokenIsClientError(t *testing.T) {
	bodies := map[string]string{
		"no meta block":   `{"email":"a@example.com","password":"secret"}`,
		"empty token":     `{"supatype_meta_security":{"captcha_token":""}}`,
		"whitespace only": `{"supatype_meta_security":{"captcha_token":"   "}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			_, err := captchaTestAPI().verifyCaptcha(httptest.NewRecorder(), captchaRequest(body))

			var httpErr *apierrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			require.Equal(t, http.StatusBadRequest, httpErr.HTTPStatus)
			require.Equal(t, apierrors.ErrorCodeCaptchaFailed, httpErr.ErrorCode)
		})
	}
}

func TestVerifyCaptchaProviderOutageIsServerError(t *testing.T) {
	orig := security.Client
	t.Cleanup(func() { security.Client = orig })
	security.Client = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: provider unreachable")
		}),
	}

	_, err := captchaTestAPI().verifyCaptcha(httptest.NewRecorder(),
		captchaRequest(`{"supatype_meta_security":{"captcha_token":"`+CaptchaResponse+`"}}`))

	var httpErr *apierrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusInternalServerError, httpErr.HTTPStatus)
}
