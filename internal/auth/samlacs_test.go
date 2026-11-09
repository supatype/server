package auth

import (
	"bytes"
	"context"
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
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gofrs/uuid"
	jwt "github.com/golang-jwt/jwt/v5"
	dsig "github.com/russellhaering/goxmldsig"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/supatype/saml"
	"github.com/supatype/saml/samlsp"
	"github.com/supatype/saml/xmlenc"
	"github.com/supatype/server/internal/auth/mailer/mockclient"
	"github.com/supatype/server/internal/auth/models"
	authprovider "github.com/supatype/server/internal/auth/provider"
	"github.com/supatype/server/internal/auth/storage"
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
	return idp.metadataXMLWithArtifactResolver("")
}

// metadataXMLWithArtifactResolver is the IdP's metadata, listing an artifact
// resolution endpoint at location when location is not empty.
func (idp *samlTestIdP) metadataXMLWithArtifactResolver(location string) string {
	resolver := ""
	if location != "" {
		resolver = fmt.Sprintf(`
    <md:ArtifactResolutionService Binding="urn:oasis:names:tc:SAML:2.0:bindings:SOAP" Location="%s" index="0"/>`, location)
	}
	return fmt.Sprintf(`<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="%[1]s">
  <md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <md:KeyDescriptor use="signing">
      <ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#"><ds:X509Data><ds:X509Certificate>%[2]s</ds:X509Certificate></ds:X509Data></ds:KeyInfo>
    </md:KeyDescriptor>
    <md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location="%[1]s/sso"/>%[3]s
  </md:IDPSSODescriptor>
</md:EntityDescriptor>`, idp.entityID, idp.certB64, resolver)
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
	return idp.wrapResponse(t, acsURL, a.ID, idp.encryptedAssertion(t, a, certificate))
}

// encryptedAssertion is the signed assertion a describes, encrypted to
// certificate.
func (idp *samlTestIdP) encryptedAssertion(t *testing.T, a samlTestAssertion, certificate *x509.Certificate) *etree.Element {
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
	return encryptedAssertion
}

// signedAssertion is the assertion a describes, signed by the IdP.
func (idp *samlTestIdP) signedAssertion(t *testing.T, a samlTestAssertion) *etree.Element {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	doc := etree.NewDocument()
	require.NoError(t, doc.ReadFromString(fmt.Sprintf(
		`<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="%s" Version="2.0" IssueInstant="%s"><saml:Issuer>%s</saml:Issuer>%s%s%s</saml:Assertion>`,
		a.ID, now, idp.entityID, a.Subject, a.Conditions, a.Statements)))
	return idp.signEnveloped(t, doc.Root())
}

// signEnveloped signs el as the IdP.
func (idp *samlTestIdP) signEnveloped(t *testing.T, el *etree.Element) *etree.Element {
	t.Helper()
	signed, err := idp.signer.SignEnveloped(el)
	require.NoError(t, err)
	// SignEnveloped appends the Signature; the schema places it after Issuer.
	sig := signed.RemoveChildAt(len(signed.Child) - 1)
	signed.InsertChildAt(1, sig)
	return signed
}

// protocolMessage is a SAML protocol message named name from the IdP, with
// the header each one carries: namespaces, ID, version, issue time, Issuer and
// a success Status.
func (idp *samlTestIdP) protocolMessage(name, id string) *etree.Element {
	msg := etree.NewElement(name)
	msg.CreateAttr("xmlns:samlp", "urn:oasis:names:tc:SAML:2.0:protocol")
	msg.CreateAttr("xmlns:saml", "urn:oasis:names:tc:SAML:2.0:assertion")
	msg.CreateAttr("ID", id)
	msg.CreateAttr("Version", "2.0")
	msg.CreateAttr("IssueInstant", time.Now().UTC().Format(time.RFC3339))
	msg.CreateElement("saml:Issuer").SetText(idp.entityID)
	msg.CreateElement("samlp:Status").CreateElement("samlp:StatusCode").
		CreateAttr("Value", "urn:oasis:names:tc:SAML:2.0:status:Success")
	return msg
}

