package keyspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// ─── Credentials, without a server ────────────────────────────────────────────

// No password means no AUTH, whatever the username says. That is the keyspace
// every deployment ran before pg_keyspace required one, and it must keep
// connecting exactly as it did.
func TestClientOptionSendsNoCredentialsWithoutAPassword(t *testing.T) {
	opt := clientOption("db:6379", Auth{Username: "supatype"})
	if opt.Username != "" || opt.Password != "" {
		t.Errorf("credentials sent without a password: user %q", opt.Username)
	}
	if len(opt.InitAddress) != 1 || opt.InitAddress[0] != "db:6379" {
		t.Errorf("InitAddress = %v", opt.InitAddress)
	}
}

// The pair goes into the options, which valkey-go applies to every connection
// it opens, reconnects included.
func TestClientOptionCarriesTheCredentials(t *testing.T) {
	opt := clientOption("db:6379", Auth{Username: "supatype", Password: "s3cret"})
	if opt.Username != "supatype" || opt.Password != "s3cret" {
		t.Errorf("got user %q and a password of %d bytes", opt.Username, len(opt.Password))
	}
}

// A refused login is reported as one, naming the address and the settings to
// check, so an operator does not go looking for a network problem.
func TestConnectErrorNamesAnAuthenticationFailure(t *testing.T) {
	for _, reply := range []string{
		"WRONGPASS invalid username-password pair or user is disabled.",
		"NOAUTH HELLO must be called with the client already authenticated",
	} {
		err := connectError("db:6379", errors.New(reply))
		if !errors.Is(err, ErrAuthFailed) {
			t.Errorf("%q: want ErrAuthFailed, got %v", reply, err)
		}
		for _, want := range []string{"authentication failed", "db:6379", "username and password", reply} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q: error %q should mention %q", reply, err, want)
			}
		}
	}
}

// Anything else is a connection failure, as it always was.
func TestConnectErrorLeavesOtherFailuresAsConnectionFailures(t *testing.T) {
	cause := errors.New("dial tcp 127.0.0.1:1: connection refused")
	err := connectError("127.0.0.1:1", cause)
	if errors.Is(err, ErrAuthFailed) {
		t.Errorf("a refused connection is not an authentication failure: %v", err)
	}
	if !errors.Is(err, cause) || !strings.HasPrefix(err.Error(), "keyspace: connect 127.0.0.1:1: ") {
		t.Errorf("got %v", err)
	}
}

// ─── Against a keyspace that requires a password ──────────────────────────────

// credentialsFromEnv reads prefix_USERNAME and prefix_PASSWORD. No password
// means no credentials; a password alone goes with the user pg_keyspace
// creates, as the server's own configuration defaults it.
func credentialsFromEnv(prefix string) Auth {
	password := os.Getenv(prefix + "_PASSWORD")
	if password == "" {
		return Auth{}
	}
	username := strings.TrimSpace(os.Getenv(prefix + "_USERNAME"))
	if username == "" {
		username = "supatype"
	}
	return Auth{Username: username, Password: password}
}

// authServer returns the address of a test keyspace that requires a password,
// and the credentials it accepts, or skips.
func authServer(t *testing.T) (string, Auth) {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("SUPATYPE_TEST_KEYSPACE_AUTH_ADDR"))
	auth := credentialsFromEnv("SUPATYPE_TEST_KEYSPACE_AUTH")
	if addr == "" || auth.Password == "" {
		t.Skip("SUPATYPE_TEST_KEYSPACE_AUTH_ADDR and SUPATYPE_TEST_KEYSPACE_AUTH_PASSWORD are not set")
	}
	return addr, auth
}

// Every command the server sends works once the client has logged in.
func TestEveryCommandWorksWithTheRightCredentials(t *testing.T) {
	addr, auth := authServer(t)
	client, err := New(addr, auth)
	if err != nil {
		t.Fatalf("New with the right credentials: %v", err)
	}
	t.Cleanup(client.Close)
	exerciseEveryCommand(t, client)
}

