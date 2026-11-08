package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/conf"
)

const dateInPast = "2001-02-03T04:05:06.789"
const dateInFarFuture = "2999-02-03T04:05:06.789"
const oneHour = "PT1H"

type SSOTestSuite struct {
	suite.Suite
	API      *API
	Config   *conf.GlobalConfiguration
	AdminJWT string
}

func TestSSO(t *testing.T) {
	api, config, err := setupAPIForTest()
	require.NoError(t, err)

	ts := &SSOTestSuite{
		API:    api,
		Config: config,
	}
	defer api.db.Close()

	if config.SAML.Enabled {
		suite.Run(t, ts)
	}
}

func (ts *SSOTestSuite) SetupTest() {
	models.TruncateAll(ts.API.db)

	ts.AdminJWT = adminToken(ts.T(), ts.Config)
}

func (ts *SSOTestSuite) TestNonAdminJWT() {
	// TODO
}

func (ts *SSOTestSuite) TestAdminListEmptySSOProviders() {
	req := httptest.NewRequest(http.MethodGet, "http://localhost/admin/sso/providers", nil)
	req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
	w := httptest.NewRecorder()

	ts.API.handler.ServeHTTP(w, req)

	require.Equal(ts.T(), http.StatusOK, w.Code)

	body, err := io.ReadAll(w.Body)
	require.NoError(ts.T(), err)

	var result struct {
		Items     []interface{} `json:"items"`
		NextToken string        `json:"next_token"`
	}

	require.NoError(ts.T(), json.Unmarshal(body, &result))
	require.Equal(ts.T(), len(result.Items), 0)
	require.Equal(ts.T(), result.NextToken, "")
}

func (ts *SSOTestSuite) TestAdminGetSSOProviderNotExist() {
	examples := []struct {
		URL string
	}{
		{
			URL: "http://localhost/admin/sso/providers/not-a-uuid",
		},
		{
			URL: "http://localhost/admin/sso/providers/677477db-3f51-4038-bc05-c6bb9bdc3c32",
		},
	}

	for _, example := range examples {
		req := httptest.NewRequest(http.MethodGet, example.URL, nil)
		req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
		w := httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		require.Equal(ts.T(), http.StatusNotFound, w.Code)
	}
}

