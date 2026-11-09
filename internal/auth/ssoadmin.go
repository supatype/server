package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/gofrs/uuid"
	"github.com/supatype/saml"
	"github.com/supatype/saml/samlsp"
	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/observability"
	"github.com/supatype/server/internal/utilities"
)

// loadSSOProvider looks for an idp_id and first checks it for a "resource_"
// prefix, if present the provider is loaded by resource_id. Otherwise the
// provider is loaded by id.
func (a *API) loadSSOProvider(w http.ResponseWriter, r *http.Request) (context.Context, error) {
	ctx := r.Context()
	db := a.db.WithContext(ctx)

	var (
		provider *models.SSOProvider
		err      error
	)

	const resourcePrefix = "resource_"
	idpParam := chi.URLParam(r, "idp_id")
	switch {
	case strings.HasPrefix(idpParam, resourcePrefix):
		resourceID := strings.TrimPrefix(idpParam, resourcePrefix)
		provider, err = models.FindSSOProviderByResourceID(db, resourceID)
	default:
		idpID, idpErr := uuid.FromString(idpParam)
		if idpErr != nil {
			return nil, apierrors.NewNotFoundError(apierrors.ErrorCodeSSOProviderNotFound, "SSO Identity Provider not found")
		}
		provider, err = models.FindSSOProviderByID(db, idpID)
	}

	if err != nil {
		if models.IsNotFoundError(err) {
			return nil, apierrors.NewNotFoundError(apierrors.ErrorCodeSSOProviderNotFound, "SSO Identity Provider not found")
		} else {
			return nil, apierrors.NewInternalServerError("Database error finding SSO Identity Provider").WithInternalError(err)
		}
	}

	observability.LogEntrySetField(r, "sso_provider_id", provider.ID.String())
	return withSSOProvider(r.Context(), provider), nil
}

// adminSSOProvidersList lists all SSO Identity Providers in the system. Does
// not deal with pagination at this time.
func (a *API) adminSSOProvidersList(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	db := a.db.WithContext(ctx)

	providers, err := models.FindAllSSOProvidersByFilter(db, r.URL.Query())
	if err != nil {
		return err
	}

	for i := range providers {
		// remove metadata XML so that the returned JSON is not ginormous
		providers[i].SAMLProvider.MetadataXML = ""
	}

	return sendJSON(w, http.StatusOK, map[string]interface{}{
		"items": providers,
	})
}

type CreateSSOProviderParams struct {
	Type string `json:"type"`

	MetadataURL      string                      `json:"metadata_url"`
	MetadataXML      string                      `json:"metadata_xml"`
	Domains          []string                    `json:"domains"`
	AttributeMapping models.SAMLAttributeMapping `json:"attribute_mapping"`
	// NameIDFormat is nil when the request leaves it out, which on update
	// keeps the stored format; an empty value clears it.
	NameIDFormat *string `json:"name_id_format"`

	ResourceID *string `json:"resource_id,omitempty"`
	Disabled   *bool   `json:"disabled,omitempty"`
}

// validate checks the parameters. The dev policy decides which metadata_url
// schemes are accepted.
func (p *CreateSSOProviderParams) validate(forUpdate bool, dev samlDevPolicy) error {
	if !forUpdate && p.Type != "saml" {
		return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "Only 'saml' supported for SSO provider type")
	} else if p.MetadataURL != "" && p.MetadataXML != "" {
		return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "Only one of metadata_xml or metadata_url needs to be set")
	} else if !forUpdate && p.MetadataURL == "" && p.MetadataXML == "" {
		return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "Either metadata_xml or metadata_url must be set")
	} else if p.MetadataURL != "" {
		metadataURL, err := url.ParseRequestURI(p.MetadataURL)
		if err != nil {
			return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "metadata_url is not a valid URL")
		}

		if !dev.allowsMetadataScheme(metadataURL.Scheme) {
			return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "metadata_url is not a HTTPS URL")
		}
	}

	switch utilities.StringValue(p.NameIDFormat) {
	case "",
		string(saml.PersistentNameIDFormat),
		string(saml.EmailAddressNameIDFormat),
		string(saml.TransientNameIDFormat),
		string(saml.UnspecifiedNameIDFormat):
		// it's valid

	default:
		return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "name_id_format must be unspecified or one of %v", strings.Join([]string{
			string(saml.PersistentNameIDFormat),
			string(saml.EmailAddressNameIDFormat),
			string(saml.TransientNameIDFormat),
			string(saml.UnspecifiedNameIDFormat),
		}, ", "))
	}

	return nil
}

