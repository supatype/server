package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	jwt "github.com/golang-jwt/jwt/v5"
	dsig "github.com/russellhaering/goxmldsig"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/supatype/saml"
	"github.com/supatype/saml/samlsp"
	"github.com/supatype/saml/xmlenc"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/conf"
)

// samlTestIdP is an identity provider whose signing key the test holds, so it
// can sign assertions the server's SAML connection trusts.
type samlTestIdP struct {
	entityID string
	certB64  string
	signer   *dsig.SigningContext
}

func newSAMLTestIdP(t *testing.T, entityID string) *samlTestIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "idp.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	signer := dsig.NewDefaultSigningContext(dsig.TLSCertKeyStore(tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}))
	// Exclusive canonicalization, as IdPs use, so the signature still holds
	// once the assertion sits inside a Response declaring other namespaces.
	signer.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")

	return &samlTestIdP{entityID: entityID, certB64: base64.StdEncoding.EncodeToString(der), signer: signer}
}

func (idp *samlTestIdP) metadataXML() string {
	return fmt.Sprintf(`<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="%[1]s">
  <md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <md:KeyDescriptor use="signing">
      <ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#"><ds:X509Data><ds:X509Certificate>%[2]s</ds:X509Certificate></ds:X509Data></ds:KeyInfo>
    </md:KeyDescriptor>
    <md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location="%[1]s/sso"/>
  </md:IDPSSODescriptor>
</md:EntityDescriptor>`, idp.entityID, idp.certB64)
}

// samlTestAssertion holds the parts of an assertion a test varies, as raw XML
// in the saml prefix.
type samlTestAssertion struct {
	ID         string
	Subject    string
	Conditions string
	Statements string
}

// response signs the assertion and wraps it in an unsigned Response addressed
// to the service provider's ACS URL, base64 encoded as the HTTP-POST binding
// sends it.
func (idp *samlTestIdP) response(t *testing.T, acsURL string, a samlTestAssertion) string {
	t.Helper()
	return idp.wrapResponse(t, acsURL, a.ID, idp.signedAssertion(t, a))
}

// encryptedResponse is response with the signed assertion encrypted to
// certificate, as an IdP does when the SP publishes an encryption key.
func (idp *samlTestIdP) encryptedResponse(t *testing.T, acsURL string, a samlTestAssertion, certificate *x509.Certificate) string {
	t.Helper()
	assertion := etree.NewDocument()
	assertion.SetRoot(idp.signedAssertion(t, a))
	plaintext, err := assertion.WriteToBytes()
	require.NoError(t, err)

	encryptedData, err := xmlenc.OAEP().Encrypt(certificate, plaintext, nil)
	require.NoError(t, err)
	encryptedData.CreateAttr("Type", "http://www.w3.org/2001/04/xmlenc#Element")

	encryptedAssertion := etree.NewElement("saml:EncryptedAssertion")
	encryptedAssertion.AddChild(encryptedData)
	return idp.wrapResponse(t, acsURL, a.ID, encryptedAssertion)
}

// signedAssertion is the assertion a describes, signed by the IdP.
func (idp *samlTestIdP) signedAssertion(t *testing.T, a samlTestAssertion) *etree.Element {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	doc := etree.NewDocument()
	require.NoError(t, doc.ReadFromString(fmt.Sprintf(
		`<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="%s" Version="2.0" IssueInstant="%s"><saml:Issuer>%s</saml:Issuer>%s%s%s</saml:Assertion>`,
		a.ID, now, idp.entityID, a.Subject, a.Conditions, a.Statements)))

	signed, err := idp.signer.SignEnveloped(doc.Root())
	require.NoError(t, err)
	// SignEnveloped appends the Signature; the schema places it after Issuer.
	sig := signed.RemoveChildAt(len(signed.Child) - 1)
	signed.InsertChildAt(1, sig)
	return signed
}

// wrapResponse puts assertion in an unsigned Response addressed to acsURL,
// base64 encoded as the HTTP-POST binding sends it.
func (idp *samlTestIdP) wrapResponse(t *testing.T, acsURL, id string, assertion *etree.Element) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	resp := etree.NewElement("samlp:Response")
	resp.CreateAttr("xmlns:samlp", "urn:oasis:names:tc:SAML:2.0:protocol")
	resp.CreateAttr("xmlns:saml", "urn:oasis:names:tc:SAML:2.0:assertion")
	resp.CreateAttr("ID", "response-"+id)
	resp.CreateAttr("Version", "2.0")
	resp.CreateAttr("IssueInstant", now)
	resp.CreateAttr("Destination", acsURL)
	resp.CreateElement("saml:Issuer").SetText(idp.entityID)
	resp.CreateElement("samlp:Status").CreateElement("samlp:StatusCode").
		CreateAttr("Value", "urn:oasis:names:tc:SAML:2.0:status:Success")
	resp.AddChild(assertion)

	out := etree.NewDocument()
	out.SetRoot(resp)
	xml, err := out.WriteToBytes()
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(xml)
}

