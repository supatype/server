package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fatih/structs"
	"github.com/gofrs/uuid"
	"github.com/supatype/saml"
	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/auth/metering"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/provider"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/observability"
	"github.com/supatype/server/internal/utilities"
)

func (a *API) samlDestroyRelayState(ctx context.Context, relayState *models.SAMLRelayState) error {
	db := a.db.WithContext(ctx)

	// It's OK to destroy the RelayState, as a user will
	// likely initiate a completely new login flow, instead
	// of reusing the same one.

	return db.Transaction(func(tx *storage.Connection) error {
		return tx.Destroy(relayState)
	})
}

func IsSAMLMetadataStale(idpMetadata *saml.EntityDescriptor, samlProvider models.SAMLProvider) bool {
	now := time.Now()

	hasValidityExpired := !idpMetadata.ValidUntil.IsZero() && now.After(idpMetadata.ValidUntil)
	hasCacheDurationExceeded := idpMetadata.CacheDuration != 0 && now.After(samlProvider.UpdatedAt.Add(idpMetadata.CacheDuration))

	// if metadata XML does not publish validity or caching information, update once in 24 hours
	needsForceUpdate := idpMetadata.ValidUntil.IsZero() && idpMetadata.CacheDuration == 0 && now.After(samlProvider.UpdatedAt.Add(24*time.Hour))

	return hasValidityExpired || hasCacheDurationExceeded || needsForceUpdate
}

func (a *API) SamlAcs(w http.ResponseWriter, r *http.Request) error {
	if err := a.handleSamlAcs(w, r); err != nil {
		u, uerr := url.Parse(a.config.SiteURL)
		if uerr != nil {
			return apierrors.NewInternalServerError("site url is improperly formattted").WithInternalError(err)
		}

		q := getErrorQueryString(err, utilities.GetRequestID(r.Context()), observability.GetLogEntry(r).Entry, u.Query())
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.String(), http.StatusSeeOther)
	}
	return nil
}

