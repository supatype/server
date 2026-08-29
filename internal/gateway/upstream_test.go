package gateway

import (
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// The precedence this file encodes is the one the gateway has always used, and
// getting it wrong sends a tenant's traffic to another deployment's upstream.
// It is asserted here rather than inferred from nine call sites.
func TestPrecedenceIsManifestThenConfigThenDefault(t *testing.T) {
	cases := []struct {
		name     string
		source   Source
		manifest *proxy.RouteManifest
		cfg      *config.Config
		want     string
	}{
		{
			name:     "manifest wins over config",
			source:   PostgRESTUpstream,
			manifest: &proxy.RouteManifest{PostgRESTURL: "http://from-manifest"},
			cfg:      &config.Config{PostgRESTURL: "http://from-config"},
			want:     "http://from-manifest",
		},
		{
			name:     "config wins over the default",
			source:   PostgRESTUpstream,
			manifest: &proxy.RouteManifest{},
			cfg:      &config.Config{PostgRESTURL: "http://from-config"},
			want:     "http://from-config",
		},
		{
			name:     "the default is last",
			source:   PostgRESTUpstream,
			manifest: &proxy.RouteManifest{},
			cfg:      &config.Config{},
			want:     defaultPostgREST,
		},
		{
			// A baseline mount passes no manifest at all.
			name:   "a nil manifest is not an opinion",
			source: PostgRESTUpstream,
			cfg:    &config.Config{PostgRESTURL: "http://from-config"},
			want:   "http://from-config",
		},
		{
			// pg_graphql is an RPC on PostgREST, not a separate service, so an
			// unset GraphQL upstream must follow REST rather than fail.
			name:     "graphql falls through to postgrest",
			source:   GraphQLUpstream,
			manifest: &proxy.RouteManifest{PostgRESTURL: "http://rest-only"},
			cfg:      &config.Config{},
			want:     "http://rest-only",
		},
		{
			name:     "graphql prefers its own upstream",
			source:   GraphQLUpstream,
			manifest: &proxy.RouteManifest{GraphQLURL: "http://graphql", PostgRESTURL: "http://rest"},
			cfg:      &config.Config{},
			want:     "http://graphql",
		},
		{
			// Storage has no default on purpose: guessing localhost would turn a
			// missing configuration into a connection refused rather than a clear
			// bad gateway.
			name:     "storage has no default",
			source:   StorageUpstream,
			manifest: &proxy.RouteManifest{},
			cfg:      &config.Config{},
			want:     "",
		},
		{
			name:     "app mode defaults to none",
			source:   AppMode,
			manifest: &proxy.RouteManifest{},
			cfg:      &config.Config{},
			want:     "none",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.source(c.manifest, c.cfg); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// A value that is only whitespace is not a configured upstream. Treating it as
// one would proxy to an unparseable URL rather than fall through.
func TestWhitespaceIsNotAnAnswer(t *testing.T) {
	got := PostgRESTUpstream(
		&proxy.RouteManifest{PostgRESTURL: "   "},
		&config.Config{PostgRESTURL: "http://from-config"},
	)
	if got != "http://from-config" {
		t.Errorf("got %q, want the config value", got)
	}
}

func TestResolveWithNoSourcesAnswersEmpty(t *testing.T) {
	if got := Resolve()(&proxy.RouteManifest{}, &config.Config{}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFromConfigToleratesANilConfig(t *testing.T) {
	source := FromConfig(func(c *config.Config) string { return c.PostgRESTURL })
	if got := source(nil, nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestResolveURL(t *testing.T) {
	u, err := ResolveURL(PostgRESTUpstream, &proxy.RouteManifest{}, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if u.String() != defaultPostgREST {
		t.Errorf("got %q", u.String())
	}

	if _, err := ResolveURL(Static("://nonsense"), nil, nil); err == nil {
		t.Error("want a parse error for an unusable upstream")
	}
}
