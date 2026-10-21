package studiomembers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/supatype/server/internal/data"
)

// membershipTriggerSQL is the engine's membership audit trigger, copied from
// `audit_studio_membership_function_sql` and `audit_studio_membership_trigger_sql`
// in supatype-schema-engine's src/state/migration_history.rs. The engine owns
// the DDL; this copy lets the store's side of the contract (the
// `supatype.membership_api` flag) be tested against the real trigger.
const membershipTriggerSQL = `
CREATE OR REPLACE FUNCTION _supatype.audit_studio_membership_change()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, _supatype
AS $fn$
DECLARE
    claims  jsonb;
    subject text;
    db_role text;
BEGIN
    IF to_regclass('_supatype.studio_audit') IS NULL THEN
        RAISE WARNING 'Studio membership changed with no _supatype.studio_audit to record it';
        RETURN NULL;
    END IF;
    BEGIN
        claims := nullif(current_setting('request.jwt.claims', true), '')::jsonb;
    EXCEPTION WHEN others THEN
        claims := NULL;
    END;
    subject := coalesce(claims ->> 'sub',
                        nullif(current_setting('request.jwt.claim.sub', true), ''));
    db_role := coalesce(nullif(current_setting('role', true), 'none'), session_user::text);
    INSERT INTO _supatype.studio_audit (actor_id, target_id, action, role, detail)
    VALUES (
        NULL,
        coalesce(NEW.user_id, NEW.platform_user_id, OLD.user_id, OLD.platform_user_id),
        'membership_changed_in_db',
        coalesce(NEW.role, OLD.role),
        jsonb_build_object(
            'operation',    TG_OP,
            'old_user_id',  coalesce(OLD.user_id, OLD.platform_user_id),
            'old_role',     OLD.role,
            'new_user_id',  coalesce(NEW.user_id, NEW.platform_user_id),
            'new_role',     NEW.role,
            'db_role',      db_role,
            'session_user', session_user::text,
            'jwt_sub',      subject,
            'via_api',      coalesce(current_setting('supatype.membership_api', true), '') = 'on',
            'source',       nullif(current_setting('supatype.membership_api', true), '')
        )
    );
    RETURN NULL;
END
$fn$;
DROP TRIGGER IF EXISTS studio_members_audit ON _supatype.studio_members;
CREATE TRIGGER studio_members_audit
    AFTER INSERT OR UPDATE OR DELETE ON _supatype.studio_members
    FOR EACH ROW EXECUTE FUNCTION _supatype.audit_studio_membership_change();`

// probeRole is a database role that can change memberships and nothing else,
// which is what a developer's direct database access looks like to the trigger.
const probeRole = "studio_audit_probe"

func adminPool(t *testing.T, store Store) *pgxpool.Pool {
	t.Helper()
	pool, err := store.pool()
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

// installMembershipTrigger adds the trigger and a role that may write
// studio_members but not studio_audit.
func installMembershipTrigger(t *testing.T, ctx context.Context, store Store) *pgxpool.Pool {
	t.Helper()
	pool := adminPool(t, store)
	for _, stmt := range []string{
		membershipTriggerSQL,
		`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + probeRole + `') THEN
				CREATE ROLE ` + probeRole + ` NOLOGIN;
			END IF;
		END $$`,
		`GRANT USAGE ON SCHEMA _supatype TO ` + probeRole,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON _supatype.studio_members TO ` + probeRole,
		`REVOKE ALL ON _supatype.studio_audit FROM ` + probeRole,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("install trigger: %v", err)
		}
	}
	return pool
}

// membershipAudit is the detail of the newest trigger row.
type membershipAudit struct {
	Operation string `json:"operation"`
	OldRole   string `json:"old_role"`
	NewRole   string `json:"new_role"`
	DBRole    string `json:"db_role"`
	JWTSub    string `json:"jwt_sub"`
	ViaAPI    bool   `json:"via_api"`
}

func latestMembershipAudit(t *testing.T, ctx context.Context, pool *pgxpool.Pool) membershipAudit {
	t.Helper()
	var raw string
	if err := pool.QueryRow(ctx, `SELECT detail::text FROM _supatype.studio_audit
		 WHERE action = 'membership_changed_in_db' ORDER BY id DESC LIMIT 1`).Scan(&raw); err != nil {
		t.Fatalf("read trigger audit: %v", err)
	}
	var got membershipAudit
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return got
}