func (p *CreateSSOProviderParams) metadata(ctx context.Context) ([]byte, *saml.EntityDescriptor, error) {
	rawMetadata := []byte(p.MetadataXML)
	if p.MetadataURL != "" {
		var err error
		if rawMetadata, err = fetchSAMLMetadata(ctx, p.MetadataURL); err != nil {
			return nil, nil, err
		}
	}

	metadata, err := parseSAMLMetadata(rawMetadata)
	var parseErr *samlMetadataParseError
	if errors.As(err, &parseErr) {
		return nil, nil, p.notMetadataError().WithInternalError(parseErr.err)
	} else if err != nil {
		return nil, nil, err
	}

	return rawMetadata, metadata, nil
}

// notMetadataError says the metadata document is not SAML metadata XML, naming
// the URL it was fetched from when it was fetched.
func (p *CreateSSOProviderParams) notMetadataError() *apierrors.HTTPError {
	if p.MetadataURL != "" {
		return samlMetadataFetchError(p.MetadataURL, "the response is not SAML metadata XML")
	}
	return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "metadata_xml is not SAML metadata XML")
}

// samlMetadataParseError is a metadata document that is not SAML metadata XML
// at all. The caller turns it into a response that says where the document
// came from.
type samlMetadataParseError struct{ err error }

func (e *samlMetadataParseError) Error() string { return e.err.Error() }

func parseSAMLMetadata(rawMetadata []byte) (*saml.EntityDescriptor, error) {
	if !utf8.Valid(rawMetadata) {
		return nil, apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAML Metadata XML contains invalid UTF-8 characters, which are not supported at this time")
	}

	metadata, err := samlsp.ParseMetadata(rawMetadata)
	if err != nil {
		return nil, &samlMetadataParseError{err: err}
	}

	if metadata.EntityID == "" {
		return nil, apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAML Metadata does not contain an EntityID")
	}

	if len(metadata.IDPSSODescriptors) < 1 {
		return nil, apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAML Metadata does not contain any IDPSSODescriptor")
	}

	if len(metadata.IDPSSODescriptors) > 1 {
		return nil, apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "SAML Metadata contains multiple IDPSSODescriptors")
	}

	return metadata, nil
}

// samlMetadataFetchTimeout bounds one fetch of a metadata_url, so an address
// that drops packets fails the request instead of holding it open.
var samlMetadataFetchTimeout = 10 * time.Second

// fetchSAMLMetadata fetches metadataURL from this server. Every failure is a
// saml_metadata_fetch_failed error naming the host and a short reason; the
// full error is kept as the internal error, which is logged.
func fetchSAMLMetadata(ctx context.Context, metadataURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, samlMetadataFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, samlMetadataFetchError(metadataURL, "the URL could not be requested").WithInternalError(err)
	}

	req.Header.Set("Accept", "application/xml;charset=UTF-8")
	req.Header.Set("Accept-Charset", "UTF-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, samlMetadataFetchError(metadataURL, samlMetadataFetchReason(err)).WithInternalError(err)
	}

	defer utilities.SafeClose(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, samlMetadataFetchError(metadataURL, fmt.Sprintf("it answered with HTTP status %d", resp.StatusCode))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, samlMetadataFetchError(metadataURL, samlMetadataFetchReason(err)).WithInternalError(err)
	}

	return data, nil
}

// samlMetadataFetchError says the server could not get SAML metadata from
// metadataURL. It names only the host: the path and query can carry tokens.
func samlMetadataFetchError(metadataURL, reason string) *apierrors.HTTPError {
	host := "metadata_url"
	if parsed, err := url.Parse(metadataURL); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	return apierrors.NewBadRequestError(apierrors.ErrorCodeSAMLMetadataFetchFailed,
		"The server could not fetch SAML metadata from %s: %s. The server fetches metadata_url itself, so the URL must be reachable from the server, not only from your machine", host, reason)
}

