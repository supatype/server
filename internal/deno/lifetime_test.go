package deno

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// 0.9.1 release test (FF12): after `supatype dev` stopped, the Deno worker the
// server had started was still running and still held the functions port, so
// the next dev refused to start. A worker must not outlive the server that
// started it, however the server ends.

// TestServerProcess is not a test. It is a stand-in server: it starts the
// supervisor with a long-running stand-in worker and waits to be killed.
func TestServerProcess(t *testing.T) {
	pidFile := os.Getenv("DENO_TEST_SERVER_PIDFILE")
	if pidFile == "" {
		t.Skip("not the server process")
	}
	command = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return selfCommand(ctx)
	}
	m := manager(t, "record")
	m.env = append(m.env, "DENO_TEST_PIDFILE="+pidFile)
	m.Start(context.Background())
	<-time.After(time.Minute)
}

// The server is killed outright, with no chance to stop its worker: a crash,
// or a kill from the task manager. The worker goes with it.
func TestTheWorkerDoesNotOutliveTheServer(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "worker.pid")
	// #nosec G204,G702 -- the only argument is this test binary's own path.
	server := exec.Command(os.Args[0], "-test.run=^TestServerProcess$", "-test.timeout=2m")
	server.Env = append(os.Environ(), "DENO_TEST_SERVER_PIDFILE="+pidFile)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })

	var worker int
	waitFor(t, "the worker to start", func() bool {
		worker = readPID(pidFile)
		return worker > 0
	})
	t.Cleanup(func() { killPID(worker) })

	if err := server.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = server.Wait()

	deadline := time.Now().Add(10 * time.Second)
	for processAlive(worker) {
		if time.Now().After(deadline) {
			t.Fatalf("worker %d is still running 10s after its server was killed", worker)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Windows can refuse to put the worker in the server's job object. Functions
// still work then: the worker runs, and the server warns that it may outlive
// a server that is killed.
func TestAWorkerTheServerCannotAdoptStillRuns(t *testing.T) {
	childCommand(t, nil)
	original := adopt
	t.Cleanup(func() { adopt = original })
	adopt = func(*os.Process) error { return errors.New("access is denied") }
	hook := logtest.NewGlobal()
	t.Cleanup(hook.Reset)

	m := manager(t, "talk")
	if err := m.run(context.Background()); err != nil {
		t.Fatalf("the worker should run and exit cleanly: %v", err)
	}

	if len(m.RecentLogs(time.Time{}, 10)) == 0 {
		t.Error("the worker did not run")
	}
	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.WarnLevel && strings.Contains(entry.Message, "may outlive") {
			return
		}
	}
	t.Error("no warning that the worker may outlive the server")
}

// readPID is the PID the stand-in worker recorded, or 0 before it has.
func readPID(path string) int {
	raw, err := os.ReadFile(path) // #nosec G304 -- a path this test created.
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	return pid
}

func killPID(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
