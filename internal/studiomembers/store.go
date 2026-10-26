package studiomembers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// writeTimeout bounds membership mutations. Longer than a lookup, since these
// run from a settings screen and not on the request path, but still bounded so
// a wedged database returns an error rather than hanging the UI.
const writeTimeout = 10 * time.Second

// ErrLastAdmin is returned when a change would leave the project with no admin.
var ErrLastAdmin = errors.New("this is the last Studio admin; promote someone else to admin first")

// ErrUnknownUser is returned when the target has no row in auth.users.
var ErrUnknownUser = errors.New("no such user in this project")

// ErrOwnRole is returned when a caller tries to change their own Studio role.
var ErrOwnRole = errors.New("you cannot change your own Studio role")

// ErrOwnAccess is returned when a caller tries to revoke their own Studio access.
var ErrOwnAccess = errors.New("you cannot revoke your own Studio access")

// Member is one membership row, joined to the project user it belongs to.
type Member struct {
	UserID    string `json:"userId"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	// PlatformAccount marks a grant held by a Supatype Cloud account rather than
	// one of this project's own users. Those cannot sign in to a self-hosted
	// deployment, so self-host lists them read-only rather than pretending
	// otherwise.
	PlatformAccount bool `json:"platformAccount"`
	// ChangedOutsideStudio marks a grant whose latest change was made in the
	// database directly rather than through the membership API: a developer
	// with database access promoting someone, say. The membership audit
	// trigger records every change; this is its newest verdict for the row.
	ChangedOutsideStudio bool `json:"changedOutsideStudio"`
}

// List returns every Studio membership for this project, project users first.
func (s Store) List(ctx context.Context) ([]Member, error) {
	pool, err := s.pool()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, `
		SELECT COALESCE(m.user_id, m.platform_user_id)::text,
		       COALESCE(u.email, ''),
		       m.role,
		       m.created_at::text,
		       m.updated_at::text,
		       m.platform_user_id IS NOT NULL
		  FROM _supatype.studio_members m
		  LEFT JOIN auth.users u ON u.id = m.user_id
		 ORDER BY (m.platform_user_id IS NOT NULL), m.created_at`)
	if err != nil {
		return nil, err
	}
	members, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Member, error) {
		var m Member
		err := row.Scan(&m.UserID, &m.Email, &m.Role, &m.CreatedAt, &m.UpdatedAt, &m.PlatformAccount)
		return m, err
	})
	if err != nil {
		return nil, err
	}
	markChangedOutsideStudio(ctx, pool, members)
	return members, nil
}

// outsideChangesSQL is, for every member the audit trigger has seen change,
// whether its newest change was made outside every trusted path: this API
// (`on`), the CLI's admin commands (`cli`) or the engine's one membership seed
// (`seed`), as the trigger records them in `source`. Anyone with SQL access can
// set the seed's mark, so it is trusted only on a row the seed itself wrote:
// one no newer than the last row recorded with the seed ($1, NULL when there is
// no seed on record). A row from before the trigger recorded a source falls
// back to `via_api`.
const outsideChangesSQL = `
	SELECT DISTINCT ON (target_id)
	       target_id::text,
	       NOT COALESCE(detail ->> 'source' IN ('on', 'cli')
	                    OR (detail ->> 'source' = 'seed' AND id <= $1::bigint),
	                    (detail ->> 'via_api')::boolean, false)
	  FROM _supatype.studio_audit
	 WHERE action = 'membership_changed_in_db' AND target_id IS NOT NULL
	 ORDER BY target_id, id DESC`

// seedRecordedSQL reports whether the engine's seed record exists, which a
// database pushed before the record existed does not have.
const seedRecordedSQL = `SELECT to_regclass('_supatype.studio_seed') IS NOT NULL`

// seedAuditBoundSQL is the last audit row the seed wrote, NULL when it ran
// before the record kept one or carried nobody forward.
const seedAuditBoundSQL = `SELECT (SELECT audit_through FROM _supatype.studio_seed WHERE id = 1)`

// seedAuditBound is the last audit row the seed's mark is trusted on, nil
// when there is none.
func seedAuditBound(ctx context.Context, pool *pgxpool.Pool) (*int64, error) {
	var recorded bool
	if err := pool.QueryRow(ctx, seedRecordedSQL).Scan(&recorded); err != nil || !recorded {
		return nil, err
	}
	var bound *int64
	err := pool.QueryRow(ctx, seedAuditBoundSQL).Scan(&bound)
	return bound, err
}

// markChangedOutsideStudio sets ChangedOutsideStudio from the audit trail.
//
// Advisory, so a trail it cannot read (a project pushed before the trigger
// existed, or an audit table someone dropped) leaves every flag false and is
// logged, rather than taking the members list down with it.
func markChangedOutsideStudio(ctx context.Context, pool *pgxpool.Pool, members []Member) {
	bound, err := seedAuditBound(ctx, pool)
	var rows pgx.Rows
	if err == nil {
		rows, err = pool.Query(ctx, outsideChangesSQL, bound)
	}
	if err == nil {
		var outside map[string]bool
		outside, err = collectOutsideChanges(rows)
		for i := range members {
			members[i].ChangedOutsideStudio = outside[members[i].UserID]
		}
	}
	if err != nil {
		logrus.WithError(err).Warn("studiomembers: could not read membership changes made outside Studio")
	}
}

func collectOutsideChanges(rows pgx.Rows) (map[string]bool, error) {
	defer rows.Close()
	outside := make(map[string]bool)
	for rows.Next() {
		var (
			target  string
			changed bool
		)
		if err := rows.Scan(&target, &changed); err != nil {
			return nil, err
		}
		outside[target] = changed
	}
	return outside, rows.Err()
}

// SetRole grants or updates Studio access for one of the project's own users.
//
// `actingUserID` is the caller. Two rules protect the project from its own
// admins. Nobody may change their own role: self-demotion is a footgun and
// self-promotion is the escalation this whole design exists to prevent. And the
// last admin cannot be demoted, because there would then be nobody able to grant
// access to anyone.
func (s Store) SetRole(ctx context.Context, actingUserID, targetUserID, role string) error {
	actingUserID = strings.TrimSpace(actingUserID)
	targetUserID = strings.TrimSpace(targetUserID)
	role = strings.TrimSpace(role)

	if targetUserID == "" {
		return ErrUnknownUser
	}
	if SameUser(actingUserID, targetUserID) {
		return ErrOwnRole
	}

	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()

	tx, err := s.beginMembershipChange(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM auth.users WHERE id = $1::uuid)`,
		targetUserID).Scan(&exists); err != nil {
		return fmt.Errorf("check user: %w", err)
	}
	if !exists {
		return ErrUnknownUser
	}

	if role != RoleAdmin {
		if err := guardLastAdmin(ctx, tx, targetUserID); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ($1::uuid, $2)
		ON CONFLICT (user_id) DO UPDATE
		  SET role = EXCLUDED.role, updated_at = now()`,
		targetUserID, role); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// Revoke removes a membership. Same self and last-admin protections as SetRole.
func (s Store) Revoke(ctx context.Context, actingUserID, targetUserID string) error {
	actingUserID = strings.TrimSpace(actingUserID)
	targetUserID = strings.TrimSpace(targetUserID)

	if targetUserID == "" {
		return ErrUnknownUser
	}
	if SameUser(actingUserID, targetUserID) {
		return ErrOwnAccess
	}

	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()

	tx, err := s.beginMembershipChange(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if err := guardLastAdmin(ctx, tx, targetUserID); err != nil {
		return err
	}

	// Either identity, so an admin can clear a stale cloud grant from self-host.
	if _, err := tx.Exec(ctx,
		`DELETE FROM _supatype.studio_members
		  WHERE user_id = $1::uuid OR platform_user_id = $1::uuid`,
		targetUserID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// beginMembershipChange opens the transaction a membership change runs in,
// flagged as coming through this API. The membership audit trigger records
// every change to studio_members with that flag, so a change made around the
// API, by a developer with database access, shows up as one. The setting is
// transaction-local and ends with the transaction.
func (s Store) beginMembershipChange(ctx context.Context) (pgx.Tx, error) {
	pool, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('supatype.membership_api', 'on', true)`); err != nil {
		_ = tx.Rollback(context.Background())
		return nil, fmt.Errorf("mark membership change: %w", err)
	}
	return tx, nil
}

