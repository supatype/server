package data

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace"
)

// stubKeyspace records the credentials each address was opened with.
func stubKeyspace(t *testing.T) map[string]keyspace.Auth {
	t.Helper()
	seen := map[string]keyspace.Auth{}
	original := openKeyspace
	openKeyspace = func(addr string, auth keyspace.Auth) (keyspace.Client, error) {
		seen[addr] = auth
		return &fakeCache{Client: keyspace.Unavailable()}, nil
	}
	t.Cleanup(func() { openKeyspace = original })
	return seen
}

// openSplit opens a split deployment with cfg's credentials and returns what
// each address was opened with.
func openSplit(t *testing.T, cfg *config.Config) (platform, project keyspace.Auth) {
	t.Helper()
	seen := stubKeyspace(t)
	cfg.KeyspaceAddr, cfg.ProjectKeyspaceAddr = "postgres-platform:6379", "postgres:6379"
	r, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return seen["postgres-platform:6379"], seen["postgres:6379"]
}

// assertAuth fails unless got is want, without printing a password.
func assertAuth(t *testing.T, which string, got, want keyspace.Auth) {
	t.Helper()
	if got != want {
		t.Errorf("%s keyspace opened as user %q with a %d-byte password, want %q with %d bytes",
			which, got.Username, len(got.Password), want.Username, len(want.Password))
	}
}

// Split, each keyspace gets its own pair.
func TestOpenSendsEachKeyspaceItsOwnCredentials(t *testing.T) {
	platform, project := openSplit(t, &config.Config{KeyspacePassword: "platform", ProjectKeyspacePassword: "project"})
	assertAuth(t, "platform", platform, keyspace.Auth{Username: config.DefaultKeyspaceUsername, Password: "platform"})
	assertAuth(t, "project", project, keyspace.Auth{Username: config.DefaultKeyspaceUsername, Password: "project"})
}

// Split with no project password, the project keyspace gets no AUTH, as
// before passwords, and never the platform pair: its credentials are given to
// users.
func TestOpenNeverSendsThePlatformPairToTheProjectKeyspace(t *testing.T) {
	platform, project := openSplit(t, &config.Config{KeyspacePassword: "platform", KeyspaceUsername: "ops"})
	assertAuth(t, "platform", platform, keyspace.Auth{Username: "ops", Password: "platform"})
	assertAuth(t, "project", project, keyspace.Auth{})
}

