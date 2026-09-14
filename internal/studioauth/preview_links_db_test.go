package studioauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
)

// Preview links end to end, against a real Postgres.
//
// The store cannot be exercised with a fake. Liveness is decided in SQL on purpose, so that expiry,
// revocation and the epoch are one condition on one row and there is no window between reading a row
// and judging it. A fake store would have to reimplement that predicate in Go, which is the thing
// most likely to drift from the query, so it would assert the copy and not the original.
//
// Skipped unless SUPATYPE_TEST_DSN points at a throwaway database.

const (
	previewAdminSub  = "bbbb1111-1111-1111-1111-111111111111"
	previewEditorSub = "bbbb2222-2222-2222-2222-222222222222"
)

// The publishing block the engine writes into admin-config.json. Everything the preview endpoints
// know about a project's policy arrives through this file.
func previewConfig(visibility string, allowProject bool) string {
	allow := "false"
	if allowProject {
		allow = "true"
	}
	return `{"publishing":{"draftVisibility":` + visibility +
		`,"previewDefaultTtl":3600,"previewMaxRecordTtl":86400,"previewMaxProjectTtl":3600` +
		`,"previewAllowProjectScope":` + allow + `}}`
}

// previewTables rebuilds the two tables the preview endpoints read, matching the shapes the schema
// engine generates. Rebuilt per subtest so one test's rows cannot decide another's result.
func previewTables(t *testing.T, pool previewStore) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE SCHEMA IF NOT EXISTS _supatype`,
		`DROP TABLE IF EXISTS _supatype.preview_links`,
		`DROP TABLE IF EXISTS _supatype.publishing_settings`,
		`CREATE TABLE _supatype.publishing_settings (
			singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
			configured_visibility TEXT[] NOT NULL DEFAULT '{}',
			override_visibility TEXT[],
			preview_epoch INTEGER NOT NULL DEFAULT 1,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`INSERT INTO _supatype.publishing_settings (singleton) VALUES (TRUE)`,
		`CREATE TABLE _supatype.preview_links (
			id TEXT PRIMARY KEY,
			secret_hash TEXT NOT NULL,
			scope TEXT NOT NULL CHECK (scope IN ('record', 'project')),
			model TEXT,
			record_id TEXT,
			epoch INTEGER NOT NULL,
			created_by UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			expires_at TIMESTAMPTZ NOT NULL,
			revoked_at TIMESTAMPTZ,
			CONSTRAINT preview_links_record_is_named
				CHECK (scope <> 'record' OR (model IS NOT NULL AND record_id IS NOT NULL)))`,
	} {
		if _, err := pool.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
}

func previewResources(t *testing.T) *data.Resources {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the preview link store against Postgres")
	}
	resources, err := data.Open(context.Background(), &config.Config{SQLDatabaseURL: dsn})
	if err != nil {
		t.Fatalf("open resources: %v", err)
	}
	t.Cleanup(func() { _ = resources.Close() })
	return resources
}

// A config whose caller is whoever the returned function says, so a test can move between roles
// without rebuilding anything.
func previewCallerConfig(t *testing.T, resources *data.Resources, configJSON string) Config {
	t.Helper()
	return Config{
		JWTSecret:       testSecret,
		AdminRoles:      DefaultAdminRoles,
		Resources:       resources,
		AdminConfigPath: writeAdminConfig(t, configJSON),
		StudioRole: func(sub string) (string, bool) {
			switch sub {
			case previewAdminSub:
				return RoleAdmin, true
			case previewEditorSub:
				return "editor", true
			}
			return "", false
		},
	}
}

func previewToken(sub string) string {
	return signClaims(jwt.MapClaims{"sub": sub, "exp": time.Now().Add(time.Hour).Unix()})
}