// RoleAdmin is the role that may grant and revoke access. Duplicated from
// studioauth rather than imported, because studioauth depends on this package's
// lookup and the cycle would be worse than the constant.
const RoleAdmin = "admin"

// ElevatedRequest is one request that skipped the app's row rules: who made it,
// with which Studio role, in which acting mode, and what it reached.
//
// Body is whatever summary the caller chose to keep: a bounded excerpt or a
// "sha256:<hex>" digest. It is stored as given. Status is the upstream status,
// zero until it is known; AuditElevatedStatus fills it in afterwards. Statement
// is the SQL a SQL editor request ran, bounded by the caller, so a membership
// change made there is attributable.
type ElevatedRequest struct {
	Actor     string `json:"-"`
	Role      string `json:"role"`
	Mode      string `json:"mode"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Query     string `json:"query,omitempty"`
	Body      string `json:"body,omitempty"`
	Statement string `json:"statement,omitempty"`
	Status    int    `json:"status,omitempty"`
}

// AuditRef is a handle to one recorded elevated request, for recording its
// upstream status once that is known. The zero value refers to nothing.
type AuditRef struct {
	id int64
}

// AuditElevated records a request that skipped the app's row rules, as the
// service role or acting as Studio, and returns a handle to the row.
//
// No target: the record is about what the actor did, not about someone else. The
// request's shape goes in `detail` so the trail says *what* was reached and on
// whose authority, which is the whole point of making it visible. Unlike Audit,
// a failure is returned as well as logged: an elevation nobody can account for
// is one the caller refuses.
func (s Store) AuditElevated(ctx context.Context, r ElevatedRequest) (AuditRef, error) {
	ref, err := s.insertElevated(ctx, r)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"actor":  r.Actor,
			"role":   r.Role,
			"method": r.Method,
			"path":   r.Path,
		}).Error("studiomembers: elevated request not audited")
	}
	return ref, err
}

func (s Store) insertElevated(ctx context.Context, r ElevatedRequest) (AuditRef, error) {
	pool, err := s.pool()
	if err != nil {
		return AuditRef{}, err
	}
	detail, err := json.Marshal(r)
	if err != nil {
		return AuditRef{}, fmt.Errorf("encode elevated request: %w", err)
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	var ref AuditRef
	if err := pool.QueryRow(writeCtx, `
		INSERT INTO _supatype.studio_audit (actor_id, target_id, action, detail)
		VALUES ($1::uuid, NULL, 'elevated_request', $2::jsonb)
		RETURNING id`,
		nullIfBlank(r.Actor), string(detail)).Scan(&ref.id); err != nil {
		return AuditRef{}, err
	}
	return ref, nil
}

// AuditElevatedStatus records the upstream status on the row AuditElevated
// wrote. A zero ref is a request that was never recorded, so there is nothing
// to update and no error.
func (s Store) AuditElevatedStatus(ctx context.Context, ref AuditRef, status int) error {
	if ref == (AuditRef{}) {
		return nil
	}
	pool, err := s.pool()
	if err != nil {
		return err
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	tag, err := pool.Exec(writeCtx, `
		UPDATE _supatype.studio_audit
		   SET detail = COALESCE(detail, '{}'::jsonb) || jsonb_build_object('status', $2::int)
		 WHERE id = $1`,
		ref.id, status)
	if err == nil && tag.RowsAffected() != 1 {
		err = fmt.Errorf("elevated request audit row %d not found", ref.id)
	}
	if err != nil {
		logrus.WithError(err).WithField("status", status).
			Error("studiomembers: elevated request status not audited")
	}
	return err
}

// Audit records a membership change in `_supatype.studio_audit`.
//
// Deliberately *not* inside SetRole/Revoke's transaction, and deliberately
// non-fatal. Refusing to revoke a compromised admin's access because the audit
// table is missing would be the worse failure; the write is logged at error level
// instead so a broken trail is noisy rather than silent.
//
// `actorID` is empty when the change came from a path with no signed-in actor
// (the dev bypass, or the CLI against the database directly).
func (s Store) Audit(ctx context.Context, actorID, targetID, action, role string) {
	pool, err := s.pool()
	if err != nil {
		logrus.WithError(err).Error("studiomembers: membership change not audited")
		return
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	if _, err := pool.Exec(writeCtx, AuditSQL,
		AuditArgs(actorID, targetID, action, role)...); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"actor":  actorID,
			"target": targetID,
			"action": action,
			"role":   role,
		}).Error("studiomembers: membership change not audited")
	}
}

// ActionRevoke is the audit action for a membership an admin removed.
const ActionRevoke = "revoke"

// ActionUserDeleted is the audit action for a membership removed because its
// user was deleted. Its role is the role the user held, so the trail says what
// went with them.
const ActionUserDeleted = "user_deleted"

// AuditSQL writes one membership change, taking AuditArgs. Exported with
// AuditArgs so a writer on another driver, such as the auth server deleting a
// user, records exactly the row Audit does.
const AuditSQL = `
	INSERT INTO _supatype.studio_audit (actor_id, target_id, action, role)
	VALUES ($1::uuid, $2::uuid, $3, $4)`

// AuditArgs are AuditSQL's arguments. A blank actor or role is recorded as
// NULL.
func AuditArgs(actorID, targetID, action, role string) []any {
	return []any{nullIfBlank(actorID), targetID, action, nullIfBlank(role)}
}

func nullIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// LastAdminQuery asks, in one row, whether `targetUserID` holds an admin
// membership (`target_is_admin`) and how many admin memberships there are
// (`admin_count`). With CheckLastAdmin it is the last-admin guard, exported so
// a caller on another driver, such as the auth server deleting a user, applies
// the members API's rule rather than a copy of it.
//
// It locks every admin membership it counts (FOR UPDATE) until the caller's
// transaction ends, so run it in the transaction that makes the change. Read
// without the lock, two transactions removing or demoting the last two admins
// each counted two and both went ahead. Locked, the second waits for the first
// and then counts what the first left; Postgres re-checks a waited-for row, so a
// deleted or demoted admin drops out of the count. The rows are locked in id
// order, so every guard takes them in the same order and two guards cannot
// deadlock each other.
func LastAdminQuery(targetUserID string) (string, []any) {
	return `
		SELECT COALESCE(bool_or(user_id = $1::uuid OR platform_user_id = $1::uuid), false)
		         AS target_is_admin,
		       count(*) AS admin_count
		  FROM (SELECT user_id, platform_user_id
		          FROM _supatype.studio_members
		         WHERE role = $2
		         ORDER BY id
		           FOR UPDATE) AS admins`,
		[]any{targetUserID, RoleAdmin}
}

// CheckLastAdmin is the guard's verdict on LastAdminQuery's row: ErrLastAdmin
// when removing the target would leave nobody able to grant access.
func CheckLastAdmin(targetIsAdmin bool, adminCount int) error {
	if targetIsAdmin && adminCount <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// guardLastAdmin refuses a change that would leave nobody able to grant access.
func guardLastAdmin(ctx context.Context, tx pgx.Tx, targetUserID string) error {
	var (
		targetIsAdmin bool
		adminCount    int
	)
	query, args := LastAdminQuery(targetUserID)
	if err := tx.QueryRow(ctx, query, args...).Scan(&targetIsAdmin, &adminCount); err != nil {
		return err
	}
	return CheckLastAdmin(targetIsAdmin, adminCount)
}
