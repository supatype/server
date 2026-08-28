package gateway

import (
	"testing"

	"github.com/supatype/server/internal/proxy"
	serverconf "github.com/supatype/server/internal/serverconf"
)

// Which worker serves a hook is the difference between a hooked table working and every write to it
// answering 503, and it is decided by a lookup order that reads as arbitrary until it is written down.
func TestAHookGoesToItsOwnDeploymentWhenOneIsRegistered(t *testing.T) {
	m := &proxy.RouteManifest{
		FunctionWorkerURLs: map[string]string{"hooks/moderate-post": "http://fn-moderate-post.p.svc:8001"},
		FunctionsWorkerURL: "http://functions-worker.p.svc:8001",
	}

	got, err := hookUpstreamURL(&serverconf.ServerConfig{}, m, "moderate-post", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://fn-moderate-post.p.svc:8001/hooks/moderate-post" {
		t.Fatalf("resolved to %q", got)
	}
}

func TestAHookNeverResolvesToAFunctionOfTheSameName(t *testing.T) {
	// Free-tier projects run one Deployment per function, so both kinds of route register URLs in this
	// map. A hook falling back to the bare name would send the call to that function's pod, which would
	// answer 404 — and a 404 is "the hook did not answer", so writes to the table start failing.
	m := &proxy.RouteManifest{
		FunctionWorkerURLs: map[string]string{"moderate-post": "http://fn-public.p.svc:8001"},
		FunctionsWorkerURL: "http://functions-worker.p.svc:8001",
	}

	got, err := hookUpstreamURL(&serverconf.ServerConfig{}, m, "moderate-post", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://functions-worker.p.svc:8001/hooks/moderate-post" {
		t.Fatalf("resolved to %q, which is the public function's own pod", got)
	}
}

func TestAHookFallsBackToTheProjectWorker(t *testing.T) {
	m := &proxy.RouteManifest{FunctionsWorkerURL: "http://functions-worker.p.svc:8001/"}

	got, err := hookUpstreamURL(&serverconf.ServerConfig{}, m, "moderate-post", false)
	if err != nil {
		t.Fatal(err)
	}
	// The trailing slash on the base must not survive into the path.
	if got != "http://functions-worker.p.svc:8001/hooks/moderate-post" {
		t.Fatalf("resolved to %q", got)
	}
}

func TestNoWorkerAtAllIsAnError(t *testing.T) {
	// Reported rather than guessed: the middleware turns this into "hook unavailable", which for a
	// before-hook refuses the write. Inventing a URL here would make it a silent skip instead.
	if _, err := hookUpstreamURL(&serverconf.ServerConfig{}, &proxy.RouteManifest{}, "moderate-post", false); err == nil {
		t.Fatal("resolving with no worker configured returned no error")
	}
}

func TestATenantManifestBeatsTheServerConfig(t *testing.T) {
	// The bug this pins: the resolver was passed a nil request, so a managed server used the platform's
	// file manifest for every tenant.
	cfg := &serverconf.ServerConfig{FunctionsWorkerURL: "http://platform-worker:8001"}
	m := &proxy.RouteManifest{FunctionsWorkerURL: "http://functions-worker.tenant-a.svc:8001"}

	got, err := hookUpstreamURL(cfg, m, "moderate-post", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://functions-worker.tenant-a.svc:8001/hooks/moderate-post" {
		t.Fatalf("resolved to %q, want the tenant's own worker", got)
	}
}