func previewCall(h http.Handler, sub, method, path, body string) (int, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if sub != "" {
		req.Header.Set("Authorization", "Bearer "+previewToken(sub))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// mintedCode pulls the one-shot credential out of a mint response.
func mintedCode(t *testing.T, body string) (code string, id string) {
	t.Helper()
	var parsed previewResponse
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("could not read the mint response %q: %v", body, err)
	}
	if parsed.Code == "" || parsed.ID == "" {
		t.Fatalf("mint response carried no code or id: %s", body)
	}
	return parsed.Code, parsed.ID
}

func TestPreviewLinkStore(t *testing.T) {
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}

	t.Run("a stored link resolves to the claim it stands for", func(t *testing.T) {
		previewTables(t, pool)
		c := Config{Resources: resources}

		id, secret, code, err := newPreviewCode()
		if err != nil {
			t.Fatalf("mint a code: %v", err)
		}
		link := PreviewLink{ID: id, Scope: "record", Model: "Post", RecordID: "r-1", CreatedBy: previewAdminSub}
		if err := storePreviewLink(context.Background(), c, link, hashPreviewSecret(secret), 1,
			time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("store: %v", err)
		}

		got, epoch, expires, err := resolvePreviewLink(context.Background(), c, code)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got.ID != id || got.Scope != "record" || got.Model != "Post" || got.RecordID != "r-1" {
			t.Errorf("resolved %+v, want the record link that was stored", got)
		}
		if epoch != 1 {
			t.Errorf("epoch = %d, want 1", epoch)
		}
		if time.Until(expires) <= 0 {
			t.Errorf("expires = %v, which is already past", expires)
		}
	})

	t.Run("the right id with the wrong secret is refused", func(t *testing.T) {
		// The point of splitting the credential. Holding the table gives you every id, so an id on
		// its own must open nothing.
		previewTables(t, pool)
		c := Config{Resources: resources}

		id, _, _, err := newPreviewCode()
		if err != nil {
			t.Fatalf("mint a code: %v", err)
		}
		if err := storePreviewLink(context.Background(), c,
			PreviewLink{ID: id, Scope: "record", Model: "Post", RecordID: "r-1"},
			hashPreviewSecret("the-real-secret"), 1, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("store: %v", err)
		}

		if _, _, _, err := resolvePreviewLink(context.Background(), c, id+".not-the-secret"); err == nil {
			t.Fatal("a guessed id opened a draft without the secret")
		} else if err != ErrPreviewLinkNotFound {
			t.Errorf("err = %v, want ErrPreviewLinkNotFound so a wrong secret is not distinguishable", err)
		}
	})

	// Every reason a link is dead, each of which must read the same from outside.
	for _, dead := range []struct {
		name  string
		setup func(t *testing.T, c Config, id string)
	}{
		{"revoked", func(t *testing.T, c Config, id string) {
			if err := revokePreviewLink(context.Background(), c, id); err != nil {
				t.Fatalf("revoke: %v", err)
			}
		}},
		{"expired", func(t *testing.T, c Config, id string) {
			if _, err := pool.Exec(context.Background(),
				`UPDATE _supatype.preview_links SET expires_at = now() - interval '1 second' WHERE id = $1`,
				id); err != nil {
				t.Fatalf("expire: %v", err)
			}
		}},
		{"stranded by an epoch bump", func(t *testing.T, c Config, id string) {
			if _, err := pool.Exec(context.Background(),
				`UPDATE _supatype.publishing_settings SET preview_epoch = preview_epoch + 1`); err != nil {
				t.Fatalf("bump: %v", err)
			}
		}},
	} {
		t.Run("a link that is "+dead.name+" does not resolve", func(t *testing.T) {
			previewTables(t, pool)
			c := Config{Resources: resources}

			id, secret, code, err := newPreviewCode()
			if err != nil {
				t.Fatalf("mint a code: %v", err)
			}
			if err := storePreviewLink(context.Background(), c,
				PreviewLink{ID: id, Scope: "record", Model: "Post", RecordID: "r-1"},
				hashPreviewSecret(secret), 1, time.Now().Add(time.Hour)); err != nil {
				t.Fatalf("store: %v", err)
			}
			dead.setup(t, c, id)

			if _, _, _, err := resolvePreviewLink(context.Background(), c, code); err != ErrPreviewLinkNotFound {
				t.Errorf("err = %v, want ErrPreviewLinkNotFound: a %s link must open nothing", err, dead.name)
			}
		})
	}

	t.Run("revoking is idempotent", func(t *testing.T) {
		// A second click is the caller's intent already satisfied, not a failure to report.
		previewTables(t, pool)
		c := Config{Resources: resources}

		id, secret, _, err := newPreviewCode()
		if err != nil {
			t.Fatalf("mint a code: %v", err)
		}
		if err := storePreviewLink(context.Background(), c,
			PreviewLink{ID: id, Scope: "record", Model: "Post", RecordID: "r-1"},
			hashPreviewSecret(secret), 1, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("store: %v", err)
		}
		for attempt := 1; attempt <= 2; attempt++ {
			if err := revokePreviewLink(context.Background(), c, id); err != nil {
				t.Fatalf("revoke attempt %d: %v", attempt, err)
			}
		}
	})

	t.Run("listing shows the live links for one record, newest first", func(t *testing.T) {
		previewTables(t, pool)
		c := Config{Resources: resources}

		var ids []string
		for i := 0; i < 3; i++ {
			id, secret, _, err := newPreviewCode()
			if err != nil {
				t.Fatalf("mint a code: %v", err)
			}
			if err := storePreviewLink(context.Background(), c,
				PreviewLink{ID: id, Scope: "record", Model: "Post", RecordID: "r-1", CreatedBy: previewAdminSub},
				hashPreviewSecret(secret), 1, time.Now().Add(time.Hour)); err != nil {
				t.Fatalf("store: %v", err)
			}
			// created_at defaults to now(); nudge them apart so the ordering is decidable.
			if _, err := pool.Exec(context.Background(),
				`UPDATE _supatype.preview_links SET created_at = now() - ($2::int * interval '1 minute') WHERE id = $1`,
				id, 10-i); err != nil {
				t.Fatalf("age the row: %v", err)
			}
			ids = append(ids, id)
		}
		// One belonging to another record, which must not appear.
		other, otherSecret, _, err := newPreviewCode()
		if err != nil {
			t.Fatalf("mint a code: %v", err)
		}
		if err := storePreviewLink(context.Background(), c,
			PreviewLink{ID: other, Scope: "record", Model: "Post", RecordID: "r-2"},
			hashPreviewSecret(otherSecret), 1, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("store: %v", err)
		}
		if err := revokePreviewLink(context.Background(), c, ids[0]); err != nil {
			t.Fatalf("revoke: %v", err)
		}

		links, err := listPreviewLinks(context.Background(), c, "Post", "r-1")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(links) != 2 {
			t.Fatalf("listed %d links, want the 2 live ones for this record: %+v", len(links), links)
		}
		if links[0].ID != ids[2] || links[1].ID != ids[1] {
			t.Errorf("order = %s, %s; want newest first (%s, %s)", links[0].ID, links[1].ID, ids[2], ids[1])
		}
		for _, link := range links {
			if link.CreatedAt == "" || link.ExpiresAt == "" {
				t.Errorf("link %+v has no times, so Studio cannot say when it dies", link)
			}
		}
	})

	t.Run("a listing carries no secret", func(t *testing.T) {
		// The table is readable end to end without opening a draft, and this is the read that would
		// break that if it ever selected the hash.
		previewTables(t, pool)
		c := Config{Resources: resources}

		id, secret, _, err := newPreviewCode()
		if err != nil {
			t.Fatalf("mint a code: %v", err)
		}
		if err := storePreviewLink(context.Background(), c,
			PreviewLink{ID: id, Scope: "record", Model: "Post", RecordID: "r-1"},
			hashPreviewSecret(secret), 1, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("store: %v", err)
		}

		links, err := listPreviewLinks(context.Background(), c, "Post", "r-1")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		encoded, err := json.Marshal(links)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, forbidden := range []string{secret, hashPreviewSecret(secret)} {
			if strings.Contains(string(encoded), forbidden) {
				t.Errorf("a listing carried the credential: %s", encoded)
			}
		}
	})

	t.Run("the epoch is read from the settings row", func(t *testing.T) {
		previewTables(t, pool)
		c := Config{Resources: resources}

		epoch, err := previewEpoch(context.Background(), c)
		if err != nil {
			t.Fatalf("read the epoch: %v", err)
		}
		if epoch != 1 {
			t.Errorf("epoch = %d, want 1", epoch)
		}
	})

	// Destructive: leaves the tables absent, so it runs after everything that needs them.
	t.Run("a missing table is reported rather than read as empty", func(t *testing.T) {
		previewTables(t, pool)
		c := Config{Resources: resources}
		if _, err := pool.Exec(context.Background(), `DROP TABLE _supatype.preview_links`); err != nil {
			t.Fatalf("drop: %v", err)
		}

		if err := storePreviewLink(context.Background(), c,
			PreviewLink{ID: "pl_x", Scope: "project"}, "hash", 1, time.Now().Add(time.Hour)); err == nil {
			t.Error("storing into a missing table reported success")
		}
		if _, err := listPreviewLinks(context.Background(), c, "Post", "r-1"); err == nil {
			t.Error("listing a missing table reported success, which would read as no links")
		}
		if err := revokePreviewLink(context.Background(), c, "pl_x"); err == nil {
			t.Error("revoking against a missing table reported success")
		}

		if _, err := pool.Exec(context.Background(), `DROP TABLE _supatype.publishing_settings`); err != nil {
			t.Fatalf("drop: %v", err)
		}
		if _, err := previewEpoch(context.Background(), c); err == nil {
			t.Error("reading the epoch from a missing table reported success")
		}
	})
}