// responseElement puts assertion in an unsigned Response addressed to acsURL.
func (idp *samlTestIdP) responseElement(acsURL, id string, assertion *etree.Element) *etree.Element {
	resp := idp.protocolMessage("samlp:Response", "response-"+id)
	resp.CreateAttr("Destination", acsURL)
	resp.AddChild(assertion)
	return resp
}

// wrapResponse is responseElement base64 encoded as the HTTP-POST binding
// sends it.
func (idp *samlTestIdP) wrapResponse(t *testing.T, acsURL, id string, assertion *etree.Element) string {
	t.Helper()
	out := etree.NewDocument()
	out.SetRoot(idp.responseElement(acsURL, id, assertion))
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
	// Mailer records the emails the server sends.
	Mailer *mockclient.MockMailer
}

func TestSAMLACS(t *testing.T) {
	mockMailer := &mockclient.MockMailer{}
	api, config, err := setupAPIForTest(WithMailer(mockMailer))
	require.NoError(t, err)
	defer api.db.Close()

	if config.SAML.Enabled {
		suite.Run(t, &SAMLACSTestSuite{API: api, Config: config, Mailer: mockMailer})
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
	ts.Mailer.Reset()

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
	return ts.subjectInResponseTo(nameID, "")
}

// subjectInResponseTo is subject confirming an answer to the AuthnRequest
// requestID, or to none when requestID is empty.
func (ts *SAMLACSTestSuite) subjectInResponseTo(nameID, requestID string) string {
	inResponseTo := ""
	if requestID != "" {
		inResponseTo = fmt.Sprintf(` InResponseTo="%s"`, requestID)
	}
	return fmt.Sprintf(`<saml:Subject>%s<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"><saml:SubjectConfirmationData%s NotOnOrAfter="%s" Recipient="%s"/></saml:SubjectConfirmation></saml:Subject>`,
		nameID, inResponseTo, samlTime(5*time.Minute), ts.ACSURL)
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

// acsRequest posts form to target, as the HTTP-POST binding and the artifact
// binding both do.
func acsRequest(target string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// postACS posts an IdP-initiated response and returns the redirect Location.
func (ts *SAMLACSTestSuite) postACS(samlResponse string) *url.URL {
	return ts.postACSForm(url.Values{"SAMLResponse": {samlResponse}})
}

// postACSForm posts form to the ACS and returns the redirect Location.
func (ts *SAMLACSTestSuite) postACSForm(form url.Values) *url.URL {
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, acsRequest("http://localhost/sso/saml/acs", form))

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

	req := acsRequest(ts.ACSURL, url.Values{"SAMLResponse": {samlResponse}})
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

// setDomains gives the test IdP's provider the domains.
func (ts *SAMLACSTestSuite) setDomains(domains ...string) {
	provider, err := models.FindSAMLProviderByEntityID(ts.API.db, ts.IdP.entityID)
	require.NoError(ts.T(), err)
	body, err := json.Marshal(map[string]interface{}{"domains": domains})
	require.NoError(ts.T(), err)
	req := httptest.NewRequest(http.MethodPut, "http://localhost/admin/sso/providers/"+provider.ID.String(), bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+adminToken(ts.T(), ts.Config))
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
}

// assertionFor is a correct assertion for subject nameID with email.
func (ts *SAMLACSTestSuite) assertionFor(id, nameID, email string) samlTestAssertion {
	return samlTestAssertion{
		ID:         id,
		Subject:    ts.subject(`<saml:NameID Format="urn:oasis:names:tc:SAML:2.0:nameid-format:persistent">` + nameID + `</saml:NameID>`),
		Conditions: conditions(ts.audienceRestriction()),
		Statements: emailStatement(email),
	}
}

// A provider with domains signs in only emails on those domains, or it could
// sign in as any address it likes.
func (ts *SAMLACSTestSuite) TestEmailOutsideProviderDomainsIsRefused() {
	ts.setDomains("example.com")
	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("outside-domain", "outsider", "ceo@other.org")))
	ts.refused(location, "SAML Assertion email is not on a domain of this identity provider")
	require.Equal(ts.T(), "saml_email_domain_not_allowed", location.Query().Get("error_code"))
}

func (ts *SAMLACSTestSuite) TestEmailOnProviderDomainSignsIn() {
	ts.setDomains("example.com", "example.org")
	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("inside-domain", "insider", "Someone@Example.ORG")))
	require.NotEmpty(ts.T(), ts.signedInAs(location), "refused: %s", location)
}

