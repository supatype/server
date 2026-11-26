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
// unless SUPATYPE_STORAGE_DISABLED, GraphQL is on unless SUPATYPE_GRAPHQL_DISABLED, functions are on when a worker is configured, and realtime,
// which has no setting of its own in the environment, is off.
func Start(cfg *config.Config, name string) bool {
	switch name {
	case proxy.ServiceStorage:
		return !cfg.StorageDisabled
	case proxy.ServiceGraphQL:
		return !cfg.GraphQLDisabled
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

// GraphQLReasonProject is the SUPATYPE_GRAPHQL_DISABLED_REASON value that says the project
// switched GraphQL off, as against a stack that has no pg_graphql.
const GraphQLReasonProject = "project"

// GraphQLOffByProject is whether GraphQL being off is the project's own doing, which is what
// the 501 message tells the caller. byManifest is whether the route manifest switched it off
// (Cloud). Otherwise the CLI says so with the reason "project" when it rendered
// services.graphql: false into a self-hosted stack. Off with no reason is a stack whose Postgres
// has no pg_graphql (native `supatype dev`).
func GraphQLOffByProject(cfg *config.Config, byManifest bool) bool {
	return byManifest || strings.EqualFold(strings.TrimSpace(cfg.GraphQLDisabledReason), GraphQLReasonProject)
}