// One address is one client, and it logs in with the platform pair, as a
// single-keyspace deployment always has. A project pair is not consulted.
func TestOneSharedKeyspaceUsesThePlatformPair(t *testing.T) {
	seen := stubKeyspace(t)
	r, err := Open(context.Background(), &config.Config{
		KeyspaceAddr: "db:6379", ProjectKeyspaceAddr: "db:6379",
		KeyspacePassword: "platform", ProjectKeyspacePassword: "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if len(seen) != 1 {
		t.Fatalf("one address should open one client, got %d", len(seen))
	}
	assertAuth(t, "shared", seen["db:6379"], keyspace.Auth{Username: config.DefaultKeyspaceUsername, Password: "platform"})
}

// A project password equal to the platform one stops the start in every mode,
// before any keyspace is dialled, with a line that carries neither value.
func TestOpenRefusesAProjectPasswordEqualToThePlatformOne(t *testing.T) {
	const shared = "shared-b-ksrv"
	for _, mode := range []string{"dev", "standalone", "managed"} {
		seen := stubKeyspace(t)
		_, err := Open(context.Background(), &config.Config{
			Mode: mode, KeyspaceAddr: "postgres-platform:6379", ProjectKeyspaceAddr: "postgres:6379",
			KeyspacePassword: shared, ProjectKeyspacePassword: shared,
		})
		if err == nil || !strings.Contains(err.Error(), "must differ") {
			t.Errorf("%s: want a refusal, got %v", mode, err)
			continue
		}
		if strings.Contains(err.Error(), shared) {
			t.Errorf("%s: the refusal carries the password: %v", mode, err)
		}
		if len(seen) != 0 {
			t.Errorf("%s: %d keyspaces dialled before the refusal", mode, len(seen))
		}
	}
}

// No password configured, no credentials sent: a deployment from before the
// password connects exactly as it did.
func TestOpenSendsNoCredentialsWithoutAPassword(t *testing.T) {
	seen := stubKeyspace(t)
	r, err := Open(context.Background(), &config.Config{KeyspaceAddr: "db:6379", KeyspaceUsername: "app"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if seen["db:6379"] != (keyspace.Auth{}) {
		t.Errorf("credentials sent without a password: user %q", seen["db:6379"].Username)
	}
}

// authKeyspace returns a test keyspace that requires a password, or skips.
func authKeyspace(t *testing.T) (addr, password string) {
	t.Helper()
	addr = strings.TrimSpace(os.Getenv("SUPATYPE_TEST_KEYSPACE_AUTH_ADDR"))
	password = os.Getenv("SUPATYPE_TEST_KEYSPACE_AUTH_PASSWORD")
	if addr == "" || password == "" {
		t.Skip("SUPATYPE_TEST_KEYSPACE_AUTH_ADDR and SUPATYPE_TEST_KEYSPACE_AUTH_PASSWORD are not set")
	}
	return addr, password
}

// The right password gives the caches a working keyspace.
func TestOpenAuthenticatesToAKeyspaceThatRequiresIt(t *testing.T) {
	addr, password := authKeyspace(t)
	r, err := Open(context.Background(), &config.Config{
		KeyspaceAddr:     addr,
		KeyspacePassword: config.Secret(password),
		KeyspaceUsername: os.Getenv("SUPATYPE_TEST_KEYSPACE_AUTH_USERNAME"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if err := r.Cache().SetBytes(context.Background(), "supatype-test:data-auth", []byte("v"), 10); err != nil {
		t.Fatalf("SET through an authenticated cache: %v", err)
	}
}

// A wrong password is said once, at start, naming the address and that the
// login failed, never the password. The service then runs with the caches
// bypassed, as it does for a keyspace it cannot reach; managed mode refuses to
// start, as it does for that too.
func TestAWrongPasswordIsReportedOnceAndTheCachesBypass(t *testing.T) {
	addr, _ := authKeyspace(t)
	const wrong = "not-the-password-b-ksrv"
	previous := logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))
	hook := logtest.NewGlobal()
	t.Cleanup(func() { logrus.StandardLogger().ReplaceHooks(previous) })

	cfg := &config.Config{Mode: "standalone", KeyspaceAddr: addr, KeyspacePassword: wrong}
	r, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("standalone must survive a refused login: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if r.Cache().Available() || r.PlatformCache().Available() {
		t.Error("a refused login should leave the caches bypassed")
	}
	assertOneAuthFailureLine(t, hook, addr, wrong)

	cfg.Mode = "managed"
	if _, err := Open(context.Background(), cfg); !errors.Is(err, keyspace.ErrAuthFailed) {
		t.Errorf("managed mode: want ErrAuthFailed, got %v", err)
	}
}

// assertOneAuthFailureLine checks the hook saw exactly one entry, that it names
// addr and the failed login, and that it does not carry the password.
func assertOneAuthFailureLine(t *testing.T, hook *logtest.Hook, addr, password string) {
	t.Helper()
	entries := hook.AllEntries()
	if len(entries) != 1 {
		t.Fatalf("want one log line, got %d", len(entries))
	}
	line, err := entries[0].String()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{addr, "authentication failed"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q should mention %q", line, want)
		}
	}
	if strings.Contains(line, password) {
		t.Errorf("the log line carries the password: %s", line)
	}
}

// Against a real keyspace that requires a password, as the project's own: its
// pair logs in, and the platform pair, though it is the right password, is
// never tried in its place. The platform address is one nothing answers on,
// so only the project client can make the cache available.
func TestAProjectKeyspaceLogsInWithItsOwnPairOnly(t *testing.T) {
	addr, password := authKeyspace(t)
	split := func(cfg *config.Config) *Resources {
		cfg.Mode, cfg.KeyspaceAddr, cfg.ProjectKeyspaceAddr = "standalone", "127.0.0.1:1", addr
		cfg.ProjectKeyspaceUsername = os.Getenv("SUPATYPE_TEST_KEYSPACE_AUTH_USERNAME")
		r, err := Open(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close() })
		return r
	}
	if !split(&config.Config{ProjectKeyspacePassword: config.Secret(password)}).Cache().Available() {
		t.Error("the project pair should log in to the project keyspace")
	}
	if split(&config.Config{KeyspacePassword: config.Secret(password)}).Cache().Available() {
		t.Error("the project keyspace logged in with the platform pair")
	}
}

// The platform keyspace is opened with the layout its login implies, and the
// project keyspace never with it: a response cache has no tenant keys to name.
func TestOpenGivesThePlatformLoginItsKeyLayout(t *testing.T) {
	platform, project := openSplit(t, &config.Config{KeyspacePassword: "platform", KeyspaceKeyLayout: "scoped"})
	if platform.Keys != keyspace.LayoutScoped {
		t.Errorf("platform layout = %q, want scoped", platform.Keys)
	}
	if project.Keys != keyspace.LayoutLegacy {
		t.Errorf("project layout = %q, want legacy", project.Keys)
	}
}

// Self-host sets nothing and keeps the layout it always had.
func TestOpenWithNoLayoutKeepsTheLegacyKeys(t *testing.T) {
	platform, _ := openSplit(t, &config.Config{})
	if platform.Keys != keyspace.LayoutLegacy {
		t.Errorf("platform layout = %q, want legacy", platform.Keys)
	}
}

// A scoped layout without a login, or a layout nobody knows, is refused before
// anything is dialled.
func TestOpenRefusesALayoutItCannotHonour(t *testing.T) {
	for name, cfg := range map[string]*config.Config{
		"scoped, no password": {KeyspaceAddr: "db:6379", KeyspaceKeyLayout: "scoped"},
		"unknown layout":      {KeyspaceAddr: "db:6379", KeyspacePassword: "pw", KeyspaceKeyLayout: "tenant"},
	} {
		t.Run(name, func(t *testing.T) {
			seen := stubKeyspace(t)
			if _, err := Open(context.Background(), cfg); err == nil {
				t.Fatal("want an error")
			}
			if len(seen) != 0 {
				t.Errorf("dialled %v before refusing", seen)
			}
		})
	}
}
