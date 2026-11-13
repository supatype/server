package keyspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// A keeps its wrapped credentials and adds to its MAU day-set, by relative
// names, and reads its credentials back. The control plane sees them as
// `{A}:dbcred:meta` and `{A}:mau:...`.
func TestScopedGatewayKeepsItsOwnKeys(t *testing.T) {
	f := scopedEnv(t)
	a := gatewayA(t, f)
	ctx := context.Background()
	if err := a.SetBytes(ctx, KeyDBCredMeta, []byte(`{"status":"pending"}`), 0); err != nil {
		t.Fatalf("SET dbcred:meta: %v", err)
	}
	if got, err := a.GetBytes(ctx, KeyDBCredMeta); err != nil || string(got) != `{"status":"pending"}` {
		t.Errorf("GET dbcred:meta = %q, %v", got, err)
	}
	day := LayoutScoped.Shared(f.user, "mau:org:org-live:d:"+time.Now().UTC().Format("2006-01-02"))
	if err := a.AddToExpiringSet(ctx, day, "user-1", time.Now().Add(time.Hour)); err != nil {
		t.Errorf("SADD %s: %v", day, err)
	}
	if got, err := a.GetBytes(ctx, day); err != nil || got != nil {
		t.Errorf("reading its own day-set = %q, %v; want nil (write-only)", got, err)
	}
}

// These run against a real pg_keyspace whose tenants log in scoped, as the
// platform keyspace does in cloud. The control plane seeds it (supatype-cloud's
// keyspace-login.ts and valkey-client.ts); the variables say what it seeded:
//
//	SUPATYPE_TEST_SCOPED_ADDR       the keyspace
//	SUPATYPE_TEST_SCOPED_USER       gateway A's tenant id, its username
//	SUPATYPE_TEST_SCOPED_PASSWORD   gateway A's password
//	SUPATYPE_TEST_SCOPED_REST_URL   the postgrest_url written in A's config
//	SUPATYPE_TEST_SCOPED_OTHER      gateway B's tenant id, whose config and
//	                                dbcred:secret:v1 the control plane wrote

type scopedFixture struct {
	addr, user, password, restURL, other string
}

func scopedEnv(t *testing.T) scopedFixture {
	t.Helper()
	f := scopedFixture{
		addr:     os.Getenv("SUPATYPE_TEST_SCOPED_ADDR"),
		user:     os.Getenv("SUPATYPE_TEST_SCOPED_USER"),
		password: os.Getenv("SUPATYPE_TEST_SCOPED_PASSWORD"),
		restURL:  os.Getenv("SUPATYPE_TEST_SCOPED_REST_URL"),
		other:    os.Getenv("SUPATYPE_TEST_SCOPED_OTHER"),
	}
	if f.addr == "" {
		t.Skip("SUPATYPE_TEST_SCOPED_ADDR is not set")
	}
	return f
}

// gatewayA logs in as A, scoped, the way a tenant gateway does.
func gatewayA(t *testing.T, f scopedFixture) Client {
	t.Helper()
	c, err := New(f.addr, Auth{Username: f.user, Password: f.password, Keys: LayoutScoped})
	if err != nil {
		t.Fatalf("gateway A cannot log in: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// A reads its own config by the relative name the keyspace stores under its tenant.
func TestScopedGatewayReadsItsOwnConfig(t *testing.T) {
	f := scopedEnv(t)
	cfg, err := gatewayA(t, f).GetTenantConfig(context.Background(), f.user)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.PostgRESTURL != f.restURL {
		t.Fatalf("config = %+v, want postgrest_url %q", cfg, f.restURL)
	}
}

// Nothing A sends reaches B: not B's stored names, not the legacy names, and
// not B's wrapped database password. GET answers nil and TTL is refused.
func TestScopedGatewayIsRefusedAnotherTenantsKeys(t *testing.T) {
	f := scopedEnv(t)
	a := gatewayA(t, f)
	ctx := context.Background()
	for _, key := range []string{
		f.other + ":config", f.other + ":dbcred:secret:v1", "tenant:" + f.other + ":config",
	} {
		if got, err := a.GetBytes(ctx, key); err != nil || got != nil {
			t.Errorf("GET %s = %q, %v; want nil", key, got, err)
		}
		if _, err := a.TTLSeconds(ctx, key); err == nil || !strings.Contains(err.Error(), "NOPERM") {
			t.Errorf("TTL %s: err = %v, want NOPERM", key, err)
		}
	}
	if err := a.SetBytes(ctx, f.other+":config", []byte("x"), 0); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Errorf("SET on B's config: err = %v, want NOPERM", err)
	}
	if err := a.SetBytes(ctx, KeyConfig, []byte("x"), 0); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Errorf("SET on its own config, which only the control plane writes: err = %v, want NOPERM", err)
	}
}

// A client that sends no login, or a wrong one, is refused at connect.
func TestTheScopedKeyspaceRefusesAClientWithoutALogin(t *testing.T) {
	f := scopedEnv(t)
	for name, auth := range map[string]Auth{
		"no login":       {},
		"wrong password": {Username: f.user, Password: f.password + "x"},
		"B's username":   {Username: f.other, Password: f.password},
	} {
		c, err := New(f.addr, auth)
		if err == nil {
			c.Close()
		}
		if !errors.Is(err, ErrAuthFailed) {
			t.Errorf("%s: err = %v, want ErrAuthFailed", name, err)
		}
	}
}
