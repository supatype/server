package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"

	"github.com/beevik/etree"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/auth/models"
)

// Studio signs in with the project's SAML providers by sending studio: true to /sso (0.9.2 release
// test, N-FF3). As with studio=1 on /authorize, the ACS then sends the browser to the server's own
// Studio address, never to an address the request supplied.

// spResponse is the IdP's answer to the AuthnRequest requestID, signing the victim in.
func (ts *SAMLACSTestSuite) spResponse(requestID string, a samlTestAssertion) string {
	a.Subject = ts.subjectInResponseTo(victimNameID, requestID)
	response := ts.IdP.responseElement(ts.ACSURL, a.ID, ts.IdP.signedAssertion(ts.T(), a))
	response.CreateAttr("InResponseTo", requestID)
	doc := etree.NewDocument()
	doc.SetRoot(response)
	out, err := doc.WriteToBytes()
	require.NoError(ts.T(), err)
	return base64.StdEncoding.EncodeToString(out)
}

// spLogin starts an SP-initiated login with the extra /sso fields, answers it with a, and returns
// where the ACS sent the browser.
func (ts *SAMLACSTestSuite) spLogin(fields map[string]interface{}, a samlTestAssertion) *url.URL {
	relayState, requestID := ts.startSPLoginWith(fields)
	return ts.postACSForm(url.Values{"SAMLResponse": {ts.spResponse(requestID, a)}, "RelayState": {relayState}})
}

func (ts *SAMLACSTestSuite) TestStudioSSO_ImplicitReturnsToStudio() {
	location := ts.spLogin(map[string]interface{}{"studio": true}, ts.victimAssertion("studio-implicit"))

	ts.Equal(studioReturn, withoutQuery(location))
	ts.NotEmpty(ts.signedInAs(location))
}

func (ts *SAMLACSTestSuite) TestStudioSSO_PKCEReturnsToStudioWithCode() {
	fields := map[string]interface{}{
		"studio":                true,
		"code_challenge":        "testtesttesttesttesttesttesttesttesttesttesttest",
		"code_challenge_method": "s256",
	}
	location := ts.spLogin(fields, ts.victimAssertion("studio-pkce"))

	ts.Equal(studioReturn, withoutQuery(location))
	ts.NotEmpty(location.Query().Get("code"))
	ts.Empty(location.Fragment)
}

func (ts *SAMLACSTestSuite) TestStudioSSO_RedirectToCannotOverrideStudio() {
	for i, target := range []string{
		"http://localhost:3000/elsewhere",            // on the allow list
		"https://example.netlify.com/admin",          // the site URL's host
		"https://evil.example/studio/",               // somewhere else entirely
		"http://localhost:9999/studio/?next=//evil/", // the right path with a payload
	} {
		fields := map[string]interface{}{"studio": true, "redirect_to": target}
		location := ts.spLogin(fields, ts.victimAssertion("studio-redirect-"+strconv.Itoa(i)))

		ts.Equal(studioReturn, withoutQuery(location), target)
		ts.Empty(location.Query().Get("next"), target)
	}
}

func (ts *SAMLACSTestSuite) TestStudioSSO_AbsentLeavesTheRedirectAlone() {
	fields := map[string]interface{}{"studio": false, "redirect_to": "https://example.netlify.com/admin"}
	location := ts.spLogin(fields, ts.victimAssertion("studio-absent"))

	ts.Equal("https://example.netlify.com/admin", withoutQuery(location))
}

func (ts *SAMLACSTestSuite) TestStudioSSO_IsKeptOnTheFlowState() {
	relayState, _ := ts.startSPLoginWith(map[string]interface{}{"studio": true, "redirect_to": "http://localhost:3000/elsewhere"})

	stored, err := models.FindSAMLRelayStateByID(ts.API.db, uuid.FromStringOrNil(relayState))
	ts.Require().NoError(err)
	ts.Empty(stored.RedirectTo, "a Studio sign-in keeps no request-supplied address")
	ts.Require().NotNil(stored.FlowState)
	ts.True(stored.FlowState.Studio)
	ts.Nil(stored.FlowState.Referrer)
}

func (ts *SAMLACSTestSuite) TestStudioSSO_ARefusalReturnsToStudio() {
	refused := ts.victimAssertion("studio-refused")
	refused.Conditions = ""
	location := ts.spLogin(map[string]interface{}{"studio": true}, refused)

	ts.Equal(studioReturn, withoutQuery(location))
	ts.refused(location, refusedInvalid)

	plain := ts.spLogin(map[string]interface{}{}, refused)
	ts.Equal("https://example.netlify.com", withoutQuery(plain), "any other refusal still goes to the Site URL")
}

// withoutStudioAddress runs the rest of the test with an API external URL that gives no Studio
// address, while SAML keeps its own working external URL, so only the Studio part can fail.
func (ts *SAMLACSTestSuite) withoutStudioAddress() {
	api, saml := ts.Config.API.ExternalURL, ts.Config.SAML.ExternalURL
	ts.Config.SAML.ExternalURL = authMountURL(api)
	ts.Config.API.ExternalURL = "ftp://localhost:9999"
	ts.T().Cleanup(func() { ts.Config.API.ExternalURL, ts.Config.SAML.ExternalURL = api, saml })
}

func (ts *SAMLACSTestSuite) TestStudioSSO_RefusedWithoutAStudioAddress() {
	ts.withoutStudioAddress()
	post := func(fields map[string]interface{}) int {
		fields["provider_id"] = ts.idpProvider().ID.String()
		fields["skip_http_redirect"] = true
		body, err := json.Marshal(fields)
		ts.Require().NoError(err)
		w := httptest.NewRecorder()
		ts.API.handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://localhost/sso", bytes.NewBuffer(body)))
		return w.Code
	}

	ts.Equal(http.StatusInternalServerError, post(map[string]interface{}{"studio": true}))
	ts.Equal(http.StatusOK, post(map[string]interface{}{}), "any other SSO sign-in still starts")
}

func (ts *SAMLACSTestSuite) TestStudioSSO_FallsBackToTheSiteURLIfTheStudioAddressWentAway() {
	relayState, requestID := ts.startSPLoginWith(map[string]interface{}{"studio": true})
	ts.withoutStudioAddress()

	location := ts.postACSForm(url.Values{"SAMLResponse": {ts.spResponse(requestID, ts.victimAssertion("studio-gone"))}, "RelayState": {relayState}})

	ts.Equal("https://example.netlify.com", withoutQuery(location))
	ts.NotEmpty(ts.signedInAs(location))
}
