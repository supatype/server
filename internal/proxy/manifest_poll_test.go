package proxy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/supatype/server/internal/filestamp"
)

// The manifest is reloaded even where the OS never reports the write.
//
// fsnotify does not deliver for a host write through a Docker bind mount, which is how every
// self-host stack on Docker Desktop runs. Verified on a live stack before this was written:
// rewriting the manifest on the host updated the file's mtime inside the container and produced no
// event at all. `supatype push` rewrote the hook map, the validator map and the cache ceiling, and
// the server served the previous ones until it was restarted.
//
// Driven through pollLoop directly rather than through Watch, because Watch also starts fsnotify
// and on a developer machine that works. A test that went through Watch would pass on the event
// path and prove nothing about the one that had to be added.

func writePollManifest(t *testing.T, path, schema string) {
	t.Helper()
	body := `{"schema":"` + schema + `","realtime_enabled":false,"functions_enabled":false}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// poll starts a loop and returns the channel it reports reloads on, plus its stop func.
func poll(t *testing.T, path string) (<-chan *RouteManifest, func()) {
	t.Helper()
	reloaded := make(chan *RouteManifest, 4)
	done := make(chan struct{})
	seed, _ := filestamp.Of(path)
	go pollLoop(path, 10*time.Millisecond, func(m *RouteManifest) { reloaded <- m }, done, seed)
	return reloaded, func() { close(done) }
}

func TestPollLoopReloadsAChangeNothingReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	writePollManifest(t, path, "public")

	reloaded, stop := poll(t, path)
	defer stop()

	// The mtime has whole-second resolution on some filesystems, so the size changes too. That is
	// also true of a real push, which never rewrites a manifest to exactly the same length.
	writePollManifest(t, path, "rewritten_by_push")

	select {
	case m := <-reloaded:
		if m.Schema != "rewritten_by_push" {
			t.Fatalf("reloaded the wrong content: %q", m.Schema)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the manifest changed on disk and was never reloaded")
	}
}

func TestPollLoopLeavesAnUnchangedFileAlone(t *testing.T) {
	// Otherwise every tick would reapply the manifest, flushing the tenant cache and logging a
	// reload several times a second for the life of the process.
	path := filepath.Join(t.TempDir(), "manifest.json")
	writePollManifest(t, path, "public")

	reloaded, stop := poll(t, path)
	defer stop()

	select {
	case m := <-reloaded:
		t.Fatalf("reloaded an unchanged file: %q", m.Schema)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestPollLoopRetriesAfterAHalfWrittenFile(t *testing.T) {
	// A push writes in more than one syscall, so a tick can land mid-write. Recording the stamp on
	// a failed parse would accept the broken read as current and never look again.
	path := filepath.Join(t.TempDir(), "manifest.json")
	writePollManifest(t, path, "public")

	reloaded, stop := poll(t, path)
	defer stop()

	if err := os.WriteFile(path, []byte(`{"schema": `), 0o600); err != nil {
		t.Fatalf("write truncated manifest: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	writePollManifest(t, path, "completed")

	select {
	case m := <-reloaded:
		if m.Schema != "completed" {
			t.Fatalf("expected the completed write, got %q", m.Schema)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a half-written file stopped the poller noticing the completed one")
	}
}

func TestDedupeAppliesEachVersionOnce(t *testing.T) {
	// Both paths run on a platform where fsnotify works. Without this the event and the tick would
	// each apply the same push, logging it twice and flushing the tenant cache twice.
	path := filepath.Join(t.TempDir(), "manifest.json")
	writePollManifest(t, path, "public")

	var applied int
	guarded := dedupe(func(*RouteManifest) { applied++ }, time.Minute)

	m, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	guarded(m)
	guarded(m)
	guarded(m)
	if applied != 1 {
		t.Fatalf("applied %d times, want 1", applied)
	}

	writePollManifest(t, path, "next")
	next, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	guarded(next)
	if applied != 2 {
		t.Fatalf("a genuinely new version was dropped: applied %d times, want 2", applied)
	}
}

func TestDedupeNeverDropsAWriteThatLandsDuringAnApply(t *testing.T) {
	// A push can land between one path reading the manifest and the guard looking at the file.
	// The guard must not record that newer write as applied while handing on the older content,
	// or the next path to read the newer content is turned away and the server keeps the stale
	// manifest until the file changes again.
	path := filepath.Join(t.TempDir(), "manifest.json")
	writePollManifest(t, path, "first")
	older, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var live *RouteManifest
	guarded := dedupe(func(m *RouteManifest) { live = m }, time.Minute)

	writePollManifest(t, path, "second_push")
	guarded(older)

	newer, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	guarded(newer)

	if live == nil || live.Schema != "second_push" {
		got := "<nothing>"
		if live != nil {
			got = live.Schema
		}
		t.Fatalf("serving %q after the second push was read and handed on", got)
	}
}

func TestDedupeReappliesAnIdenticalPushOutsideTheWindow(t *testing.T) {
	// The window only absorbs the two paths reporting one push. A re-push of the same content
	// later is applied again, because on a managed pod that is what retries a keyspace merge that
	// failed the first time.
	path := filepath.Join(t.TempDir(), "manifest.json")
	writePollManifest(t, path, "public")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var applied int
	guarded := dedupe(func(*RouteManifest) { applied++ }, 20*time.Millisecond)
	guarded(m)
	guarded(m)
	if applied != 1 {
		t.Fatalf("applied %d times inside the window, want 1", applied)
	}

	time.Sleep(40 * time.Millisecond)
	guarded(m)
	if applied != 2 {
		t.Fatalf("applied %d times after the window, want 2", applied)
	}
}

func TestWatchSeesAWriteBetweenTheStartupLoadAndWatch(t *testing.T) {
	// The server loads the manifest, then does other startup work, then calls Watch. A push that
	// lands in that gap is newer than what is live, and neither path can report it: fsnotify was
	// not watching yet, and a poll seeded from the file as Watch found it takes the new version as
	// its baseline. The server would serve the startup manifest until the next push.
	//
	// Through Watch rather than pollLoop: the event path cannot rescue this case, since the write
	// is over before fsnotify starts, so only the poll's baseline decides the outcome.
	path := filepath.Join(t.TempDir(), "manifest.json")
	writePollManifest(t, path, "public")

	_, loaded, err := LoadStamped(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	writePollManifest(t, path, "pushed_during_startup")

	reloaded := make(chan *RouteManifest, 4)
	if err := Watch(path, loaded, func(m *RouteManifest) { reloaded <- m }); err != nil {
		t.Fatalf("watch: %v", err)
	}

	select {
	case m := <-reloaded:
		if m.Schema != "pushed_during_startup" {
			t.Fatalf("reloaded the wrong content: %q", m.Schema)
		}
	case <-time.After(3 * manifestPollInterval):
		t.Fatal("a write between the startup load and Watch was never applied")
	}
}