func TestPreviewStoreWithoutADatabase(t *testing.T) {
	// Every entry point must say "no database" rather than behaving as though there were one and
	// finding nothing, which reads to a link holder as a draft that does not exist.
	c := Config{}
	if _, err := previewPool(c); err != errNoPreviewDatabase {
		t.Errorf("previewPool err = %v, want errNoPreviewDatabase", err)
	}
	if err := storePreviewLink(context.Background(), c, PreviewLink{ID: "pl_x"}, "h", 1, time.Now()); err != errNoPreviewDatabase {
		t.Errorf("storePreviewLink err = %v, want errNoPreviewDatabase", err)
	}
	if _, err := listPreviewLinks(context.Background(), c, "Post", "r-1"); err != errNoPreviewDatabase {
		t.Errorf("listPreviewLinks err = %v, want errNoPreviewDatabase", err)
	}
	if err := revokePreviewLink(context.Background(), c, "pl_x"); err != errNoPreviewDatabase {
		t.Errorf("revokePreviewLink err = %v, want errNoPreviewDatabase", err)
	}
	if _, _, _, err := resolvePreviewLink(context.Background(), c, "pl_abc.secret"); err != errNoPreviewDatabase {
		t.Errorf("resolvePreviewLink err = %v, want errNoPreviewDatabase", err)
	}
	if _, err := previewEpoch(context.Background(), c); err != errNoDatabase {
		t.Errorf("previewEpoch err = %v, want errNoDatabase", err)
	}
}

