package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	tst "testing"
	"time"

	dsig "github.com/russellhaering/goxmldsig"

	"github.com/stretchr/testify/require"
	"github.com/supatype/saml"
	"github.com/supatype/saml/samlsp"
	"github.com/supatype/server/internal/conf"
)

func TestSAMLMetadataWithAPI(t *tst.T) {
	config, err := conf.LoadGlobal(apiTestConfig)
	require.NoError(t, err)
	config.API.ExternalURL = "https://projectref.supatype.com/auth/v1/"
	config.SAML.Enabled = true
	config.SAML.PrivateKey = "MIIEowIBAAKCAQEAszrVveMQcSsa0Y+zN1ZFb19cRS0jn4UgIHTprW2tVBmO2PABzjY3XFCfx6vPirMAPWBYpsKmXrvm1tr0A6DZYmA8YmJd937VUQ67fa6DMyppBYTjNgGEkEhmKuszvF3MARsIKCGtZqUrmS7UG4404wYxVppnr2EYm3RGtHlkYsXu20MBqSDXP47bQP+PkJqC3BuNGk3xt5UHl2FSFpTHelkI6lBynw16B+lUT1F96SERNDaMqi/TRsZdGe5mB/29ngC/QBMpEbRBLNRir5iUevKS7Pn4aph9Qjaxx/97siktK210FJT23KjHpgcUfjoQ6BgPBTLtEeQdRyDuc/CgfwIDAQABAoIBAGYDWOEpupQPSsZ4mjMnAYJwrp4ZISuMpEqVAORbhspVeb70bLKonT4IDcmiexCg7cQBcLQKGpPVM4CbQ0RFazXZPMVq470ZDeWDEyhoCfk3bGtdxc1Zc9CDxNMs6FeQs6r1beEZug6weG5J/yRn/qYxQife3qEuDMl+lzfl2EN3HYVOSnBmdt50dxRuX26iW3nqqbMRqYn9OHuJ1LvRRfYeyVKqgC5vgt/6Tf7DAJwGe0dD7q08byHV8DBZ0pnMVU0bYpf1GTgMibgjnLjK//EVWafFHtN+RXcjzGmyJrk3+7ZyPUpzpDjO21kpzUQLrpEkkBRnmg6bwHnSrBr8avECgYEA3pq1PTCAOuLQoIm1CWR9/dhkbJQiKTJevlWV8slXQLR50P0WvI2RdFuSxlWmA4xZej8s4e7iD3MYye6SBsQHygOVGc4efvvEZV8/XTlDdyj7iLVGhnEmu2r7AFKzy8cOvXx0QcLg+zNd7vxZv/8D3Qj9Jje2LjLHKM5n/dZ3RzUCgYEAzh5Lo2anc4WN8faLGt7rPkGQF+7/18ImQE11joHWa3LzAEy7FbeOGpE/vhOv5umq5M/KlWFIRahMEQv4RusieHWI19ZLIP+JwQFxWxS+cPp3xOiGcquSAZnlyVSxZ//dlVgaZq2o2MfrxECcovRlaknl2csyf+HjFFwKlNxHm2MCgYAr//R3BdEy0oZeVRndo2lr9YvUEmu2LOihQpWDCd0fQw0ZDA2kc28eysL2RROte95r1XTvq6IvX5a0w11FzRWlDpQ4J4/LlcQ6LVt+98SoFwew+/PWuyLmxLycUbyMOOpm9eSc4wJJZNvaUzMCSkvfMtmm5jgyZYMMQ9A2Ul/9SQKBgB9mfh9mhBwVPIqgBJETZMMXOdxrjI5SBYHGSyJqpT+5Q0vIZLfqPrvNZOiQFzwWXPJ+tV4Mc/YorW3rZOdo6tdvEGnRO6DLTTEaByrY/io3/gcBZXoSqSuVRmxleqFdWWRnB56c1hwwWLqNHU+1671FhL6pNghFYVK4suP6qu4BAoGBAMk+VipXcIlD67mfGrET/xDqiWWBZtgTzTMjTpODhDY1GZck1eb4CQMP5j5V3gFJ4cSgWDJvnWg8rcz0unz/q4aeMGl1rah5WNDWj1QKWMS6vJhMHM/rqN1WHWR0ZnV83svYgtg0zDnQKlLujqW4JmGXLMU7ur6a+e6lpa1fvLsP"
	config.API.MaxRequestDuration = 5 * time.Second

	require.NoError(t, config.ApplyDefaults())
	require.NoError(t, config.SAML.PopulateFields())

	require.NotNil(t, config.SAML.Certificate)

	api := NewAPI(config, nil)

	// Setup request
	req := httptest.NewRequest(http.MethodGet, "http://localhost/sso/saml/metadata", nil)

	w := httptest.NewRecorder()
	api.handler.ServeHTTP(w, req)
	require.Equal(t, w.Code, http.StatusOK)

	metadata := saml.EntityDescriptor{}
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &metadata))

	require.Equal(t, metadata.EntityID, "https://projectref.supatype.com/auth/v1/sso/saml/metadata")
	require.Equal(t, len(metadata.SPSSODescriptors), 1)

	require.Nil(t, metadata.SPSSODescriptors[0].AuthnRequestsSigned)
	require.True(t, *(metadata.SPSSODescriptors[0].WantAssertionsSigned))

	require.Equal(t, len(metadata.SPSSODescriptors[0].AssertionConsumerServices), 2)
	require.Equal(t, metadata.SPSSODescriptors[0].AssertionConsumerServices[0].Location, "https://projectref.supatype.com/auth/v1/sso/saml/acs")
	require.Equal(t, metadata.SPSSODescriptors[0].AssertionConsumerServices[1].Location, "https://projectref.supatype.com/auth/v1/sso/saml/acs")
	require.Equal(t, len(metadata.SPSSODescriptors[0].SingleLogoutServices), 1)
	require.Equal(t, metadata.SPSSODescriptors[0].SingleLogoutServices[0].Location, "https://projectref.supatype.com/auth/v1/sso/saml/slo")

	require.Equal(t, len(metadata.SPSSODescriptors[0].KeyDescriptors), 1)
	require.Equal(t, metadata.SPSSODescriptors[0].KeyDescriptors[0].Use, "signing")

	require.Equal(t, len(metadata.SPSSODescriptors[0].NameIDFormats), 2)
	require.Equal(t, metadata.SPSSODescriptors[0].NameIDFormats[0], saml.EmailAddressNameIDFormat)
	require.Equal(t, metadata.SPSSODescriptors[0].NameIDFormats[1], saml.PersistentNameIDFormat)
}

