package sqlrunner

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/supatype/server/internal/config"
)

// ─── The statement ────────────────────────────────────────────────────────────

// The handover goes through the function the stack's start-up creates, and only
// from the editor's own session. Everything the statement calls is schema
// qualified, because the request's search_path is whatever the caller asked for.
func TestTheHandoverCallsTheFunctionForTheEditorOnly(t *testing.T) {
	guard := strings.Index(reassignToOwner, "session_user = 'supatype_sql_editor'")
	call := strings.Index(reassignToOwner, "PERFORM supatype_editor.hand_over()")
	if guard < 0 || call < 0 || call < guard {
		t.Fatalf("the function is not called behind the editor guard:\n%s", reassignToOwner)
	}
	if !strings.Contains(reassignToOwner, "pg_catalog.to_regprocedure('supatype_editor.hand_over()') IS NOT NULL") {
		t.Errorf("the function's presence is not checked first:\n%s", reassignToOwner)
	}
}

// An older stack has no function, so it keeps the inline REASSIGN, aimed at the
// database owner read from the catalogue.
func TestTheHandoverFallsBackToTheInlineReassign(t *testing.T) {
	elseAt := strings.Index(reassignToOwner, "ELSE")
	if elseAt < 0 {
		t.Fatalf("there is no fallback branch:\n%s", reassignToOwner)
	}
	fallback := reassignToOwner[elseAt:]
	for _, want := range []string{
		"REASSIGN OWNED BY supatype_sql_editor TO %I",
		"pg_catalog.pg_get_userbyid(datdba)",
		"datname = pg_catalog.current_database()",
	} {
		if !strings.Contains(fallback, want) {
			t.Errorf("the fallback lacks %q:\n%s", want, fallback)
		}
	}
}

// ─── Against Postgres ─────────────────────────────────────────────────────────

// handoverSQL is what the CLI sends at every stack start
// (packages/cli/src/compose-db-roles.ts, EDITOR_HANDOVER_SQL), run here as the
// superuser. Keep the two the same: the attack tests below are about this text.
const handoverSQL = `DO $setup$
DECLARE
  target text;
  acl record;
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'supatype_sql_editor') THEN
    RETURN;
  END IF;
  SELECT pg_catalog.pg_get_userbyid(datdba) INTO target FROM pg_catalog.pg_database
   WHERE datname = pg_catalog.current_database();
  DROP SCHEMA IF EXISTS supatype_editor CASCADE;
  IF EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = target AND rolsuper)
     OR NOT pg_catalog.pg_has_role('supatype_sql_editor', target, 'USAGE') THEN
    RAISE WARNING 'supatype: SQL editor handover disabled: the database owner is a superuser or not a role the editor inherits';
    RETURN;
  END IF;
  CREATE SCHEMA supatype_editor;
  REVOKE ALL ON SCHEMA supatype_editor FROM PUBLIC;
  EXECUTE pg_catalog.format('CREATE FUNCTION supatype_editor.hand_over() RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = ''''
    AS $handover$ BEGIN REASSIGN OWNED BY supatype_sql_editor TO %I; END $handover$', target);
  REVOKE ALL ON FUNCTION supatype_editor.hand_over() FROM PUBLIC;
  FOR acl IN
    SELECT DISTINCT a.grantee FROM pg_catalog.pg_namespace n, pg_catalog.aclexplode(n.nspacl) a
     WHERE n.nspname = 'supatype_editor' AND a.grantee OPERATOR(pg_catalog.<>) 0::pg_catalog.oid
    UNION
    SELECT DISTINCT a.grantee FROM pg_catalog.pg_proc p, pg_catalog.aclexplode(p.proacl) a
     WHERE p.oid OPERATOR(pg_catalog.=) 'supatype_editor.hand_over()'::pg_catalog.regprocedure
       AND a.grantee OPERATOR(pg_catalog.<>) 0::pg_catalog.oid
  LOOP
    CONTINUE WHEN acl.grantee OPERATOR(pg_catalog.=) (SELECT nspowner FROM pg_catalog.pg_namespace WHERE nspname = 'supatype_editor');
    EXECUTE pg_catalog.format('REVOKE ALL ON FUNCTION supatype_editor.hand_over() FROM %I', pg_catalog.pg_get_userbyid(acl.grantee));
    EXECUTE pg_catalog.format('REVOKE ALL ON SCHEMA supatype_editor FROM %I', pg_catalog.pg_get_userbyid(acl.grantee));
  END LOOP;
  GRANT USAGE ON SCHEMA supatype_editor TO supatype_sql_editor;
  GRANT EXECUTE ON FUNCTION supatype_editor.hand_over() TO supatype_sql_editor;
END
$setup$;
`

