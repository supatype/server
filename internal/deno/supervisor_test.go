package deno

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The supervisor had no test that started anything. What it is for is keeping a
// crashed worker coming back and keeping its output where `supatype dev` can
// show it, and neither was exercised.

// TestChildProcess is not a test. It is the program the supervisor runs when a
// test replaces the command seam, and it only does anything when the
// environment says so.
//
// This is the standard way to test os/exec without shipping a fixture binary:
// the test binary re-runs itself.
func TestChildProcess(t *testing.T) {
	behaviour := os.Getenv("DENO_TEST_CHILD")
	if behaviour == "" {
		t.Skip("not the child process")
	}

	switch behaviour {
	case "talk":
		fmt.Fprintln(os.Stdout, "listening on port "+os.Getenv("PORT"))
		fmt.Fprintln(os.Stderr, "a warning")
		fmt.Fprintln(os.Stdout, "second line")
	case "crash":
		fmt.Fprintln(os.Stderr, "crashing")
		os.Exit(1)
	case "linger":
		<-time.After(30 * time.Second)
	}
	os.Exit(0)
}

// childCommand replaces the seam with a run of TestChildProcess.
//
// The behaviour is chosen through the Manager's own extra environment rather
// than the command's, because run sets cmd.Env itself — which is the whole
// point of envForDenoProcess and would otherwise be bypassed here.
// The counter is atomic because the supervisor starts children from its own
// goroutine while the test reads the count from this one.
func childCommand(t *testing.T, counter *atomic.Int64) {
	t.Helper()

	original := command
	t.Cleanup(func() { command = original })
	command = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		if counter != nil {
			counter.Add(1)
		}
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestChildProcess")
	}
}

// manager returns a Manager whose paths pass validation and whose child, when
// the seam is in place, behaves as named.
func manager(t *testing.T, behaviour string) *Manager {
	t.Helper()
	entry := filepath.Join(t.TempDir(), "functions-router.ts")
	if err := os.WriteFile(entry, []byte("// router"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"EXTRA=1"}
	if behaviour != "" {
		env = append(env, "DENO_TEST_CHILD="+behaviour)
	}
	return New(filepath.Join(t.TempDir(), "deno"), entry, 8001, env, false)
}

// waitFor polls until condition holds, so a test does not have to guess how
// long a child takes to start.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ─── Running ──────────────────────────────────────────────────────────────────

// Both streams reach the ring buffer, labelled, so `supatype dev` can show a
// worker's own output and tell a log line from an error.
func TestBothStreamsAreCaptured(t *testing.T) {
	childCommand(t, nil)
	m := manager(t, "talk")

	if err := m.run(context.Background()); err != nil {
		t.Fatalf("the child exited cleanly, so run should not report: %v", err)
	}

	logs := m.RecentLogs(time.Time{}, 100)
	var info, errs []string
	for _, line := range logs {
		switch line.Level {
		case "info":
			info = append(info, line.Message)
		case "error":
			errs = append(errs, line.Message)
		}
	}

	if len(info) != 2 || !strings.Contains(info[0], "listening on port 8001") {
		t.Errorf("stdout = %v, want both lines with the port the manager chose", info)
	}
	if info[1] != "second line" {
		t.Errorf("stdout lines are out of order: %v", info)
	}
	if len(errs) != 1 || errs[0] != "a warning" {
		t.Errorf("stderr = %v", errs)
	}
}

// A child that exits non-zero is a crash, and run has to say so or the
// supervisor will not restart it.
func TestACrashIsReported(t *testing.T) {
	childCommand(t, nil)
	m := manager(t, "crash")

	if err := m.run(context.Background()); err == nil {
		t.Error("a non-zero exit was not reported")
	}
	if logs := m.RecentLogs(time.Time{}, 10); len(logs) == 0 {
		t.Error("the child's last words were not captured")
	}
}

// Cancelling the context stops the child rather than leaving it running.
func TestCancellingStopsTheChild(t *testing.T) {
	childCommand(t, nil)
	m := manager(t, "linger")

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- m.run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the child outlived its context")
	}
}

// ─── Supervision ──────────────────────────────────────────────────────────────

// A crashed worker comes back. Without this, one transient failure at boot
// leaves functions dead until someone notices.
func TestACrashedWorkerIsRestarted(t *testing.T) {
	var starts atomic.Int64
	childCommand(t, &starts)

	m := manager(t, "crash")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)
	// The first backoff is a second, so a second start is the proof that the
	// schedule is being waited out rather than skipped.
	waitFor(t, "a restart", func() bool { return starts.Load() >= 2 })
	m.Stop()
}