// The SAML endpoints are under /auth/v1 exactly once, whichever convention the
// external URL follows: the gateway root (Docker, where the gateway routes only
// /auth/v1/* here) or the auth mount itself (native dev, Cloud). An explicit
// SAML external URL is the operator's own choice and is used as written.
func TestSAMLServiceProviderURLs(t *tst.T) {
	for _, tc := range []struct {
		api, saml, wantACS string
	}{
		{"http://localhost:8000", "", "http://localhost:8000/auth/v1/sso/saml/acs"},
		{"http://localhost:8000/", "", "http://localhost:8000/auth/v1/sso/saml/acs"},
		{"http://localhost:9999/auth/v1", "", "http://localhost:9999/auth/v1/sso/saml/acs"},
		{"https://p.example.com/auth/v1/", "", "https://p.example.com/auth/v1/sso/saml/acs"},
		{"http://localhost:8000", "https://sso.example.com/", "https://sso.example.com/sso/saml/acs"},
	} {
		config := &conf.GlobalConfiguration{}
		config.API.ExternalURL = tc.api
		config.SAML.ExternalURL = tc.saml
		api := &API{config: config}

		sp := api.getSAMLServiceProvider(nil, true)
		require.Equal(t, tc.wantACS, sp.AcsURL.String(), tc.api)
		require.Equal(t, strings.TrimSuffix(tc.wantACS, "/acs")+"/metadata", sp.MetadataURL.String(), tc.api)
	}
}

// newSAMLTestKey returns a fresh RSA 2048 key in the form
// SUPATYPE_SAML_PRIVATE_KEY takes.
func newSAMLTestKey(t *tst.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(key))
}