func configurableSAMLIDPMetadata(entityID, validUntil, cacheDuration string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="%s" validUntil='%s' cacheDuration='%s'>
  <md:IDPSSODescriptor WantAuthnRequestsSigned="false" protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <md:KeyDescriptor use="signing">
      <ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#">
        <ds:X509Data>
          <ds:X509Certificate>MIIDdDCCAlygAwIBAgIGAYKSjRZiMA0GCSqGSIb3DQEBCwUAMHsxFDASBgNVBAoTC0dvb2dsZSBJ
bmMuMRYwFAYDVQQHEw1Nb3VudGFpbiBWaWV3MQ8wDQYDVQQDEwZHb29nbGUxGDAWBgNVBAsTD0dv
b2dsZSBGb3IgV29yazELMAkGA1UEBhMCVVMxEzARBgNVBAgTCkNhbGlmb3JuaWEwHhcNMjIwODEy
MTQ1NDU1WhcNMjcwODExMTQ1NDU1WjB7MRQwEgYDVQQKEwtHb29nbGUgSW5jLjEWMBQGA1UEBxMN
TW91bnRhaW4gVmlldzEPMA0GA1UEAxMGR29vZ2xlMRgwFgYDVQQLEw9Hb29nbGUgRm9yIFdvcmsx
CzAJBgNVBAYTAlVTMRMwEQYDVQQIEwpDYWxpZm9ybmlhMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEAlncFzErcnZm7ZWO71NZStnCIAoYNKf6Uw3LPLzcvk0YrA/eBC3PVDHSfahi+apGO
Ytdq7IQUvBdto3rJTvP49fjyO0WLbAbiPC+dILt2Gx9kttxpSp99Bf+8ObL/fTy5Y2oHbJBfBX1V
qfDQIY0fcej3AndFYUOE0gZXyeSbnROB8W1PzHxOc7rq1mlas0rvyja7AK4gwXjIwyIGsFDmHnve
buqWOYMzOT9oD+iQq9BWYVHkXGZn0BXzKtnw9w8I3IxQdndUoCl95pYRIvdl1b0dWdO9cXtSsTkL
kAa8B/mCQcF4W2M3t/yKtrcLcRTALg3/Hc+Xz+3BpY/fSDk1SwIDAQABMA0GCSqGSIb3DQEBCwUA
A4IBAQCER02WLf6bKwTGVD/3VTntetIiETuPs46Dum8blbsg+2BYdAHIQcB9cLuMRosIw0nYj54m
SfiyfoWGcx3CkMup1MtKyWu+SqDHl9Bpf+GFLG0ngKD/zB6xwpv/TCi+g/FBYe2TvzD6B1V0z7Vs
Xf+Gc2TWBKmCuKf/g2AUt7IQLpOaqxuJVoZjp4sEMov6d3FnaoHQEd0lg+XmnYfLNtwe3QRSU0BD
x6lVV4kXi0x0n198/gkjnA85rPZoZ6dmqHtkcM0Gabgg6KEE5ubSDlWDsdv27uANceCZAoxd1+in
4/KqqkhynnbJs7Op5ZX8cckiHGGTGHNb35kys/XukuCo</ds:X509Certificate>
        </ds:X509Data>
      </ds:KeyInfo>
    </md:KeyDescriptor>
    <md:NameIDFormat>urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress</md:NameIDFormat>
    <md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="%s"/>
    <md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location="%s"/>
  </md:IDPSSODescriptor>
</md:EntityDescriptor>`, entityID, validUntil, cacheDuration, entityID, entityID)

}

func (ts *SSOTestSuite) TestIsStaleSAMLMetadata() {

	// https://en.wikipedia.org/wiki/ISO_8601
	currentTime := time.Now()
	currentTimeAsISO8601 := currentTime.UTC().Format("2006-01-02T15:04:05Z07:00")
	examples := []struct {
		Description           string
		Metadata              []byte
		IsStale               bool
		CacheDurationExceeded bool
	}{
		{
			Description:           "Metadata is valid and within cache duration",
			Metadata:              []byte(configurableSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-B", dateInFarFuture, oneHour)),
			IsStale:               false,
			CacheDurationExceeded: false,
		},
		{

			Description:           "Metadata is valid but is a minute past cache duration",
			Metadata:              []byte(configurableSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-B", currentTimeAsISO8601, oneHour)),
			IsStale:               true,
			CacheDurationExceeded: true,
		},

		{
			Description:           "Metadata is invalid but within cache duration",
			Metadata:              []byte(configurableSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-B", dateInPast, oneHour)),
			IsStale:               true,
			CacheDurationExceeded: false,
		},
	}

	for _, example := range examples {
		metadata, err := parseSAMLMetadata(example.Metadata)
		require.NoError(ts.T(), err)
		provider := models.SAMLProvider{
			EntityID:    metadata.EntityID,
			MetadataXML: string(example.Metadata),
			UpdatedAt:   currentTime,
		}
		if example.CacheDurationExceeded {
			provider.UpdatedAt = currentTime.Add(-time.Minute * 59)
		}

		require.Equal(ts.T(), example.IsStale, IsSAMLMetadataStale(metadata, provider))
	}

}

func validSAMLIDPMetadata(entityID string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="%s">
  <md:IDPSSODescriptor WantAuthnRequestsSigned="false" protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <md:KeyDescriptor use="signing">
      <ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#">
        <ds:X509Data>
          <ds:X509Certificate>MIIDdDCCAlygAwIBAgIGAYKSjRZiMA0GCSqGSIb3DQEBCwUAMHsxFDASBgNVBAoTC0dvb2dsZSBJ
bmMuMRYwFAYDVQQHEw1Nb3VudGFpbiBWaWV3MQ8wDQYDVQQDEwZHb29nbGUxGDAWBgNVBAsTD0dv
b2dsZSBGb3IgV29yazELMAkGA1UEBhMCVVMxEzARBgNVBAgTCkNhbGlmb3JuaWEwHhcNMjIwODEy
MTQ1NDU1WhcNMjcwODExMTQ1NDU1WjB7MRQwEgYDVQQKEwtHb29nbGUgSW5jLjEWMBQGA1UEBxMN
TW91bnRhaW4gVmlldzEPMA0GA1UEAxMGR29vZ2xlMRgwFgYDVQQLEw9Hb29nbGUgRm9yIFdvcmsx
CzAJBgNVBAYTAlVTMRMwEQYDVQQIEwpDYWxpZm9ybmlhMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEAlncFzErcnZm7ZWO71NZStnCIAoYNKf6Uw3LPLzcvk0YrA/eBC3PVDHSfahi+apGO
Ytdq7IQUvBdto3rJTvP49fjyO0WLbAbiPC+dILt2Gx9kttxpSp99Bf+8ObL/fTy5Y2oHbJBfBX1V
qfDQIY0fcej3AndFYUOE0gZXyeSbnROB8W1PzHxOc7rq1mlas0rvyja7AK4gwXjIwyIGsFDmHnve
buqWOYMzOT9oD+iQq9BWYVHkXGZn0BXzKtnw9w8I3IxQdndUoCl95pYRIvdl1b0dWdO9cXtSsTkL
kAa8B/mCQcF4W2M3t/yKtrcLcRTALg3/Hc+Xz+3BpY/fSDk1SwIDAQABMA0GCSqGSIb3DQEBCwUA
A4IBAQCER02WLf6bKwTGVD/3VTntetIiETuPs46Dum8blbsg+2BYdAHIQcB9cLuMRosIw0nYj54m
SfiyfoWGcx3CkMup1MtKyWu+SqDHl9Bpf+GFLG0ngKD/zB6xwpv/TCi+g/FBYe2TvzD6B1V0z7Vs
Xf+Gc2TWBKmCuKf/g2AUt7IQLpOaqxuJVoZjp4sEMov6d3FnaoHQEd0lg+XmnYfLNtwe3QRSU0BD
x6lVV4kXi0x0n198/gkjnA85rPZoZ6dmqHtkcM0Gabgg6KEE5ubSDlWDsdv27uANceCZAoxd1+in
4/KqqkhynnbJs7Op5ZX8cckiHGGTGHNb35kys/XukuCo</ds:X509Certificate>
        </ds:X509Data>
      </ds:KeyInfo>
    </md:KeyDescriptor>
    <md:NameIDFormat>urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress</md:NameIDFormat>
    <md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="%s"/>
    <md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location="%s"/>
  </md:IDPSSODescriptor>
</md:EntityDescriptor>`, entityID, entityID, entityID)
}