// handleSamlAcs implements the main Assertion Consumer Service endpoint behavior.
func (a *API) handleSamlAcs(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	db := a.db.WithContext(ctx)
	config := a.config
	log := observability.GetLogEntry(r).Entry

	relayStateValue := r.FormValue("RelayState")
	relayStateUUID := uuid.FromStringOrNil(relayStateValue)
	relayStateURL, _ := url.ParseRequestURI(relayStateValue)

	entityId := ""
	initiatedBy := ""
	redirectTo := ""
	var requestIds []string

	var flowState *models.FlowState
	if relayStateUUID != uuid.Nil {
		// relay state is a valid UUID, therefore this is likely a SP initiated flow

		relayState, err := models.FindSAMLRelayStateByID(db, relayStateUUID)
		if models.IsNotFoundError(err) {
			return apierrors.NewNotFoundError(apierrors.ErrorCodeSAMLRelayStateNotFound, "SAML RelayState does not exist, try logging in again?")
		} else if err != nil {
			return err
		}

		if time.Since(relayState.CreatedAt) >= a.config.SAML.RelayStateValidityPeriod {
			if err := a.samlDestroyRelayState(ctx, relayState); err != nil {
				return apierrors.NewInternalServerError("SAML RelayState has expired and destroying it failed. Try logging in again?").WithInternalError(err)
			}

			return apierrors.NewUnprocessableEntityError(apierrors.ErrorCodeSAMLRelayStateExpired, "SAML RelayState has expired. Try logging in again?")
		}

		// TODO: add abuse detection to bind the RelayState UUID with a
		// HTTP-Only cookie

		ssoProvider, err := models.FindSSOProviderByID(db, relayState.SSOProviderID)
		if err != nil {
			return apierrors.NewInternalServerError("Unable to find SSO Provider from SAML RelayState")
		}

		if !ssoProvider.IsEnabled() {
			return apierrors.NewNotFoundError(
				apierrors.ErrorCodeSSOProviderDisabled,
				"SSO Provider assigned for this domain is currently disabled")
		}

		initiatedBy = "sp"
		entityId = ssoProvider.SAMLProvider.EntityID
		redirectTo = relayState.RedirectTo
		requestIds = append(requestIds, relayState.RequestID)
		if relayState.FlowState != nil {
			flowState = relayState.FlowState
		}

		if err := a.samlDestroyRelayState(ctx, relayState); err != nil {
			return err
		}
	} else if relayStateValue == "" || relayStateURL != nil {
		// RelayState may be a URL in which case it's the URL where the
		// IdP is telling us to redirect the user to

		if r.FormValue("SAMLart") != "" {
			// SAML Artifact responses are possible only when
			// RelayState can be used to identify the Identity
			// Provider.
			return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAML Artifact response can only be used with SP initiated flow")
		}

		samlResponse := r.FormValue("SAMLResponse")
		if samlResponse == "" {
			return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAMLResponse is missing")
		}

		responseXML, err := base64.StdEncoding.DecodeString(samlResponse)
		if err != nil {
			return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAMLResponse is not a valid Base64 string")
		}

		entityId, err = samlResponseIssuer(responseXML)
		if err != nil {
			return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAMLResponse is not a valid XML SAML assertion").WithInternalError(err)
		}

		initiatedBy = "idp"
		redirectTo = relayStateValue
	} else {
		// RelayState can't be identified, so SAML flow can't continue
		return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAML RelayState is not a valid UUID or URL")
	}

	ssoProvider, err := models.FindSAMLProviderByEntityID(db, entityId)
	if models.IsNotFoundError(err) {
		return apierrors.NewNotFoundError(apierrors.ErrorCodeSAMLIdPNotFound, "A SAML connection has not been established with this Identity Provider")
	} else if err != nil {
		return err
	}

	if !ssoProvider.IsEnabled() {
		return apierrors.NewNotFoundError(
			apierrors.ErrorCodeSSOProviderDisabled,
			"SSO Provider assigned for this domain is currently disabled")
	}

	idpMetadata, err := ssoProvider.SAMLProvider.EntityDescriptor()
	if err != nil {
		return err
	}

	samlMetadataModified := false

	if ssoProvider.SAMLProvider.MetadataURL == nil {
		if !idpMetadata.ValidUntil.IsZero() && time.Until(idpMetadata.ValidUntil) <= (30*24*60)*time.Second {
			logentry := log.WithField("sso_provider_id", ssoProvider.ID.String())
			logentry = logentry.WithField("expires_in", time.Until(idpMetadata.ValidUntil).String())
			logentry = logentry.WithField("valid_until", idpMetadata.ValidUntil)
			logentry = logentry.WithField("saml_entity_id", ssoProvider.SAMLProvider.EntityID)

			logentry.Warn("SAML Metadata for identity provider will expire soon! Update its metadata_xml!")
		}
	} else if *ssoProvider.SAMLProvider.MetadataURL != "" && IsSAMLMetadataStale(idpMetadata, ssoProvider.SAMLProvider) {
		rawMetadata, err := fetchSAMLMetadata(ctx, *ssoProvider.SAMLProvider.MetadataURL)
		if err != nil {
			// Fail silently but raise warning and continue with existing metadata
			logentry := log.WithField("sso_provider_id", ssoProvider.ID.String())
			logentry = logentry.WithField("expires_in", time.Until(idpMetadata.ValidUntil).String())
			logentry = logentry.WithField("valid_until", idpMetadata.ValidUntil)
			logentry = logentry.WithError(err)
			logentry.Warn("SAML Metadata could not be retrieved, continuing with existing metadata")
		} else {
			ssoProvider.SAMLProvider.MetadataXML = string(rawMetadata)
			samlMetadataModified = true
		}
	}

	serviceProvider := a.getSAMLServiceProvider(idpMetadata, initiatedBy == "idp")
	// Every caller of ParseResponse must set a ReplayCache, or replays pass.
	serviceProvider.ReplayCache = samlReplayCache{db: db, ssoProviderID: ssoProvider.ID}
	// The library decrypts with the next key too while a rotation is in
	// progress (see getSAMLServiceProvider), on every binding.
	spAssertion, err := serviceProvider.ParseResponse(r, requestIds)
	if err != nil {
		// The error is sent back to the browser in a redirect URL, so it names
		// no part of the response: the whole signed response would land in
		// browser history and access logs, and can exceed URL length limits.
		// The reason the library refused it is logged as the internal error.
		err = samlPrivateError(err)
		if errors.Is(err, saml.ErrAssertionReplayed) {
			return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAML Assertion has already been used").WithInternalError(err)
		}

		return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAML Assertion is not valid").WithInternalError(err)
	}

	assertion := SAMLAssertion{
		spAssertion,
	}

	userID := assertion.UserID()
	if userID == "" {
		return apierrors.NewBadRequestError(apierrors.ErrorCodeSAMLAssertionNoUserID, "SAML Assertion did not contain a persistent Subject Identifier attribute or Subject NameID uniquely identifying this user")
	}

	claims := assertion.Process(ssoProvider.SAMLProvider.AttributeMapping)

	email, ok := claims["email"].(string)
	if !ok || email == "" {
		// mapping does not identify the email attribute, try to figure it out
		email = assertion.Email()
	}

	if email == "" {
		return apierrors.NewBadRequestError(apierrors.ErrorCodeSAMLAssertionNoEmail, "SAML Assertion does not contain an email address")
	}
	// A provider with domains may only sign in addresses on them; without
	// this it could sign in as any address, another organisation's included.
	// The provider's domains are loaded with it.
	emailDomain := ssoProvider.DomainOf(email)
	if len(ssoProvider.SSODomains) > 0 && emailDomain == nil {
		return apierrors.NewBadRequestError(apierrors.ErrorCodeSAMLEmailDomainNotAllowed, "SAML Assertion email is not on a domain of this identity provider")
	}
	claims["email"] = email

	jsonClaims, err := json.Marshal(claims)
	if err != nil {
		return apierrors.NewInternalServerError("Mapped claims from provider could not be serialized into JSON").WithInternalError(err)
	}

	providerClaims := &provider.Claims{}
	if err := json.Unmarshal(jsonClaims, providerClaims); err != nil {
		return apierrors.NewInternalServerError("Mapped claims from provider could not be deserialized from JSON").WithInternalError(err)
	}

	providerClaims.Subject = userID
	providerClaims.Issuer = ssoProvider.SAMLProvider.EntityID
	providerClaims.Email = email
	providerClaims.EmailVerified = true

	providerClaimsMap := structs.Map(providerClaims)

	// remove all of the parsed claims, so that the rest can go into CustomClaims
	for key := range providerClaimsMap {
		delete(claims, key)
	}

	providerClaims.CustomClaims = claims

	var userProvidedData provider.UserProvidedData

	userProvidedData.Emails = append(userProvidedData.Emails, provider.Email{
		Email:    email,
		Verified: true,
		Primary:  true,
	})
	// A first login may link to an existing account with the email only on
	// a verified domain.
	userProvidedData.SSODomainVerified = emailDomain != nil && emailDomain.IsVerified()

	// userProvidedData.Provider.Type = "saml"
	// userProvidedData.Provider.ID = ssoProvider.ID.String()
	// userProvidedData.Provider.SAMLEntityID = ssoProvider.SAMLProvider.EntityID
	// userProvidedData.Provider.SAMLInitiatedBy = initiatedBy

	userProvidedData.Metadata = providerClaims

	// TODO: below
	// refreshTokenParams.SSOProviderID = ssoProvider.ID
	// refreshTokenParams.InitiatedByProvider = initiatedBy == "idp"
	// refreshTokenParams.NotBefore = assertion.NotBefore()
	// refreshTokenParams.NotAfter = assertion.NotAfter()

	notAfter := assertion.NotAfter()

	var grantParams models.GrantParams

	grantParams.FillGrantParams(r)

	if !notAfter.IsZero() {
		grantParams.SessionNotAfter = &notAfter
	}

	var token *AccessTokenResponse
	if samlMetadataModified {
		if err := db.UpdateColumns(&ssoProvider.SAMLProvider, "metadata_xml", "updated_at"); err != nil {
			return err
		}
	}

	providerType := "sso:" + ssoProvider.ID.String()
	if err := a.triggerBeforeUserCreatedExternal(
		r, db, &userProvidedData, providerType); err != nil {
		return err
	}

	var createdUser bool
	var user *models.User
	if err := db.Transaction(func(tx *storage.Connection) error {
		var terr error

		// accounts potentially created via SAML can contain non-unique email addresses in the auth.users table
		var decision models.AccountLinkingDecision
		if decision, user, terr = a.createAccountFromExternalIdentity(tx, r, &userProvidedData, providerType, false); terr != nil {
			return terr
		}
		createdUser = decision == models.CreateAccount

		if flowState != nil && flowState.IsPKCE() {
			// PKCE flow: update flow state with user ID
			flowState.UserID = &(user.ID)
			if terr := tx.Update(flowState); terr != nil {
				return terr
			}
		}

		token, terr = a.issueRefreshToken(r, w.Header(), tx, user, models.SSOSAML, grantParams)

		if terr != nil {
			return apierrors.NewInternalServerError("Unable to issue refresh token from SAML Assertion").WithInternalError(terr)
		}

		return nil
	}); err != nil {
		return err
	}
	if createdUser {
		if err := a.triggerAfterUserCreated(r, db, user); err != nil {
			return err
		}
	}

	if !utilities.IsRedirectURLValid(config, redirectTo) {
		redirectTo = config.SiteURL
	}
	if flowState != nil && flowState.IsPKCE() {
		// PKCE flow: redirect with auth code
		redirectTo, err = a.prepPKCERedirectURL(redirectTo, *flowState.AuthCode)
		if err != nil {
			return err
		}
		redirectFound(w, r, redirectTo)
		return nil
	}

	// Record login for analytics - only when token is issued (not during pkce authorize)
	if token != nil {
		metering.RecordLogin(metering.LoginTypeSSO, token.User.ID, &metering.LoginData{
			Provider: metering.ProviderSAML,
		})
	}

	redirectFound(w, r, token.AsRedirectURL(redirectTo, url.Values{}))

	return nil
}