// publicInACL matches a grant to PUBLIC in an aclitem list, which has no grantee
// name before the equals sign.
var publicInACL = regexp.MustCompile(`(^|[{,])=`)

const bystander = "sqlrunner_test_bystander"

// installHandover makes the function as the start-up step does, after a default
// privilege that would otherwise hand EXECUTE on every new function to a
// bystander role, which the step has to take back.
func installHandover(t *testing.T, stack editorEnv) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DO $$ BEGIN CREATE ROLE ` + bystander + ` NOLOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
		`ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO ` + bystander,
	} {
		if _, err := stack.admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = stack.admin.Exec(ctx, `DROP SCHEMA IF EXISTS supatype_editor CASCADE`)
		_, _ = stack.admin.Exec(ctx, `ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM `+bystander)
		_, _ = stack.admin.Exec(ctx, `DROP ROLE IF EXISTS `+bystander)
	})
	if _, err := stack.admin.Exec(ctx, handoverSQL); err != nil {
		t.Fatalf("install the handover function: %v", err)
	}
}

// The editor hands over what it made and nothing else: a table another role owns
// stays where it is, and a second statement works the same way.
func TestTheHandoverMovesOnlyWhatTheEditorOwns(t *testing.T) {
	stack := editorStack(t)
	installHandover(t, stack)
	ctx := context.Background()
	if _, err := stack.admin.Exec(ctx, `CREATE TABLE public.handover_other_made (id int)`); err != nil {
		t.Fatalf("create the bystander table: %v", err)
	}
	if _, err := stack.admin.Exec(ctx, `ALTER TABLE public.handover_other_made OWNER TO `+bystander); err != nil {
		t.Fatalf("hand it to the bystander: %v", err)
	}

	for _, table := range []string{"editor_made", "handover_second"} {
		rec := post(t, authorised(&config.Config{}), serving(stack.editor),
			`{"query":"CREATE TABLE public.`+table+` (id int)"}`, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d (%s)", table, rec.Code, rec.Body.String())
		}
		if owner := tableOwner(t, stack, table); owner != stack.owner {
			t.Errorf("%s is owned by %q, want %q", table, owner, stack.owner)
		}
	}
	if owner := tableOwner(t, stack, "handover_other_made"); owner != bystander {
		t.Errorf("another role's table moved to %q", owner)
	}
}

// The function is fixed when it is made: a superuser owns it, it runs with an
// empty search_path, and its one statement names the editor and the database
// owner.
func TestTheFunctionIsLockedDown(t *testing.T) {
	stack := editorStack(t)
	installHandover(t, stack)

	var owner, config, body string
	var superuser, definer bool
	err := stack.admin.QueryRow(context.Background(), `
SELECT pg_get_userbyid(p.proowner), r.rolsuper, p.prosecdef, coalesce(p.proconfig::text, ''), p.prosrc
  FROM pg_proc p JOIN pg_roles r ON r.oid = p.proowner
 WHERE p.oid = 'supatype_editor.hand_over()'::regprocedure`).Scan(&owner, &superuser, &definer, &config, &body)
	if err != nil {
		t.Fatalf("read the function: %v", err)
	}
	if !superuser {
		t.Errorf("owned by %q, which is not a superuser, so REASSIGN would fail", owner)
	}
	if !definer {
		t.Error("not SECURITY DEFINER")
	}
	if config != `{"search_path=\"\""}` {
		t.Errorf("proconfig = %s, want an empty search_path", config)
	}
	if want := `REASSIGN OWNED BY supatype_sql_editor TO ` + stack.owner + `;`; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
}

// Only the editor holds USAGE on the schema and EXECUTE on the function. The
// default privilege installHandover set gave the bystander EXECUTE on the new
// function, and PUBLIC holds it by default; both must be gone, and so must every
// service role the image makes.
func TestNobodyButTheEditorCanRunIt(t *testing.T) {
	stack := editorStack(t)
	installHandover(t, stack)
	ctx := context.Background()

	var publicRuns, publicUses bool
	if err := stack.admin.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM pg_proc p, aclexplode(p.proacl) a WHERE p.oid = 'supatype_editor.hand_over()'::regprocedure AND a.grantee = 0),
       EXISTS (SELECT 1 FROM pg_namespace n, aclexplode(n.nspacl) a WHERE n.nspname = 'supatype_editor' AND a.grantee = 0)`).Scan(&publicRuns, &publicUses); err != nil {
		t.Fatalf("read the grants to PUBLIC: %v", err)
	}
	if publicRuns || publicUses {
		t.Errorf("PUBLIC can run the function (execute=%v, usage=%v)", publicRuns, publicUses)
	}

	for _, role := range []string{bystander, "anon", "authenticated", "service_role"} {
		var canRun, canUse bool
		err := stack.admin.QueryRow(ctx, `
