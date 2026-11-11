// Package serviceswitch decides whether a switchable upstream service is on for a request: the
// route manifest's services map, read per request, over the server's own start value.
//
// One rule for every service the manifest can switch. The gateway, which answers a switched-off
// service itself, and /health, which reports it as disabled, both ask here, so they cannot
// disagree about a service (0.9.2).
package serviceswitch

import (
	"strings"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// Start is what a service is before the route manifest says anything about it: storage is on
// unless SUPATYPE_STORAGE_DISABLED, functions are on when a worker is configured, and realtime,
// which has no setting of its own in the environment, is off.
func Start(cfg *config.Config, name string) bool {
	switch name {
	case proxy.ServiceStorage:
		return !cfg.StorageDisabled
	case proxy.ServiceFunctions:
		return strings.TrimSpace(cfg.FunctionsWorkerURL) != ""
	default:
		return false
	}
}

// On is whether the named service is on for a request answered from m.
func On(m *proxy.RouteManifest, cfg *config.Config, name string) bool {
	return m.ServiceOn(name, Start(cfg, name))
}