func (ts *SSOTestSuite) TestAdminCreateSSOProvider() {
	examples := []struct {
		StatusCode int
		Request    map[string]interface{}
	}{
		{
			StatusCode: http.StatusBadRequest,
			Request:    map[string]interface{}{},
		},
		{
			StatusCode: http.StatusBadRequest,
			Request: map[string]interface{}{
				"type": "saml",
			},
		},
		{
			StatusCode: http.StatusBadRequest,
			Request: map[string]interface{}{
				"type": "oidc",
			},
		},
		{
			StatusCode: http.StatusCreated,
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-A"),
			},
		},
		{
			StatusCode: http.StatusCreated,
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-B"),
			},
		},
		{
			StatusCode: http.StatusCreated,
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-DUPLICATE"),
			},
		},
		{
			StatusCode: http.StatusCreated,
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-WITH-ATTRIBUTE-MAPPING"),
				"attribute_mapping": map[string]interface{}{
					"keys": map[string]interface{}{
						"username": map[string]interface{}{
							"name": "mail",
						},
					},
				},
			},
		},
		{
			StatusCode: http.StatusUnprocessableEntity,
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-DUPLICATE"),
			},
		},
		{
			StatusCode: http.StatusCreated,
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-WITH-DOMAIN-A"),
				"domains": []string{
					"example.com",
				},
			},
		},
		{
			StatusCode: http.StatusBadRequest,
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-WITH-DOMAIN-B"),
				"domains": []string{
					"example.com",
				},
			},
		},
		{
			StatusCode: http.StatusBadRequest,
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_url": "https://accounts.google.com/o/saml2?idpid=EXAMPLE-WITH-METADATA-URL-TOO",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-WITH-METADATA-URL-TOO"),
			},
		},
		{
			StatusCode: http.StatusBadRequest,
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_url": "http://accounts.google.com/o/saml2?idpid=EXAMPLE-WITH-METADATA-OVER-HTTP",
			},
		},
		{
			StatusCode: http.StatusBadRequest,
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_url": "https://accounts.google.com\\o/saml2?idpid=EXAMPLE-WITH-INVALID-METADATA-URL",
			},
		},
		// TODO: add example with metadata_url
	}

	for i, example := range examples {
		body, err := json.Marshal(example.Request)
		require.NoError(ts.T(), err)

		req := httptest.NewRequest(http.MethodPost, "http://localhost/admin/sso/providers", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
		w := httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		response, err := io.ReadAll(w.Body)
		require.NoError(ts.T(), err)

		require.Equal(ts.T(), example.StatusCode, w.Code, "Example %d failed with body %q", i, response)

		if example.StatusCode != http.StatusCreated {
			continue
		}

		// now check if the provider can be queried (GET)
		var provider struct {
			ID string `json:"id"`
		}

		require.NoError(ts.T(), json.Unmarshal(response, &provider))

		req = httptest.NewRequest(http.MethodGet, "http://localhost/admin/sso/providers/"+provider.ID, nil)
		req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
		w = httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		response, err = io.ReadAll(w.Body)
		require.NoError(ts.T(), err)

		require.Equal(ts.T(), http.StatusOK, w.Code)

		originalProviderID := provider.ID
		provider.ID = ""

		require.NoError(ts.T(), json.Unmarshal(response, &provider))
		require.Equal(ts.T(), provider.ID, originalProviderID)

		// now check if the provider can be queried (List)
		var providers struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}

		req = httptest.NewRequest(http.MethodGet, "http://localhost/admin/sso/providers", nil)
		req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
		w = httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		response, err = io.ReadAll(w.Body)
		require.NoError(ts.T(), err)

		require.NoError(ts.T(), json.Unmarshal(response, &providers))

		contained := false
		for _, listProvider := range providers.Items {
			if listProvider.ID == provider.ID {
				contained = true
				break
			}
		}

		require.True(ts.T(), contained)
	}
}

