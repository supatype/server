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
	"github.com/supatype/server/internal/serviceswitch"
)

func functionsInvocationProxy(
	cfg *config.Config,
	manifestFor func(*http.Request) *proxy.RouteManifest,
	inProcessDeno bool,
) http.Handler {
	opts := proxy.ProxyOpts{RequestTimeout: defaultUpstreamHTTPTimeout}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		m := manifestFor(req)
		if !serviceswitch.On(m, cfg, proxy.ServiceFunctions) {
			http.Error(w, "functions disabled", http.StatusNotFound)
			return
		}
		// A dot segment is refused outright. The worker resolves "." and ".." in the URL it receives, so
		// "x/../_logs" is a call to x as far as the check below can tell and `_logs` by the time the
		// worker has parsed it. 400 rather than 404: the path is malformed, not merely absent, and the
		// distinction keeps a probe from reading a refusal as "no such function".
		if hasDotSegment(req.URL) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Hooks are procedural: the API server calls them around a write. They live under a `hooks/`
		// route on the same worker, and that route is not a public endpoint, so a caller holding the anon
		// key must not be able to invoke one directly with a payload of their own choosing. The same goes
		// for the routes the worker answers itself (`_logs`, `_functions`): see reservedFunctionRoute.
		if reservedFunctionRoute(req.URL.Path) {
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
		if !serviceswitch.On(m, cfg, proxy.ServiceRealtime) {
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

// reservedFunctionRoute reports whether path names a route of the worker that is not a function:
// `hooks/`, or any first segment starting with an underscore. No function can have such a name (the
// worker skips them), the worker answers its own `_logs` and `_functions` there, and the server
// reaches those directly, never through this public path.
//
// The segment is read as the worker reads it, which drops one leading `/functions/v1` first. A
// caller sending `/functions/v1/functions/v1/_logs` is otherwise judged by the segment `functions`
// and reaches `_logs` all the same.
func reservedFunctionRoute(path string) bool {
	if rest, ok := strings.CutPrefix(path, "/functions/v1"); ok {
		path = strings.TrimPrefix(rest, "/")
	}
	seg := firstURLSegment(path)
	return seg == "hooks" || strings.HasPrefix(seg, "_")
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
		Host:   denoAddr(cfg),
	}, nil
}

// hasDotSegment reports whether u carries a "." or ".." path segment in any spelling a downstream
// URL parser would resolve. The decoded path catches "%2e%2e" and "x%2f..%2f_logs" (Go decodes both
// into Path), the raw path catches the encoded forms as sent, and each segment is unescaped once
// more so "%252e%252e" is refused too, though only a parser that decodes twice would act on it.
func hasDotSegment(u *url.URL) bool {
	for _, p := range []string{u.Path, u.RawPath} {
		for _, seg := range strings.Split(p, "/") {
			if isDotSegment(seg) {
				return true
			}
			if dec, err := url.PathUnescape(seg); err == nil && isDotSegment(dec) {
				return true
			}
		}
	}
	return false
}

func isDotSegment(seg string) bool {
	return seg == "." || seg == ".."
}