// samlMetadataFetchReason names why a request to a metadata URL failed,
// without any detail from the response.
func samlMetadataFetchReason(err error) string {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var netErr net.Error
	switch {
	case errors.Is(err, errConnectionRefused):
		return "connection refused"
	case errors.As(err, &dnsErr):
		return "the host name could not be resolved"
	case errors.As(err, &certErr):
		return "its TLS certificate is not trusted"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return fmt.Sprintf("timed out after %s", samlMetadataFetchTimeout)
	default:
		return "the request failed"
	}
}

// adminSSOProvidersCreate creates a new SAML Identity Provider in the system.
func (a *API) adminSSOProvidersCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	db := a.db.WithContext(ctx)

	params := &CreateSSOProviderParams{}
	if err := retrieveRequestParams(r, params); err != nil {
		return err
	}

	if err := params.validate(false /* <- forUpdate */, a.samlDev); err != nil {
		return err
	}

	rawMetadata, metadata, err := params.metadata(ctx)
	if err != nil {
		return err
	}

	existingProvider, err := models.FindSAMLProviderByEntityID(db, metadata.EntityID)
	if err != nil && !models.IsNotFoundError(err) {
		return err
	}
	if existingProvider != nil {
		return apierrors.NewUnprocessableEntityError(apierrors.ErrorCodeSAMLIdPAlreadyExists, "SAML Identity Provider with this EntityID (%s) already exists", metadata.EntityID)
	}

	provider := &models.SSOProvider{

		// TODO handle Name, Description, Attribute Mapping
		SAMLProvider: models.SAMLProvider{
			EntityID:    metadata.EntityID,
			MetadataXML: string(rawMetadata),
		},
	}

	if params.ResourceID != nil {
		provider.ResourceID = params.ResourceID
	}
	if params.Disabled != nil {
		provider.Disabled = params.Disabled
	}
	if params.MetadataURL != "" {
		provider.SAMLProvider.MetadataURL = &params.MetadataURL
	}
	provider.SAMLProvider.NameIDFormat = utilities.StringPtr(utilities.StringValue(params.NameIDFormat))

	provider.SAMLProvider.AttributeMapping = params.AttributeMapping

	for _, domain := range params.Domains {
		existingProvider, err := models.FindSSOProviderByDomain(db, domain)
		if err != nil && !models.IsNotFoundError(err) {
			return err
		}
		if existingProvider != nil {
			return apierrors.NewBadRequestError(apierrors.ErrorCodeSSODomainAlreadyExists, "SSO Domain '%s' is already assigned to an SSO identity provider (%s)", domain, existingProvider.ID.String())
		}

		provider.SSODomains = append(provider.SSODomains, models.SSODomain{
			Domain: domain,
		})
	}

	if err := db.Transaction(func(tx *storage.Connection) error {
		if terr := tx.Eager().Create(provider); terr != nil {
			return terr
		}

		return tx.Eager().Load(provider)
	}); err != nil {
		return err
	}

	return sendJSON(w, http.StatusCreated, provider)
}

// adminSSOProvidersGet returns an existing SAML Identity Provider in the system.
func (a *API) adminSSOProvidersGet(w http.ResponseWriter, r *http.Request) error {
	provider := getSSOProvider(r.Context())

	return sendJSON(w, http.StatusOK, provider)
}