func TestResolveRefusesRubbishBeforeTouchingTheDatabase(t *testing.T) {
	// A code that cannot even be split is refused without a query, so a flood of malformed codes
	// costs no database work.
	if _, _, _, err := resolvePreviewLink(context.Background(), Config{}, "not-a-code"); err != ErrPreviewLinkNotFound {
		t.Errorf("err = %v, want ErrPreviewLinkNotFound", err)
	}
}

func TestPreviewAPIRouting(t *testing.T) {
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)

	c := previewCallerConfig(t, resources, previewConfig(`["admin","editor"]`, true))
	api := PreviewAPI(c)

	t.Run("an unknown path under the prefix is a 404", func(t *testing.T) {
		code, _ := previewCall(api, previewAdminSub, http.MethodPost, "/admin/preview-links/nonsense", "{}")
		if code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", code)
		}
	})

	t.Run("a method that is neither GET nor POST is refused", func(t *testing.T) {
		code, body := previewCall(api, previewAdminSub, http.MethodDelete, "/admin/preview-links", "")
		if code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405: %s", code, body)
		}
	})

	t.Run("a body that is not JSON is refused", func(t *testing.T) {
		code, _ := previewCall(api, previewAdminSub, http.MethodPost, "/admin/preview-links", "{not json")
		if code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", code)
		}
	})

	t.Run("an unknown scope is refused rather than defaulted", func(t *testing.T) {
		code, body := previewCall(api, previewAdminSub, http.MethodPost, "/admin/preview-links",
			`{"scope":"everything"}`)
		if code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400: %s", code, body)
		}
	})

	t.Run("a record link with no record named is refused", func(t *testing.T) {
		code, body := previewCall(api, previewAdminSub, http.MethodPost, "/admin/preview-links",
			`{"scope":"record","model":"  "}`)
		if code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400: %s", code, body)
		}
	})

	t.Run("a ttl past the ceiling is refused rather than clamped", func(t *testing.T) {
		// Silently shortening it would hand someone a link they believed in.
		code, body := previewCall(api, previewAdminSub, http.MethodPost, "/admin/preview-links",
			`{"scope":"record","model":"Post","recordId":"r-1","ttl":999999}`)
		if code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400: %s", code, body)
		}
		if !strings.Contains(body, "at most") {
			t.Errorf("body = %q, want it to say what the ceiling is", body)
		}
	})

	t.Run("an unauthenticated caller is told to authenticate", func(t *testing.T) {
		code, _ := previewCall(api, "", http.MethodPost, "/admin/preview-links",
			`{"scope":"record","model":"Post","recordId":"r-1"}`)
		if code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", code)
		}
	})

	t.Run("a caller with no membership row is refused", func(t *testing.T) {
		code, _ := previewCall(api, "cccc3333-3333-3333-3333-333333333333", http.MethodPost,
			"/admin/preview-links", `{"scope":"record","model":"Post","recordId":"r-1"}`)
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", code)
		}
	})

	t.Run("an editor may not mint a project-wide link", func(t *testing.T) {
		// Not because an editor cannot see the drafts, but because handing a URL to someone with no
		// account is a different authority from editing content.
		code, body := previewCall(api, previewEditorSub, http.MethodPost, "/admin/preview-links",
			`{"scope":"project"}`)
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403: %s", code, body)
		}
		if !strings.Contains(body, "record instead") {
			t.Errorf("body = %q, want it to point at the thing they can do", body)
		}
	})

	t.Run("a listing needs a model and a record", func(t *testing.T) {
		code, _ := previewCall(api, previewAdminSub, http.MethodGet, "/admin/preview-links", "")
		if code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", code)
		}
	})

	t.Run("an id that is not a preview id is refused", func(t *testing.T) {
		code, _ := previewCall(api, previewAdminSub, http.MethodPost,
			"/admin/preview-links/not-an-id/revoke", "")
		if code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", code)
		}
	})

	t.Run("mint, list, revoke, and it stops resolving", func(t *testing.T) {
		previewTables(t, pool)

		code, body := previewCall(api, previewAdminSub, http.MethodPost, "/admin/preview-links",
			`{"scope":"record","model":"Post","recordId":"r-9"}`)
		if code != http.StatusOK {
			t.Fatalf("mint: status = %d: %s", code, body)
		}
		shareCode, id := mintedCode(t, body)

		code, body = previewCall(api, previewAdminSub, http.MethodGet,
			"/admin/preview-links?model=Post&recordId=r-9", "")
		if code != http.StatusOK {
			t.Fatalf("list: status = %d: %s", code, body)
		}
		if !strings.Contains(body, id) {
			t.Errorf("listing %q does not include the link just minted (%s)", body, id)
		}

		resolve := PreviewResolveAPI(c)
		code, body = previewCall(resolve, "", http.MethodPost, "/preview-links/resolve",
			`{"code":"`+shareCode+`"}`)
		if code != http.StatusOK {
			t.Fatalf("resolve: status = %d: %s", code, body)
		}

		code, body = previewCall(api, previewAdminSub, http.MethodPost,
			"/admin/preview-links/"+id+"/revoke", "")
		if code != http.StatusOK {
			t.Fatalf("revoke: status = %d: %s", code, body)
		}

		// The whole point: one link withdrawn, immediately, without touching any other.
		code, body = previewCall(resolve, "", http.MethodPost, "/preview-links/resolve",
			`{"code":"`+shareCode+`"}`)
		if code != http.StatusNotFound {
			t.Errorf("a revoked link still resolved: status = %d: %s", code, body)
		}
	})

	t.Run("revoking everything strands the links that were live", func(t *testing.T) {
		previewTables(t, pool)

		_, body := previewCall(api, previewAdminSub, http.MethodPost, "/admin/preview-links",
			`{"scope":"record","model":"Post","recordId":"r-9"}`)
		shareCode, _ := mintedCode(t, body)

		code, body := previewCall(api, previewAdminSub, http.MethodPost, "/admin/preview-links/revoke", "")
		if code != http.StatusOK {
			t.Fatalf("revoke all: status = %d: %s", code, body)
		}
		if !strings.Contains(body, `"epoch":2`) {
			t.Errorf("body = %q, want the bumped epoch", body)
		}

		code, body = previewCall(PreviewResolveAPI(c), "", http.MethodPost, "/preview-links/resolve",
			`{"code":"`+shareCode+`"}`)
		if code != http.StatusNotFound {
			t.Errorf("a stranded link still resolved: status = %d: %s", code, body)
		}
	})

	t.Run("an editor may not revoke everything", func(t *testing.T) {
		code, _ := previewCall(api, previewEditorSub, http.MethodPost, "/admin/preview-links/revoke", "")
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", code)
		}
	})
}