type SAMLACSTestSuite struct {
	suite.Suite
	API    *API
	Config *conf.GlobalConfiguration
	IdP    *samlTestIdP
	ACSURL string
	// Audience is the service provider entity ID assertions are issued to.
	Audience string
}

func TestSAMLACS(t *testing.T) {
	api, config, err := setupAPIForTest()
	require.NoError(t, err)
	defer api.db.Close()

	if config.SAML.Enabled {
		suite.Run(t, &SAMLACSTestSuite{API: api, Config: config})
	}
}

// SetupSuite generates the IdP's key once, since that takes a noticeable moment.
func (ts *SAMLACSTestSuite) SetupSuite() {
	sp := ts.API.getSAMLServiceProvider(nil, true)
	ts.ACSURL = sp.AcsURL.String()
	ts.Audience = sp.MetadataURL.String()
	ts.IdP = newSAMLTestIdP(ts.T(), "https://idp.example.com/saml")
}

func (ts *SAMLACSTestSuite) SetupTest() {
	require.NoError(ts.T(), models.TruncateAll(ts.API.db))

	body, err := json.Marshal(map[string]string{"type": "saml", "metadata_xml": ts.IdP.metadataXML()})
	require.NoError(ts.T(), err)
	req := httptest.NewRequest(http.MethodPost, "http://localhost/admin/sso/providers", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+adminToken(ts.T(), ts.Config))
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusCreated, w.Code, w.Body.String())
}

const victimNameID = `<saml:NameID Format="urn:oasis:names:tc:SAML:2.0:nameid-format:persistent">victim-id</saml:NameID>`

func samlTime(offset time.Duration) string {
	return time.Now().UTC().Add(offset).Format(time.RFC3339)
}

// subject wraps nameID, which may be empty, with a bearer confirmation for
// the ACS.
func (ts *SAMLACSTestSuite) subject(nameID string) string {
	return fmt.Sprintf(`<saml:Subject>%s<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"><saml:SubjectConfirmationData NotOnOrAfter="%s" Recipient="%s"/></saml:SubjectConfirmation></saml:Subject>`,
		nameID, samlTime(5*time.Minute), ts.ACSURL)
}

// conditions is a currently valid Conditions element holding inner.
func conditions(inner string) string {
	return fmt.Sprintf(`<saml:Conditions NotBefore="%s" NotOnOrAfter="%s">%s</saml:Conditions>`,
		samlTime(-time.Minute), samlTime(5*time.Minute), inner)
}

func (ts *SAMLACSTestSuite) audienceRestriction() string {
	return `<saml:AudienceRestriction><saml:Audience>` + ts.Audience + `</saml:Audience></saml:AudienceRestriction>`
}

func emailStatement(email string) string {
	return fmt.Sprintf(`<saml:AttributeStatement><saml:Attribute Name="email"><saml:AttributeValue>%s</saml:AttributeValue></saml:Attribute></saml:AttributeStatement>`, email)
}

// victimAssertion is a correct assertion for the user "victim-id".
func (ts *SAMLACSTestSuite) victimAssertion(id string) samlTestAssertion {
	return samlTestAssertion{
		ID:         id,
		Subject:    ts.subject(victimNameID),
		Conditions: conditions(ts.audienceRestriction()),
		Statements: emailStatement("victim@example.com"),
	}
}

// acsRequest is the HTTP-POST binding's form carrying samlResponse to target.
func acsRequest(target, samlResponse string) *http.Request {
	form := url.Values{"SAMLResponse": {samlResponse}}
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// postACS posts an IdP-initiated response and returns the redirect Location.
func (ts *SAMLACSTestSuite) postACS(samlResponse string) *url.URL {
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, acsRequest("http://localhost/sso/saml/acs", samlResponse))

	require.Contains(ts.T(), []int{http.StatusFound, http.StatusSeeOther}, w.Code,
		"the ACS answers with a redirect, not a crash: %d %s", w.Code, w.Body.String())
	location, err := url.Parse(w.Header().Get("Location"))
	require.NoError(ts.T(), err)
	return location
}

// signedInAs returns the user ID the redirect signs the browser in as, or ""
// when the redirect carries no session.
func (ts *SAMLACSTestSuite) signedInAs(location *url.URL) string {
	fragment, err := url.ParseQuery(location.Fragment)
	require.NoError(ts.T(), err)
	accessToken := fragment.Get("access_token")
	if accessToken == "" {
		return ""
	}
	claims := &AccessTokenClaims{}
	_, _, err = jwt.NewParser().ParseUnverified(accessToken, claims)
	require.NoError(ts.T(), err)
	return claims.Subject
}

