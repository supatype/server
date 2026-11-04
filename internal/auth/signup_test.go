package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	mail "github.com/supatype/server/internal/auth/mailer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/conf"
)

type SignupTestSuite struct {
	suite.Suite
	API    *API
	Config *conf.GlobalConfiguration
}

func TestSignup(t *testing.T) {
	api, config, err := setupAPIForTest()
	require.NoError(t, err)

	ts := &SignupTestSuite{
		API:    api,
		Config: config,
	}
	defer api.db.Close()

	suite.Run(t, ts)
}

func (ts *SignupTestSuite) SetupTest() {
	models.TruncateAll(ts.API.db)
}

// TestSignup tests API /signup route
func (ts *SignupTestSuite) TestSignup() {
	// Request body
	var buffer bytes.Buffer
	require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(map[string]interface{}{
		"email":    "test@example.com",
		"password": "test123",
		"data": map[string]interface{}{
			"a": 1,
		},
	}))

	// Setup request
	req := httptest.NewRequest(http.MethodPost, "/signup", &buffer)
	req.Header.Set("Content-Type", "application/json")

	// Setup response recorder
	w := httptest.NewRecorder()

	ts.API.handler.ServeHTTP(w, req)

	require.Equal(ts.T(), http.StatusOK, w.Code)

	data := models.User{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
	assert.Equal(ts.T(), "test@example.com", data.GetEmail())
	assert.Equal(ts.T(), ts.Config.JWT.Aud, data.Aud)
	assert.Equal(ts.T(), 1.0, data.UserMetaData["a"])
	assert.Equal(ts.T(), "email", data.AppMetaData["provider"])
	assert.Equal(ts.T(), []interface{}{"email"}, data.AppMetaData["providers"])
}

// TestSignupTwice checks to make sure the same email cannot be registered twice
func (ts *SignupTestSuite) TestSignupTwice() {
	// Request body
	var buffer bytes.Buffer

	encode := func() {
		require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(map[string]interface{}{
			"email":    "test1@example.com",
			"password": "test123",
			"data": map[string]interface{}{
				"a": 1,
			},
		}))
	}

	encode()

	// Setup request
	req := httptest.NewRequest(http.MethodPost, "http://localhost/signup", &buffer)
	req.Header.Set("Content-Type", "application/json")

	// Setup response recorder
	w := httptest.NewRecorder()
	y := httptest.NewRecorder()

	ts.API.handler.ServeHTTP(y, req)
	u, err := models.FindUserByEmailAndAudience(ts.API.db, "test1@example.com", ts.Config.JWT.Aud)
	if err == nil {
		require.NoError(ts.T(), u.Confirm(ts.API.db))
	}

	encode()
	ts.API.handler.ServeHTTP(w, req)

	data := models.User{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))

	require.Equal(ts.T(), http.StatusOK, w.Code)

	assert.NotEqual(ts.T(), u.ID, data.ID)
	assert.Equal(ts.T(), "test1@example.com", data.GetEmail())
	assert.Equal(ts.T(), ts.Config.JWT.Aud, data.Aud)
	assert.Equal(ts.T(), 1.0, data.UserMetaData["a"])
	assert.Equal(ts.T(), "email", data.AppMetaData["provider"])
	assert.Equal(ts.T(), []interface{}{"email"}, data.AppMetaData["providers"])
}

func (ts *SignupTestSuite) TestVerifySignup() {
	user, err := models.NewUser("123456789", "test@example.com", "testing", ts.Config.JWT.Aud, nil)
	user.ConfirmationToken = "asdf3"
	now := time.Now()
	user.ConfirmationSentAt = &now
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), ts.API.db.Create(user))
	require.NoError(ts.T(), models.CreateOneTimeToken(ts.API.db, user.ID, user.GetEmail(), user.ConfirmationToken, models.ConfirmationToken))

	// Find test user
	u, err := models.FindUserByEmailAndAudience(ts.API.db, "test@example.com", ts.Config.JWT.Aud)
	require.NoError(ts.T(), err)

	// Setup request
	reqUrl := fmt.Sprintf("http://localhost/verify?type=%s&token=%s", mail.SignupVerification, u.ConfirmationToken)
	req := httptest.NewRequest(http.MethodGet, reqUrl, nil)

	// Setup response recorder
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	assert.Equal(ts.T(), http.StatusSeeOther, w.Code)

	urlVal, err := url.Parse(w.Result().Header.Get("Location"))
	require.NoError(ts.T(), err)
	v, err := url.ParseQuery(urlVal.Fragment)
	require.NoError(ts.T(), err)
	require.NotEmpty(ts.T(), v.Get("access_token"))
	require.NotEmpty(ts.T(), v.Get("expires_in"))
	require.NotEmpty(ts.T(), v.Get("refresh_token"))
}