// newSAMLTestKeyConfig is a SAML configuration over a fresh key, with the key
// parsed and its certificate derived.
func newSAMLTestKeyConfig(t *tst.T) *conf.SAMLConfiguration {
	t.Helper()
	c := &conf.SAMLConfiguration{PrivateKey: newSAMLTestKey(t)}
	require.NoError(t, c.PopulateFields())
	return c
}

// samlMetadataTestConfig is the test configuration with SAML populated, and a
// next key when nextKey is set.
func samlMetadataTestConfig(t *tst.T, nextKey string, allowEncrypted bool) *conf.GlobalConfiguration {
	t.Helper()
	config, err := conf.LoadGlobal(apiTestConfig)
	require.NoError(t, err)
	config.SAML.NextPrivateKey = nextKey
	config.SAML.AllowEncryptedAssertions = allowEncrypted
	require.NoError(t, config.SAML.Validate())
	require.NoError(t, config.SAML.PopulateFields())
	return config
}

// fetchSPMetadata serves the SP metadata from an API over config.
func fetchSPMetadata(t *tst.T, config *conf.GlobalConfiguration) []byte {
	t.Helper()
	api := NewAPI(config, nil)
	w := httptest.NewRecorder()
	api.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/sso/saml/metadata", nil))
	require.Equal(t, http.StatusOK, w.Code)
	return w.Body.Bytes()
}

// keyDescriptorUses lists the metadata's key descriptors as "use:certificate".
func keyDescriptorUses(t *tst.T, metadataXML []byte) []string {
	t.Helper()
	metadata := saml.EntityDescriptor{}
	require.NoError(t, xml.Unmarshal(metadataXML, &metadata))
	require.Len(t, metadata.SPSSODescriptors, 1)

	var uses []string
	for _, kd := range metadata.SPSSODescriptors[0].KeyDescriptors {
		require.Len(t, kd.KeyInfo.X509Data.X509Certificates, 1)
		uses = append(uses, kd.Use+":"+kd.KeyInfo.X509Data.X509Certificates[0].Data)
	}
	return uses
}

// During a rotation the metadata lists the next key's certificate beside the
// current one, for each use the current one is listed for, so an identity
// provider can trust both before the switch.
func TestSAMLMetadataKeyDescriptors(t *tst.T) {
	next := newSAMLTestKey(t)
	certOf := func(config *conf.GlobalConfiguration) string {
		return base64.StdEncoding.EncodeToString(config.SAML.Certificate.Raw)
	}
	nextCertOf := func(config *conf.GlobalConfiguration) string {
		return base64.StdEncoding.EncodeToString(config.SAML.NextCertificate.Raw)
	}

	t.Run("CurrentKeyOnly", func(t *tst.T) {
		config := samlMetadataTestConfig(t, "", false)
		require.Equal(t, []string{"signing:" + certOf(config)}, keyDescriptorUses(t, fetchSPMetadata(t, config)))
	})

	t.Run("CurrentKeyWithEncryption", func(t *tst.T) {
		config := samlMetadataTestConfig(t, "", true)
		require.Equal(t, []string{
			"encryption:" + certOf(config),
			"signing:" + certOf(config),
		}, keyDescriptorUses(t, fetchSPMetadata(t, config)))
	})

	t.Run("NextKey", func(t *tst.T) {
		config := samlMetadataTestConfig(t, next, false)
		require.Equal(t, []string{
			"signing:" + certOf(config),
			"signing:" + nextCertOf(config),
		}, keyDescriptorUses(t, fetchSPMetadata(t, config)))
	})

	t.Run("NextKeyWithEncryption", func(t *tst.T) {
		config := samlMetadataTestConfig(t, next, true)
		require.Equal(t, []string{
			"encryption:" + certOf(config),
			"signing:" + certOf(config),
			"encryption:" + nextCertOf(config),
			"signing:" + nextCertOf(config),
		}, keyDescriptorUses(t, fetchSPMetadata(t, config)))
	})
}

