package auth

import (
	"strings"
	"time"

	"github.com/supatype/saml"
	"github.com/supatype/server/internal/auth/models"
)

type SAMLAssertion struct {
	*saml.Assertion
}

const (
	SAMLSubjectIDAttributeName = "urn:oasis:names:tc:SAML:attribute:subject-id"
)

// Attribute returns the first matching attribute value in the attribute
// statements where name equals the official SAML attribute Name or
// FriendlyName. Returns nil if such an attribute can't be found.
func (a *SAMLAssertion) Attribute(name string) []saml.AttributeValue {
	var values []saml.AttributeValue

	for _, stmt := range a.AttributeStatements {
		for _, attr := range stmt.Attributes {
			if strings.EqualFold(attr.Name, name) || strings.EqualFold(attr.FriendlyName, name) {
				values = append(values, attr.Values...)
			}
		}
	}

	return values
}

// UserID returns the best choice for a persistent user identifier on the
// Identity Provider side. Don't assume the format of the string returned, as
// it's Identity Provider specific.
func (a *SAMLAssertion) UserID() string {
	// First we look up the SAMLSubjectIDAttributeName in the attribute
	// section of the assertion, as this is the preferred way to
	// persistently identify users in SAML 2.0.
	// See: https://docs.oasis-open.org/security/saml-subject-id-attr/v1.0/cs01/saml-subject-id-attr-v1.0-cs01.html#_Toc536097226
	values := a.Attribute(SAMLSubjectIDAttributeName)
	if len(values) > 0 {
		return values[0].Value
	}

	// Otherwise, fall back to the SubjectID value.
	subjectID, isPersistent := a.SubjectID()
	if !isPersistent {
		return ""
	}

	return subjectID
}

// SubjectID returns the user identifier in present in the Subject section of
// the SAML assertion. Note that this way of identifying the Subject is
// generally superseded by the SAMLSubjectIDAttributeName assertion attribute;
// tho must be present in all assertions. It can have a few formats, of which
// the most important are: saml.EmailAddressNameIDFormat (meaning the user ID
// is an email address), saml.PersistentNameIDFormat (the user ID is an opaque
// string that does not change with each assertion, e.g. UUID),
// saml.TransientNameIDFormat (the user ID changes with each assertion -- can't
// be used to identify a user). The boolean returned identifies if the user ID
// is persistent. If it's an email address, it's lowercased just in case.
func (a *SAMLAssertion) SubjectID() (string, bool) {
	nameID := a.nameID()
	if nameID == nil || nameID.Value == "" {
		return "", false
	}

	if nameID.Format == string(saml.EmailAddressNameIDFormat) {
		return strings.ToLower(strings.TrimSpace(nameID.Value)), true
	}

	// all other NameID formats are regarded as persistent
	isPersistent := nameID.Format != string(saml.TransientNameIDFormat)

	return nameID.Value, isPersistent
}

// nameID returns the subject's NameID, or nil. Subject and NameID are both
// optional: an assertion can identify the user with the subject-id attribute
// alone.
func (a *SAMLAssertion) nameID() *saml.NameID {
	if a.Subject == nil {
		return nil
	}
	return a.Subject.NameID
}

// Email returns the best guess for an email address.
func (a *SAMLAssertion) Email() string {
	attributeNames := []string{
		"urn:oid:0.9.2342.19200300.100.1.3",
		"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress",
		"http://schemas.xmlsoap.org/claims/EmailAddress",
		"mail",
		"Mail",
		"email",
	}

	for _, name := range attributeNames {
		for _, attr := range a.Attribute(name) {
			if attr.Value != "" {
				return attr.Value
			}
		}
	}

	if nameID := a.nameID(); nameID != nil && nameID.Format == string(saml.EmailAddressNameIDFormat) {
		return nameID.Value
	}

	return ""
}

// Process processes this assertion according to the SAMLAttributeMapping. Never returns nil.
func (a *SAMLAssertion) Process(mapping models.SAMLAttributeMapping) map[string]interface{} {
	ret := make(map[string]interface{})

	for key, mapper := range mapping.Keys {
		names := []string{}
		if mapper.Name != "" {
			names = append(names, mapper.Name)
		}
		names = append(names, mapper.Names...)

		setKey := false

		for _, name := range names {
			for _, attr := range a.Attribute(name) {
				if attr.Value != "" {
					setKey = true

					if mapper.Array {
						if ret[key] == nil {
							ret[key] = []string{}
						}

						ret[key] = append(ret[key].([]string), attr.Value)
					} else {
						ret[key] = attr.Value
						break
					}
				}
			}

			if setKey {
				break
			}
		}

		if !setKey && mapper.Default != nil {
			ret[key] = mapper.Default
		}
	}

	return ret
}

// NotBefore extracts the time before which this assertion should not be
// considered.
func (a *SAMLAssertion) NotBefore() time.Time {
	if a.Conditions != nil && !a.Conditions.NotBefore.IsZero() {
		return a.Conditions.NotBefore.UTC()
	}

	return time.Time{}
}

// NotAfter extracts the time at which or after this assertion should not be
// considered.
func (a *SAMLAssertion) NotAfter() time.Time {
	var notOnOrAfter time.Time

	for _, statement := range a.AuthnStatements {
		if statement.SessionNotOnOrAfter == nil {
			continue
		}

		notOnOrAfter = *statement.SessionNotOnOrAfter
		if !notOnOrAfter.IsZero() {
			break
		}
	}

	return notOnOrAfter
}

// AcceptableUntil returns a time after which the SAML library refuses this
// assertion: the latest NotOnOrAfter of its Conditions and subject
// confirmations, plus the clock skew the library allows. A record of the
// assertion kept until then outlives every chance to replay it.
func (a *SAMLAssertion) AcceptableUntil() time.Time {
	var latest time.Time

	if a.Conditions != nil {
		latest = a.Conditions.NotOnOrAfter
	}
	if a.Subject != nil {
		for _, confirmation := range a.Subject.SubjectConfirmations {
			data := confirmation.SubjectConfirmationData
			if data != nil && data.NotOnOrAfter.After(latest) {
				latest = data.NotOnOrAfter
			}
		}
	}

	return latest.Add(saml.MaxClockSkew).UTC()
}
