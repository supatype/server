package gateway

// Whether this deployment runs edge functions in a Deno process of its own, and
// where they answer.
//
// Three deployments want three different things. A hosted tenant is given an
// external worker and must not start a second one. A local `supatype dev` has a
// functions directory and a Deno on PATH, and wants the subprocess. A server
// with neither runs without functions, which is a warning rather than a refusal
// to start: the rest of the stack is still worth serving.

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/deno"
	"github.com/supatype/server/internal/utilities"
)

// defaultDenoPort is where the subprocess listens when none is configured. The
// configuration defaults to the same value; this covers a deployment that set
// the variable to nothing.
const defaultDenoPort = "8001"

// lookPath is exec.LookPath, swapped in tests. Whether Deno is installed is the
// one input to this decision that no configuration can supply, and CI does not
// install it.
var lookPath = exec.LookPath

// denoSupervisor is the in-process Deno worker for this configuration, or nil
// when there is not one to run. It builds the supervisor without starting it,
// so the rule can be tested without a subprocess.
func denoSupervisor(cfg *config.Config, externalURL string) *deno.Manager {
	if strings.TrimSpace(cfg.FunctionsWorkerURL) != "" {
		// Someone else is already serving them, and a second process would bind
		// the same port and answer for nobody.
		return nil
	}
	if cfg.DenoFunctionsDir == "" || cfg.DenoPath == "" {
		return nil
	}
	if _, err := lookPath(cfg.DenoPath); err != nil {
		logrus.WithError(err).
			Warn("serve: Deno not found on PATH, edge function invocations disabled; install Deno or set SUPATYPE_DENO_PATH")
		return nil
	}

	// The CLI generates a router script and names it; a project with only a
	// directory has Deno serve that instead.
	entry := utilities.FirstNonEmpty(strings.TrimSpace(cfg.DenoServeScript), cfg.DenoFunctionsDir)

	return deno.New(
		cfg.DenoPath,
		entry,
		denoPort(cfg.DenoPort),
		deno.EdgeSubprocessEnv(cfg, strings.TrimSpace(externalURL)),
		strings.TrimSpace(cfg.Mode) == "dev",
	)
}

// denoPort is the configured port, or the default when it is unset or is not a
// number. A typo is not worth refusing to start over.
func denoPort(configured string) int {
	port, err := strconv.Atoi(strings.TrimSpace(configured))
	if err != nil {
		port, _ = strconv.Atoi(defaultDenoPort)
	}
	return port
}

// denoBaseURL is where the health probes and the functions admin API look for
// edge functions: an external worker if one is configured, this deployment's own
// subprocess if it started one, and nowhere when it serves no functions at all.
func denoBaseURL(cfg *config.Config, worker string, supervisor *deno.Manager) string {
	if cfg.DenoFunctionsDir == "" {
		return ""
	}
	if worker != "" {
		return worker
	}
	if supervisor == nil {
		return ""
	}
	return "http://127.0.0.1:" + utilities.FirstNonEmpty(cfg.DenoPort, defaultDenoPort)
}