func TestPreviewAPIWithProjectScopeTurnedOff(t *testing.T) {
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)

	c := previewCallerConfig(t, resources, previewConfig(`["admin","editor"]`, false))
	code, body := previewCall(PreviewAPI(c), previewAdminSub, http.MethodPost, "/admin/preview-links",
		`{"scope":"project"}`)
	if code != http.StatusForbidden {
		t.Errorf("status = %d, want 403: %s", code, body)
	}
	if !strings.Contains(body, "does not allow project-wide") {
		t.Errorf("body = %q, want it to name the project setting", body)
	}
}

func TestPreviewAPIWhenARoleCannotSeeDrafts(t *testing.T) {
	// If you cannot read the draft, you cannot show it to anyone.
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)

	c := previewCallerConfig(t, resources, previewConfig(`["admin"]`, true))
	code, body := previewCall(PreviewAPI(c), previewEditorSub, http.MethodPost, "/admin/preview-links",
		`{"scope":"record","model":"Post","recordId":"r-1"}`)
	if code != http.StatusForbidden {
		t.Errorf("status = %d, want 403: %s", code, body)
	}
	if !strings.Contains(body, "cannot see drafts") {
		t.Errorf("body = %q, want it to say why", body)
	}
}

func TestPreviewEndpointsOnAProjectWithNoVersionedModels(t *testing.T) {
	// No publishing block in admin-config.json means the project has no drafts at all. Every
	// endpoint says so rather than failing in a way that looks like a fault.
	c := Config{
		JWTSecret:       testSecret,
		AdminConfigPath: writeAdminConfig(t, `{"models":[]}`),
	}
	api := PreviewAPI(c)

	for _, call := range []struct {
		name   string
		method string
		path   string
	}{
		{"mint", http.MethodPost, "/admin/preview-links"},
		{"list", http.MethodGet, "/admin/preview-links?model=Post&recordId=r-1"},
		{"revoke one", http.MethodPost, "/admin/preview-links/pl_abc/revoke"},
		{"revoke all", http.MethodPost, "/admin/preview-links/revoke"},
	} {
		t.Run(call.name, func(t *testing.T) {
			code, body := previewCall(api, "", call.method, call.path, "{}")
			if code != http.StatusNotImplemented {
				t.Errorf("status = %d, want 501: %s", code, body)
			}
			if !strings.Contains(body, "no versioned models") {
				t.Errorf("body = %q, want it to explain there are no drafts", body)
			}
		})
	}
}

