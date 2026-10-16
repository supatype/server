package studiomembers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
	defer rows.Close()

	members := make([]Member, 0)
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.CreatedAt, &m.UpdatedAt,
			&m.PlatformAccount); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
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
	if actingUserID != "" && actingUserID == targetUserID {
		return errors.New("you cannot change your own Studio role")
	}

	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()

	pool, err := s.pool()
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
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
	if actingUserID != "" && actingUserID == targetUserID {
		return errors.New("you cannot revoke your own Studio access")
	}

	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()

	pool, err := s.pool()
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
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

// RoleAdmin is the role that may grant and revoke access. Duplicated from
// studioauth rather than imported, because studioauth depends on this package's
// lookup and the cycle would be worse than the constant.
const RoleAdmin = "admin"

// AuditElevated records a request that bypassed RLS with the service role.
//
// No target: the record is about what the actor did, not about someone else. The
// method and path go in `detail` so the trail says *what* was reached, which is
// the whole point of making elevation visible.
func (s Store) AuditElevated(ctx context.Context, actorID, method, path string) {
	pool, err := s.pool()
	if err != nil {
		logrus.WithError(err).Error("studiomembers: elevated request not audited")
		return
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	var actor any
	if strings.TrimSpace(actorID) != "" {
		actor = actorID
	}
	detail := fmt.Sprintf(`{"method":%q,"path":%q}`, method, path)

	if _, err := pool.Exec(writeCtx, `
		INSERT INTO _supatype.studio_audit (actor_id, target_id, action, detail)
		VALUES ($1::uuid, NULL, 'elevated_request', $2::jsonb)`,
		actor, detail); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"actor":  actorID,
			"method": method,
			"path":   path,
		}).Error("studiomembers: elevated request not audited")
	}
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
