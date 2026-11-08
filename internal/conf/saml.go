package conf

import (
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

// SAMLConfiguration holds configuration for native SAML support.
type SAMLConfiguration struct {
	Enabled                  bool          `json:"enabled"`
	PrivateKey               string        `json:"-" split_words:"true"`
	AllowEncryptedAssertions bool          `json:"allow_encrypted_assertions" split_words:"true"`
	RelayStateValidityPeriod time.Duration `json:"relay_state_validity_period" split_words:"true"`

	// NextPrivateKey is the key a rotation moves to. While it is set the
	// metadata publishes its certificate beside the current one and assertions
	// encrypted to it are accepted, but requests are still signed with
	// PrivateKey. Promoting it means moving it to PrivateKey.
	NextPrivateKey string `json:"-" split_words:"true"`

	// The parsed keys and their certificates, filled by PopulateFields.
	// They are ignored by the environment parser, which would otherwise read
	// every field of an x509.Certificate from a variable and leave an empty
	// certificate behind where PopulateFields sets none.
	RSAPrivateKey *rsa.PrivateKey   `json:"-" ignored:"true"`
	Certificate   *x509.Certificate `json:"-" ignored:"true"`

	NextRSAPrivateKey *rsa.PrivateKey   `json:"-" ignored:"true"`
	NextCertificate   *x509.Certificate `json:"-" ignored:"true"`

	ExternalURL string `json:"external_url,omitempty" split_words:"true"`

	RateLimitAssertion float64 `default:"15" split_words:"true"`
}

func (c *SAMLConfiguration) GoString() string { return c.String() }
func (c *SAMLConfiguration) String() string {
	if c == nil {
		return "(*SAMLConfiguration)(nil)"
	}
	return fmt.Sprintf("SAMLConfiguration(Enabled: %v)", c.Enabled)
}

func (c *SAMLConfiguration) Validate() error {
	if !c.Enabled {
		return nil
	}

	if err := validateSAMLPrivateKey(c.PrivateKey); err != nil {
		return err
	}

	if c.hasNextPrivateKey() {
		if err := validateSAMLPrivateKey(c.NextPrivateKey); err != nil {
			return fmt.Errorf("SUPATYPE_SAML_NEXT_PRIVATE_KEY: %w", err)
		}
	}

	if c.RelayStateValidityPeriod < 0 {
		return errors.New("SAML RelayState validity period should be a positive duration")
	}

	if c.ExternalURL != "" {
		if _, err := url.ParseRequestURI(c.ExternalURL); err != nil {
			return err
		}
	}

	return nil
}

// hasNextPrivateKey reports whether a next key is set. Compose passes the
// variable on every start, empty when no rotation is in progress, so a blank
// value is no next key.
func (c *SAMLConfiguration) hasNextPrivateKey() bool {
	return strings.TrimSpace(c.NextPrivateKey) != ""
}

// validateSAMLPrivateKey checks encoded is a standard base64 PKCS#1 RSA key of
// at least 2048 bits with the 65537 public exponent.
func validateSAMLPrivateKey(encoded string) error {
	privateKey, err := parseSAMLPrivateKey(encoded)
	if err != nil {
		return err
	}

	if privateKey.E != 0x10001 {
		return errors.New("SAML private key should use the 65537 (0x10001) RSA public exponent")
	}

	if privateKey.N.BitLen() < 2048 {
		return errors.New("SAML private key must be at least RSA 2048")
	}

	return nil
}

// PopulateFields parses the private keys and derives the service provider
// certificate of each.
func (c *SAMLConfiguration) PopulateFields() error {
	privateKey, certificate, err := samlKeyAndCertificate(c.PrivateKey)
	if err != nil {
		return err
	}

	c.RSAPrivateKey = privateKey
	c.Certificate = certificate

	if c.hasNextPrivateKey() {
		nextKey, nextCertificate, err := samlKeyAndCertificate(c.NextPrivateKey)
		if err != nil {
			return err
		}
		// The next key equal to the current one is left over from a
		// promotion, not a rotation in progress.
		if !nextKey.Equal(privateKey) {
			c.NextRSAPrivateKey = nextKey
			c.NextCertificate = nextCertificate
		}
	}

	if c.RelayStateValidityPeriod == 0 {
		c.RelayStateValidityPeriod = 2 * time.Minute
	}
	return nil
}

func samlKeyAndCertificate(encoded string) (*rsa.PrivateKey, *x509.Certificate, error) {
	privateKey, err := parseSAMLPrivateKey(encoded)
	if err != nil {
		return nil, nil, err
	}

	certificate, err := samlCertificate(privateKey)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, certificate, nil
}

// parseSAMLPrivateKey decodes a standard base64 PKCS#1 RSA key. Validate and
// PopulateFields both parse with it, so a bad key reads the same either way.
func parseSAMLPrivateKey(encoded string) (*rsa.PrivateKey, error) {
	bytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("SAML private key not in standard Base64 format")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(bytes)
	if err != nil {
		return nil, errors.New("SAML private key not in PKCS#1 format")
	}
	return privateKey, nil
}

// samlCertificateTemplate is the fixed template of the service provider
// certificate. SAML uses the certificate only to carry the public key, and an
// identity provider pins the certificate as a whole, so nothing in it may
// depend on the host, the environment, the clock or any setting: the key
// alone decides the certificate. Changing a value here changes the published
// certificate and breaks every identity provider that trusts it.
//
// KeyUsage allows both uses the key has: signing AuthnRequests and receiving
// the symmetric key of an encrypted assertion (RSA key transport is key
// encipherment). It does not follow the encrypted-assertions setting, so
// switching that on or off leaves the certificate as it was.
func samlCertificateTemplate() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(0),
		IsCA:         false,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		NotBefore:    time.UnixMilli(0).UTC(),
		NotAfter:     time.UnixMilli(0).UTC().AddDate(200, 0, 0),
		Subject: pkix.Name{
			CommonName: "SAML 2.0 Service Provider",
		},
	}
}

// samlCertificate self-signs the fixed template with privateKey. RSA PKCS#1
// v1.5 signatures are deterministic, so the same key gives the same bytes.
func samlCertificate(privateKey *rsa.PrivateKey) (*x509.Certificate, error) {
	template := samlCertificateTemplate()
	certDer, err := x509.CreateCertificate(nil, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(certDer)
}