// samlReplayCache records a provider's accepted assertions in the database,
// for the SAML library's replay check. Every server instance shares it.
type samlReplayCache struct {
	db            *storage.Connection
	ssoProviderID uuid.UUID
}

func (c samlReplayCache) Consume(assertionID string, expiresAt time.Time) error {
	return models.ConsumeSAMLAssertion(c.db, c.ssoProviderID, assertionID, expiresAt)
}

// samlPrivateError is the reason inside the library's InvalidResponseError,
// whose own message is deliberately uninformative.
func samlPrivateError(err error) error {
	var ire *saml.InvalidResponseError
	if errors.As(err, &ire) && ire.PrivateErr != nil {
		return ire.PrivateErr
	}
	return err
}

func samlResponseIssuer(responseXML []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(responseXML)) // #nosec G709 -- token scan only; the SAML response is fully validated by github.com/supatype/saml after provider lookup.
	for {
		tok, err := decoder.Token()
		if err != nil {
			return "", err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "Issuer" {
			continue
		}

		var issuer strings.Builder
		for {
			tok, err := decoder.Token()
			if err != nil {
				return "", err
			}
			switch t := tok.(type) {
			case xml.CharData:
				issuer.Write([]byte(t))
			case xml.EndElement:
				if t.Name.Local == start.Name.Local {
					return strings.TrimSpace(issuer.String()), nil
				}
			}
		}
	}
}