func (ts *SSOTestSuite) TestAdminUpdateSSOProvider() {
	providers := []struct {
		ID      string
		Request map[string]interface{}
	}{
		{
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-A"),
			},
		},
		{
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-C"),
				"domains": []string{
					"example.com",
				},
			},
		},
	}

	for i, example := range providers {
		body, err := json.Marshal(example.Request)
		require.NoError(ts.T(), err)

		req := httptest.NewRequest(http.MethodPost, "http://localhost/admin/sso/providers", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
		w := httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		response, err := io.ReadAll(w.Body)
		require.NoError(ts.T(), err)

		var payload struct {
			ID string `json:"id"`
		}

		require.NoError(ts.T(), json.Unmarshal(response, &payload))

		providers[i].ID = payload.ID
	}

	examples := []struct {
		ID      string
		Status  int
		Request map[string]interface{}
	}{
		{
			ID:     providers[0].ID,
			Status: http.StatusBadRequest, // changing entity ID
			Request: map[string]interface{}{
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-B"),
			},
		},
		{
			ID:     providers[0].ID,
			Status: http.StatusBadRequest, // domain already exists
			Request: map[string]interface{}{
				"domains": []string{
					"example.com",
				},
			},
		},
		{
			ID:     providers[1].ID,
			Status: http.StatusOK,
			Request: map[string]interface{}{
				"domains": []string{
					"example.com",
					"example.org",
				},
			},
		},
		{
			ID:     providers[1].ID,
			Status: http.StatusOK,
			Request: map[string]interface{}{
				"attribute_mapping": map[string]interface{}{
					"keys": map[string]interface{}{
						"username": map[string]interface{}{
							"name": "mail",
						},
					},
				},
			},
		},
	}

	for _, example := range examples {
		body, err := json.Marshal(example.Request)
		require.NoError(ts.T(), err)

		req := httptest.NewRequest(http.MethodPut, "http://localhost/admin/sso/providers/"+example.ID, bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
		w := httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		require.Equal(ts.T(), w.Code, example.Status)
	}
}

func (ts *SSOTestSuite) TestAdminDeleteSSOProvider() {
	providers := []struct {
		ID      string
		Request map[string]interface{}
	}{
		{
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-A"),
			},
		},
	}

	for i, example := range providers {
		body, err := json.Marshal(example.Request)
		require.NoError(ts.T(), err)

		req := httptest.NewRequest(http.MethodPost, "http://localhost/admin/sso/providers", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
		w := httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		response, err := io.ReadAll(w.Body)
		require.NoError(ts.T(), err)

		var payload struct {
			ID string `json:"id"`
		}

		require.NoError(ts.T(), json.Unmarshal(response, &payload))

		providers[i].ID = payload.ID
	}

	examples := []struct {
		ID     string
		Status int
	}{
		{
			ID:     providers[0].ID,
			Status: http.StatusOK,
		},
	}

	for _, example := range examples {
		req := httptest.NewRequest(http.MethodDelete, "http://localhost/admin/sso/providers/"+example.ID, nil)
		req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
		w := httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		require.Equal(ts.T(), w.Code, example.Status)
	}

	check := []struct {
		ID string
	}{
		{
			ID: providers[0].ID,
		},
	}

	for _, example := range check {
		req := httptest.NewRequest(http.MethodGet, "http://localhost/admin/sso/providers/"+example.ID, nil)
		req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
		w := httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		require.Equal(ts.T(), http.StatusNotFound, w.Code)
	}
}

func (ts *SSOTestSuite) TestSingleSignOn() {
	providers := []struct {
		ID      string
		Request map[string]interface{}
	}{
		{
			// creates a SAML provider (EXAMPLE-A)
			// does not have a domain mapping
			Request: map[string]interface{}{
				"type":         "saml",
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-A"),
			},
		},
		{
			// creates a SAML provider (EXAMPLE-B)
			// does have a domain mapping on example.com
			Request: map[string]interface{}{
				"type": "saml",
				"domains": []string{
					"example.com",
				},
				"metadata_xml": validSAMLIDPMetadata("https://accounts.google.com/o/saml2?idpid=EXAMPLE-B"),
			},
		},
	}

	for i, example := range providers {
		body, err := json.Marshal(example.Request)
		require.NoError(ts.T(), err)

		req := httptest.NewRequest(http.MethodPost, "http://localhost/admin/sso/providers", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
		w := httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		response, err := io.ReadAll(w.Body)
		require.NoError(ts.T(), err)

		var payload struct {
			ID string `json:"id"`
		}

		require.NoError(ts.T(), json.Unmarshal(response, &payload))

		providers[i].ID = payload.ID
	}

	examples := []struct {
		Code    int
		Request map[string]interface{}
		URL     string
	}{
		{
			// call /sso with provider_id (EXAMPLE-A)
			// should be successful and redirect to the EXAMPLE-A SSO URL
			Request: map[string]interface{}{
				"provider_id": providers[0].ID,
			},
			Code: http.StatusSeeOther,
			URL:  "https://accounts.google.com/o/saml2?idpid=EXAMPLE-A",
		},
		{
			// call /sso with provider_id (EXAMPLE-A) and SSO PKCE
			// should be successful and redirect to the EXAMPLE-A SSO URL
			Request: map[string]interface{}{
				"provider_id":           providers[0].ID,
				"code_challenge":        "vby3iMQ4XUuycKkEyNsYHXshPql1Dod7Ebey2iXTXm4",
				"code_challenge_method": "s256",
			},
			Code: http.StatusSeeOther,
			URL:  "https://accounts.google.com/o/saml2?idpid=EXAMPLE-A",
		},
		{
			// call /sso with domain=example.com (provider=EXAMPLE-B)
			// should be successful and redirect to the EXAMPLE-B SSO URL
			Request: map[string]interface{}{
				"domain": "example.com",
			},
			Code: http.StatusSeeOther,
			URL:  "https://accounts.google.com/o/saml2?idpid=EXAMPLE-B",
		},
		{
			// call /sso with domain=example.com (provider=EXAMPLE-B)
			// should be successful and redirect to the EXAMPLE-B SSO URL
			Request: map[string]interface{}{
				"domain":             "example.com",
				"skip_http_redirect": true,
			},
			Code: http.StatusOK,
			URL:  "https://accounts.google.com/o/saml2?idpid=EXAMPLE-B",
		},
		{
			// call /sso with domain=example.org (no such provider)
			// should be unsuccessful with 404
			Request: map[string]interface{}{
				"domain": "example.org",
			},
			Code: http.StatusNotFound,
		},
		{
			// call /sso with a provider_id=<random-uuid> (no such provider)
			// should be unsuccessful with 404
			Request: map[string]interface{}{
				"provider_id": "14d906bf-9bd5-4734-b7d1-3904e240610e",
			},
			Code: http.StatusNotFound,
		},
	}

	for _, example := range examples {
		body, err := json.Marshal(example.Request)
		require.NoError(ts.T(), err)

		req := httptest.NewRequest(http.MethodPost, "http://localhost/sso", bytes.NewBuffer(body))
		// no authorization header intentional, this is a login endpoint
		w := httptest.NewRecorder()

		ts.API.handler.ServeHTTP(w, req)

		require.Equal(ts.T(), w.Code, example.Code)

		locationURLString := ""

		if example.Code == http.StatusSeeOther {
			locationURLString = w.Header().Get("Location")
		} else if example.Code == http.StatusOK {
			var response struct {
				URL string `json:"url"`
			}

			require.NoError(ts.T(), json.NewDecoder(w.Body).Decode(&response))

			require.NotEmpty(ts.T(), response.URL)

			locationURLString = response.URL
		} else {
			continue
		}

		locationURL, err := url.ParseRequestURI(locationURLString)
		require.NoError(ts.T(), err)

		locationQuery, err := url.ParseQuery(locationURL.RawQuery)

		require.NoError(ts.T(), err)

		samlQueryParams := []string{
			"SAMLRequest",
			"RelayState",
			"SigAlg",
			"Signature",
		}

		for _, param := range samlQueryParams {
			require.True(ts.T(), locationQuery.Has(param))
		}

		for _, param := range samlQueryParams {
			locationQuery.Del(param)
		}

		locationURL.RawQuery = locationQuery.Encode()

		require.Equal(ts.T(), locationURL.String(), example.URL)
	}
}

func TestSSOCreateParamsValidation(t *testing.T) {
	// TODO
}

// serveSAMLMetadata serves IdP metadata for entityID over plain HTTP, as a
// local test identity provider does.
func (ts *SSOTestSuite) serveSAMLMetadata(entityID string) string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(validSAMLIDPMetadata(entityID)))
	}))
	ts.T().Cleanup(server.Close)
	return server.URL + "/metadata"
}

