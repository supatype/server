package auth

import (
	"net/http"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/storage"
)

// identitiesBeforeSSOLink loads the account's identities when providerType
// is an SSO provider, before the new identity is created, so one load answers
// both whether the link is the first through that provider and which ways in
// it closes. It is nil for any other provider, which needs neither.
func identitiesBeforeSSOLink(tx *storage.Connection, user *models.User, providerType string) ([]*models.Identity, error) {
	if !strings.HasPrefix(providerType, "sso:") {
		return nil, nil
	}
	identities, err := models.FindIdentitiesByUserID(tx, user.ID)
	if err != nil {
		return nil, apierrors.NewInternalServerError("Database error loading identities").WithInternalError(err)
	}
	return identities, nil
}

// isFirstSSOLink reports whether providerType is an SSO provider none of the
// account's identities is for yet, so linking it is the account's first
// sign-in through that provider.
func isFirstSSOLink(identities []*models.Identity, providerType string) bool {
	if !strings.HasPrefix(providerType, "sso:") {
		return false
	}
	for _, identity := range identities {
		if identity.Provider == providerType {
			return false
		}
	}
	return true
}

// secureAccountForSSOLink runs when an SSO identity is linked to an existing
// account for the first time. The account may have been registered by someone
// who never owned the address (any stack that auto-confirms sign-ups allows
// it), so every way in set up before the link is closed and every session
// signed out: from now on the account signs in with SSO, or with an OAuth
// identity whose provider verified its email. 0.9.1 release test (RB1).
// priorIdentities are the account's identities from before identity was
// created.
func (a *API) secureAccountForSSOLink(tx *storage.Connection, r *http.Request, user *models.User, identity *models.Identity, priorIdentities []*models.Identity) error {
	providerType := identity.Provider
	cleanup, err := user.RemoveCredentialsForSSOLink(tx, identity, priorIdentities)
	if err != nil {
		return apierrors.NewInternalServerError("Error removing credentials for SSO").WithInternalError(err)
	}

	if err := models.NewAuditLogEntry(a.config.AuditLog, r, tx, user, models.SSOIdentityLinkedAction, "", map[string]interface{}{
		"provider":           providerType,
		"removed_identities": cleanup.RemovedIdentities,
		"password_removed":   cleanup.PasswordRemoved,
		"phone_removed":      cleanup.PhoneRemoved,
	}); err != nil {
		return err
	}

	// Sent whatever the identity linked notification setting says: the address
	// has to learn that its password and other ways in are gone.
	if user.GetEmail() != "" {
		if err := a.sendSSOLinkedNotification(r, tx, user, ssoProviderName(tx, providerType, user.GetEmail())); err != nil {
			// As with manual linking, a failed notification does not fail the sign-in.
			logrus.WithError(err).Warn("Unable to send SSO linked notification email")
		}
	}
	return nil
}

// ssoProviderName names the SSO provider of providerType ("sso:<id>") for a
// person to read: the provider's domain that email is on, else its resource
// ID, else its first domain. It is empty when none of those is known, never
// the provider's ID.
func ssoProviderName(tx *storage.Connection, providerType, email string) string {
	id, err := uuid.FromString(strings.TrimPrefix(providerType, "sso:"))
	if err != nil {
		return ""
	}
	provider, err := models.FindSSOProviderByID(tx, id)
	if err != nil {
		return ""
	}
	if domain := provider.DomainOf(email); domain != nil {
		return domain.Domain
	}
	if provider.ResourceID != nil && *provider.ResourceID != "" {
		return *provider.ResourceID
	}
	if len(provider.SSODomains) > 0 {
		return provider.SSODomains[0].Domain
	}
	return ""
}
