package reloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/conf"
)

// kubeletMount lays a directory out the way the kubelet does for a Secret
// volume: a timestamped directory holding the files, a ..data symlink to it,
// and each file name a symlink through ..data. An update writes a new
// timestamped directory and swaps ..data over with a rename, so the file
// symlinks and the mount directory itself are never touched.
type kubeletMount struct {
	t    *testing.T
	dir  string
	seen int
}

// newKubeletMount takes the absolute path of dir, since the kubelet mounts at one.
func newKubeletMount(t *testing.T, dir string) *kubeletMount {
	t.Helper()
	abs, err := filepath.Abs(dir)
	require.NoError(t, err)
	return &kubeletMount{t: t, dir: abs}
}

// publish writes content as the new auth.env and swaps ..data to it. It skips
// the test where the host cannot create symlinks (Windows without developer
// mode or the privilege), since the layout cannot be built there.
func (m *kubeletMount) publish(content string) {
	m.t.Helper()
	m.seen++
	version := fmt.Sprintf("..2026_11_16_10_00_%02d.0", m.seen)
	require.NoError(m.t, os.MkdirAll(filepath.Join(m.dir, version), 0750))
	require.NoError(m.t, os.WriteFile(filepath.Join(m.dir, version, "auth.env"), []byte(content), 0600))

	tmp := filepath.Join(m.dir, "..data_tmp")
	if err := os.Symlink(version, tmp); err != nil {
		m.t.Skipf("cannot create symlinks on this host, so the kubelet layout cannot be built: %v", err)
	}
	data := filepath.Join(m.dir, "..data")
	if err := os.Rename(tmp, data); err != nil {
		// Windows refuses to rename a directory symlink. Fall back to replacing it, which
		// is not atomic but raises the same Create event on ..data.
		require.NoError(m.t, os.Remove(tmp))
		_ = os.Remove(data)
		if err := os.Symlink(version, data); err != nil {
			m.t.Skipf("cannot create symlinks on this host: %v", err)
		}
	}
	if m.seen == 1 {
		if err := os.Symlink(filepath.Join("..data", "auth.env"), filepath.Join(m.dir, "auth.env")); err != nil {
			m.t.Skipf("cannot create symlinks on this host: %v", err)
		}
	}
}

// reloadedContents returns a reloader whose reload reads auth.env through the
// symlinks and reports what it found, which is what the server's config load
// sees after the swap.
func reloadedContents(dir string, cfg conf.ReloadingConfiguration) (*Reloader, chan string) {
	got := make(chan string, 16)
	rl := NewReloader(cfg, dir)
	rl.tickerIval = time.Millisecond * 20
	rl.reloadFn = func(dir string) (*conf.GlobalConfiguration, error) {
		b, err := os.ReadFile(filepath.Join(dir, "auth.env")) // #nosec G304 -- test directory.
		if err != nil {
			return nil, err
		}
		got <- string(b)
		return &conf.GlobalConfiguration{}, nil
	}
	return rl, got
}

func awaitContents(t *testing.T, got chan string, want string) {
	t.Helper()
	deadline := time.After(time.Second * 5)
	for {
		select {
		case v := <-got:
			if v == want {
				return
			}
		case <-deadline:
			require.FailNow(t, "the reload never saw the new auth.env", "wanted %q", want)
		}
	}
}

func TestKubeletSecretSwap(t *testing.T) {
	restoreEnvironment(t)

	t.Run("NotifyReloadsOnTheDataSymlinkSwap", func(t *testing.T) {
		dir, cleanup := helpTestDir(t)
		defer cleanup()

		mount := newKubeletMount(t, dir)
		mount.publish("SUPATYPE_AUTH_SETTINGS_VERSION=1\n")

		cfg := conf.ReloadingConfiguration{NotifyEnabled: true, GracePeriodInterval: time.Millisecond * 50}
		rl, got := reloadedContents(dir, cfg)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- rl.Watch(ctx, func(*conf.GlobalConfiguration) {}) }()

		// Let the watcher attach before the swap.
		time.Sleep(time.Millisecond * 300)
		mount.publish("SUPATYPE_AUTH_SETTINGS_VERSION=2\n")

		awaitContents(t, got, "SUPATYPE_AUTH_SETTINGS_VERSION=2\n")
		cancel()
		<-done
	})

	t.Run("PollerSeesTheTargetOfTheFileSymlink", func(t *testing.T) {
		dir, cleanup := helpTestDir(t)
		defer cleanup()

		mount := newKubeletMount(t, dir)
		mount.publish("SUPATYPE_AUTH_SETTINGS_VERSION=1\n")

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()

		pr := newPoller(dir)
		_, err := pr.poll(ctx)
		require.NoError(t, err)
		changed, err := pr.poll(ctx)
		require.NoError(t, err)
		require.False(t, changed, "nothing was swapped yet")

		// A different length, so the change shows even where timestamps are coarse.
		mount.publish("SUPATYPE_AUTH_SETTINGS_VERSION=22\n")
		changed, err = pr.poll(ctx)
		require.NoError(t, err)
		require.True(t, changed, "the swap behind the auth.env symlink must read as a change")
	})
}