// inMode runs the rest of the test with SUPATYPE_MODE set to mode, and the
// SAML dev policy an API started in that mode has.
func (ts *SSOTestSuite) inMode(mode string) {
	previousMode, previousPolicy := ts.Config.Mode, ts.API.samlDev
	ts.Config.Mode = mode
	ts.API.samlDev = newSAMLDevPolicy(ts.Config)
	ts.T().Cleanup(func() {
		ts.Config.Mode = previousMode
		ts.API.samlDev = previousPolicy
	})
}

func (ts *SSOTestSuite) adminSSORequest(method, path string, body map[string]interface{}) *httptest.ResponseRecorder {
	encoded, err := json.Marshal(body)
	require.NoError(ts.T(), err)
	req := httptest.NewRequest(method, "http://localhost/admin/sso/providers"+path, bytes.NewBuffer(encoded))
	req.Header.Set("Authorization", "Bearer "+ts.AdminJWT)
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	return w
}

// A local identity provider is usually served over plain HTTP, so in dev mode
// an http metadata_url is accepted. Every other mode requires HTTPS, and no
// mode accepts another scheme.
func (ts *SSOTestSuite) TestAdminCreateSSOProviderHTTPMetadataURL() {
	ts.Run("NotDev", func() {
		ts.inMode("standalone")
		w := ts.adminSSORequest(http.MethodPost, "", map[string]interface{}{
			"type":         "saml",
			"metadata_url": ts.serveSAMLMetadata("http://idp.local/create-not-dev"),
		})
		require.Equal(ts.T(), http.StatusBadRequest, w.Code, w.Body.String())
		require.Contains(ts.T(), w.Body.String(), "metadata_url is not a HTTPS URL")
	})

	ts.Run("Dev", func() {
		ts.inMode("dev")
		w := ts.adminSSORequest(http.MethodPost, "", map[string]interface{}{
			"type":         "saml",
			"metadata_url": ts.serveSAMLMetadata("http://idp.local/create-dev"),
		})
		require.Equal(ts.T(), http.StatusCreated, w.Code, w.Body.String())
	})

	ts.Run("DevOtherScheme", func() {
		ts.inMode("dev")
		w := ts.adminSSORequest(http.MethodPost, "", map[string]interface{}{
			"type":         "saml",
			"metadata_url": "file:///etc/passwd",
		})
		require.Equal(ts.T(), http.StatusBadRequest, w.Code, w.Body.String())
	})
}

