package gateway

// Where a functions or realtime request is sent, and the rules that decide it.
//
// These resolve per request rather than per mount, because a managed deployment
// picks the worker from the calling tenant's manifest.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/modelhooks"
	"github.com/supatype/server/internal/proxy"
)

func functionsInvocationProxy(
	cfg *config.Config,
	manifestFor func(*http.Request) *proxy.RouteManifest,
	inProcessDeno bool,
) http.Handler {
	opts := proxy.ProxyOpts{RequestTimeout: defaultUpstreamHTTPTimeout}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		m := manifestFor(req)
		if m != nil && !m.FunctionsEnabled && strings.TrimSpace(cfg.FunctionsWorkerURL) == "" {
			http.Error(w, "functions disabled", http.StatusNotFound)
			return
		}
		// Hooks are procedural: the API server calls them around a write. They live under a `hooks/`
		// route on the same worker, and that route is not a public endpoint — a caller holding the anon
		// key must not be able to invoke one directly with a payload of their own choosing.
		if seg := firstURLSegment(req.URL.Path); seg == "hooks" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		fnName := firstURLSegment(req.URL.Path)
		u, err := resolveFunctionsUpstreamURL(cfg, m, fnName, inProcessDeno)
		if err != nil {
			logrus.WithError(err).Error("mux: functions upstream resolve failed")
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		proxy.WebSocketProxy(u, proxy.New(u, opts)).ServeHTTP(w, req)
	})
}

// realtimeInvocationProxy forwards /realtime/v1 to the external realtime service.
func realtimeInvocationProxy(
	cfg *config.Config,
	manifestFor func(*http.Request) *proxy.RouteManifest,
) http.Handler {
	opts := proxy.ProxyOpts{RequestTimeout: defaultUpstreamHTTPTimeout}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		m := manifestFor(req)
		if m != nil && !m.RealtimeEnabled {
			http.Error(w, "realtime disabled", http.StatusNotFound)
			return
		}
		u, err := resolveRealtimeUpstreamURL(cfg, m)
		if err != nil {
			logrus.WithError(err).Error("mux: realtime upstream resolve failed")
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		proxy.WebSocketProxy(u, proxy.New(u, opts)).ServeHTTP(w, req)
	})
}

func resolveRealtimeUpstreamURL(
	cfg *config.Config,
	m *proxy.RouteManifest,
) (*url.URL, error) {
	if m != nil {
		if u := strings.TrimSpace(m.RealtimeURL); u != "" {
			return url.Parse(u)
		}
	}
	if u := strings.TrimSpace(cfg.RealtimeURL); u != "" {
		return url.Parse(u)
	}
	return nil, fmt.Errorf("no realtime upstream configured")
}

func firstURLSegment(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	if i := strings.Index(path, "/"); i >= 0 {
		return path[:i]
	}
	return path
}

// hookUpstreamURL is where a hook invocation is sent.
//
// Extracted from the mount so the rule can be tested without building a mux: which worker serves a
// hook is the difference between a hooked table working and every write to it answering 503.
func hookUpstreamURL(
	cfg *config.Config,
	m *proxy.RouteManifest,
	function string,
	inProcessDeno bool,
) (string, error) {
	// A hook's own Deployment is registered under its namespaced name, so a project may have a hook and
	// a public function sharing a name without one resolving to the other's pod.
	if m != nil {
		if u := strings.TrimSpace(m.FunctionWorkerURLs[modelhooks.HooksRoutePrefix+function]); u != "" {
			return strings.TrimRight(u, "/") + "/" + modelhooks.HooksRoutePrefix + function, nil
		}
	}

	// Empty function name on purpose: the per-function map has already been consulted under the
	// namespaced key, and consulting it again under the bare name is exactly the collision above. What
	// is left is the project's own worker.
	base, err := resolveFunctionsUpstreamURL(cfg, m, "", inProcessDeno)
	if err != nil {
		return "", err
	}
	// The invocation proxy forwards the request path, so what comes back is a base and the route has to
	// be appended for a direct call. `hooks/` is the namespace the worker serves them under, and the one
	// the public functions path refuses.
	return strings.TrimRight(base.String(), "/") + "/" + modelhooks.HooksRoutePrefix + function, nil
}

func resolveFunctionsUpstreamURL(
	cfg *config.Config,
	m *proxy.RouteManifest,
	fnName string,
	inProcessDeno bool,
) (*url.URL, error) {
	if m != nil && fnName != "" {
		if u := strings.TrimSpace(m.FunctionWorkerURLs[fnName]); u != "" {
			return url.Parse(u)
		}
	}
	if m != nil {
		if u := strings.TrimSpace(m.FunctionsWorkerURL); u != "" {
			return url.Parse(u)
		}
	}
	if u := strings.TrimSpace(cfg.FunctionsWorkerURL); u != "" {
		return url.Parse(u)
	}
	if inProcessDeno {
		return functionsUpstreamURL(cfg)
	}
	return nil, fmt.Errorf("no functions worker configured")
}

// functionsUpstreamURL resolves the in-process Deno subprocess target.
func functionsUpstreamURL(cfg *config.Config) (*url.URL, error) {
	if u := strings.TrimSpace(cfg.FunctionsWorkerURL); u != "" {
		parsed, err := url.Parse(u)
		if err != nil {
			return nil, err
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("SUPATYPE_FUNCTIONS_WORKER_URL must include scheme and host")
		}
		return parsed, nil
	}
	return &url.URL{
		Scheme: "http",
		Host:   "127.0.0.1:" + cfg.DenoPort,
	}, nil
}