// TestSignupRequestBodyTooLarge verifies that the MaxBytesReader middleware
// rejects oversized request bodies with a 413 error through the full handler chain.
func (ts *SignupTestSuite) TestSignupRequestBodyTooLarge() {
	// Create a body that exceeds the 1MB limit (1<<20 + 1 bytes)
	largeBody := make([]byte, (1<<20)+1)
	req := httptest.NewRequest(http.MethodPost, "/signup", bytes.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)

	require.Equal(ts.T(), http.StatusRequestEntityTooLarge, w.Code)

	var data map[string]interface{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
	require.Equal(ts.T(), "request_entity_too_large", data["error_code"])
}

// postSignup sends body to /signup through the full handler chain.
func (ts *SignupTestSuite) postSignup(body map[string]interface{}) *httptest.ResponseRecorder {
	var buffer bytes.Buffer
	require.NoError(ts.T(), json.NewEncoder(&buffer).Encode(body))
	req := httptest.NewRequest(http.MethodPost, "/signup", &buffer)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	return w
}

// A password with no email or phone is a broken signup form, not a request for
// an anonymous account. Whether anonymous sign-ins are on must not change that:
// with them on, the old routing created an anonymous user and dropped the
// password without a word.
func (ts *SignupTestSuite) TestSignupPasswordWithoutEmailOrPhone() {
	defer func(was bool) { ts.Config.External.AnonymousUsers.Enabled = was }(ts.Config.External.AnonymousUsers.Enabled)

	for _, anonymous := range []bool{false, true} {
		ts.Run(fmt.Sprintf("anonymous=%v", anonymous), func() {
			ts.Config.External.AnonymousUsers.Enabled = anonymous
			before, err := ts.API.db.Count(&models.User{})
			require.NoError(ts.T(), err)

			w := ts.postSignup(map[string]interface{}{"email": "", "password": "test-password"})

			after, err := ts.API.db.Count(&models.User{})
			require.NoError(ts.T(), err)
			assert.Equal(ts.T(), before, after, "no user may be created")

			require.Equal(ts.T(), http.StatusBadRequest, w.Code, w.Body.String())
			var data map[string]interface{}
			require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
			assert.Equal(ts.T(), "validation_failed", data["error_code"])
			assert.Equal(ts.T(), "Signup requires an email or phone number", data["msg"])
		})
	}
}

// An empty body is still an anonymous sign-in, so with anonymous sign-ins off it
// keeps the anonymous error rather than the validation one.
func (ts *SignupTestSuite) TestSignupEmptyBodyWithAnonymousDisabled() {
	defer func(was bool) { ts.Config.External.AnonymousUsers.Enabled = was }(ts.Config.External.AnonymousUsers.Enabled)
	ts.Config.External.AnonymousUsers.Enabled = false

	w := ts.postSignup(map[string]interface{}{})

	require.Equal(ts.T(), http.StatusUnprocessableEntity, w.Code, w.Body.String())
	var data map[string]interface{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
	assert.Equal(ts.T(), "anonymous_provider_disabled", data["error_code"])
}

// A phone number and a password with no email is a phone signup, and the new
// email-or-phone check must not catch it.
func (ts *SignupTestSuite) TestSignupWithPhoneOnly() {
	defer func(phone conf.PhoneProviderConfiguration, sms conf.SmsProviderConfiguration) {
		ts.Config.External.Phone = phone
		ts.Config.Sms = sms
	}(ts.Config.External.Phone, ts.Config.Sms)
	ts.Config.External.Phone.Enabled = true
	ts.Config.Sms.TestOTP = map[string]string{"1234567890": "000000"}
	ts.Config.Sms.Provider = "twilio"
	ts.Config.Sms.Twilio.AccountSid = "fake-sid"
	ts.Config.Sms.Twilio.AuthToken = "fake-token"
	ts.Config.Sms.Twilio.MessageServiceSid = "fake-message-service-sid"

	w := ts.postSignup(map[string]interface{}{"phone": "1234567890", "password": "test-password"})

	require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
	data := models.User{}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&data))
	assert.Equal(ts.T(), "1234567890", data.GetPhone())
	assert.False(ts.T(), data.IsAnonymous)
	assert.Equal(ts.T(), "phone", data.AppMetaData["provider"])
}
