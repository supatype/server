package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/serverconf"
)

// The route table is a behaviour lock for the coherence refactor.
//
// buildOuterMux mounts twenty-odd routes across 348 imperative lines, several of
// them conditional on config and several ordered so that one does not shadow
// another (the Studio routes must register before the app catch-all at "/", for
// one). Turning that into a route table plus a fold is the point of the
// refactor, and this file is how we tell "the same routes, assembled
// differently" from "a route quietly stopped existing".
//
// Walking chi requires the bare *chi.Mux. buildOuterMux returns it unwrapped
// only for standalone mode with no CORS origins; the per-mode middleware
// wrapping is asserted separately, by request, in muxbehaviour_test.go.

// routeScenario is a named configuration whose mounted routes are recorded.
type routeScenario struct {
	name     string
	golden   string
	cfg      *serverconf.ServerConfig
	manifest *proxy.RouteManifest
}

func routeScenarios() []routeScenario {
	return []routeScenario{
		{
			// Nothing optional configured: the routes that always exist.
			name:     "minimal",
			golden:   "routes-minimal.txt",
			cfg:      &serverconf.ServerConfig{Mode: "standalone"},
			manifest: &proxy.RouteManifest{Schema: "public"},
		},
		{
			// Every conditional mount switched on, so the golden records the
			// maximal route set and a dropped conditional shows up as a diff.
			name:   "full",
			golden: "routes-full.txt",
			cfg: &serverconf.ServerConfig{
				Mode:               "standalone",
				PostgRESTURL:       "http://postgrest.invalid",
				GraphQLURL:         "http://graphql.invalid",
				StorageProvider:    "local",
				StoragePath:        "/tmp/supatype-routes-test",
				DenoFunctionsDir:   "functions",
				FunctionsWorkerURL: "http://functions.invalid",
				RealtimeURL:        "http://realtime.invalid",
				AppMode:            "static",
				AppStaticDir:       "/tmp/supatype-routes-test",
			},
			manifest: &proxy.RouteManifest{
				Schema:           "public",
				RealtimeEnabled:  true,
				FunctionsEnabled: true,
				AppMode:          "static",
				AppStaticDir:     "/tmp/supatype-routes-test",
			},
		},
	}
}

func TestRouteTableGolden(t *testing.T) {
	for _, sc := range routeScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			got := renderRoutes(t, sc)
			path := filepath.Join("testdata", sc.golden)
			want, err := os.ReadFile(path) // #nosec G304 -- fixed testdata path
			if err != nil {
				t.Fatalf("%v\n\nIf this scenario is new, write the file with:\n%s", err, got)
			}
			if got != string(want) {
				t.Errorf("mounted routes no longer match %s.\n\nwant:\n%s\ngot:\n%s", path, want, got)
			}
		})
	}
}

// TestRouteTableGoldensAreNotEmpty stops the lock going vacuous: a builder that
// mounted nothing would otherwise match an empty golden.
func TestRouteTableGoldensAreNotEmpty(t *testing.T) {
	for _, sc := range routeScenarios() {
		got := renderRoutes(t, sc)
		if n := strings.Count(got, "\n"); n < 10 {
			t.Errorf("%s: only %d routes recorded, which is too few to be a real mux:\n%s", sc.name, n, got)
		}
	}
}

// TestFullRouteTableCoversEveryService names the services the gateway exists to
// front. A refactor that loses one of these mounts has lost a product feature,
// and the diff in a golden file is easy to wave through; a named assertion is
// not.
func TestFullRouteTableCoversEveryService(t *testing.T) {
	full := routeScenarios()[1]
	got := renderRoutes(t, full)
	for _, prefix := range []string{
		"/admin/v1",
		"/auth/v1",
		"/rest/v1",
		"/graphql/v1",
		"/storage/v1",
		"/functions/v1",
		"/realtime/v1",
		"/platform/v1",
		"/studio/schema",
		"/studio/session",
		"/studio/proxy",
		"/studio/auth/verify",
		"/studio-config",
		"/admin/studio-members",
		"/sql",
		"/health",
	} {
		if !hasRouteUnder(got, prefix) {
			t.Errorf("no route mounted under %s:\n%s", prefix, got)
		}
	}
}

// hasRouteUnder reports whether any recorded route is exactly prefix or sits
// beneath it.
//
// Whole patterns are compared rather than substrings. A substring check is
// satisfied by "/sqlx" when it was asked for "/sql", which is exactly the typo
// this assertion exists to catch.
func hasRouteUnder(rendered, prefix string) bool {
	for _, line := range strings.Split(rendered, "\n") {
		_, route, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		route = strings.TrimSpace(route)
		if route == prefix || strings.HasPrefix(route, prefix+"/") {
			return true
		}
	}
	return false
}

// TestGenerateRouteGoldens rewrites the goldens. It is skipped unless
// GENERATE_ROUTE_GOLDENS is set, so an ordinary run only ever compares.
func TestGenerateRouteGoldens(t *testing.T) {
	if os.Getenv("GENERATE_ROUTE_GOLDENS") == "" {
		t.Skip("set GENERATE_ROUTE_GOLDENS=1 to rewrite server/testdata/routes-*.txt")
	}
	for _, sc := range routeScenarios() {
		got := renderRoutes(t, sc)
		path := filepath.Join("testdata", sc.golden)
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
	}
}

// renderRoutes builds the mux for a scenario and returns its routes, one
// "METHOD /pattern" per line, sorted for a stable diff.
func renderRoutes(t *testing.T, sc routeScenario) string {
	t.Helper()
	clearAmbientEnv(t)

	handler := buildOuterMux(
		sc.cfg,
		func(*http.Request) *proxy.RouteManifest { return sc.manifest },
		func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(sc.cfg, sc.manifest, "") },
		http.NotFoundHandler(),
		nil,
		"routetable-test",
		nil,
		nil,
	)
	mux, ok := handler.(*chi.Mux)
	if !ok {
		t.Fatalf("standalone mode with no CORS origins should return the bare *chi.Mux, got %T", handler)
	}

	var lines []string
	walk := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		lines = append(lines, fmt.Sprintf("%-7s %s", method, route))
		return nil
	}
	if err := chi.Walk(mux, walk); err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// clearAmbientEnv removes the variables the builder reads directly, so the
// recorded table is a function of the scenario and not of the developer's shell.
// That these reads exist at all is one of the things the refactor removes.
func clearAmbientEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SUPATYPE_MODE",
		"SUPATYPE_SERVICE_ROLE_KEY",
		"SUPATYPE_CONTROL_PLANE_URL",
		"SUPATYPE_SQL_DATABASE_URL",
		"DATABASE_URL",
		"STUDIO_OPEN_DEV",
		"STUDIO_ADMIN_ROLES",
	} {
		t.Setenv(key, "")
	}
}
