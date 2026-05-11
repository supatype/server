package outerhealth

import (
	"strings"

	"github.com/supatype/auth/internal/proxy"
	"github.com/supatype/auth/internal/serverconf"
)

// ProbeConfigFrom builds probe targets from a route manifest plus server config
// (same resolution rules as cmd/mux.go for static startup).
func ProbeConfigFrom(cfg *serverconf.ServerConfig, m *proxy.RouteManifest, denoBaseURL string) ProbeConfig {
	if m == nil {
		m = &proxy.RouteManifest{Schema: "public"}
	}
	postgRESTURL := firstNonEmptyStr(m.PostgRESTURL, cfg.PostgRESTURL, "http://localhost:3000")
	graphQLProbeBase := firstNonEmptyStr(m.GraphQLURL, cfg.GraphQLURL, postgRESTURL)

	var storageLocalPath, storageRemoteURL string
	if cfg.StorageProvider == "local" && cfg.StoragePath != "" {
		storageLocalPath = cfg.StoragePath
	} else {
		storageRemoteURL = firstNonEmptyStr(m.StorageURL, cfg.StorageURL)
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

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
