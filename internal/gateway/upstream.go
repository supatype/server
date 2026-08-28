package gateway

import (
	"net/url"
	"strings"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// Every upstream in this gateway is chosen the same way: the tenant's route
// manifest first, then the deployment's configuration, then a built-in default.
// That rule was written out at nine call sites, twice with the fallback chain
// nested inside another one, and there was nothing to stop the tenth site
// getting the order wrong.
//
// A Source answers "where should this request go?" for one candidate. Resolve
// tries them in order and takes the first that has an answer, so precedence is
// the order of a list rather than the shape of a nested expression.

// Source proposes an upstream for a request's manifest and the service config.
// An empty string means "no opinion", and the next Source is consulted.
type Source func(*proxy.RouteManifest, *config.Config) string

// Resolve returns a Source that takes the first non-empty answer.
func Resolve(sources ...Source) Source {
	return func(m *proxy.RouteManifest, cfg *config.Config) string {
		for _, source := range sources {
			if answer := strings.TrimSpace(source(m, cfg)); answer != "" {
				return answer
			}
		}
		return ""
	}
}

// FromManifest reads a candidate from the tenant's route manifest. A nil
// manifest has no opinion, which is what a baseline mount passes.
func FromManifest(read func(*proxy.RouteManifest) string) Source {
	return func(m *proxy.RouteManifest, _ *config.Config) string {
		if m == nil {
			return ""
		}
		return read(m)
	}
}

// FromConfig reads a candidate from the deployment's configuration.
func FromConfig(read func(*config.Config) string) Source {
	return func(_ *proxy.RouteManifest, cfg *config.Config) string {
		if cfg == nil {
			return ""
		}
		return read(cfg)
	}
}

// Static is a built-in fallback.
func Static(value string) Source {
	return func(*proxy.RouteManifest, *config.Config) string { return value }
}

// defaultPostgREST is where REST goes when nothing says otherwise. It is also
// the GraphQL fallback, because pg_graphql is reached as a PostgREST RPC.
const defaultPostgREST = "http://localhost:3000"

// The upstreams, each stated once.
var (
	// PostgRESTUpstream serves /rest/v1.
	PostgRESTUpstream = Resolve(
		FromManifest(func(m *proxy.RouteManifest) string { return m.PostgRESTURL }),
		FromConfig(func(c *config.Config) string { return c.PostgRESTURL }),
		Static(defaultPostgREST),
	)

	// GraphQLUpstream serves /graphql/v1. It falls through to PostgREST because
	// pg_graphql is an RPC on the same server, not a separate service.
	GraphQLUpstream = Resolve(
		FromManifest(func(m *proxy.RouteManifest) string { return m.GraphQLURL }),
		FromConfig(func(c *config.Config) string { return c.GraphQLURL }),
		PostgRESTUpstream,
	)

	// StorageUpstream serves /storage/v1 when the local object store is not in
	// use. It has no default: an unconfigured storage upstream is a bad gateway,
	// not a guess at localhost.
	StorageUpstream = Resolve(
		FromManifest(func(m *proxy.RouteManifest) string { return m.StorageURL }),
		FromConfig(func(c *config.Config) string { return c.StorageURL }),
	)

	// AppUpstream is the application behind the catch-all mount.
	AppUpstream = Resolve(
		FromManifest(func(m *proxy.RouteManifest) string { return m.AppUpstream }),
		FromConfig(func(c *config.Config) string { return c.AppUpstream }),
	)

	// AppStaticDir is the directory served by the catch-all mount.
	AppStaticDir = Resolve(
		FromManifest(func(m *proxy.RouteManifest) string { return m.AppStaticDir }),
		FromConfig(func(c *config.Config) string { return c.AppStaticDir }),
	)

	// ViteDevURL is the Vite dev server proxied at /_vite in dev mode.
	ViteDevURL = Resolve(
		FromManifest(func(m *proxy.RouteManifest) string { return m.ViteDevURL }),
		FromConfig(func(c *config.Config) string { return c.ViteDevURL }),
	)

	// AppMode decides what answers the catch-all mount.
	AppMode = Resolve(
		FromManifest(func(m *proxy.RouteManifest) string { return m.AppMode }),
		FromConfig(func(c *config.Config) string { return c.AppMode }),
		Static("none"),
	)
)

// ResolveURL runs a Source and parses the result.
func ResolveURL(source Source, m *proxy.RouteManifest, cfg *config.Config) (*url.URL, error) {
	return url.Parse(source(m, cfg))
}