const (
	refusedInvalid = "SAML Assertion is not valid"
	refusedNoUser  = "SAML Assertion did not contain a persistent Subject Identifier attribute or Subject NameID uniquely identifying this user"
)

// refused checks the redirect signs no one in and reports exactly
// description, so nothing of the response is echoed into the URL.
func (ts *SAMLACSTestSuite) refused(location *url.URL, description string) {
	ts.T().Helper()
	require.Empty(ts.T(), ts.signedInAs(location), "signed in, but the assertion should be refused: %s", location)
	require.Equal(ts.T(), description, location.Query().Get("error_description"))
}

// libraryError parses the response as the ACS does and returns the reason the
// SAML library refuses it. The ACS logs that reason but does not send it to
// the browser.
func (ts *SAMLACSTestSuite) libraryError(samlResponse string) string {
	metadata, err := samlsp.ParseMetadata([]byte(ts.IdP.metadataXML()))
	require.NoError(ts.T(), err)
	sp := ts.API.getSAMLServiceProvider(metadata, true)

	req := acsRequest(ts.ACSURL, samlResponse)
	// ParseResponse reads PostForm, which the ACS has already parsed by now.
	require.NoError(ts.T(), req.ParseForm())
	_, err = sp.ParseResponse(req, nil)

	var ire *saml.InvalidResponseError
	require.ErrorAs(ts.T(), err, &ire)
	return ire.PrivateErr.Error()
}

// refusedByLibrary posts a, checks it is refused, and checks the SAML library
// refused it for reason rather than for, say, a bad signature.
func (ts *SAMLACSTestSuite) refusedByLibrary(a samlTestAssertion, reason string) {
	ts.T().Helper()
	response := ts.IdP.response(ts.T(), ts.ACSURL, a)
	ts.refused(ts.postACS(response), refusedInvalid)
	require.Equal(ts.T(), reason, ts.libraryError(response))
}

// signInVictim signs the victim in once with a correct assertion, so the
// account an attacker would reach exists.
func (ts *SAMLACSTestSuite) signInVictim() {
	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.victimAssertion("victim-login")))
	require.NotEmpty(ts.T(), ts.signedInAs(location), "the victim's own login failed: %s", location)
}

func (ts *SAMLACSTestSuite) TestCorrectAssertionSignsIn() {
	ts.signInVictim()
}

// GO-2026-6909: a NameID in another namespace is not the subject's NameID.
func (ts *SAMLACSTestSuite) TestForeignNamespaceNameIDIsRefused() {
	ts.signInVictim()

	a := ts.victimAssertion("attack-nameid")
	a.Subject = ts.subject(`<x:NameID xmlns:x="urn:example:foreign" Format="urn:oasis:names:tc:SAML:2.0:nameid-format:persistent">victim-id</x:NameID>`)
	ts.refused(ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, a)), refusedNoUser)
}

// The same flaw through the subject-id attribute, which the server prefers to
// NameID.
func (ts *SAMLACSTestSuite) TestForeignNamespaceSubjectIDAttributeIsRefused() {
	ts.signInVictim()

	a := ts.victimAssertion("attack-subject-id")
	a.Subject = ts.subject(`<saml:NameID Format="urn:oasis:names:tc:SAML:2.0:nameid-format:transient">one-off</saml:NameID>`)
	a.Statements = emailStatement("victim@example.com") +
		`<x:AttributeStatement xmlns:x="urn:example:foreign"><x:Attribute Name="urn:oasis:names:tc:SAML:attribute:subject-id"><x:AttributeValue>victim-id</x:AttributeValue></x:Attribute></x:AttributeStatement>`
	ts.refused(ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, a)), refusedNoUser)
}

func (ts *SAMLACSTestSuite) TestAssertionWithoutSubjectIsRefused() {
	a := ts.victimAssertion("no-subject")
	a.Subject = ""
	ts.refusedByLibrary(a, "assertion has no Subject")
}

func (ts *SAMLACSTestSuite) TestAssertionWithoutConditionsIsRefused() {
	a := ts.victimAssertion("no-conditions")
	a.Conditions = ""
	ts.refusedByLibrary(a, "assertion has no Conditions")
}

// Without a SubjectConfirmation the Recipient and expiry checks never run.
func (ts *SAMLACSTestSuite) TestAssertionWithoutSubjectConfirmationIsRefused() {
	a := ts.victimAssertion("no-confirmation")
	a.Subject = `<saml:Subject>` + victimNameID + `</saml:Subject>`
	ts.refusedByLibrary(a, "assertion has no bearer SubjectConfirmation")
}

