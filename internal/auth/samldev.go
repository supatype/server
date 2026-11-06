package auth

import (
	"github.com/supatype/server/internal/conf"
)

// samlDevPolicy is what dev mode relaxes in SAML, so a local identity provider
// can be tried end to end. It is built once from the configuration the API
// starts with; outside dev mode it relaxes nothing.
type samlDevPolicy struct {
	dev bool
}

func newSAMLDevPolicy(config *conf.GlobalConfiguration) samlDevPolicy {
	return samlDevPolicy{dev: config.IsDevMode()}
}

// allowsMetadataScheme reports whether a metadata_url may use scheme: https
// always, and http in dev mode only, as a local identity provider is usually
// served over plain HTTP.
func (p samlDevPolicy) allowsMetadataScheme(scheme string) bool {
	return scheme == "https" || (p.dev && scheme == "http")
}