func (ts *SSOTestSuite) TestAdminUpdateSSOProviderHTTPMetadataURL() {
	const entityID = "http://idp.local/update"
	id := ts.createProviderWithDomains(entityID)
	metadataURL := ts.serveSAMLMetadata(entityID)

	ts.Run("NotDev", func() {
		ts.inMode("")
		w := ts.adminSSORequest(http.MethodPut, "/"+id, map[string]interface{}{"metadata_url": metadataURL})
		require.Equal(ts.T(), http.StatusBadRequest, w.Code, w.Body.String())
		require.Contains(ts.T(), w.Body.String(), "metadata_url is not a HTTPS URL")
	})

	ts.Run("Dev", func() {
		ts.inMode("dev")
		w := ts.adminSSORequest(http.MethodPut, "/"+id, map[string]interface{}{"metadata_url": metadataURL})
		require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
		require.Contains(ts.T(), w.Body.String(), metadataURL)
	})
}

// An update changes only what it names: a PUT without name_id_format keeps the
// stored one, and an explicit empty value clears it.
func (ts *SSOTestSuite) TestAdminUpdateSSOProviderNameIDFormat() {
	const (
		emailFormat      = "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress"
		persistentFormat = "urn:oasis:names:tc:SAML:2.0:nameid-format:persistent"
	)
	w := ts.adminSSORequest(http.MethodPost, "", map[string]interface{}{
		"type":           "saml",
		"metadata_xml":   validSAMLIDPMetadata("https://idp.example.com/name-id-format"),
		"name_id_format": emailFormat,
	})
	require.Equal(ts.T(), http.StatusCreated, w.Code, w.Body.String())

	type nameIDProvider struct {
		ID   string `json:"id"`
		SAML struct {
			NameIDFormat *string `json:"name_id_format"`
		} `json:"saml"`
	}
	var created nameIDProvider
	require.NoError(ts.T(), json.Unmarshal(w.Body.Bytes(), &created))
	require.NotNil(ts.T(), created.SAML.NameIDFormat)

	update := func(body map[string]interface{}) *string {
		w := ts.adminSSORequest(http.MethodPut, "/"+created.ID, body)
		require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
		var updated nameIDProvider
		require.NoError(ts.T(), json.Unmarshal(w.Body.Bytes(), &updated))
		return updated.SAML.NameIDFormat
	}

	// The attribute mapping is saved with the SAML settings, so this is the
	// update that used to write the cleared format.
	for _, body := range []map[string]interface{}{
		{"domains": []string{"name-id-format.example.com"}},
		{"attribute_mapping": map[string]interface{}{"keys": map[string]interface{}{"email": map[string]interface{}{"name": "mail"}}}},
	} {
		kept := update(body)
		require.NotNil(ts.T(), kept, "omitting name_id_format keeps it: %v", body)
		require.Equal(ts.T(), emailFormat, *kept)
	}

	changed := update(map[string]interface{}{"name_id_format": persistentFormat})
	require.NotNil(ts.T(), changed, "name_id_format on its own is saved")
	require.Equal(ts.T(), persistentFormat, *changed)

	require.Nil(ts.T(), update(map[string]interface{}{"name_id_format": ""}), "an empty name_id_format clears it")
}

