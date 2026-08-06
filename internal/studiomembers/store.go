package studiomembers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/dbpool"
)

// writeTimeout bounds membership mutations. Longer than a lookup — these run from
// a settings screen, not on the request path — but still bounded so a wedged
// database returns an error rather than hanging the UI.
const writeTimeout = 10 * time.Second

// ErrLastAdmin is returned when a change would leave the project with no admin.
var ErrLastAdmin = errors.New("this is the last Studio admin — promote someone else first")

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
	// GoTrue, so self-host lists them read-only rather than pretending otherwise.
	PlatformAccount bool `json:"platformAccount"`
}

// List returns every Studio membership for this project, project users first.
func List(ctx context.Context) ([]Member, error) {
	pool, err := dbpool.Pool(ctx)
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
// admins: nobody may change their own role — self-demotion is a footgun and
// self-promotion is the escalation this whole design exists to prevent — and the
// last admin cannot be demoted, because there would then be nobody able to grant
// access to anyone.
func SetRole(ctx context.Context, actingUserID, targetUserID, role string) error {
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

	pool, err := dbpool.Pool(ctx)
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
func Revoke(ctx context.Context, actingUserID, targetUserID string) error {
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

	pool, err := dbpool.Pool(ctx)
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

// Audit records a membership change in `_supatype.studio_audit`.
//
// Deliberately *not* inside SetRole/Revoke's transaction, and deliberately
// non-fatal. Refusing to revoke a compromised admin's access because the audit
// table is missing would be the worse failure; the write is logged at error level
// instead so a broken trail is noisy rather than silent.
//
// `actorID` is empty when the change came from a path with no signed-in actor
// (the dev bypass, or the CLI against the database directly).
func Audit(ctx context.Context, actorID, targetID, action, role string) {
	pool, err := dbpool.Pool(ctx)
	if err != nil {
		logrus.WithError(err).Error("studiomembers: membership change not audited")
		return
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	var actor, roleArg any
	if strings.TrimSpace(actorID) != "" {
		actor = actorID
	}
	if strings.TrimSpace(role) != "" {
		roleArg = role
	}

	if _, err := pool.Exec(writeCtx, `
		INSERT INTO _supatype.studio_audit (actor_id, target_id, action, role)
		VALUES ($1::uuid, $2::uuid, $3, $4)`,
		actor, targetID, action, roleArg); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"actor":  actorID,
			"target": targetID,
			"action": action,
			"role":   role,
		}).Error("studiomembers: membership change not audited")
	}
}

// guardLastAdmin refuses a change that would leave nobody able to grant access.
func guardLastAdmin(ctx context.Context, tx pgx.Tx, targetUserID string) error {
	var targetIsAdmin bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM _supatype.studio_members
			 WHERE role = $2
			   AND (user_id = $1::uuid OR platform_user_id = $1::uuid))`,
		targetUserID, RoleAdmin).Scan(&targetIsAdmin); err != nil {
		return err
	}
	if !targetIsAdmin {
		return nil
	}

	var adminCount int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM _supatype.studio_members WHERE role = $1`,
		RoleAdmin).Scan(&adminCount); err != nil {
		return err
	}
	if adminCount <= 1 {
		return ErrLastAdmin
	}
	return nil
}