// asProbe runs one statement as the restricted role, with optional
// transaction-local settings applied first.
func asProbe(t *testing.T, ctx context.Context, pool *pgxpool.Pool, settings map[string]string, stmt string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for key, value := range settings {
		if _, err := tx.Exec(ctx, `SELECT set_config($1, $2, true)`, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+probeRole); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, stmt); err != nil {
		t.Fatalf("%s as %s: %v", stmt, probeRole, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// ─── Elevated request audit ───────────────────────────────────────────────────

// The proxy fails closed on an unrecorded elevation, so a missing database has
// to reach it as an error rather than only a log line.
func TestAuditElevatedFailsWithoutADatabase(t *testing.T) {
	ref, err := Store{}.AuditElevated(context.Background(), ElevatedRequest{Actor: adminA, Method: "GET", Path: "/x"})
	if !errors.Is(err, data.ErrNoDatabase) {
		t.Fatalf("err = %v, want ErrNoDatabase", err)
	}
	if ref != (AuditRef{}) {
		t.Errorf("ref = %+v, want zero", ref)
	}
}

func TestAuditElevatedFailsWhenTheInsertFails(t *testing.T) {
	ctx, store := setupMembership(t, "")
	dropTables(t, ctx, store, "_supatype.studio_audit")

	ref, err := store.AuditElevated(ctx, ElevatedRequest{Actor: adminA, Method: "GET", Path: "/x"})
	if err == nil {
		t.Fatal("want an error when the audit row cannot be written")
	}
	if ref != (AuditRef{}) {
		t.Errorf("ref = %+v, want zero", ref)
	}
}

// The query, the body summary and, once the upstream answers, its status all
// land in the row the request was recorded in.
func TestAuditElevatedRecordsQueryBodyAndStatus(t *testing.T) {
	ctx, store := setupMembership(t, "")
	pool := adminPool(t, store)

	ref, err := store.AuditElevated(ctx, ElevatedRequest{
		Actor: adminA, Role: "editor", Mode: "studio", Method: "PATCH", Path: "/rest/v1/posts",
		Query: "id=eq.7", Body: "sha256:abc123",
	})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if ref == (AuditRef{}) {
		t.Fatal("want a handle to the inserted row")
	}
	if err := store.AuditElevatedStatus(ctx, ref, 403); err != nil {
		t.Fatalf("status: %v", err)
	}

	var got struct {
		Query  string `json:"query"`
		Body   string `json:"body"`
		Status int    `json:"status"`
		Path   string `json:"path"`
	}
	var raw string
	if err := pool.QueryRow(ctx, `SELECT detail::text FROM _supatype.studio_audit WHERE id = $1`,
		ref.id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Query != "id=eq.7" || got.Body != "sha256:abc123" || got.Status != 403 || got.Path != "/rest/v1/posts" {
		t.Errorf("detail = %s", raw)
	}
}

// A zero handle is what a caller holds when nothing was recorded; recording a
// status against it does nothing rather than touching some other row.
func TestAuditElevatedStatusWithAZeroRef(t *testing.T) {
	if err := (Store{}).AuditElevatedStatus(context.Background(), AuditRef{}, 200); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestAuditElevatedStatusOnAMissingRow(t *testing.T) {
	ctx, store := setupMembership(t, "")
	if err := store.AuditElevatedStatus(ctx, AuditRef{id: 987654}, 200); err == nil {
		t.Error("want an error when the row is not there")
	}
}

// ─── Membership changed outside the API ───────────────────────────────────────

// A developer with direct database access promoting someone (themselves, say)
// leaves a row naming the database role that did it.
func TestADirectMembershipChangeIsAudited(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+plainUsr+`', 'editor')`)
	pool := installMembershipTrigger(t, ctx, store)

	asProbe(t, ctx, pool, nil,
		`UPDATE _supatype.studio_members SET role = 'admin' WHERE user_id = '`+plainUsr+`'`)

	got := latestMembershipAudit(t, ctx, pool)
	want := membershipAudit{Operation: "UPDATE", OldRole: "editor", NewRole: "admin", DBRole: probeRole}
	if got != want {
		t.Errorf("audit = %+v, want %+v", got, want)
	}
}

// The same change through the membership API is marked as such, so only the
// changes made around it stand out.
func TestAChangeThroughTheStoreIsMarkedViaAPI(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin'), ('`+plainUsr+`', 'editor')`)
	pool := installMembershipTrigger(t, ctx, store)

	if err := store.SetRole(ctx, adminA, plainUsr, "developer"); err != nil {
		t.Fatal(err)
	}
	if got := latestMembershipAudit(t, ctx, pool); !got.ViaAPI || got.Operation != "UPDATE" {
		t.Errorf("SetRole audit = %+v, want via_api", got)
	}
	if err := store.Revoke(ctx, adminA, plainUsr); err != nil {
		t.Fatal(err)
	}
	if got := latestMembershipAudit(t, ctx, pool); !got.ViaAPI || got.Operation != "DELETE" {
		t.Errorf("Revoke audit = %+v, want via_api", got)
	}
}

// A change made through the data API carries the caller's JWT, in either of
// the two shapes PostgREST has used for it.
func TestTheJWTSubjectIsRecorded(t *testing.T) {
	ctx, store := setupMembership(t, "")
	pool := installMembershipTrigger(t, ctx, store)

	asProbe(t, ctx, pool, map[string]string{"request.jwt.claims": `{"sub":"` + adminB + `"}`},
		`INSERT INTO _supatype.studio_members (user_id, role) VALUES ('`+plainUsr+`', 'admin')`)
	if got := latestMembershipAudit(t, ctx, pool); got.JWTSub != adminB || got.ViaAPI {
		t.Errorf("claims audit = %+v", got)
	}

	asProbe(t, ctx, pool, map[string]string{"request.jwt.claim.sub": adminA},
		`DELETE FROM _supatype.studio_members WHERE user_id = '`+plainUsr+`'`)
	if got := latestMembershipAudit(t, ctx, pool); got.JWTSub != adminA || got.Operation != "DELETE" {
		t.Errorf("claim.sub audit = %+v", got)
	}
}

// The members list says which grants were last changed outside Studio, and a
// change through Studio afterwards clears it.
func TestListFlagsAChangeMadeOutsideStudio(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin'), ('`+plainUsr+`', 'editor')`)
	pool := installMembershipTrigger(t, ctx, store)

	asProbe(t, ctx, pool, nil,
		`UPDATE _supatype.studio_members SET role = 'admin' WHERE user_id = '`+plainUsr+`'`)
	if !changedOutside(t, ctx, store, plainUsr) {
		t.Fatal("a direct promotion was not flagged")
	}
	if changedOutside(t, ctx, store, adminA) {
		t.Error("an untouched grant was flagged")
	}

	if err := store.SetRole(ctx, adminA, plainUsr, "editor"); err != nil {
		t.Fatal(err)
	}
	if changedOutside(t, ctx, store, plainUsr) {
		t.Error("a change through Studio did not clear the flag")
	}
}

const (
	promoteUsr = `UPDATE _supatype.studio_members SET role = 'admin' WHERE user_id = '` + plainUsr + `'`
	demoteUsr  = `UPDATE _supatype.studio_members SET role = 'viewer' WHERE user_id = '` + plainUsr + `'`
)

// membershipStep is one change to plainUsr's grant, the mark it was made
// under, and whether the members list should flag it.
type membershipStep struct {
	source  string
	stmt    string
	flagged bool
}

func checkSteps(t *testing.T, ctx context.Context, store Store, pool *pgxpool.Pool, steps []membershipStep) {
	t.Helper()
	for _, step := range steps {
		asProbe(t, ctx, pool, map[string]string{"supatype.membership_api": step.source}, step.stmt)
		if got := changedOutside(t, ctx, store, plainUsr); got != step.flagged {
			t.Errorf("source %q: flagged = %v, want %v", step.source, got, step.flagged)
		}
	}
}

// The CLI's admin commands are a trusted way to change membership, marked
// `cli`; any other mark, or none, is a change made around Studio and stays
// flagged.
func TestListTrustsTheCLI(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin'), ('`+plainUsr+`', 'editor')`)
	pool := installMembershipTrigger(t, ctx, store)
	checkSteps(t, ctx, store, pool, []membershipStep{
		{"cli", promoteUsr, false},
		{"", demoteUsr, true},
		{"anything-else", promoteUsr, true},
	})
}

// studioSeedSQL is the engine's record of its one membership seed
// (`create_studio_seed_table_sql` in supatype-schema-engine's
// src/state/migration_history.rs), holding the newest audit row as the last
// the seed wrote.
const studioSeedSQL = `
CREATE TABLE IF NOT EXISTS _supatype.studio_seed (
    id            INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    seeded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    audit_through BIGINT
);
INSERT INTO _supatype.studio_seed (audit_through) SELECT max(id) FROM _supatype.studio_audit`

// The seed's mark is trusted only on a row the seed wrote: anyone with SQL can
// set it, so a mark with no seed on record, or on a row after the seed's, is a
// change made around Studio.
func TestListTrustsOnlyTheSeedsOwnRows(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin'), ('`+plainUsr+`', 'editor')`)
	pool := installMembershipTrigger(t, ctx, store)
	checkSteps(t, ctx, store, pool, []membershipStep{{"seed", promoteUsr, true}})

	if _, err := pool.Exec(ctx, studioSeedSQL); err != nil {
		t.Fatalf("record the seed: %v", err)
	}
	if changedOutside(t, ctx, store, plainUsr) {
		t.Error("the seed's own row was flagged")
	}
	checkSteps(t, ctx, store, pool, []membershipStep{
		{"seed", demoteUsr, true},
		{"cli", promoteUsr, false},
	})
}

func changedOutside(t *testing.T, ctx context.Context, store Store, userID string) bool {
	t.Helper()
	members, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		if m.UserID == userID {
			return m.ChangedOutsideStudio
		}
	}
	t.Fatalf("%s is not a member", userID)
	return false
}

// The flag is advisory: a trail the list cannot read leaves it false rather
// than taking the members screen down.
func TestListWithNoAuditTable(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin')`)
	dropTables(t, ctx, store, "_supatype.studio_audit")

	members, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(members) != 1 || members[0].ChangedOutsideStudio {
		t.Errorf("members = %+v", members)
	}
}