func TestPreviewResolveRefusals(t *testing.T) {
	c := Config{JWTSecret: testSecret}
	api := PreviewResolveAPI(c)

	t.Run("a GET is refused", func(t *testing.T) {
		if code, _ := previewCall(api, "", http.MethodGet, "/preview-links/resolve", ""); code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", code)
		}
	})

	t.Run("a body that is not JSON is refused", func(t *testing.T) {
		if code, _ := previewCall(api, "", http.MethodPost, "/preview-links/resolve", "{nope"); code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", code)
		}
	})

	t.Run("an empty code is refused", func(t *testing.T) {
		if code, _ := previewCall(api, "", http.MethodPost, "/preview-links/resolve", `{"code":"   "}`); code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", code)
		}
	})

	t.Run("with no database it says so rather than 404", func(t *testing.T) {
		// A 404 here would tell a holder their link is dead when the truth is that this deployment
		// cannot answer, which is a different thing and a different fix.
		code, body := previewCall(api, "", http.MethodPost, "/preview-links/resolve", `{"code":"pl_abc.secret"}`)
		if code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503: %s", code, body)
		}
	})
}

func TestPreviewResolveNeverOutlivesTheLink(t *testing.T) {
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)

	c := previewCallerConfig(t, resources, previewConfig(`["admin"]`, true))

	// A link with less left than the exchange window. The token must expire with the link, not a
	// minute after it.
	id, secret, shareCode, err := newPreviewCode()
	if err != nil {
		t.Fatalf("mint a code: %v", err)
	}
	linkExpires := time.Now().Add(10 * time.Second)
	if err := storePreviewLink(context.Background(), c,
		PreviewLink{ID: id, Scope: "record", Model: "Post", RecordID: "r-1"},
		hashPreviewSecret(secret), 1, linkExpires); err != nil {
		t.Fatalf("store: %v", err)
	}

	code, body := previewCall(PreviewResolveAPI(c), "", http.MethodPost, "/preview-links/resolve",
		`{"code":"`+shareCode+`"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}

	var parsed struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("read the response: %v", err)
	}
	expires, err := time.Parse(time.RFC3339, parsed.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expiry: %v", err)
	}
	if expires.After(linkExpires.Add(time.Second)) {
		t.Errorf("token expires %v, after the link itself at %v", expires, linkExpires)
	}

	// The exchanged token must carry no subject: a token with one would make its bearer that person.
	// Verified against the secret this config signs with, not the shared helper's own literal.
	verified, err := jwt.Parse(parsed.Token,
		func(*jwt.Token) (interface{}, error) { return []byte(testSecret), nil },
		jwt.WithoutClaimsValidation())
	if err != nil {
		t.Fatalf("the exchanged token does not verify: %v", err)
	}
	claims, ok := verified.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("claims are not a map")
	}
	if _, present := claims["sub"]; present {
		t.Error("the exchanged token carries a subject, so its holder inherits an identity")
	}
}

func TestRoleSeesDraftsWithoutAPublishingConfig(t *testing.T) {
	// A project with no versioned models shows no draft affordances at all.
	c := Config{AdminConfigPath: writeAdminConfig(t, `{"models":[]}`)}
	if roleSeesDrafts(RoleAdmin, c) {
		t.Error("a project with no publishing config offered drafts to admin")
	}
}

func TestRoleSeesDraftsUnderTheDevBypass(t *testing.T) {
	// The bypass has no membership row and no role, and it exists to open Studio completely on a
	// locally addressed deployment. Withholding drafts from it would be a strange half-measure.
	c := Config{
		Mode:            "dev",
		OpenDev:         true,
		PublicURLs:      []string{"http://localhost:8000"},
		AdminConfigPath: writeAdminConfig(t, previewConfig(`["admin"]`, true)),
	}
	if !c.DevBypass() {
		t.Fatal("the test config does not actually engage the dev bypass")
	}
	if !roleSeesDrafts("", c) {
		t.Error("the dev bypass was refused drafts")
	}
}

func TestPreviewMinterUnderTheDevBypass(t *testing.T) {
	c := Config{
		Mode:       "dev",
		OpenDev:    true,
		PublicURLs: []string{"http://127.0.0.1:8000"},
	}
	role, sub, ok := requirePreviewMinter(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil),
		c, PreviewScopeProject, PublishingSettings{AllowProjectScope: true})
	if !ok {
		t.Fatal("the dev bypass was refused")
	}
	if role != "dev-bypass" || sub != "" {
		t.Errorf("role, sub = %q, %q; want dev-bypass and no subject", role, sub)
	}
}

// A Resources that exists but reaches no database, which is what a deployment configured without a
// DSN holds. Distinct from a nil Resources: the code checks both, and only one of them is the case
// a misconfigured deployment actually produces.
func poollessResources() *data.Resources { return &data.Resources{} }

func TestPreviewEndpointsWhenTheDatabaseIsConfiguredButUnreachable(t *testing.T) {
	c := Config{
		Mode:            "dev",
		OpenDev:         true,
		PublicURLs:      []string{"http://localhost:8000"},
		Resources:       poollessResources(),
		AdminConfigPath: writeAdminConfig(t, previewConfig(`["admin"]`, true)),
	}

	if _, err := previewEpoch(context.Background(), c); err == nil {
		t.Error("previewEpoch reported success with no pool behind it")
	}
	if _, err := previewPool(c); err == nil {
		t.Error("previewPool handed back a pool it does not have")
	}

	code, body := previewCall(PreviewAPI(c), "", http.MethodPost, "/admin/preview-links/revoke", "")
	if code != http.StatusServiceUnavailable {
		t.Errorf("revoke all: status = %d, want 503: %s", code, body)
	}
}

func TestRevokeEverythingWithoutADatabaseAtAll(t *testing.T) {
	c := Config{
		Mode:            "dev",
		OpenDev:         true,
		PublicURLs:      []string{"http://localhost:8000"},
		AdminConfigPath: writeAdminConfig(t, previewConfig(`["admin"]`, true)),
	}
	code, body := previewCall(PreviewAPI(c), "", http.MethodPost, "/admin/preview-links/revoke", "")
	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503: %s", code, body)
	}
	if !strings.Contains(body, "no database") {
		t.Errorf("body = %q, want it to say there is no database", body)
	}
}

func TestMintingStopsWhenTheRandomSourceFails(t *testing.T) {
	// There is no degraded mode here. A link whose secret came from anywhere but the CSPRNG is a
	// guessable credential for a draft, so minting must fail rather than produce one.
	previous := randRead
	randRead = func([]byte) (int, error) { return 0, errNoDatabase }
	t.Cleanup(func() { randRead = previous })

	if _, _, _, err := newPreviewCode(); err == nil {
		t.Fatal("newPreviewCode produced a code from a broken random source")
	}

	// The second read is a separate branch: an id that came out fine followed by a secret that did
	// not must still refuse, rather than shipping a link with a short or empty secret.
	calls := 0
	randRead = func(b []byte) (int, error) {
		calls++
		if calls == 1 {
			return previous(b)
		}
		return 0, errNoDatabase
	}
	if _, _, _, err := newPreviewCode(); err == nil {
		t.Fatal("newPreviewCode produced a code whose secret came from a failed read")
	}
}

func TestMintReportsARandomSourceThatFails(t *testing.T) {
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)

	c := previewCallerConfig(t, resources, previewConfig(`["admin"]`, true))

	previous := randRead
	randRead = func([]byte) (int, error) { return 0, errNoDatabase }
	t.Cleanup(func() { randRead = previous })

	code, body := previewCall(PreviewAPI(c), previewAdminSub, http.MethodPost, "/admin/preview-links",
		`{"scope":"record","model":"Post","recordId":"r-1"}`)
	if code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500: %s", code, body)
	}
}

func TestMintDefaultsToARecordLink(t *testing.T) {
	// A request naming no scope gets the narrow one. Defaulting the other way would make an omitted
	// field hand out every draft in the project.
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)

	c := previewCallerConfig(t, resources, previewConfig(`["admin"]`, true))
	code, body := previewCall(PreviewAPI(c), previewAdminSub, http.MethodPost, "/admin/preview-links",
		`{"model":"Post","recordId":"r-1"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	var parsed previewResponse
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("read the response: %v", err)
	}
	if parsed.Scope != string(PreviewScopeRecord) {
		t.Errorf("scope = %q, want %q", parsed.Scope, PreviewScopeRecord)
	}
}

