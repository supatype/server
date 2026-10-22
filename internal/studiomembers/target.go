package studiomembers

import (
	"context"
	"fmt"
	"strings"
)

// TargetKind is how a user administration request names the project user it
// acts on: by user id, by one of their sessions, or by email address.
type TargetKind int

const (
	TargetUserID TargetKind = iota
	TargetSessionID
	TargetEmail
)

// targetRoleSQL finds the Studio roles held by the users a request names. An
// email can name more than one user (SSO accounts share addresses), so every
// match is returned and the caller judges them all.
var targetRoleSQL = map[TargetKind]string{
	TargetUserID: `SELECT role FROM _supatype.studio_members WHERE user_id = $1::uuid`,
	TargetSessionID: `SELECT m.role FROM auth.sessions s
		JOIN _supatype.studio_members m ON m.user_id = s.user_id
		WHERE s.id = $1::uuid`,
	TargetEmail: `SELECT m.role FROM auth.users u
		JOIN _supatype.studio_members m ON m.user_id = u.id
		WHERE lower(u.email) = lower(btrim($1))`,
}

// TargetRoles returns the Studio roles held by the project users a user
// administration request names, none when they hold no Studio role. An error
// means the answer is unknown, and the caller must refuse rather than guess.
func (s Store) TargetRoles(ctx context.Context, kind TargetKind, value string) ([]string, error) {
	query, ok := targetRoleSQL[kind]
	if !ok {
		return nil, fmt.Errorf("unknown target kind %d", kind)
	}
	pool, err := s.pool()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	rows, err := pool.Query(ctx, query, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, strings.TrimSpace(role))
	}
	return roles, rows.Err()
}
