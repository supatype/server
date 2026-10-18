package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace/keyspacetest"
)

// Studio's connection strings used to be built from the API URL, so they named the gateway's port,
// the `postgres` login and the `postgres` database whatever the stack really ran. These tests hold
// the route that replaced that guess to what each deployment shape declared.

func connectionOf(t *testing.T, cfg *config.Config) (connectionResponse, string) {
	t.Helper()
	handler, _ := api(t, cfg, keyspacetest.New())
	rec := call(t, handler, http.MethodGet, "/database/connection", serviceKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var got connectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got, rec.Body.String()
}

func TestConnectionDetailsPerDeploymentShape(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  *config.Config
		want dbEndpoint
	}{
		"native dev": {
			&config.Config{Mode: "dev", DBPublicURL: "postgres://postgres@127.0.0.1:5433/blog?sslmode=disable"},
			dbEndpoint{Host: "127.0.0.1", Port: 5433, Database: "blog", User: "postgres", Params: "sslmode=disable"},
		},
		"docker dev": {
			&config.Config{Mode: "dev", DBPublicURL: "postgresql://supatype_admin@127.0.0.1:54329/supatype"},
			dbEndpoint{Host: "127.0.0.1", Port: 54329, Database: "supatype", User: "supatype_admin"},
		},
		"self-host compose": {
			&config.Config{Mode: "standalone", DBPublicURL: "postgresql://supatype_admin@db.example.com:5432/shop"},
			dbEndpoint{Host: "db.example.com", Port: 5432, Database: "shop", User: "supatype_admin"},
		},
		"external database, no port": {
			&config.Config{Mode: "standalone", DBPublicURL: "postgresql://app@pg.example.net/prod?sslmode=require"},
			dbEndpoint{Host: "pg.example.net", Port: 5432, Database: "prod", User: "app", Params: "sslmode=require"},
		},
	} {
		got, _ := connectionOf(t, tc.cfg)
		if got.Direct == nil {
			t.Errorf("%s: no direct endpoint (%q)", name, got.Unavailable)
			continue
		}
		if *got.Direct != tc.want {
			t.Errorf("%s: direct = %+v, want %+v", name, *got.Direct, tc.want)
		}
		if got.Pooler != nil {
			t.Errorf("%s: a pooler nobody configured: %+v", name, *got.Pooler)
		}
		if got.Unavailable != "" {
			t.Errorf("%s: unavailable = %q alongside an endpoint", name, got.Unavailable)
		}
	}
}

// An external URL carries its password. The route is for showing a string on screen, and a
// password in it would bypass the credential routes' rules about when one may be revealed.
func TestTheConnectionDetailsNeverCarryAPassword(t *testing.T) {
	got, body := connectionOf(t, &config.Config{
		Mode:        "standalone",
		DBPublicURL: "postgresql://app:hunter2@pg.example.net:6543/prod",
		DBPoolerURL: "postgresql://app:hunter3@pool.example.net:6432/prod",
	})
	if strings.Contains(body, "hunter") {
		t.Fatalf("a password reached the response: %s", body)
	}
	if got.Direct == nil || got.Direct.User != "app" || got.Direct.Port != 6543 {
		t.Errorf("direct = %+v", got.Direct)
	}
}

func TestAPoolerIsReportedOnlyWhenConfigured(t *testing.T) {
	got, _ := connectionOf(t, &config.Config{
		Mode:        "standalone",
		DBPublicURL: "postgresql://supatype_admin@127.0.0.1:5432/shop",
		DBPoolerURL: "postgresql://supatype_admin@127.0.0.1:6432/shop",
	})
	want := dbEndpoint{Host: "127.0.0.1", Port: 6432, Database: "shop", User: "supatype_admin"}
	if got.Pooler == nil || *got.Pooler != want {
		t.Errorf("pooler = %+v, want %+v", got.Pooler, want)
	}
}

// A database the host cannot reach has no connection string. Saying so is the fix: the old screen
// printed one anyway, and it pointed at the API.
func TestAnUnpublishedDatabaseSaysSo(t *testing.T) {
	for _, mode := range []string{"dev", "standalone"} {
		got, body := connectionOf(t, &config.Config{Mode: mode})
		if got.Direct != nil || got.Pooler != nil {
			t.Errorf("%s: endpoints for an unpublished database: %s", mode, body)
		}
		if !strings.Contains(got.Unavailable, "does not publish its database") {
			t.Errorf("%s: unavailable = %q", mode, got.Unavailable)
		}
	}
}

// On Cloud an environment variable is no remedy, so the answer points at the project settings.
func TestAManagedProjectIsPointedAtCloud(t *testing.T) {
	got, _ := connectionOf(t, &config.Config{Mode: "managed"})
	if got.Direct != nil || !strings.Contains(got.Unavailable, "Cloud project settings") {
		t.Errorf("managed: %+v", got)
	}
}

func TestAConnectionURLThatWillNotParse(t *testing.T) {
	for name, raw := range map[string]string{
		"not a URL":    "://nope",
		"wrong scheme": "mysql://root@127.0.0.1:3306/app",
		"no host":      "postgresql:///app",
		"no database":  "postgresql://u@127.0.0.1:5432",
		"bad port":     "postgresql://u@127.0.0.1:99999/app",
	} {
		handler, _ := api(t, &config.Config{Mode: "dev", DBPublicURL: raw}, keyspacetest.New())
		rec := call(t, handler, http.MethodGet, "/database/connection", serviceKey, "")
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500 (%s)", name, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(errorOf(t, rec), "SUPATYPE_DB_PUBLIC_URL") {
			t.Errorf("%s: error does not name the variable: %q", name, errorOf(t, rec))
		}
	}
}

func TestTheConnectionRouteIsReadOnly(t *testing.T) {
	handler, _ := api(t, devConfig(), keyspacetest.New())
	if rec := call(t, handler, http.MethodPost, "/database/connection", "", "{}"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", rec.Code)
	}
}