SELECT coalesce(has_function_privilege(r.rolname, 'supatype_editor.hand_over()', 'EXECUTE'), false),
       coalesce(has_schema_privilege(r.rolname, 'supatype_editor', 'USAGE'), false)
  FROM pg_roles r WHERE r.rolname = $1`, role).Scan(&canRun, &canUse)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if canRun || canUse {
			t.Errorf("%s can run the function (execute=%v, usage=%v)", role, canRun, canUse)
		}
	}

	var editorCanRun bool
	if err := stack.admin.QueryRow(ctx, `SELECT has_function_privilege('supatype_sql_editor', 'supatype_editor.hand_over()', 'EXECUTE')`).Scan(&editorCanRun); err != nil || !editorCanRun {
		t.Errorf("the editor cannot run it (err=%v)", err)
	}
}

// Everything submitted SQL could try against the function, as the editor, then
// the invariants again: it is still there, still the same, still the editor's
// alone.
func TestTheEditorCannotChangeOrWidenTheFunction(t *testing.T) {
	stack := editorStack(t)
	installHandover(t, stack)
	ctx := context.Background()

	refused := map[string]string{
		"replace it":            `CREATE OR REPLACE FUNCTION supatype_editor.hand_over() RETURNS void LANGUAGE sql AS 'SELECT 1'`,
		"drop it":               `DROP FUNCTION supatype_editor.hand_over()`,
		"change its owner":      `ALTER FUNCTION supatype_editor.hand_over() OWNER TO supatype_sql_editor`,
		"widen its search_path": `ALTER FUNCTION supatype_editor.hand_over() SET search_path = public`,
		"make it invoker":       `ALTER FUNCTION supatype_editor.hand_over() SECURITY INVOKER`,
		"plant a table":         `CREATE TABLE supatype_editor.planted (id int)`,
		"plant a function":      `CREATE FUNCTION supatype_editor.planted() RETURNS void LANGUAGE sql AS 'SELECT 1'`,
		"overload it":           `CREATE FUNCTION supatype_editor.hand_over(x int) RETURNS void LANGUAGE sql AS 'SELECT 1'`,
		"drop the schema":       `DROP SCHEMA supatype_editor CASCADE`,
		"rename the schema":     `ALTER SCHEMA supatype_editor RENAME TO supatype_editor_old`,
		"take the schema":       `ALTER SCHEMA supatype_editor OWNER TO supatype_sql_editor`,
	}
	for name, stmt := range refused {
		if _, err := stack.editor.Exec(ctx, stmt); err == nil {
			t.Errorf("the editor could %s: %s", name, stmt)
		}
	}

	// A grant by a non-owner is not an error in Postgres, only a warning that
	// nothing was granted, so what matters is that nothing was.
	for _, stmt := range []string{
		`GRANT EXECUTE ON FUNCTION supatype_editor.hand_over() TO PUBLIC`,
		`GRANT EXECUTE ON FUNCTION supatype_editor.hand_over() TO ` + bystander,
		`GRANT USAGE ON SCHEMA supatype_editor TO ` + bystander,
		`GRANT ALL ON SCHEMA supatype_editor TO PUBLIC`,
	} {
		_, _ = stack.editor.Exec(ctx, stmt)
	}

	var function, extra int
	var acl string
	err := stack.admin.QueryRow(ctx, `
