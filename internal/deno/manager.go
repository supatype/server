package deno

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	backoffInitial = 1 * time.Second
	backoffMax     = 30 * time.Second
	logRingSize    = 1000 // max log lines retained in memory
)

// LogLine is a captured line from the Deno process stdout/stderr.
type LogLine struct {
	Timestamp time.Time
	Level     string // "info" or "error"
	Message   string
}

// Manager supervises a Deno process that serves edge functions.
// It spawns `deno run [--watch] --allow-net --allow-env --allow-read {serveEntry}` where
// serveEntry is the generated router (uses `Deno.serve` internally — equivalent role to a
// dedicated `deno serve` file server, but `deno run` is required for our multi-route TS entry).
// Restarts on crash with exponential backoff (1s → 2s → 4s → … → 30s cap).
// PORT must be communicated via environment; the child's PORT is overridden so it cannot
// inherit the main server's listen port by mistake.
type Manager struct {
	denoPath   string
	serveEntry string // absolute path to generated router .ts (or legacy path)
	port       int
	env        []string // extra env vars in "KEY=VALUE" form
	watch      bool     // when true, Deno is started with --watch (dev hot reload)

	mu      sync.Mutex
	cancel  context.CancelFunc
	stopped bool

	// Log ring buffer — capped at logRingSize entries.
	logMu  sync.RWMutex
	logBuf []LogLine
}

// New creates a Manager. serveEntry is the script path passed to `deno run`
// (router generated under .supatype/functions-router.ts during `supatype dev`).
// port is the port Deno will listen on. env is appended after inheriting OS env minus PORT=
// plus the explicit PORT=value for Deno children.
// When watch is true, `deno run --watch` reloads the worker when source files change.
func New(denoPath, serveEntry string, port int, env []string, watch bool) *Manager {
	return &Manager{
		denoPath:   denoPath,
		serveEntry: serveEntry,
		port:       port,
		env:        env,
		watch:      watch,
		logBuf:     make([]LogLine, 0, logRingSize),
	}
}

// Start spawns the Deno process and begins the crash-restart loop.
// It returns immediately; the process runs in background goroutines.
// ctx cancellation (or Stop) shuts Deno down cleanly.
func (m *Manager) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	m.cancel = cancel
	m.stopped = false
	m.mu.Unlock()

	go m.runLoop(ctx)
}

// Stop signals the Deno process to terminate and stops the restart loop.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stopped = true
	if m.cancel != nil {
		m.cancel()
	}
}

// RecentLogs returns up to n log lines captured since the given time.
// If since is zero, all buffered lines are returned.
func (m *Manager) RecentLogs(since time.Time, n int) []LogLine {
	m.logMu.RLock()
	defer m.logMu.RUnlock()

	result := make([]LogLine, 0, n)
	for i := len(m.logBuf) - 1; i >= 0 && len(result) < n; i-- {
		l := m.logBuf[i]
		if since.IsZero() || l.Timestamp.After(since) {
			result = append(result, l)
		}
	}
	// Reverse to chronological order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func (m *Manager) appendLog(level, message string) {
	m.logMu.Lock()
	defer m.logMu.Unlock()

	if len(m.logBuf) >= logRingSize {
		// Drop oldest entry.
		m.logBuf = m.logBuf[1:]
	}
	m.logBuf = append(m.logBuf, LogLine{
		Timestamp: time.Now().UTC(),
		Level:     level,
		Message:   message,
	})
}

func (m *Manager) runLoop(ctx context.Context) {
	backoff := backoffInitial

	for {
		if ctx.Err() != nil {
			return
		}

		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		logrus.WithFields(logrus.Fields{
			"deno":  m.denoPath,
			"entry": m.serveEntry,
			"port":  m.port,
		}).Info("deno: starting edge functions server")

		if err := m.run(ctx); err != nil {
			if ctx.Err() != nil {
				// Context cancelled — clean shutdown, not a crash.
				return
			}

			logrus.WithError(err).Warnf("deno: process exited, restarting in %s", backoff)

			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}

			backoff = min(backoff*2, backoffMax)
		} else {
			// Clean exit — reset backoff.
			backoff = backoffInitial
		}
	}
}

// run spawns a single Deno process and blocks until it exits.
func (m *Manager) run(ctx context.Context) error {
	args := []string{"run"}
	if m.watch {
		args = append(args, "--watch")
	}
	args = append(args,
		"--allow-net",
		"--allow-env",
		"--allow-read",
		m.serveEntry,
	)

	cmd := exec.CommandContext(ctx, m.denoPath, args...)
	cmd.Env = envForDenoProcess(m.port, m.env)

	// Capture stdout and stderr into the ring buffer.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("deno stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("deno stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("deno start: %w", err)
	}

	// Drain stdout and stderr concurrently into the ring buffer.
	var wg sync.WaitGroup
	drain := func(r io.Reader, level string) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			m.appendLog(level, line)
		}
	}
	wg.Add(2)
	go drain(stdout, "info")
	go drain(stderr, "error")
	wg.Wait()

	return cmd.Wait()
}

func envForDenoProcess(port int, extra []string) []string {
	var out []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "PORT=") {
			continue
		}
		out = append(out, e)
	}
	out = append(out, extra...)
	out = append(out, fmt.Sprintf("PORT=%d", port))
	return out
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