func TestMintFailsClosedWhenTheStoreWillNotTakeTheLink(t *testing.T) {
	// Returning a code that resolves against nothing would read to its holder as a draft that does
	// not exist, which sends them to the wrong person with the wrong question.
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)
	if _, err := pool.Exec(context.Background(), `DROP TABLE _supatype.preview_links`); err != nil {
		t.Fatalf("drop: %v", err)
	}

	c := previewCallerConfig(t, resources, previewConfig(`["admin"]`, true))
	code, body := previewCall(PreviewAPI(c), previewAdminSub, http.MethodPost, "/admin/preview-links",
		`{"scope":"record","model":"Post","recordId":"r-1"}`)
	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503: %s", code, body)
	}
	if !strings.Contains(body, "could not record") {
		t.Errorf("body = %q, want it to say the link was not recorded", body)
	}
}

func TestMintFailsClosedWhenTheEpochCannotBeRead(t *testing.T) {
	// Minting without the epoch would stamp a link no policy can match, which its holder reads as a
	// missing draft rather than as an outage.
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)
	if _, err := pool.Exec(context.Background(), `DROP TABLE _supatype.publishing_settings`); err != nil {
		t.Fatalf("drop: %v", err)
	}

	c := previewCallerConfig(t, resources, previewConfig(`["admin"]`, true))
	code, body := previewCall(PreviewAPI(c), previewAdminSub, http.MethodPost, "/admin/preview-links",
		`{"scope":"record","model":"Post","recordId":"r-1"}`)
	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503: %s", code, body)
	}
	if !strings.Contains(body, "revocation counter") {
		t.Errorf("body = %q, want it to name what could not be read", body)
	}
}