// AuthnRequests are signed with the current key only while a rotation is in
// progress: identity providers that have not yet picked up the next
// certificate must still accept them.
func TestSAMLAuthnRequestSignedWithCurrentKey(t *tst.T) {
	config := samlMetadataTestConfig(t, newSAMLTestKey(t), false)
	api := &API{config: config}

	idpMetadata, err := samlsp.ParseMetadata([]byte(newSAMLTestIdP(t, "https://idp.example.com/saml").metadataXML()))
	require.NoError(t, err)
	sp := api.getSAMLServiceProvider(idpMetadata, false)

	request, err := sp.MakeAuthenticationRequest(sp.GetSSOBindingLocation(saml.HTTPRedirectBinding), saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	require.NoError(t, err)
	redirect, err := request.Redirect("relay-state", sp)
	require.NoError(t, err)

	signedPart, encodedSignature, found := strings.Cut(redirect.RawQuery, "&Signature=")
	require.True(t, found, "the redirect carries a signature: %s", redirect)
	signatureB64, err := url.QueryUnescape(encodedSignature)
	require.NoError(t, err)
	signature, err := base64.StdEncoding.DecodeString(signatureB64)
	require.NoError(t, err)

	verify := func(certificate *x509.Certificate) error {
		return certificate.CheckSignature(x509.SHA256WithRSA, []byte(signedPart), signature)
	}

	require.NoError(t, verify(config.SAML.Certificate), "signed with the current key")
	require.Error(t, verify(config.SAML.NextCertificate), "not signed with the next key")
}

// validUntilAttr matches the validUntil the metadata stamps on each request.
var validUntilAttr = regexp.MustCompile(`validUntil="[^"]*"`)

// The SP metadata is what an IdP trusts, so with the same keys it is the same
// document after a restart or a configuration reload, apart from validUntil,
// which is stamped per request. The configuration is read the way the
// reloader reads it: the watch directory's files, then the environment.
func TestSAMLMetadataStableAcrossReload(t *tst.T) {
	_, err := conf.LoadGlobal(apiTestConfig)
	require.NoError(t, err)

	dir := t.TempDir()
	settings := "SUPATYPE_SAML_NEXT_PRIVATE_KEY=" + newSAMLTestKey(t) + "\nSUPATYPE_SAML_ALLOW_ENCRYPTED_ASSERTIONS=true\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "saml.env"), []byte(settings), 0o600))
	// Registered so the cleanup restores what LoadDirectory overwrites.
	t.Setenv("SUPATYPE_SAML_NEXT_PRIVATE_KEY", "")
	t.Setenv("SUPATYPE_SAML_ALLOW_ENCRYPTED_ASSERTIONS", "")

	render := func() string {
		require.NoError(t, conf.LoadDirectory(dir))
		config, err := conf.LoadGlobalFromEnv()
		require.NoError(t, err)
		require.NotNil(t, config.SAML.NextCertificate, "the next key was read")
		return validUntilAttr.ReplaceAllString(string(fetchSPMetadata(t, config)), `validUntil="2000-01-01T00:00:00Z"`)
	}

	first := render()
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, first, render())
	require.Len(t, keyDescriptorUses(t, []byte(first)), 4, "both keys, for signing and encryption")
}

// AuthnRequests are signed with RSA-SHA256, not the library's SHA-1 default.
func TestSAMLAuthnRequestSignedWithSHA256(t *tst.T) {
	config := samlMetadataTestConfig(t, "", false)
	api := &API{config: config}
	idpMetadata, err := samlsp.ParseMetadata([]byte(newSAMLTestIdP(t, "https://idp.example.com/saml").metadataXML()))
	require.NoError(t, err)
	sp := api.getSAMLServiceProvider(idpMetadata, false)
	require.Equal(t, dsig.RSASHA256SignatureMethod, sp.SignatureMethod)

	request, err := sp.MakeAuthenticationRequest(sp.GetSSOBindingLocation(saml.HTTPRedirectBinding), saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	require.NoError(t, err)
	redirect, err := request.Redirect("relay-state", sp)
	require.NoError(t, err)
	signedPart, encodedSignature, found := strings.Cut(redirect.RawQuery, "&Signature=")
	require.True(t, found)
	require.Contains(t, signedPart, "SigAlg="+url.QueryEscape(dsig.RSASHA256SignatureMethod))
	signatureB64, err := url.QueryUnescape(encodedSignature)
	require.NoError(t, err)
	signature, err := base64.StdEncoding.DecodeString(signatureB64)
	require.NoError(t, err)
	require.NoError(t, config.SAML.Certificate.CheckSignature(x509.SHA256WithRSA, []byte(signedPart), signature))
}