// exerciseEveryCommand runs each operation on the Client surface once.
func exerciseEveryCommand(t *testing.T, c Client) {
	t.Helper()
	ctx := context.Background()
	k, set := key(t, "bytes"), key(t, "set")
	t.Cleanup(func() { _ = c.Del(ctx, k, set) })

	if err := c.SetBytes(ctx, k, []byte("v"), 60); err != nil {
		t.Fatalf("SET EX: %v", err)
	}
	if got, err := c.GetBytes(ctx, k); err != nil || string(got) != "v" {
		t.Fatalf("GET = %q, %v", got, err)
	}
	if ttl, err := c.TTLSeconds(ctx, k); err != nil || ttl <= 0 {
		t.Fatalf("TTL = %d, %v", ttl, err)
	}
	if _, _, err := c.ScanPage(ctx, 0, k, 1000); err != nil {
		t.Fatalf("SCAN: %v", err)
	}
	if err := c.AddToExpiringSet(ctx, set, "m", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SADD and EXPIREAT: %v", err)
	}
	if _, err := c.GetTenantConfig(ctx, "supatype-test-absent"); err != nil {
		t.Fatalf("GET tenant config: %v", err)
	}
	if err := c.Del(ctx, k); err != nil {
		t.Fatalf("DEL: %v", err)
	}
}

// A dropped connection comes back logged in. valkey-go redials with the same
// options, so a keyspace restart does not leave the cache bypassed for good.
func TestAReconnectLogsInAgain(t *testing.T) {
	addr, auth := authServer(t)
	client, err := New(addr, auth)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	c := client.(*conn)

	before := connectionID(t, c)
	// Kills every connection this user holds, the one sending it included
	// (SKIPME defaults to yes for the filter form), so the reply may be lost
	// with the connection; only what follows matters.
	kill := c.vc.B().Arbitrary("CLIENT", "KILL").Args("USER", auth.Username, "SKIPME", "no").Build()
	_ = c.vc.Do(context.Background(), kill).Error()

	// The first command after the kill can still be written to the dying
	// connection and come back as EOF before the client notices it is gone,
	// as any caller would see once. Wait for the redial, then check that
	// everything works on the new, logged-in connection.
	waitForNewConnection(t, c, before)
	exerciseEveryCommand(t, client)
}

// waitForNewConnection waits until c sends on a connection other than before.
func waitForNewConnection(t *testing.T, c *conn, before int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		id, err := c.vc.Do(context.Background(), c.vc.B().ClientId().Build()).AsInt64()
		if err == nil && id != before {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("still on connection %d after 5s: the kill did not force a reconnect, so this proved nothing", before)
}

// connectionID is the server's id for the connection c is sending on.
func connectionID(t *testing.T, c *conn) int64 {
	t.Helper()
	id, err := c.vc.Do(context.Background(), c.vc.B().ClientId().Build()).AsInt64()
	if err != nil {
		t.Fatalf("CLIENT ID: %v", err)
	}
	return id
}

// Without credentials the keyspace refuses the login, and New says so.
func TestNewReportsMissingCredentials(t *testing.T) {
	addr, _ := authServer(t)
	_, err := New(addr, Auth{})
	if !errors.Is(err, ErrAuthFailed) || !strings.Contains(err.Error(), addr) {
		t.Errorf("want ErrAuthFailed naming %s, got %v", addr, err)
	}
}

// With the wrong password the error says authentication failed and does not
// carry the password that was tried.
func TestNewReportsAWrongPasswordWithoutEchoingIt(t *testing.T) {
	addr, auth := authServer(t)
	auth.Password = "not-the-password-b-ksrv"
	_, err := New(addr, auth)
	if !errors.Is(err, ErrAuthFailed) || !strings.Contains(err.Error(), addr) {
		t.Fatalf("want ErrAuthFailed naming %s, got %v", addr, err)
	}
	if strings.Contains(err.Error(), auth.Password) {
		t.Errorf("the error carries the password: %v", err)
	}
}
