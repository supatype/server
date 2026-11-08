package auth

// Verification of the email domains an SSO provider claims. The administrator
// publishes a DNS TXT record _supatype-challenge.<domain> holding a per-domain
// token; once the record is seen the domain is verified and stays verified.
// Only a verified domain links a SAML login to an existing account with the
// same email.

import (
	"context"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/auth/crypto"
	"github.com/supatype/server/internal/auth/models"
)

// lookupTXT resolves DNS TXT records. Tests replace it.
var lookupTXT = net.DefaultResolver.LookupTXT

const (
	domainChallengePrefix = "_supatype-challenge."
	txtLookupTimeout      = 10 * time.Second
)

// DomainChallenge is the DNS TXT record that proves a domain.
type DomainChallenge struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DomainVerificationResponse is the answer of the verify endpoint: the domain
// as every admin response shows it, and its challenge. Found is what DNS
// returned at the challenge name, nil when no lookup was made.
type DomainVerificationResponse struct {
	models.SSODomainView
	Challenge DomainChallenge `json:"challenge"`
	Found     []string        `json:"found"`
}

// ssoProviderDomain is the domain in the URL, which must be one of the
// provider's.
func ssoProviderDomain(r *http.Request) (*models.SSODomain, error) {
	name := chi.URLParam(r, "domain")
	if domain := getSSOProvider(r.Context()).Domain(name); domain != nil {
		return domain, nil
	}
	return nil, apierrors.NewNotFoundError(apierrors.ErrorCodeSSODomainNotFound, "Domain %q is not a domain of this SSO provider", name)
}

// adminSSODomainVerify issues the domain's verification token on the first
// call and checks DNS on every call. A domain whose TXT record holds the token
// is verified and stays verified, so the record may be removed afterwards.
// The token is kept once issued, so a record already published stays valid.
// What changed is saved in one write.
func (a *API) adminSSODomainVerify(w http.ResponseWriter, r *http.Request) error {
	domain, err := ssoProviderDomain(r)
	if err != nil {
		return err
	}

	var changed []string
	if domain.VerificationToken == nil {
		token := newDomainVerificationToken()
		domain.VerificationToken = &token
		changed = append(changed, "verification_token")
	}
	verified, found := a.checkDomainRecord(r.Context(), domain)
	if verified && !domain.IsVerified() {
		now := a.Now().UTC()
		domain.VerifiedAt = &now
		changed = append(changed, "verified_at")
	}
	if len(changed) > 0 {
		if err := a.db.WithContext(r.Context()).UpdateOnly(domain, append(changed, "updated_at")...); err != nil {
			return apierrors.NewInternalServerError("Unable to save the domain verification").WithInternalError(err)
		}
	}

	return sendJSON(w, http.StatusOK, DomainVerificationResponse{
		SSODomainView: domain.View(),
		Challenge:     DomainChallenge{Name: domainChallengeName(domain), Value: *domain.VerificationToken},
		Found:         found,
	})
}

func domainChallengeName(domain *models.SSODomain) string {
	return domainChallengePrefix + strings.ToLower(domain.Domain)
}

// checkDomainRecord reports whether the domain's TXT record holds its token,
// and the values DNS returned. A domain the SAML dev policy verifies passes
// with no lookup, and found is nil. A failed lookup is a record not found.
func (a *API) checkDomainRecord(ctx context.Context, domain *models.SSODomain) (bool, []string) {
	if a.samlDev.verifiesWithoutLookup(domain.Domain) {
		return true, nil
	}

	ctx, cancel := context.WithTimeout(ctx, txtLookupTimeout)
	defer cancel()
	found, err := lookupTXT(ctx, domainChallengeName(domain))
	if err != nil || found == nil {
		found = []string{}
	}
	matches := slices.ContainsFunc(found, func(v string) bool { return strings.TrimSpace(v) == *domain.VerificationToken })
	return matches, found
}

// newDomainVerificationToken is a fresh token: 52 random base32 characters,
// 260 bits.
func newDomainVerificationToken() string {
	return "supatype-domain-verification=" + crypto.SecureAlphanumeric(52)
}