// A subdomain is a different domain: it must be listed itself.
func (ts *SAMLACSTestSuite) TestEmailOnSubdomainIsRefused() {
	ts.setDomains("example.com")
	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("subdomain", "sub", "someone@eu.example.com")))
	ts.refused(location, "SAML Assertion email is not on a domain of this identity provider")
}

// markVerified marks the provider's domain verified, as the verify endpoint
// does once it sees the TXT record.
func (ts *SAMLACSTestSuite) markVerified(domain string) {
	provider, err := models.FindSAMLProviderByEntityID(ts.API.db, ts.IdP.entityID)
	require.NoError(ts.T(), err)
	d := provider.Domain(domain)
	require.NotNil(ts.T(), d, "%s is a domain of the provider", domain)
	now := time.Now().UTC()
	d.VerifiedAt = &now
	require.NoError(ts.T(), ts.API.db.UpdateOnly(d, "verified_at"))
}

// existingUser is a confirmed account for email that signs in with a
// password, outside SSO. The email is stored as given: NewUser lowercases it,
// but rows written by other means can differ from each other in case alone.
func (ts *SAMLACSTestSuite) existingUser(email string) *models.User {
	user, err := models.NewUser("", email, "password", ts.Config.JWT.Aud, nil)
	require.NoError(ts.T(), err)
	user.Email = storage.NullString(email)
	confirmedAt := time.Now().UTC()
	user.EmailConfirmedAt = &confirmedAt
	require.NoError(ts.T(), ts.API.db.Create(user))
	_, err = ts.API.createNewIdentity(ts.API.db, user, "email", map[string]interface{}{"sub": user.ID.String(), "email": email})
	require.NoError(ts.T(), err)
	return user
}

const (
	refusedUnverifiedDomain = "This email already has an account. Ask your administrator to verify the domain so they can be linked."
	refusedMultipleAccounts = "More than one account has this email. Ask your administrator to merge them before signing in with SSO."
)

// Several accounts with the email in different case: the one stored exactly
// as the identity provider sends it is linked.
func (ts *SAMLACSTestSuite) TestLoginLinksTheExactCaseAccount() {
	ts.setDomains("example.com")
	ts.markVerified("example.com")
	ts.existingUser("member@example.com")
	exact := ts.existingUser("Member@Example.com")

	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("exact-case", "member-id", "Member@Example.com")))
	require.Equal(ts.T(), exact.ID.String(), ts.signedInAs(location), "signed in as the exact-case account: %s", location)
}

// Several accounts with the email and none stored exactly as sent: no account
// is picked, and the refusal says why instead of a server error.
func (ts *SAMLACSTestSuite) TestLoginWithSeveralAccountsAndNoExactCaseIsRefused() {
	ts.setDomains("example.com")
	ts.markVerified("example.com")
	ts.existingUser("member@example.com")
	ts.existingUser("Member@Example.com")

	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("no-exact-case", "member-id", "MEMBER@example.com")))
	ts.refused(location, refusedMultipleAccounts)
	require.Equal(ts.T(), "sso_multiple_accounts", location.Query().Get("error_code"))

	// The redirect carries no status, so the decision is checked directly.
	provider, err := models.FindSAMLProviderByEntityID(ts.API.db, ts.IdP.entityID)
	require.NoError(ts.T(), err)
	userData := &authprovider.UserProvidedData{
		Emails:            []authprovider.Email{{Email: "MEMBER@example.com", Verified: true, Primary: true}},
		Metadata:          &authprovider.Claims{Subject: "member-id"},
		SSODomainVerified: true,
	}
	_, _, err = ts.API.createAccountFromExternalIdentity(ts.API.db, httptest.NewRequest(http.MethodPost, ts.ACSURL, nil), userData, "sso:"+provider.ID.String(), false)
	var httpErr *HTTPError
	require.ErrorAs(ts.T(), err, &httpErr)
	require.Equal(ts.T(), http.StatusUnprocessableEntity, httpErr.HTTPStatus)
	require.Equal(ts.T(), refusedMultipleAccounts, httpErr.Message)
}

