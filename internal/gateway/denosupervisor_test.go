package gateway

import (
	"errors"
	"testing"

	"github.com/supatype/server/internal/config"
)

// installed makes lookPath report that Deno is there, which on a machine
// without it is the only way to reach the branch that builds the supervisor.
func installed(t *testing.T, found bool) {
	t.Helper()
	previous := lookPath
	t.Cleanup(func() { lookPath = previous })
	lookPath = func(file string) (string, error) {
		if !found {
			return "", errors.New("executable file not found in $PATH")
		}
		return "/usr/local/bin/" + file, nil
	}
}

// Whether a deployment runs edge functions in a process of its own. Starting a
// second one alongside a configured worker would bind the same port and answer
// for nobody; not starting one where it is wanted leaves every function 502.
func TestWhetherToRunDenoInProcess(t *testing.T) {
	dir := t.TempDir()

	for name, tc := range map[string]struct {
		cfg   config.Config
		deno  bool
		wants bool
	}{
		"a functions directory and Deno installed": {
			config.Config{DenoFunctionsDir: dir, DenoPath: "deno"}, true, true,
		},
		"an external worker is already serving them": {
			config.Config{DenoFunctionsDir: dir, DenoPath: "deno", FunctionsWorkerURL: "http://worker:8000"}, true, false,
		},
		"no functions directory": {
			config.Config{DenoPath: "deno"}, true, false,
		},
		"no Deno configured": {
			config.Config{DenoFunctionsDir: dir}, true, false,
		},
		"Deno is not installed": {
			config.Config{DenoFunctionsDir: dir, DenoPath: "deno"}, false, false,
		},
	} {
		installed(t, tc.deno)
		cfg := tc.cfg

		if got := denoSupervisor(&cfg, "http://localhost:9999") != nil; got != tc.wants {
			t.Errorf("%s: supervisor = %v, want %v", name, got, tc.wants)
		}
	}
}

// The port it listens on. A typo is not worth refusing to start over, and the
// configuration's own default is the same number, so an unset value and an
// unparseable one land in the same place.
func TestTheDenoPort(t *testing.T) {
	for configured, want := range map[string]int{
		"9001":       9001,
		" 9001 ":     9001,
		"":           8001,
		"not-a-port": 8001,
	} {
		if got := denoPort(configured); got != want {
			t.Errorf("denoPort(%q) = %d, want %d", configured, got, want)
		}
	}
}

// Where the health probes and the functions admin API look for edge functions.
// Naming a subprocess that was never started is what makes a probe report a
// service that cannot answer.
func TestWhereEdgeFunctionsAnswer(t *testing.T) {
	dir := t.TempDir()
	installed(t, true)

	running := denoSupervisor(&config.Config{DenoFunctionsDir: dir, DenoPath: "deno"}, "")
	if running == nil {
		t.Fatal("no supervisor to describe")
	}

	for name, tc := range map[string]struct {
		cfg    config.Config
		worker string
		sup    bool
		want   string
	}{
		"its own subprocess": {
			config.Config{DenoFunctionsDir: dir, DenoPort: "9001"}, "", true, "http://127.0.0.1:9001",
		},
		"with no port configured": {
			config.Config{DenoFunctionsDir: dir}, "", true, "http://127.0.0.1:8001",
		},
		"an external worker wins": {
			config.Config{DenoFunctionsDir: dir, DenoPort: "9001"}, "http://worker:8000", true, "http://worker:8000",
		},
		"nothing started and no worker": {
			config.Config{DenoFunctionsDir: dir}, "", false, "",
		},
		"no functions at all": {
			config.Config{}, "http://worker:8000", true, "",
		},
	} {
		cfg := tc.cfg
		supervisor := running
		if !tc.sup {
			supervisor = nil
		}

		if got := denoBaseURL(&cfg, tc.worker, supervisor); got != tc.want {
			t.Errorf("%s: base = %q, want %q", name, got, tc.want)
		}
	}
}
