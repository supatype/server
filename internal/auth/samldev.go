package auth

import (
	"strings"

	"github.com/supatype/server/internal/conf"
)

// devTestDomainSuffix marks domains that, in dev mode, count as verified with
// no lookup.
const devTestDomainSuffix = ".supatype.test"

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

// allowsInsecureSOAPEndpoints reports whether the service provider may send
// artifact resolution and other SOAP messages to an http endpoint of the
// identity provider: in dev mode only. Those messages carry the user's
// assertion, so production sends them over https only.
func (p samlDevPolicy) allowsInsecureSOAPEndpoints() bool {
	return p.dev
}

// verifiesWithoutLookup reports whether domain counts as verified with no DNS
// lookup: a .supatype.test domain in dev mode.
func (p samlDevPolicy) verifiesWithoutLookup(domain string) bool {
	return p.dev && strings.HasSuffix(strings.ToLower(domain), devTestDomainSuffix)
}