// On a verified domain a first SAML login links to the account that already
// has the email, so the person keeps one account.
func (ts *SAMLACSTestSuite) TestLoginOnVerifiedDomainLinksExistingAccount() {
	ts.setDomains("example.com")
	ts.markVerified("example.com")
	existing := ts.existingUser("member@example.com")

	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("link-1", "member-id", "Member@Example.com")))
	require.Equal(ts.T(), existing.ID.String(), ts.signedInAs(location), "signed in as the existing account: %s", location)

	user, err := models.FindUserByID(ts.API.db, existing.ID)
	require.NoError(ts.T(), err)
	require.False(ts.T(), user.IsSSOUser, "a linked account stays a regular account")
	require.Contains(ts.T(), strings.Join(identityProviders(user), ","), "sso:")

	// The next login finds the linked identity.
	location = ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("link-2", "member-id", "member@example.com")))
	require.Equal(ts.T(), existing.ID.String(), ts.signedInAs(location))
}

// Before the domain is verified a login whose email already has an account is
// refused, rather than creating a second account for the same address.
func (ts *SAMLACSTestSuite) TestLoginOnUnverifiedDomainWithExistingAccountIsRefused() {
	ts.setDomains("example.com")
	ts.existingUser("member@example.com")

	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("unverified", "member-id", "member@example.com")))
	ts.refused(location, refusedUnverifiedDomain)
	require.Equal(ts.T(), "sso_domain_not_verified", location.Query().Get("error_code"))
}

// A provider with no domains has none verified, so it never links.
func (ts *SAMLACSTestSuite) TestLoginWithoutDomainsWithExistingAccountIsRefused() {
	ts.existingUser("member@example.com")
	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("no-domains", "member-id", "member@example.com")))
	ts.refused(location, refusedUnverifiedDomain)
}

// With no account for the email a login creates one, verified domain or not,
// and the new account is an SSO account as before.
func (ts *SAMLACSTestSuite) TestLoginWithoutExistingAccountCreatesSSOUser() {
	ts.setDomains("example.com")
	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("new-user", "new-id", "new@example.com")))
	id := ts.signedInAs(location)
	require.NotEmpty(ts.T(), id, "refused: %s", location)

	user, err := models.FindUserByID(ts.API.db, uuid.FromStringOrNil(id))
	require.NoError(ts.T(), err)
	require.True(ts.T(), user.IsSSOUser)
}

// identityProviders lists the providers of user's identities.
func identityProviders(user *models.User) []string {
	providers := []string{}
	for _, identity := range user.Identities {
		providers = append(providers, identity.Provider)
	}
	return providers
}

// tokenStatus posts body to the token endpoint with grantType and returns the
// status code.
func (ts *SAMLACSTestSuite) tokenStatus(grantType string, body map[string]string) int {
	encoded, err := json.Marshal(body)
	require.NoError(ts.T(), err)
	req := httptest.NewRequest(http.MethodPost, "http://localhost/token?grant_type="+grantType, bytes.NewBuffer(encoded))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	return w.Code
}

// factorCount is the number of MFA factors user has enrolled.
func (ts *SAMLACSTestSuite) factorCount(user *models.User) int {
	count, err := ts.API.db.Q().Where("user_id = ?", user.ID).Count(&models.Factor{})
	require.NoError(ts.T(), err)
	return count
}

// passwordSignInStatus is the status of a password sign-in as email.
func (ts *SAMLACSTestSuite) passwordSignInStatus(email, password string) int {
	return ts.tokenStatus("password", map[string]string{"email": email, "password": password})
}

// refreshStatus is the status of exchanging refreshToken for a new session.
func (ts *SAMLACSTestSuite) refreshStatus(refreshToken string) int {
	return ts.tokenStatus("refresh_token", map[string]string{"refresh_token": refreshToken})
}

