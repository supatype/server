package auth

import (
	"crypto/rsa"
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"time"

	dsig "github.com/russellhaering/goxmldsig"
	"github.com/supatype/saml"
	"github.com/supatype/saml/samlsp"
)

// getSAMLServiceProvider generates a new service provider object with the
// (optionally) provided descriptor (metadata) for the identity provider.
func (a *API) getSAMLServiceProvider(identityProvider *saml.EntityDescriptor, idpInitiated bool) *saml.ServiceProvider {
	var externalURL *url.URL

	if a.config.SAML.ExternalURL != "" {
		url, err := url.ParseRequestURI(a.config.SAML.ExternalURL)
		if err != nil {
			// this should not fail as a.config should have been validated using #Validate()
			panic(err)
		}

		externalURL = url
	} else {
		// The SSO routes are under the gateway's /auth/v1 mount, the same as
		// every other route of this API.
		url, err := url.ParseRequestURI(authMountURL(a.config.API.ExternalURL))
		if err != nil {
			// this should not fail as a.config should have been validated using #Validate()
			panic(err)
		}

		externalURL = url
	}

	if !strings.HasSuffix(externalURL.Path, "/") {
		externalURL.Path += "/"
	}

	externalURL.Path += "sso/"

	provider := samlsp.DefaultServiceProvider(samlsp.Options{
		URL:               *externalURL,
		Key:               a.config.SAML.RSAPrivateKey,
		Certificate:       a.config.SAML.Certificate,
		SignRequest:       true,
		AllowIDPInitiated: idpInitiated,
		IDPMetadata:       identityProvider,
	})

	provider.AuthnNameIDFormat = saml.PersistentNameIDFormat
	// While a key rotation is in progress an IdP may already encrypt to the
	// next key. Requests stay signed with the current key.
	if a.config.SAML.NextRSAPrivateKey != nil {
		provider.DecryptionKeys = []*rsa.PrivateKey{a.config.SAML.NextRSAPrivateKey}
	}
	// The library signs with RSA-SHA1 by default, which identity providers
	// increasingly refuse.
	provider.SignatureMethod = dsig.RSASHA256SignatureMethod

	return &provider
}

// samlKeyDescriptors lists the certificates the SP metadata publishes: the
// current key's, from the metadata the library already wrote for it, and,
// while a rotation is in progress, the next key's. Each is listed for signing,
// and for encryption only when encrypted assertions are allowed.
func (a *API) samlKeyDescriptors(serviceProvider *saml.ServiceProvider, metadata *saml.EntityDescriptor) []saml.KeyDescriptor {
	written := spKeyDescriptors(metadata)
	if a.config.SAML.NextCertificate != nil {
		// The library writes the descriptors of the certificate it holds.
		// The next certificate belongs in the supatype/saml fork's
		// Metadata(), with the extra keys carrying their certificates; this
		// copy goes once the fork does that.
		next := *serviceProvider
		next.Certificate = a.config.SAML.NextCertificate
		written = append(written, spKeyDescriptors(next.Metadata())...)
	}

	var keyDescriptors []saml.KeyDescriptor
	for _, kd := range written {
		if kd.Use == "signing" || (a.config.SAML.AllowEncryptedAssertions && kd.Use == "encryption") {
			keyDescriptors = append(keyDescriptors, kd)
		}
	}
	return keyDescriptors
}

// spKeyDescriptors collects the key descriptors of the metadata's SP
// descriptors into a new slice.
func spKeyDescriptors(metadata *saml.EntityDescriptor) []saml.KeyDescriptor {
	var keyDescriptors []saml.KeyDescriptor
	for _, spd := range metadata.SPSSODescriptors {
		keyDescriptors = append(keyDescriptors, spd.KeyDescriptors...)
	}
	return keyDescriptors
}

// SAMLMetadata serves the SAML Service Provider metadata file.
func (a *API) SAMLMetadata(w http.ResponseWriter, r *http.Request) error {
	serviceProvider := a.getSAMLServiceProvider(nil, true)

	metadata := serviceProvider.Metadata()

	if r.FormValue("download") == "true" {
		// 5 year expiration, comparable to what GSuite does
		metadata.ValidUntil = time.Now().UTC().AddDate(5, 0, 0)
	}

	keyDescriptors := a.samlKeyDescriptors(serviceProvider, metadata)
	for i := range metadata.SPSSODescriptors {
		// we set this to false since the IdP initiated flow can only
		// sign the Assertion, and not the full Request
		// unfortunately ServiceProvider.Metadata in github.com/supatype/saml
		// hardcodes this if signatures (instead of encryption) are supported
		metadata.SPSSODescriptors[i].AuthnRequestsSigned = nil

		// advertize the requested NameID formats (either persistent or email address)
		metadata.SPSSODescriptors[i].NameIDFormats = []saml.NameIDFormat{
			saml.EmailAddressNameIDFormat,
			saml.PersistentNameIDFormat,
		}

		metadata.SPSSODescriptors[i].KeyDescriptors = keyDescriptors
	}

	metadataXML, err := xml.Marshal(metadata)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Cache-Control", "public, max-age=600") // cache at CDN for 10 minutes

	if r.FormValue("download") == "true" {
		w.Header().Set("Content-Disposition", "attachment; filename=\"metadata.xml\"")
	}

	_, err = w.Write(metadataXML) // #nosec G705

	return err
}
