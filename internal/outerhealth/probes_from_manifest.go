package outerhealth

import (
	"strings"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// ProbeConfigFrom builds probe targets from a route manifest plus server config
// (same resolution rules as cmd/mux.go for static startup).
func ProbeConfigFrom(cfg *config.Config, m *proxy.RouteManifest, denoBaseURL string) ProbeConfig {
	if m == nil {
		m = &proxy.RouteManifest{Schema: "public"}
	}
	postgRESTURL := firstNonEmpty(m.PostgRESTURL, cfg.PostgRESTURL, "http://localhost:3000")
	graphQLProbeBase := firstNonEmpty(m.GraphQLURL, cfg.GraphQLURL, postgRESTURL)

	var storageLocalPath, storageRemoteURL string
	if cfg.StorageProvider == "local" && cfg.StoragePath != "" {
		storageLocalPath = cfg.StoragePath
	} else {
		storageRemoteURL = firstNonEmpty(m.StorageURL, cfg.StorageURL)
	}

	return ProbeConfig{
		PostgRESTURL:     postgRESTURL,
		GraphQLURL:       graphQLProbeBase,
		StorageLocalPath: storageLocalPath,
		StorageRemoteURL: storageRemoteURL,
		DenoBaseURL:      strings.TrimSpace(denoBaseURL),
		RealtimeEnabled:  m.RealtimeEnabled,
	}
}

// firstNonEmpty returns the first value that is not blank, which is the
// resolution rule everywhere in this package: a manifest overrides
// configuration, configuration overrides the built-in default.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