// identityFor links an identity of provider to user with identity data
// carrying email and emailVerified, as an OAuth sign-in would.
func (ts *SAMLACSTestSuite) identityFor(user *models.User, provider, email string, emailVerified bool) {
	_, err := ts.API.createNewIdentity(ts.API.db, user, provider, map[string]interface{}{
		"sub":            provider + "-" + email,
		"email":          email,
		"email_verified": emailVerified,
	})
	require.NoError(ts.T(), err)
}

// preRegister gives user every way back in that whoever registered it could
// set up: a phone number with its identity, a verified TOTP factor, a passkey,
// a pending email change to their own address and a signed-in session, plus a
// profile of their choosing. It returns that session's refresh token.
func (ts *SAMLACSTestSuite) preRegister(user *models.User) string {
	db := ts.API.db
	now := time.Now().UTC()
	user.Phone = storage.NullString("15550001111")
	user.PhoneConfirmedAt = &now
	user.EmailChange = "attacker@evil.test"
	user.EmailChangeTokenNew = "pending-email-change"
	user.UserMetaData = map[string]interface{}{"full_name": "Registrant", "pay_to": "registrant-account"}
	require.NoError(ts.T(), db.UpdateOnly(user, "phone", "phone_confirmed_at", "email_change", "email_change_token_new", "raw_user_meta_data"))
	_, err := ts.API.createNewIdentity(db, user, "phone", map[string]interface{}{"sub": user.ID.String(), "phone": "15550001111"})
	require.NoError(ts.T(), err)

	require.NoError(ts.T(), db.Create(models.NewFactor(user, "attacker totp", models.TOTP, models.FactorStateVerified)))
	passkey := &webauthn.Credential{ID: []byte("attacker-passkey"), PublicKey: []byte("public-key")}
	require.NoError(ts.T(), db.Create(models.NewWebAuthnCredential(user.ID, passkey, "attacker laptop")))

	token, err := models.GrantAuthenticatedUser(db, user, models.GrantParams{})
	require.NoError(ts.T(), err)
	return token.Token
}

// 0.9.1 release test (RB1): someone registers a confirmed password account for
// a colleague's address (any stack that auto-confirms sign-ups allows it).
// When the colleague first signs in with SSO on a verified domain the identity
// is linked to that account, and nothing the registrant set up before the
// link may still sign in: password, phone, MFA factor, passkey, pending email
// change, or a session they already hold. An OAuth identity whose provider
// verified the account's own address stays.
func (ts *SAMLACSTestSuite) TestFirstSSOLinkRemovesWaysInSetUpBefore() {
	ts.setDomains("example.com")
	ts.markVerified("example.com")
	registered := ts.existingUser("member@example.com")
	refreshToken := ts.preRegister(registered)
	ts.identityFor(registered, "google", "member@example.com", true)
	ts.identityFor(registered, "github", "attacker@evil.test", true)
	ts.identityFor(registered, "gitlab", "member@example.com", false)
	require.Equal(ts.T(), http.StatusOK, ts.passwordSignInStatus("member@example.com", "password"), "the password signs in before the link")

	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("first-link", "member-id", "member@example.com")))
	require.Equal(ts.T(), registered.ID.String(), ts.signedInAs(location), "the SSO user keeps the one account: %s", location)

	require.Equal(ts.T(), http.StatusBadRequest, ts.passwordSignInStatus("member@example.com", "password"), "the registrant's password no longer signs in")
	require.Equal(ts.T(), http.StatusBadRequest, ts.refreshStatus(refreshToken), "the registrant's session is signed out")

	user, err := models.FindUserByID(ts.API.db, registered.ID)
	require.NoError(ts.T(), err)
	require.False(ts.T(), user.HasPassword(), "the password is cleared")
	require.Empty(ts.T(), user.GetPhone(), "the phone number no longer signs in")
	require.Nil(ts.T(), user.PhoneConfirmedAt)
	require.Empty(ts.T(), user.EmailChange, "the pending email change is dropped")
	require.Empty(ts.T(), user.EmailChangeTokenNew)
	require.NotContains(ts.T(), user.UserMetaData, "pay_to", "the registrant's profile is replaced by the SSO profile")
	require.NotEqual(ts.T(), "Registrant", user.UserMetaData["full_name"])
	require.Equal(ts.T(), "member@example.com", user.UserMetaData["email"])

	providers := identityProviders(user)
	require.Len(ts.T(), providers, 2, "only SSO and the verified OAuth identity for this address stay: %v", providers)
	require.Contains(ts.T(), providers, "google")
	require.Contains(ts.T(), strings.Join(providers, ","), "sso:")

	require.Zero(ts.T(), ts.factorCount(user), "MFA factors enrolled before the link are removed")
	passkeys, err := models.FindWebAuthnCredentialsByUserID(ts.API.db, user.ID)
	require.NoError(ts.T(), err)
	require.Empty(ts.T(), passkeys, "passkeys registered before the link are removed")

	var entries []models.AuditLogEntry
	require.NoError(ts.T(), ts.API.db.Q().Where("payload->>'action' = ? and payload->>'actor_id' = ?", "sso_identity_linked", user.ID.String()).All(&entries))
	require.Len(ts.T(), entries, 1, "the link is audited")

	require.Len(ts.T(), ts.Mailer.SSOLinkedMailCalls, 1, "the address is told SSO is now how it signs in, notifications setting or not")
	require.Equal(ts.T(), user.ID, ts.Mailer.SSOLinkedMailCalls[0].User.ID)
	require.Equal(ts.T(), "example.com", ts.Mailer.SSOLinkedMailCalls[0].Provider, "the provider is named by its domain, not its sso: id")
	require.Empty(ts.T(), ts.Mailer.IdentityLinkedMailCalls, "the generic identity linked email stays behind its setting")
}