func TestRevokeEverythingReportsAFailedBump(t *testing.T) {
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)
	if _, err := pool.Exec(context.Background(), `DROP TABLE _supatype.publishing_settings`); err != nil {
		t.Fatalf("drop: %v", err)
	}

	c := previewCallerConfig(t, resources, previewConfig(`["admin"]`, true))
	code, body := previewCall(PreviewAPI(c), previewAdminSub, http.MethodPost,
		"/admin/preview-links/revoke", "")
	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503: %s", code, body)
	}
}

func TestListingAndRevokingReportADatabaseTheyCannotRead(t *testing.T) {
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)
	if _, err := pool.Exec(context.Background(), `DROP TABLE _supatype.preview_links`); err != nil {
		t.Fatalf("drop: %v", err)
	}

	c := previewCallerConfig(t, resources, previewConfig(`["admin"]`, true))
	api := PreviewAPI(c)

	// An empty list would say "nothing is shared" when the truth is "we cannot tell", and someone
	// checking what they had handed out would be reassured by a lie.
	code, body := previewCall(api, previewAdminSub, http.MethodGet,
		"/admin/preview-links?model=Post&recordId=r-1", "")
	if code != http.StatusServiceUnavailable {
		t.Errorf("list: status = %d, want 503: %s", code, body)
	}

	code, body = previewCall(api, previewAdminSub, http.MethodPost,
		"/admin/preview-links/pl_abcdefgh/revoke", "")
	if code != http.StatusServiceUnavailable {
		t.Errorf("revoke one: status = %d, want 503: %s", code, body)
	}
}

func TestListingAndRevokingRefuseACallerWhoCannotSeeDrafts(t *testing.T) {
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)

	c := previewCallerConfig(t, resources, previewConfig(`["admin"]`, true))
	api := PreviewAPI(c)

	code, _ := previewCall(api, previewEditorSub, http.MethodGet,
		"/admin/preview-links?model=Post&recordId=r-1", "")
	if code != http.StatusForbidden {
		t.Errorf("list: status = %d, want 403", code)
	}

	code, _ = previewCall(api, previewEditorSub, http.MethodPost,
		"/admin/preview-links/pl_abcdefgh/revoke", "")
	if code != http.StatusForbidden {
		t.Errorf("revoke one: status = %d, want 403", code)
	}
}

func TestRoleSeesDraftsFollowsTheConfiguredVisibility(t *testing.T) {
	// The non-bypass path: one stored answer, read here and by the generated policies, so the two
	// cannot disagree about who sees a draft.
	c := Config{AdminConfigPath: writeAdminConfig(t, previewConfig(`["admin"]`, true))}
	if !roleSeesDrafts(RoleAdmin, c) {
		t.Error("admin is in draftVisibility and was refused")
	}
	if roleSeesDrafts("editor", c) {
		t.Error("editor is not in draftVisibility and was allowed")
	}
}

func TestListingReportsARowItCannotRead(t *testing.T) {
	// A row whose columns are not the shape this expects must be an error, not a link quietly
	// dropped from the listing. Someone auditing what they have shared has to be told the answer is
	// incomplete, because a short list reads exactly like a short list of real links.
	resources := previewResources(t)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	previewTables(t, pool)

	// created_at as text, so scanning it into a time.Time fails on the row rather than on the query.
	for _, stmt := range []string{
		`ALTER TABLE _supatype.preview_links DROP COLUMN created_at`,
		`ALTER TABLE _supatype.preview_links ADD COLUMN created_at TEXT NOT NULL DEFAULT 'not a time'`,
	} {
		if _, err := pool.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("reshape %q: %v", stmt, err)
		}
	}

	c := Config{Resources: resources}
	id, secret, _, err := newPreviewCode()
	if err != nil {
		t.Fatalf("mint a code: %v", err)
	}
	if err := storePreviewLink(context.Background(), c,
		PreviewLink{ID: id, Scope: "record", Model: "Post", RecordID: "r-1"},
		hashPreviewSecret(secret), 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("store: %v", err)
	}

	links, err := listPreviewLinks(context.Background(), c, "Post", "r-1")
	if err == nil {
		t.Fatalf("listing read an unreadable row and returned %+v", links)
	}
}
