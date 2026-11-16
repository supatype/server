package gateway

// Which functions the project has, asked of the workers calls are sent to.
//
// The functions directory this server holds is not where functions live once they run in workers:
// a per-project worker has its own directory, and a per-function pod serves one name. Listing the
// server's own directory there answered "no functions" about functions that were answering calls.
// So the list follows the routing the invocation proxy uses (resolveFunctionsUpstreamURL) instead
// of a platform switch.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/functions"
	"github.com/supatype/server/internal/modelhooks"
	"github.com/supatype/server/internal/proxy"
)

// workerListPath is the worker's reserved route for the functions it serves. The underscore prefix
// is one no function can have, and the public invocation proxy refuses it (reservedFunctionRoute).
const workerListPath = "/functions/v1/_functions"

const workerListTimeout = 10 * time.Second

// maxWorkerListBytes caps what is read of a worker's list, which is a few hundred bytes in practice.
const maxWorkerListBytes = 1 << 20

// functionsLister lists the functions this server would send calls to:
//   - every name the manifest gives its own worker (hooks are not functions);
//   - the functions the project or shared worker serves, asked of it;
//   - this server's own directory, when no worker is configured.
//
// A worker that cannot be asked fails the list (functions.ErrWorkerUnavailable). An empty answer
// there would read as "no functions deployed".
func functionsLister(
	cfg *config.Config,
	manifestFor func(*http.Request) *proxy.RouteManifest,
	client *http.Client,
) functions.Lister {
	own := functions.DirLister(cfg.DenoFunctionsDir)
	return func(r *http.Request) ([]functions.FunctionMeta, error) {
		var m *proxy.RouteManifest
		if manifestFor != nil {
			m = manifestFor(r)
		}
		base := projectWorkerURL(cfg, m)

		byName := map[string]functions.FunctionMeta{}
		perFunction := 0
		if m != nil {
			for name, u := range m.FunctionWorkerURLs {
				if strings.HasPrefix(name, modelhooks.HooksRoutePrefix) || strings.TrimSpace(u) == "" {
					continue
				}
				byName[name] = functions.FunctionMeta{Name: name}
				perFunction++
			}
		}

		switch {
		case base != "":
			served, err := askWorker(r.Context(), client, base)
			if err != nil {
				return nil, err
			}
			for _, f := range served {
				byName[f.Name] = f
			}
		case perFunction == 0:
			// Nothing routes to a worker: the directory this server runs functions from, if any.
			served, err := own(r)
			if err != nil {
				return nil, err
			}
			for _, f := range served {
				byName[f.Name] = f
			}
		}

		out := make([]functions.FunctionMeta, 0, len(byName))
		for _, f := range byName {
			out = append(out, f)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out, nil
	}
}

// projectWorkerURL is the worker that serves functions the manifest does not name one for: the
// same two steps resolveFunctionsUpstreamURL takes with no function name. Empty when there is none.
func projectWorkerURL(cfg *config.Config, m *proxy.RouteManifest) string {
	u, err := resolveFunctionsUpstreamURL(cfg, m, "", false)
	if err != nil {
		return ""
	}
	return strings.TrimRight(u.String(), "/")
}

// askWorker is what the worker at base says it serves.
func askWorker(ctx context.Context, client *http.Client, base string) ([]functions.FunctionMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, workerListTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+workerListPath, nil)
	if err != nil {
		// Not the error itself: it quotes the worker's address, which is not the caller's to know.
		return nil, fmt.Errorf("%w: the request could not be built", functions.ErrWorkerUnavailable)
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: could not reach it", functions.ErrWorkerUnavailable)
	}
	defer func() { _ = res.Body.Close() }()
	// A redirect is a failure, not something to chase: the client does not follow it (workerListClient).
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: it answered %d", functions.ErrWorkerUnavailable, res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxWorkerListBytes+1))
	if err != nil || len(raw) > maxWorkerListBytes {
		return nil, fmt.Errorf("%w: its list could not be read", functions.ErrWorkerUnavailable)
	}
	var body struct {
		Data []functions.FunctionMeta `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("%w: its list could not be read", functions.ErrWorkerUnavailable)
	}
	return body.Data, nil
}

// workerListClient is the client the list call uses. It never follows a redirect: a worker that
// answers one is pointing this server at somewhere the project did not configure.
func workerListClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
