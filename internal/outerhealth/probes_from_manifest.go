package outerhealth

import (
	"strings"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/utilities"
)

// ProbeConfigFrom builds probe targets from a route manifest plus server config
// (same resolution rules as cmd/mux.go for static startup).
func ProbeConfigFrom(cfg *config.Config, m *proxy.RouteManifest, denoBaseURL string) ProbeConfig {
	if m == nil {
		m = &proxy.RouteManifest{Schema: "public"}
	}
	postgRESTURL := utilities.FirstNonEmpty(m.PostgRESTURL, cfg.PostgRESTURL, "http://localhost:3000")
	graphQLProbeBase := utilities.FirstNonEmpty(m.GraphQLURL, cfg.GraphQLURL, postgRESTURL)

	var storageLocalPath, storageRemoteURL string
	if cfg.StorageProvider == "local" && cfg.StoragePath != "" {
		storageLocalPath = cfg.StoragePath
	} else {
		storageRemoteURL = utilities.FirstNonEmpty(m.StorageURL, cfg.StorageURL)
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