// Without an AudienceRestriction the assertion would be valid at any service
// provider trusting this IdP.
func (ts *SAMLACSTestSuite) TestAssertionWithoutAudienceRestrictionIsRefused() {
	a := ts.victimAssertion("no-audience")
	a.Conditions = conditions("")
	ts.refusedByLibrary(a, fmt.Sprintf("assertion Conditions has no AudienceRestriction for %q", ts.Audience))
}

// A captured IdP-initiated response cannot be posted a second time.
func (ts *SAMLACSTestSuite) TestReplayedAssertionIsRefused() {
	response := ts.IdP.response(ts.T(), ts.ACSURL, ts.victimAssertion("replayed"))
	require.NotEmpty(ts.T(), ts.signedInAs(ts.postACS(response)))

	ts.refused(ts.postACS(response), "SAML Assertion has already been used")
}

// An assertion identified by subject-id with no NameID and no email attribute
// is refused for the missing email, not with a crash.
func (ts *SAMLACSTestSuite) TestAssertionWithoutNameIDOrEmailIsRefused() {
	a := ts.victimAssertion("no-nameid-no-email")
	a.Subject = ts.subject("")
	a.Statements = `<saml:AttributeStatement><saml:Attribute Name="urn:oasis:names:tc:SAML:attribute:subject-id"><saml:AttributeValue>victim-id</saml:AttributeValue></saml:Attribute></saml:AttributeStatement>`
	ts.refused(ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, a)), "SAML Assertion does not contain an email address")
}

// An assertion ID recorded for an assertion that has since expired can be
// recorded again; one that has not expired cannot.
func (ts *SAMLACSTestSuite) TestConsumedAssertionIDIsFreedOnExpiry() {
	provider, err := models.FindSAMLProviderByEntityID(ts.API.db, ts.IdP.entityID)
	require.NoError(ts.T(), err)

	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Minute)
	require.NoError(ts.T(), models.ConsumeSAMLAssertion(ts.API.db, provider.ID, "reused-id", past))
	require.NoError(ts.T(), models.ConsumeSAMLAssertion(ts.API.db, provider.ID, "reused-id", future))
	require.ErrorAs(ts.T(), models.ConsumeSAMLAssertion(ts.API.db, provider.ID, "reused-id", future), &models.SAMLAssertionReplayedError{})
	require.Error(ts.T(), models.ConsumeSAMLAssertion(ts.API.db, provider.ID, "", future))
}

// withNextKey starts a key rotation for the length of the test, returning the
// next key's certificate.
func (ts *SAMLACSTestSuite) withNextKey() *x509.Certificate {
	next := newSAMLTestKeyConfig(ts.T())
	ts.Config.SAML.NextRSAPrivateKey = next.RSAPrivateKey
	ts.Config.SAML.NextCertificate = next.Certificate
	ts.T().Cleanup(func() {
		ts.Config.SAML.NextRSAPrivateKey = nil
		ts.Config.SAML.NextCertificate = nil
	})
	return next.Certificate
}

func (ts *SAMLACSTestSuite) TestAssertionEncryptedToCurrentKeySignsIn() {
	ts.withNextKey()
	response := ts.IdP.encryptedResponse(ts.T(), ts.ACSURL, ts.victimAssertion("encrypted-current"), ts.Config.SAML.Certificate)
	location := ts.postACS(response)
	require.NotEmpty(ts.T(), ts.signedInAs(location), "refused: %s", location)
}

// During a rotation an IdP may already encrypt to the next key.
func (ts *SAMLACSTestSuite) TestAssertionEncryptedToNextKeySignsIn() {
	next := ts.withNextKey()
	response := ts.IdP.encryptedResponse(ts.T(), ts.ACSURL, ts.victimAssertion("encrypted-next"), next)
	location := ts.postACS(response)
	require.NotEmpty(ts.T(), ts.signedInAs(location), "refused: %s", location)
}

func (ts *SAMLACSTestSuite) TestAssertionEncryptedToUnknownKeyIsRefused() {
	ts.withNextKey()
	stranger := newSAMLTestKeyConfig(ts.T())
	response := ts.IdP.encryptedResponse(ts.T(), ts.ACSURL, ts.victimAssertion("encrypted-unknown"), stranger.Certificate)
	ts.refused(ts.postACS(response), refusedInvalid)
}

// Without a rotation in progress an assertion encrypted to some other key is
// refused, as before.
func (ts *SAMLACSTestSuite) TestAssertionEncryptedWithoutNextKeyToOtherKeyIsRefused() {
	other := newSAMLTestKeyConfig(ts.T())
	response := ts.IdP.encryptedResponse(ts.T(), ts.ACSURL, ts.victimAssertion("encrypted-other"), other.Certificate)
	ts.refused(ts.postACS(response), refusedInvalid)
}