// Only the first link clears the account. Once linked, a password the SSO user
// sets for themselves, and their sessions, survive their next SSO sign-in.
func (ts *SAMLACSTestSuite) TestLaterSSOSignInKeepsCredentials() {
	ts.setDomains("example.com")
	ts.markVerified("example.com")
	registered := ts.existingUser("member@example.com")
	location := ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("link-once", "member-id", "member@example.com")))
	require.Equal(ts.T(), registered.ID.String(), ts.signedInAs(location))

	user, err := models.FindUserByID(ts.API.db, registered.ID)
	require.NoError(ts.T(), err)
	require.NoError(ts.T(), user.SetPassword(context.Background(), "chosen-later", false, "", ""))
	require.NoError(ts.T(), ts.API.db.UpdateOnly(user, "encrypted_password"))
	token, err := models.GrantAuthenticatedUser(ts.API.db, user, models.GrantParams{})
	require.NoError(ts.T(), err)

	location = ts.postACS(ts.IdP.response(ts.T(), ts.ACSURL, ts.assertionFor("link-again", "member-id", "member@example.com")))
	require.Equal(ts.T(), registered.ID.String(), ts.signedInAs(location))
	require.Equal(ts.T(), http.StatusOK, ts.passwordSignInStatus("member@example.com", "chosen-later"))
	require.Equal(ts.T(), http.StatusOK, ts.refreshStatus(token.Token))
	require.Len(ts.T(), ts.Mailer.SSOLinkedMailCalls, 1, "only the first link sends the email")
}

// Linking a non-SSO identity is unchanged: the account keeps its password,
// sessions and factors.
func (ts *SAMLACSTestSuite) TestOAuthLinkKeepsCredentials() {
	registered := ts.existingUser("member@example.com")
	refreshToken := ts.preRegister(registered)
	userData := &authprovider.UserProvidedData{
		Emails:   []authprovider.Email{{Email: "member@example.com", Verified: true, Primary: true}},
		Metadata: &authprovider.Claims{Subject: "google-member", Email: "member@example.com", EmailVerified: true},
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/callback", nil)
	decision, user, err := ts.API.createAccountFromExternalIdentity(ts.API.db, req, userData, "google", false)
	require.NoError(ts.T(), err)
	require.Equal(ts.T(), models.LinkAccount, decision)
	require.Equal(ts.T(), registered.ID, user.ID)

	require.Equal(ts.T(), http.StatusOK, ts.passwordSignInStatus("member@example.com", "password"))
	require.Equal(ts.T(), http.StatusOK, ts.refreshStatus(refreshToken))
	require.Equal(ts.T(), 1, ts.factorCount(user))
}