// Stop ends the loop rather than restarting for ever.
func TestStopEndsTheLoop(t *testing.T) {
	childCommand(t, nil)
	m := manager(t, "crash")

	m.Start(context.Background())
	m.Stop()

	// Stopping is idempotent: a shutdown path that panics on a second call is a
	// shutdown path nobody can call safely.
	m.Stop()
}

// A loop asked to run with a context that is already done does nothing at all.
func TestRunLoopOnACancelledContext(t *testing.T) {
	var starts atomic.Int64
	childCommand(t, &starts)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager(t, "talk").runLoop(ctx)

	if got := starts.Load(); got != 0 {
		t.Errorf("started %d children under a cancelled context", got)
	}
}

// A Manager already stopped does not start anything when its loop runs.
func TestRunLoopOnAStoppedManager(t *testing.T) {
	var starts atomic.Int64
	childCommand(t, &starts)

	m := manager(t, "talk")
	// Through Stop, not by writing the field: the flag is behind a mutex and a
	// test that reaches past it is not testing what production does.
	m.Stop()
	m.runLoop(context.Background())

	if got := starts.Load(); got != 0 {
		t.Errorf("started %d children after Stop", got)
	}
}

// A worker that exits cleanly is still restarted, but without waiting: a clean
// exit is not a crash loop.
func TestACleanExitResetsTheBackoff(t *testing.T) {
	var starts atomic.Int64
	childCommand(t, &starts)

	m := manager(t, "talk")
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)

	// No backoff is waited out, so several restarts happen quickly.
	waitFor(t, "restarts without a backoff", func() bool { return starts.Load() >= 3 })
	cancel()
	m.Stop()
}

// Stop before Start has no cancel function to call, and must not panic on the
// way past it.
func TestStopBeforeStart(t *testing.T) {
	manager(t, "").Stop()
}

// ─── Refusals ─────────────────────────────────────────────────────────────────

// The paths are interpolated into a process launch, so anything that is not a
// plain path is refused rather than cleaned up and run anyway.
func TestValidateCommandPath(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "deno")

	for name, tc := range map[string]struct {
		path    string
		wantErr bool
	}{
		"an absolute path":      {absolute, false},
		"a local relative path": {filepath.Join("bin", "deno"), false},
		"nothing":               {"", true},
		"only whitespace":       {"   ", true},
		"a null byte":           {"deno\x00rm", true},
		"an unclean path":       {filepath.Join("bin", "..", "deno") + string(filepath.Separator) + ".", true},
		"a parent reference":    {filepath.Join("..", "deno"), true},
	} {
		err := validateCommandPath(tc.path, "deno path")
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: validateCommandPath(%q) = %v, want error = %v", name, tc.path, err, tc.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "deno path") {
			t.Errorf("%s: the message should name what was wrong: %v", name, err)
		}
	}
}