// adminSSOProvidersUpdate updates a provider with the provided diff values.
func (a *API) adminSSOProvidersUpdate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	db := a.db.WithContext(ctx)

	params := &CreateSSOProviderParams{}
	if err := retrieveRequestParams(r, params); err != nil {
		return err
	}

	if err := params.validate(true /* <- forUpdate */, a.samlDev); err != nil {
		return err
	}

	modified := false
	updateSAMLProvider := false

	provider := getSSOProvider(ctx)

	if params.MetadataXML != "" || params.MetadataURL != "" {
		// metadata is being updated
		rawMetadata, metadata, err := params.metadata(ctx)
		if err != nil {
			return err
		}

		if provider.SAMLProvider.EntityID != metadata.EntityID {
			return apierrors.NewBadRequestError(apierrors.ErrorCodeSAMLEntityIDMismatch, "SAML Metadata can be updated only if the EntityID matches for the provider; expected '%s' but got '%s'", provider.SAMLProvider.EntityID, metadata.EntityID)
		}

		if params.MetadataURL != "" {
			provider.SAMLProvider.MetadataURL = &params.MetadataURL
		}

		provider.SAMLProvider.MetadataXML = string(rawMetadata)
		updateSAMLProvider = true
		modified = true
	}

	// domains are being "updated" only when params.Domains is not nil, if
	// it was nil (but not `[]`) then the caller is expecting not to modify
	// the domains
	updateDomains := params.Domains != nil

	var createDomains, deleteDomains []models.SSODomain
	keepDomains := make(map[string]bool)

	for _, domain := range params.Domains {
		existingProvider, err := models.FindSSOProviderByDomain(db, domain)
		if err != nil && !models.IsNotFoundError(err) {
			return err
		}
		if existingProvider != nil {
			if existingProvider.ID == provider.ID {
				keepDomains[domain] = true
			} else {
				return apierrors.NewBadRequestError(apierrors.ErrorCodeSSODomainAlreadyExists, "SSO domain '%s' already assigned to another provider (%s)", domain, existingProvider.ID.String())
			}
		} else {
			modified = true
			createDomains = append(createDomains, models.SSODomain{
				Domain:        domain,
				SSOProviderID: provider.ID,
			})
		}
	}

	if updateDomains {
		for i, domain := range provider.SSODomains {
			if !keepDomains[domain.Domain] {
				modified = true
				deleteDomains = append(deleteDomains, provider.SSODomains[i])
			}
		}
	}

	updateAttributeMapping := false
	if params.AttributeMapping.Keys != nil {
		updateAttributeMapping = !provider.SAMLProvider.AttributeMapping.Equal(&params.AttributeMapping)
		if updateAttributeMapping {
			modified = true
			provider.SAMLProvider.AttributeMapping = params.AttributeMapping
		}
	}

	if params.NameIDFormat != nil && updateNameIDFormat(&provider.SAMLProvider, *params.NameIDFormat) {
		// The format is a column of the SAML provider, saved with it.
		modified = true
		updateSAMLProvider = true
	}

	if params.ResourceID != nil {
		resourceID := *params.ResourceID
		switch {
		case resourceID == "" && provider.ResourceID != nil:
			provider.ResourceID = nil
			modified = true
		case resourceID != "" &&
			(provider.ResourceID == nil ||
				*provider.ResourceID != resourceID):
			provider.ResourceID = &resourceID
			modified = true
		}
	}

	if params.Disabled != nil {
		disabled := *params.Disabled
		if provider.Disabled == nil || *provider.Disabled != disabled {
			provider.Disabled = &disabled
			modified = true
		}
	}

	if modified {
		if err := db.Transaction(func(tx *storage.Connection) error {
			if terr := tx.Eager().Update(provider); terr != nil {
				return terr
			}

			if updateDomains {
				if terr := tx.Destroy(deleteDomains); terr != nil {
					return terr
				}

				if terr := tx.Eager().Create(createDomains); terr != nil {
					return terr
				}
			}

			if updateAttributeMapping || updateSAMLProvider {
				if terr := tx.Eager().Update(&provider.SAMLProvider); terr != nil {
					return terr
				}
			}

			return tx.Eager().Load(provider)
		}); err != nil {
			return apierrors.NewUnprocessableEntityError(apierrors.ErrorCodeConflict, "Updating SSO provider failed, likely due to a conflict. Try again?").WithInternalError(err)
		}
	}

	return sendJSON(w, http.StatusOK, provider)
}

// updateNameIDFormat sets the provider's NameID format, clearing it when
// format is empty, and reports whether that changed it.
func updateNameIDFormat(provider *models.SAMLProvider, format string) bool {
	if format == utilities.StringValue(provider.NameIDFormat) {
		return false
	}
	provider.NameIDFormat = utilities.StringPtr(format)
	return true
}

// adminSSOProvidersDelete deletes a SAML identity provider.
func (a *API) adminSSOProvidersDelete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	db := a.db.WithContext(ctx)

	provider := getSSOProvider(ctx)

	if err := db.Transaction(func(tx *storage.Connection) error {
		return tx.Eager().Destroy(provider)
	}); err != nil {
		return err
	}

	return sendJSON(w, http.StatusOK, provider)
}
