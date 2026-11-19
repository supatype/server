package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
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
	return ts.startSPLoginWith(map[string]interface{}{})
}

// startSPLoginWith is startSPLogin with extra /sso body fields, such as studio or redirect_to.
func (ts *SAMLACSTestSuite) startSPLoginWith(fields map[string]interface{}) (relayState, requestID string) {
	fields["provider_id"] = ts.idpProvider().ID.String()
	fields["skip_http_redirect"] = true
	body, err := json.Marshal(fields)
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

	// The resolver answers any artifact, but the library records each one it
	// resolves and refuses to resolve the same artifact twice, so every login
	// needs its own: a type code, an endpoint index, a source ID and a random
	// message handle.
	artifact := make([]byte, 44)
	copy(artifact, []byte{0x00, 0x04, 0x00, 0x00})
	_, err := rand.Read(artifact[24:])
	require.NoError(ts.T(), err)
	return ts.postACSForm(url.Values{"SAMLart": {base64.StdEncoding.EncodeToString(artifact)}, "RelayState": {relayState}})
}

// inMode runs the rest of the test with SUPATYPE_MODE set to mode, and the SAML
// dev policy an API started in that mode has.
func (ts *SAMLACSTestSuite) inMode(mode string) {
	previousMode, previousPolicy := ts.Config.Mode, ts.API.samlDev
	ts.Config.Mode = mode
	ts.API.samlDev = newSAMLDevPolicy(ts.Config)
	ts.T().Cleanup(func() {
		ts.Config.Mode = previousMode
		ts.API.samlDev = previousPolicy
	})
}

// The artifact is resolved by a message that carries the user's assertion, so
// outside dev mode the library must not send it to an http endpoint.
func (ts *SAMLACSTestSuite) TestArtifactOverPlainHTTPIsRefusedOutsideDev() {
	for _, mode := range []string{"", "production", "standalone"} {
		ts.Run("mode "+mode, func() {
			ts.inMode(mode)
			location := ts.artifactLogin(ts.Config.SAML.Certificate)
			require.Empty(ts.T(), ts.signedInAs(location), "signed in over http: %s", location)
		})
	}
}

// An IdP using the artifact binding may encrypt to either key while a
// rotation is in progress.
func (ts *SAMLACSTestSuite) TestArtifactAssertionEncryptedToEitherKeySignsIn() {
	// The test IdP's resolver listens on plain http, which only dev mode allows.
	ts.inMode("dev")
	next := ts.withNextKey()
	for name, certificate := range map[string]*x509.Certificate{"current key": ts.Config.SAML.Certificate, "next key": next} {
		ts.Run(name, func() {
			location := ts.artifactLogin(certificate)
			require.NotEmpty(ts.T(), ts.signedInAs(location), "refused: %s", location)
		})
	}
}