SELECT (SELECT count(*) FROM pg_proc WHERE pronamespace = 'supatype_editor'::regnamespace),
       (SELECT count(*) FROM pg_class WHERE relnamespace = 'supatype_editor'::regnamespace),
       coalesce((SELECT proacl::text FROM pg_proc WHERE oid = 'supatype_editor.hand_over()'::regprocedure), '') ||
       coalesce((SELECT nspacl::text FROM pg_namespace WHERE nspname = 'supatype_editor'), '')`).Scan(&function, &extra, &acl)
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	if function != 1 || extra != 0 {
		t.Errorf("schema holds %d functions and %d relations, want exactly the one function", function, extra)
	}
	if strings.Contains(acl, bystander) || publicInACL.MatchString(acl) {
		t.Errorf("the editor widened the grants: %s", acl)
	}
}

// What a role planted in the schema or granted on the function before the
// start-up step runs again does not survive it.
func TestStartUpRemovesWhatWasPlantedBefore(t *testing.T) {
	stack := editorStack(t)
	installHandover(t, stack)
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE supatype_editor.handover_planted (id int)`,
		`GRANT EXECUTE ON FUNCTION supatype_editor.hand_over() TO PUBLIC`,
		`GRANT USAGE, CREATE ON SCHEMA supatype_editor TO ` + bystander,
	} {
		if _, err := stack.admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if _, err := stack.admin.Exec(ctx, handoverSQL); err != nil {
		t.Fatalf("run the start-up step again: %v", err)
	}

	var planted bool
	var acl string
	err := stack.admin.QueryRow(ctx, `
SELECT to_regclass('supatype_editor.handover_planted') IS NOT NULL,
       (SELECT proacl::text FROM pg_proc WHERE oid = 'supatype_editor.hand_over()'::regprocedure) ||
       coalesce((SELECT nspacl::text FROM pg_namespace WHERE nspname = 'supatype_editor'), '')`).Scan(&planted, &acl)
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	if planted {
		t.Error("a table planted in the schema survived")
	}
	if strings.Contains(acl, bystander) || publicInACL.MatchString(acl) {
		t.Errorf("a planted grant survived: %s", acl)
	}
}

// With no editor role the step does nothing at all: an image that predates the
// role must not get a schema nobody can use.
func TestTheStepDoesNothingWithoutTheEditorRole(t *testing.T) {
	stack := editorStack(t)
	ctx := context.Background()
	if _, err := stack.admin.Exec(ctx, strings.Replace(handoverSQL, "rolname = 'supatype_sql_editor'", "rolname = 'sqlrunner_no_such_role'", 1)); err != nil {
		t.Fatalf("run the step: %v", err)
	}
	var made bool
	if err := stack.admin.QueryRow(ctx, `SELECT to_regnamespace('supatype_editor') IS NOT NULL`).Scan(&made); err != nil {
		t.Fatalf("look up the schema: %v", err)
	}
	if made {
		t.Error("the step made the schema although the editor role is missing")
	}
}

// The inline fallback refuses a database owned by a superuser, even where the
// editor could SET ROLE to it and the REASSIGN would otherwise go through.
func TestTheFallbackRefusesASuperuserOwner(t *testing.T) {
	stack := editorStack(t)
	ctx := context.Background()
	const super = "sqlrunner_test_super"

	var database string
	if err := stack.admin.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatalf("read the database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = stack.admin.Exec(ctx, `ALTER DATABASE `+database+` OWNER TO `+stack.owner)
		_, _ = stack.admin.Exec(ctx, `REVOKE `+super+` FROM `+editorRole)
		_, _ = stack.admin.Exec(ctx, `DROP ROLE IF EXISTS `+super)
	})
	for _, stmt := range []string{
		`DROP ROLE IF EXISTS ` + super,
		`CREATE ROLE ` + super + ` SUPERUSER NOLOGIN`,
		`GRANT ` + super + ` TO ` + editorRole + ` WITH INHERIT TRUE, SET TRUE`,
		`ALTER DATABASE ` + database + ` OWNER TO ` + super,
	} {
		if _, err := stack.admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	_, err := stack.editor.Exec(ctx, reassignToOwner)
	if err == nil || !strings.Contains(err.Error(), "a superuser") {
		t.Fatalf("the handover to a superuser owner = %v, want a refusal naming it", err)
	}
}

// The refusal sits in the fallback branch, before the REASSIGN it guards.
func TestTheFallbackChecksTheOwnerBeforeReassigning(t *testing.T) {
	check := strings.Index(reassignToOwner, "rolsuper")
	reassign := strings.Index(reassignToOwner, "REASSIGN OWNED BY")
	if check < 0 || reassign < 0 || check > reassign {
		t.Errorf("the superuser check does not come before the REASSIGN:\n%s", reassignToOwner)
	}
}

// ─── The image's function comes first ─────────────────────────────────────────

