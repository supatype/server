package auth

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/beevik/etree"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/auth/models"
)

// artifactResolver serves the IdP's artifact resolution endpoint: it answers
// any ArtifactResolve with a signed ArtifactResponse carrying response.
func (ts *SAMLACSTestSuite) artifactResolver(response *etree.Element) string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(ts.T(), err)
		request := etree.NewDocument()
		require.NoError(ts.T(), request.ReadFromBytes(body))
		resolve := request.FindElement("//ArtifactResolve")
		require.NotNil(ts.T(), resolve, "no ArtifactResolve in %s", body)

		artifact := ts.IdP.protocolMessage("samlp:ArtifactResponse", "artifact-response-"+resolve.SelectAttrValue("ID", ""))
		artifact.CreateAttr("InResponseTo", resolve.SelectAttrValue("ID", ""))
		artifact.AddChild(response.Copy())

		envelope := etree.NewElement("soapenv:Envelope")
		envelope.CreateAttr("xmlns:soapenv", "http://schemas.xmlsoap.org/soap/envelope/")
		envelope.CreateElement("soapenv:Body").AddChild(ts.IdP.signEnveloped(ts.T(), artifact))
		doc := etree.NewDocument()
		doc.SetRoot(envelope)
		out, err := doc.WriteToBytes()
		require.NoError(ts.T(), err)

		w.Header().Set("Content-Type", "text/xml")
		_, err = w.Write(out)
		require.NoError(ts.T(), err)
	}))
	ts.T().Cleanup(server.Close)
	return server.URL
}

// idpProvider is the SSO provider registered for the test IdP.
func (ts *SAMLACSTestSuite) idpProvider() *models.SSOProvider {
	provider, err := models.FindSAMLProviderByEntityID(ts.API.db, ts.IdP.entityID)
	require.NoError(ts.T(), err)
	return provider
}

// startSPLogin starts an SP-initiated login with the test IdP, returning the
// RelayState and the ID of the AuthnRequest the response must answer.
func (ts *SAMLACSTestSuite) startSPLogin() (relayState, requestID string) {
	body, err := json.Marshal(struct {
		ProviderID       string `json:"provider_id"`
		SkipHTTPRedirect bool   `json:"skip_http_redirect"`
	}{ts.idpProvider().ID.String(), true})
	require.NoError(ts.T(), err)
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://localhost/sso", bytes.NewBuffer(body)))
	require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())

	var started struct {
		URL string `json:"url"`
	}
	require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&started))
	redirect, err := url.Parse(started.URL)
	require.NoError(ts.T(), err)
	relayState = redirect.Query().Get("RelayState")
	stored, err := models.FindSAMLRelayStateByID(ts.API.db, uuid.FromStringOrNil(relayState))
	require.NoError(ts.T(), err)
	return relayState, stored.RequestID
}

// artifactLogin runs an SP-initiated login whose response comes back by the
// artifact binding with the assertion encrypted to certificate, and returns
// the ACS redirect.
func (ts *SAMLACSTestSuite) artifactLogin(certificate *x509.Certificate) *url.URL {
	relayState, requestID := ts.startSPLogin()

	a := ts.victimAssertion("artifact-" + requestID)
	a.Subject = ts.subjectInResponseTo(victimNameID, requestID)
	response := ts.IdP.responseElement(ts.ACSURL, a.ID, ts.IdP.encryptedAssertion(ts.T(), a, certificate))
	response.CreateAttr("InResponseTo", requestID)

	provider := ts.idpProvider()
	provider.SAMLProvider.MetadataXML = ts.IdP.metadataXMLWithArtifactResolver(ts.artifactResolver(response))
	require.NoError(ts.T(), ts.API.db.UpdateColumns(&provider.SAMLProvider, "metadata_xml"))

	// The artifact's value does not matter: the resolver answers any.
	return ts.postACSForm(url.Values{"SAMLart": {"AAQAAMFbLinkpkf0SCP5a4cX0KD3NOhGeTd69x5JM5lW7JnSDNhSMEzwnS8="}, "RelayState": {relayState}})
}

// An IdP using the artifact binding may encrypt to either key while a
// rotation is in progress.
func (ts *SAMLACSTestSuite) TestArtifactAssertionEncryptedToEitherKeySignsIn() {
	next := ts.withNextKey()
	for name, certificate := range map[string]*x509.Certificate{"current key": ts.Config.SAML.Certificate, "next key": next} {
		ts.Run(name, func() {
			location := ts.artifactLogin(certificate)
			require.NotEmpty(ts.T(), ts.signedInAs(location), "refused: %s", location)
		})
	}
}
