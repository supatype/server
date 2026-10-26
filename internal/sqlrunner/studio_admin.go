package sqlrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/utilities"
)

// The Studio operations that need more than the SQL editor's non-superuser role
// can do (SQ2): enabling an extension the image ships, and turning row level
// security on or off or writing a policy on the auth and storage tables. They
// run on the admin (superuser) connection, which is why they are a separate
// route from /sql behind the SQLEditor capability, and each is recorded as
// elevated by the proxy. The DDL for auth and storage goes through the image's
// SECURITY DEFINER helpers, so it runs as the table's owning admin role and
// never as a superuser.

// studioAdminSchemas are the schemas whose RLS and policy helpers exist.
var studioAdminSchemas = map[string]bool{"auth": true, "storage": true}

// ExtensionHandler serves POST /studio-admin/extensions.
func ExtensionHandler(cfg *config.Config, pools Pools) http.Handler {
	return adminWrite(cfg, pools, runExtension)
}

// RLSHandler serves POST /studio-admin/rls.
func RLSHandler(cfg *config.Config, pools Pools) http.Handler {
	return adminWrite(cfg, pools, runRLS)
}

// PolicyHandler serves POST /studio-admin/policies.
func PolicyHandler(cfg *config.Config, pools Pools) http.Handler {
	return adminWrite(cfg, pools, runPolicy)
}

// adminWrite is the shared frame: POST only, service-role gated, one statement
// in a transaction on the admin pool, answered with {"ok": true} or an error.
func adminWrite(cfg *config.Config, pools Pools, run func(context.Context, pgx.Tx, *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		if !checkServiceRole(cfg, r) {
			writeError(w, http.StatusUnauthorized, errors.New("service role key required"))
			return
		}
		pool, err := pools()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("database not available: %w", err))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		defer cancel()
		if err := runInTx(ctx, pool, run, r); err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		utilities.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
}

// runInTx runs one admin write in a transaction, rolled back on any error.
func runInTx(ctx context.Context, pool Pool, run func(context.Context, pgx.Tx, *http.Request) error, r *http.Request) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return failing(http.StatusServiceUnavailable, "failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := run(ctx, tx, r); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return failing(http.StatusInternalServerError, "failed to commit: %w", err)
	}
	return nil
}

func runExtension(ctx context.Context, tx pgx.Tx, r *http.Request) error {
	var body struct {
		Name   string `json:"name"`
		Enable bool   `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return failing(http.StatusBadRequest, "invalid request body")
	}
	if body.Name == "" {
		return failing(http.StatusBadRequest, "an extension name is required")
	}
	var known bool
	if err := tx.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = $1)", body.Name).Scan(&known); err != nil {
		return err
	}
	if !known {
		return failing(http.StatusBadRequest, "%s is not an extension this image ships", body.Name)
	}
	ident := pgx.Identifier{body.Name}.Sanitize()
	stmt := "CREATE EXTENSION IF NOT EXISTS " + ident
	if !body.Enable {
		stmt = "DROP EXTENSION IF EXISTS " + ident
	}
	_, err := tx.Exec(ctx, stmt)
	return err
}

func runRLS(ctx context.Context, tx pgx.Tx, r *http.Request) error {
	var body struct {
		Schema string `json:"schema"`
		Table  string `json:"table"`
		Enable bool   `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return failing(http.StatusBadRequest, "invalid request body")
	}
	if err := assertStudioSchema(body.Schema); err != nil {
		return err
	}
	call := fmt.Sprintf("SELECT %s.supatype_studio_set_rls($1, $2)", pgx.Identifier{body.Schema}.Sanitize())
	_, err := tx.Exec(ctx, call, body.Table, body.Enable)
	return err
}

func runPolicy(ctx context.Context, tx pgx.Tx, r *http.Request) error {
	var body struct {
		Schema  string   `json:"schema"`
		Table   string   `json:"table"`
		Op      string   `json:"op"`
		Name    string   `json:"name"`
		Command string   `json:"command"`
		Roles   []string `json:"roles"`
		Using   *string  `json:"using"`
		Check   *string  `json:"check"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return failing(http.StatusBadRequest, "invalid request body")
	}
	if err := assertStudioSchema(body.Schema); err != nil {
		return err
	}
	call := fmt.Sprintf("SELECT %s.supatype_studio_write_policy($1, $2, $3, $4, $5, $6, $7)",
		pgx.Identifier{body.Schema}.Sanitize())
	_, err := tx.Exec(ctx, call, body.Op, body.Table, body.Name, body.Command, body.Roles, body.Using, body.Check)
	return err
}

func assertStudioSchema(schema string) error {
	if !studioAdminSchemas[schema] {
		return failing(http.StatusBadRequest, "%q is not the auth or storage schema", schema)
	}
	return nil
}