// withTXTRecords answers DNS TXT lookups from records for the rest of the
// test, and counts them.
func (ts *SSOTestSuite) withTXTRecords(records map[string][]string) *int {
	lookups := 0
	previous := lookupTXT
	lookupTXT = func(_ context.Context, name string) ([]string, error) {
		lookups++
		if values, ok := records[name]; ok {
			return values, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	ts.T().Cleanup(func() { lookupTXT = previous })
	return &lookups
}

type testDomainVerification struct {
	Domain     string     `json:"domain"`
	Verified   bool       `json:"verified"`
	VerifiedAt *time.Time `json:"verified_at"`
	Challenge  struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"challenge"`
	Found []string `json:"found"`
}

type testDomainEntry struct {
	Domain     string     `json:"domain"`
	Verified   bool       `json:"verified"`
	VerifiedAt *time.Time `json:"verified_at"`
}

// createProviderWithDomains registers a provider, with the last part of
// entityID as its resource ID, and returns its ID.
func (ts *SSOTestSuite) createProviderWithDomains(entityID string, domains ...string) string {
	w := ts.adminSSORequest(http.MethodPost, "", map[string]interface{}{
		"type":         "saml",
		"metadata_xml": validSAMLIDPMetadata(entityID),
		"domains":      domains,
		"resource_id":  entityID[strings.LastIndex(entityID, "/")+1:],
	})
	require.Equal(ts.T(), http.StatusCreated, w.Code, w.Body.String())
	var provider struct {
		ID      string            `json:"id"`
		Domains []testDomainEntry `json:"domains"`
	}
	require.NoError(ts.T(), json.Unmarshal(w.Body.Bytes(), &provider))
	for _, d := range provider.Domains {
		require.False(ts.T(), d.Verified, "a new domain starts unverified: %s", d.Domain)
		require.Nil(ts.T(), d.VerifiedAt)
	}
	if len(domains) > 0 {
		require.Contains(ts.T(), w.Body.String(), `"verified_at":null`)
	}
	return provider.ID
}

func (ts *SSOTestSuite) verifyDomain(providerRef, domain string, wantStatus int) testDomainVerification {
	w := ts.adminSSORequest(http.MethodPost, "/"+providerRef+"/domains/"+domain+"/verify", nil)
	require.Equal(ts.T(), wantStatus, w.Code, w.Body.String())
	var response testDomainVerification
	if wantStatus == http.StatusOK {
		require.NoError(ts.T(), json.Unmarshal(w.Body.Bytes(), &response))
	}
	return response
}

// domainEntries are the provider's domains as GET shows them.
func (ts *SSOTestSuite) domainEntries(providerID string) []testDomainEntry {
	w := ts.adminSSORequest(http.MethodGet, "/"+providerID, nil)
	require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
	var provider struct {
		Domains []testDomainEntry `json:"domains"`
	}
	require.NoError(ts.T(), json.Unmarshal(w.Body.Bytes(), &provider))
	return provider.Domains
}

// The first call issues the token, every call checks DNS, and the domain is
// verified once its TXT record holds the token.
func (ts *SSOTestSuite) TestDomainVerify() {
	id := ts.createProviderWithDomains("https://idp.example.com/verify", "example.com")

	ts.withTXTRecords(map[string][]string{"_supatype-challenge.example.com": {"something-else"}})
	first := ts.verifyDomain(id, "Example.COM", http.StatusOK)
	require.Equal(ts.T(), "example.com", first.Domain)
	require.False(ts.T(), first.Verified)
	require.Nil(ts.T(), first.VerifiedAt)
	require.Equal(ts.T(), "_supatype-challenge.example.com", first.Challenge.Name)
	require.GreaterOrEqual(ts.T(), len(first.Challenge.Value), 32)
	require.Equal(ts.T(), []string{"something-else"}, first.Found)

	ts.withTXTRecords(nil)
	missing := ts.verifyDomain(id, "example.com", http.StatusOK)
	require.Equal(ts.T(), first.Challenge.Value, missing.Challenge.Value, "the token stays the one already issued")
	require.False(ts.T(), missing.Verified)
	require.NotNil(ts.T(), missing.Found, "a lookup was made")
	require.Empty(ts.T(), missing.Found)

	ts.withTXTRecords(map[string][]string{"_supatype-challenge.example.com": {"v=spf1 -all", first.Challenge.Value}})
	verified := ts.verifyDomain(id, "example.com", http.StatusOK)
	require.True(ts.T(), verified.Verified)
	require.NotNil(ts.T(), verified.VerifiedAt)

	// Verified once and kept: the record may go away afterwards.
	ts.withTXTRecords(nil)
	kept := ts.verifyDomain(id, "example.com", http.StatusOK)
	require.True(ts.T(), kept.Verified)
	require.Equal(ts.T(), verified.VerifiedAt.Unix(), kept.VerifiedAt.Unix())

	entries := ts.domainEntries(id)
	require.Len(ts.T(), entries, 1)
	require.True(ts.T(), entries[0].Verified)
	require.NotNil(ts.T(), entries[0].VerifiedAt)
	w := ts.adminSSORequest(http.MethodGet, "/"+id, nil)
	require.NotContains(ts.T(), w.Body.String(), first.Challenge.Value, "the token is shown only by the verify endpoint")
}

// Keeping a domain in an update keeps its verification; the list shows it.
func (ts *SSOTestSuite) TestDomainVerificationSurvivesUpdate() {
	id := ts.createProviderWithDomains("https://idp.example.com/verify-update", "example.com")
	ts.withTXTRecords(nil)
	first := ts.verifyDomain(id, "example.com", http.StatusOK)
	ts.withTXTRecords(map[string][]string{"_supatype-challenge.example.com": {first.Challenge.Value}})
	require.True(ts.T(), ts.verifyDomain(id, "example.com", http.StatusOK).Verified)

	w := ts.adminSSORequest(http.MethodPut, "/"+id, map[string]interface{}{"domains": []string{"example.com", "example.org"}})
	require.Equal(ts.T(), http.StatusOK, w.Code, w.Body.String())
	verified := map[string]bool{}
	for _, d := range ts.domainEntries(id) {
		verified[d.Domain] = d.Verified
	}
	require.Equal(ts.T(), map[string]bool{"example.com": true, "example.org": false}, verified)

	w = ts.adminSSORequest(http.MethodGet, "", nil)
	require.Contains(ts.T(), w.Body.String(), `"domain":"example.com","verified":true`)
}

func (ts *SSOTestSuite) TestDomainVerifyByResourceID() {
	ts.createProviderWithDomains("https://idp.example.com/verify-resource", "example.com")
	ts.withTXTRecords(nil)
	response := ts.verifyDomain("resource_verify-resource", "example.com", http.StatusOK)
	require.Equal(ts.T(), "_supatype-challenge.example.com", response.Challenge.Name)
}

func (ts *SSOTestSuite) TestDomainVerifyUnknownDomain() {
	id := ts.createProviderWithDomains("https://idp.example.com/verify-unknown", "example.com")
	ts.verifyDomain(id, "other.com", http.StatusNotFound)
}

// In dev mode a .supatype.test domain is verified with no DNS lookup, so a
// local identity provider can be tried end to end. Other modes always look.
func (ts *SSOTestSuite) TestDomainVerifyDevTestDomain() {
	id := ts.createProviderWithDomains("https://idp.example.com/verify-dev", "acme.supatype.test")

	ts.inMode("standalone")
	lookups := ts.withTXTRecords(nil)
	require.False(ts.T(), ts.verifyDomain(id, "acme.supatype.test", http.StatusOK).Verified)
	require.Equal(ts.T(), 1, *lookups, "outside dev mode the record is looked up")

	ts.inMode("dev")
	verified := ts.verifyDomain(id, "acme.supatype.test", http.StatusOK)
	require.True(ts.T(), verified.Verified)
	require.Nil(ts.T(), verified.Found, "no lookup was made")
	require.Equal(ts.T(), 1, *lookups, "no lookup in dev mode")
}

// Dev mode looks up every other domain.
func (ts *SSOTestSuite) TestDomainVerifyDevOtherDomain() {
	id := ts.createProviderWithDomains("https://idp.example.com/verify-dev-other", "example.com")
	ts.inMode("dev")
	lookups := ts.withTXTRecords(nil)
	require.False(ts.T(), ts.verifyDomain(id, "example.com", http.StatusOK).Verified)
	require.Equal(ts.T(), 1, *lookups)
}

// serveBody answers every request with status and body.
func (ts *SSOTestSuite) serveBody(status int, body string) string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	ts.T().Cleanup(server.Close)
	return server.URL + "/metadata"
}

// serveSlowly answers after longer than the fetch timeout the test sets.
func (ts *SSOTestSuite) serveSlowly() string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		_, _ = w.Write([]byte("secret-body"))
	}))
	ts.T().Cleanup(server.Close)
	return server.URL + "/metadata"
}

// closedURL is a metadata URL on a port nothing listens on.
func (ts *SSOTestSuite) closedURL() string {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	return server.URL + "/metadata"
}

// The server fetches metadata_url itself, so a URL that works from the
// admin's machine can still fail from the server. Create and update both
// answer 400 saml_metadata_fetch_failed naming the host and the reason, and
// never echo what the URL answered with.
func (ts *SSOTestSuite) TestAdminSSOProviderMetadataURLFetchFailure() {
	ts.inMode("dev")
	id := ts.createProviderWithDomains("http://idp.local/fetch-failure")
	previousTimeout := samlMetadataFetchTimeout
	samlMetadataFetchTimeout = 200 * time.Millisecond
	ts.T().Cleanup(func() { samlMetadataFetchTimeout = previousTimeout })

	cases := []struct {
		name, url, reason string
	}{
		{"ConnectionRefused", ts.closedURL(), "connection refused"},
		{"ErrorStatus", ts.serveBody(http.StatusInternalServerError, "secret-body"), "HTTP status 500"},
		{"NotXML", ts.serveBody(http.StatusOK, "secret-body"), "not SAML metadata XML"},
		{"Timeout", ts.serveSlowly(), "timed out"},
	}
	for _, c := range cases {
		parsed, err := url.Parse(c.url)
		require.NoError(ts.T(), err)
		requests := []struct {
			method, path string
			body         map[string]interface{}
		}{
			{http.MethodPost, "", map[string]interface{}{"type": "saml", "metadata_url": c.url}},
			{http.MethodPut, "/" + id, map[string]interface{}{"metadata_url": c.url}},
		}
		for _, req := range requests {
			ts.Run(c.name+req.method, func() {
				w := ts.adminSSORequest(req.method, req.path, req.body)
				require.Equal(ts.T(), http.StatusBadRequest, w.Code, w.Body.String())
				var resp struct {
					ErrorCode string `json:"error_code"`
					Msg       string `json:"msg"`
				}
				require.NoError(ts.T(), json.Unmarshal(w.Body.Bytes(), &resp))
				require.Equal(ts.T(), "saml_metadata_fetch_failed", resp.ErrorCode)
				require.Contains(ts.T(), resp.Msg, parsed.Host)
				require.Contains(ts.T(), resp.Msg, c.reason)
				require.Contains(ts.T(), resp.Msg, "reachable from the server")
				require.NotContains(ts.T(), w.Body.String(), "secret-body")
			})
		}
	}
}

// Pasted metadata_xml that is not XML is the admin's mistake, so it is a 400.
func (ts *SSOTestSuite) TestAdminCreateSSOProviderMetadataXMLNotXML() {
	w := ts.adminSSORequest(http.MethodPost, "", map[string]interface{}{
		"type":         "saml",
		"metadata_xml": "not xml",
	})
	require.Equal(ts.T(), http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(ts.T(), w.Body.String(), `"error_code":"validation_failed"`)
	require.Contains(ts.T(), w.Body.String(), "metadata_xml is not SAML metadata XML")
}