// The handover tries the postgres image's own function, then the one an older
// CLI's start-up creates, then the inline REASSIGN, the order Cloud's control
// plane uses. A stack with both must not run the older one.
func TestTheImageFunctionIsTriedBeforeTheStartUpFunction(t *testing.T) {
	image := strings.Index(reassignToOwner, "PERFORM public.supatype_sql_editor_reassign_owned()")
	startUp := strings.Index(reassignToOwner, "PERFORM supatype_editor.hand_over()")
	inline := strings.Index(reassignToOwner, "EXECUTE pg_catalog.format('REASSIGN OWNED BY")
	if image < 0 || startUp < 0 || inline < 0 {
		t.Fatalf("a branch of the handover is missing:\n%s", reassignToOwner)
	}
	if !(image < startUp && startUp < inline) {
		t.Errorf("the branches are not in the order image function, start-up function, inline REASSIGN:\n%s", reassignToOwner)
	}
	if !strings.Contains(reassignToOwner, "pg_catalog.to_regprocedure('public.supatype_sql_editor_reassign_owned()') IS NOT NULL") {
		t.Errorf("the image function's presence is not checked first:\n%s", reassignToOwner)
	}
}

// imageHandoverSQL is the postgres image's migration
// (migrations/db/migrations/20261121120000_sql_editor_reassign_owned.sql), run
// here as the superuser: a dedicated NOLOGIN owner that inherits the editor and
// may SET ROLE to the database owner, and a definer function only the editor may
// execute.
const imageHandoverSQL = `DO $setup$
DECLARE
  owner_role text;
BEGIN
  CREATE ROLE supatype_sql_editor_reassigner NOLOGIN NOSUPERUSER NOINHERIT;
  GRANT supatype_sql_editor TO supatype_sql_editor_reassigner WITH INHERIT TRUE, SET FALSE;
  SELECT pg_get_userbyid(datdba) INTO owner_role FROM pg_database WHERE datname = current_database();
  EXECUTE format('GRANT %I TO supatype_sql_editor_reassigner WITH INHERIT FALSE, SET TRUE', owner_role);
END $setup$;
CREATE FUNCTION public.supatype_sql_editor_reassign_owned()
  RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp
AS $fn$
DECLARE target name;
BEGIN
  SELECT r.rolname INTO target FROM pg_catalog.pg_database d
    JOIN pg_catalog.pg_roles r ON r.oid = d.datdba WHERE d.datname = pg_catalog.current_database();
  EXECUTE pg_catalog.format('REASSIGN OWNED BY supatype_sql_editor TO %I', target);
END;
$fn$;
REVOKE ALL ON FUNCTION public.supatype_sql_editor_reassign_owned() FROM PUBLIC;
ALTER FUNCTION public.supatype_sql_editor_reassign_owned() OWNER TO supatype_sql_editor_reassigner;
GRANT EXECUTE ON FUNCTION public.supatype_sql_editor_reassign_owned() TO supatype_sql_editor;
`

func installImageHandover(t *testing.T, stack editorEnv) {
	t.Helper()
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = stack.admin.Exec(ctx, `DROP FUNCTION IF EXISTS public.supatype_sql_editor_reassign_owned()`)
		_, _ = stack.admin.Exec(ctx, `DROP ROLE IF EXISTS supatype_sql_editor_reassigner`)
	})
	if _, err := stack.admin.Exec(ctx, imageHandoverSQL); err != nil {
		t.Fatalf("install the image's handover function: %v", err)
	}
}

// With only the image's function, the editor's table still ends up the owner's.
func TestTheImageFunctionHandsOverWithoutTheStartUpFunction(t *testing.T) {
	stack := editorStack(t)
	installImageHandover(t, stack)

	rec := post(t, authorised(&config.Config{}), serving(stack.editor),
		`{"query":"CREATE TABLE public.handover_second (id int)"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if owner := tableOwner(t, stack, "handover_second"); owner != stack.owner {
		t.Errorf("handover_second is owned by %q, want %q", owner, stack.owner)
	}
}

// With both functions present the image's one runs. The start-up function is
// replaced by one that refuses, so a handover that reaches it fails the statement.
func TestTheImageFunctionWinsWhenBothExist(t *testing.T) {
	stack := editorStack(t)
	installHandover(t, stack)
	installImageHandover(t, stack)
	if _, err := stack.admin.Exec(context.Background(),
		`CREATE OR REPLACE FUNCTION supatype_editor.hand_over() RETURNS void LANGUAGE plpgsql SECURITY DEFINER
		   SET search_path = '' AS $f$ BEGIN RAISE EXCEPTION 'the start-up function ran'; END $f$`); err != nil {
		t.Fatalf("replace the start-up function: %v", err)
	}

	rec := post(t, authorised(&config.Config{}), serving(stack.editor),
		`{"query":"CREATE TABLE public.handover_second (id int)"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, the start-up function was reached (%s)", rec.Code, rec.Body.String())
	}
	if owner := tableOwner(t, stack, "handover_second"); owner != stack.owner {
		t.Errorf("handover_second is owned by %q, want %q", owner, stack.owner)
	}
}