// Both paths are checked, so a valid binary with a nonsense entry is refused
// too.
func TestRunRefusesEitherBadPath(t *testing.T) {
	entry := filepath.Join(t.TempDir(), "router.ts")
	if err := os.WriteFile(entry, []byte("//"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := New("", entry, 8001, nil, false).run(context.Background()); err == nil {
		t.Error("an empty deno path was accepted")
	}
	if err := New(filepath.Join(t.TempDir(), "deno"), "", 8001, nil, false).run(context.Background()); err == nil {
		t.Error("an empty serve entry was accepted")
	}
}

// A binary that is not there cannot be started, and that is worth reporting
// rather than looping silently.
func TestRunReportsAStartFailure(t *testing.T) {
	m := manager(t, "")
	if err := m.run(context.Background()); err == nil {
		t.Error("want an error when the binary does not exist")
	}
}

// --watch is what makes `supatype dev` hot-reload, so whether it is passed has
// to follow the flag.
func TestWatchAddsTheFlag(t *testing.T) {
	for _, watch := range []bool{true, false} {
		var got []string
		original := command
		command = func(ctx context.Context, path string, args ...string) *exec.Cmd {
			got = args
			return exec.CommandContext(ctx, os.Args[0], "-test.run=TestChildProcess")
		}

		entry := filepath.Join(t.TempDir(), "router.ts")
		if err := os.WriteFile(entry, []byte("//"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = New(filepath.Join(t.TempDir(), "deno"), entry, 8001, nil, watch).run(context.Background())
		command = original

		hasWatch := len(got) > 1 && got[1] == "--watch"
		if hasWatch != watch {
			t.Errorf("watch = %v, args = %v", watch, got)
		}
		if len(got) == 0 || got[0] != "run" {
			t.Errorf("args = %v, want `deno run`", got)
		}
		if got[len(got)-1] != entry {
			t.Errorf("the entry is not last: %v", got)
		}
	}
}

// ─── Logs ─────────────────────────────────────────────────────────────────────

// The buffer is bounded, or a chatty worker eventually costs the server its
// memory.
func TestTheLogBufferIsBounded(t *testing.T) {
	m := manager(t, "")
	for i := 0; i < logRingSize+50; i++ {
		m.appendLog("info", fmt.Sprintf("line %d", i))
	}

	logs := m.RecentLogs(time.Time{}, logRingSize+100)
	if len(logs) != logRingSize {
		t.Fatalf("kept %d lines, want the ring size %d", len(logs), logRingSize)
	}
	// The oldest went first.
	if logs[0].Message != "line 50" {
		t.Errorf("oldest kept = %q, want line 50", logs[0].Message)
	}
	if logs[len(logs)-1].Message != fmt.Sprintf("line %d", logRingSize+49) {
		t.Errorf("newest = %q", logs[len(logs)-1].Message)
	}
}

// Logs come back oldest first, which is the order anyone reading them expects,
// and n caps from the newest end.
func TestRecentLogsOrderAndLimit(t *testing.T) {
	m := manager(t, "")
	for i := 0; i < 5; i++ {
		m.appendLog("info", fmt.Sprintf("line %d", i))
	}

	all := m.RecentLogs(time.Time{}, 10)
	if len(all) != 5 || all[0].Message != "line 0" || all[4].Message != "line 4" {
		t.Fatalf("logs = %v, want chronological", all)
	}

	last := m.RecentLogs(time.Time{}, 2)
	if len(last) != 2 || last[0].Message != "line 3" || last[1].Message != "line 4" {
		t.Errorf("last two = %v, want the newest two in order", last)
	}
}

// Asking for logs since a moment gets only what happened after it, which is
// how a follower avoids repeating itself.
func TestRecentLogsSince(t *testing.T) {
	m := manager(t, "")
	m.appendLog("info", "before")

	// The timestamps are UTC to the nanosecond, so a marker taken now is after
	// the first line and before the second.
	time.Sleep(2 * time.Millisecond)
	marker := time.Now().UTC()
	time.Sleep(2 * time.Millisecond)
	m.appendLog("info", "after")

	got := m.RecentLogs(marker, 10)
	if len(got) != 1 || got[0].Message != "after" {
		t.Errorf("logs = %v, want only what came after the marker", got)
	}
}

// Nothing logged is an empty answer, not a nil one to be checked for.
func TestRecentLogsOnAQuietWorker(t *testing.T) {
	if got := manager(t, "").RecentLogs(time.Time{}, 10); len(got) != 0 {
		t.Errorf("logs = %v", got)
	}
}

// A shutdown during the restart wait must not sit out the backoff first: on a
// 30-second backoff that would hold the whole server's shutdown open.
func TestCancellingDuringTheBackoffReturnsAtOnce(t *testing.T) {
	var starts atomic.Int64
	childCommand(t, &starts)

	m := manager(t, "crash")
	ctx, cancel := context.WithCancel(context.Background())

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		m.runLoop(ctx)
	}()

	waitFor(t, "the first crash", func() bool { return starts.Load() >= 1 })
	// Settle, so the loop is inside the backoff wait rather than still running
	// the child: cancelling before it fails takes a different path out.
	time.Sleep(backoffInitial / 4)
	cancel()

	select {
	case <-finished:
	case <-time.After(backoffInitial / 2):
		t.Fatal("the loop waited out the backoff before noticing the cancellation")
	}
}

// StdoutPipe refuses when the command already has a destination. Unreachable
// through the supervisor, which sets neither, but a nil pipe passed to the
// scanner would panic rather than report.
func TestRunReportsAPipeItCannotOpen(t *testing.T) {
	for name, prepare := range map[string]func(*exec.Cmd){
		"stdout is already taken": func(cmd *exec.Cmd) { cmd.Stdout = io.Discard },
		"stderr is already taken": func(cmd *exec.Cmd) { cmd.Stderr = io.Discard },
	} {
		original := command
		command = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestChildProcess")
			prepare(cmd)
			return cmd
		}
		err := manager(t, "talk").run(context.Background())
		command = original

		if err == nil {
			t.Errorf("%s: want an error", name)
		} else if !strings.Contains(err.Error(), "pipe") {
			t.Errorf("%s: the error should name the pipe: %v", name, err)
		}
	}
}
