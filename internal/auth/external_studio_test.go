package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/gofrs/uuid"
	"github.com/supatype/server/internal/auth/models"
)

// Studio signs in with the project's OAuth providers by adding studio=1 to /authorize. The server
// keeps that on the flow state and, on the callback, sends the browser to its own Studio address
// (the API's external URL plus /studio/), never to an address the request supplied.

// test.env sets SUPATYPE_API_EXTERNAL_URL to http://localhost:9999.
const studioReturn = "http://localhost:9999/studio/"

// studioAuthorize starts a GitHub sign-in with the given extra query and returns the state the
// server handed to the provider.
func studioAuthorize(ts *ExternalTestSuite, extra string) string {
	req := httptest.NewRequest(http.MethodGet, "http://localhost/authorize?provider=github"+extra, nil)
	req.Header.Set("Referer", "https://example.netlify.com/admin")
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	ts.Require().Equal(http.StatusFound, w.Code)
	u, err := url.Parse(w.Header().Get("Location"))
	ts.Require().NoError(err)
	ts.Empty(u.Query().Get("studio"), "the studio flag is the server's, not the provider's")
	return u.Query().Get("state")
}

// studioCallback answers the callback as the provider would and returns where the server sent the
// browser.
func studioCallback(ts *ExternalTestSuite, state, code string) (int, *url.URL) {
	v := url.Values{}
	v.Set("code", code)
	v.Set("state", state)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/callback?"+v.Encode(), nil)
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	u, err := url.Parse(w.Header().Get("Location"))
	ts.Require().NoError(err)
	return w.Code, u
}

// withoutQuery is the address a redirect lands on, without the code, tokens or error it carries.
func withoutQuery(u *url.URL) string {
	return u.Scheme + "://" + u.Host + u.Path
}

func (ts *ExternalTestSuite) TestStudioFlag_ImplicitReturnsToStudio() {
	server := setupGenericOAuthServer(ts, "authcode")
	defer server.Close()

	status, u := studioCallback(ts, studioAuthorize(ts, "&studio=1"), "authcode")

	ts.Require().Equal(http.StatusFound, status)
	ts.Equal(studioReturn, withoutQuery(u))
	fragment, err := url.ParseQuery(u.Fragment)
	ts.Require().NoError(err)
	ts.NotEmpty(fragment.Get("access_token"))
}

func (ts *ExternalTestSuite) TestStudioFlag_PKCEReturnsToStudioWithCode() {
	server := setupGenericOAuthServer(ts, "authcode")
	defer server.Close()
	challenge := "&code_challenge=" + "testtesttesttesttesttesttesttesttesttesttesttest" + "&code_challenge_method=s256"

	status, u := studioCallback(ts, studioAuthorize(ts, "&studio=1"+challenge), "authcode")

	ts.Require().Equal(http.StatusFound, status)
	ts.Equal(studioReturn, withoutQuery(u))
	ts.NotEmpty(u.Query().Get("code"))
	ts.Empty(u.Fragment)
}

func (ts *ExternalTestSuite) TestStudioFlag_AbsentLeavesTheRedirectAlone() {
	server := setupGenericOAuthServer(ts, "authcode")
	defer server.Close()

	for _, extra := range []string{"", "&studio=0", "&studio=false"} {
		status, u := studioCallback(ts, studioAuthorize(ts, extra), "authcode")

		ts.Require().Equal(http.StatusFound, status, extra)
		ts.Equal("https://example.netlify.com/admin", withoutQuery(u), extra)
	}
}

func (ts *ExternalTestSuite) TestStudioFlag_IsKeptOnTheFlowState() {
	state := studioAuthorize(ts, "&studio=1&redirect_to=http://localhost:3000/elsewhere")

	flowState, err := models.FindFlowStateByID(ts.API.db, state)
	ts.Require().NoError(err)
	ts.True(flowState.Studio)
	ts.Nil(flowState.Referrer, "a Studio sign-in keeps no request-supplied address")

	plain, err := models.FindFlowStateByID(ts.API.db, studioAuthorize(ts, ""))
	ts.Require().NoError(err)
	ts.False(plain.Studio)
}

func (ts *ExternalTestSuite) TestStudioFlag_RedirectToCannotOverrideStudio() {
	server := setupGenericOAuthServer(ts, "authcode")
	defer server.Close()

	for _, target := range []string{
		"http://localhost:3000/elsewhere",            // on the allow list
		"https://example.netlify.com/admin",          // the site URL's host
		"https://evil.example/studio/",               // somewhere else entirely
		"http://localhost:9999/studiox/",             // a lookalike of the Studio path
		"http://localhost:9999/studio/../admin",      // a traversal out of it
		"http://localhost:9999/studio/?next=//evil/", // the right path with a payload
	} {
		state := studioAuthorize(ts, "&studio=1&redirect_to="+url.QueryEscape(target))
		status, u := studioCallback(ts, state, "authcode")

		ts.Require().Equal(http.StatusFound, status, target)
		ts.Equal(studioReturn, withoutQuery(u), target)
		ts.Empty(u.Query().Get("next"), target)
	}
}

func (ts *ExternalTestSuite) TestStudioFlag_TamperedStateIsRejected() {
	server := setupGenericOAuthServer(ts, "authcode")
	defer server.Close()
	state := studioAuthorize(ts, "&studio=1")

	forged := uuid.Must(uuid.NewV4()).String()
	status, u := studioCallback(ts, forged, "authcode")
	ts.Equal(http.StatusSeeOther, status)
	ts.NotEqual(studioReturn, withoutQuery(u))
	ts.NotEmpty(u.Query().Get("error"))

	status, u = studioCallback(ts, state[:len(state)-1]+"x", "authcode")
	ts.Equal(http.StatusSeeOther, status)
	ts.NotEqual(studioReturn, withoutQuery(u))
}

func (ts *ExternalTestSuite) TestStudioFlag_ARefusalReturnsToStudio() {
	ts.Config.DisableSignup = true
	server := setupGenericOAuthServer(ts, "authcode")
	defer server.Close()

	status, u := studioCallback(ts, studioAuthorize(ts, "&studio=1"), "authcode")

	ts.Require().Equal(http.StatusFound, status)
	ts.Equal(studioReturn, withoutQuery(u))
	ts.Equal("access_denied", u.Query().Get("error"))
}
