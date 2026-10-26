package studioauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/gofrs/uuid"
	"github.com/supatype/server/internal/studiomembers"
)

// Developers must never be able to control admins (owner rule). A developer
// holds manageUsers, so the auth admin API is open to them; these are the
// requests through it that act on one existing user's account, and the guard
// refuses them when that user holds the Studio admin role and the caller does
// not: ban or disable, delete, password, email, app metadata and role changes
// (PUT on the user), MFA factor removal or change, session revocation, and the
// links that sign someone in, reset a password or resend an invitation.

// refusedAdminTarget is the answer to a non-admin acting on an admin's account.
const refusedAdminTarget = "Only a Studio admin can change another admin's account."

// refusedUncheckedTarget is the answer when whose account it is cannot be told.
const refusedUncheckedTarget = "Studio could not check whose account this is, so the change was not made. Try again."

// TargetRoleLookup returns the Studio roles held by the users one request names.
type TargetRoleLookup func(ctx context.Context, kind studiomembers.TargetKind, value string) ([]string, error)

// targetRoles is where this config looks targets up: TargetRoles when set,
// else the membership store, which with no database fails every lookup.
func (c Config) targetRoles() TargetRoleLookup {
	if c.TargetRoles != nil {
		return c.TargetRoles
	}
	return c.Members.TargetRoles
}

var (
	authAdminUser    = regexp.MustCompile(`^/auth/v1/admin/users/([^/]+)(/.*)?$`)
	authAdminSession = regexp.MustCompile(`^/auth/v1/admin/sessions/([^/]+)/?$`)
)

// emailTargetPaths are the POSTs that name their user by an `email` field.
var emailTargetPaths = map[string]bool{
	"/auth/v1/admin/generate_link": true,
	"/auth/v1/invite":              true,
	"/auth/v1/recover":             true,
}

// errNoTarget means the request acts on no one user the guard can name. One
// whose id the auth server cannot parse either is refused there.
var errNoTarget = errors.New("no target")

// accountTarget names the user one auth request changes, or errNoTarget.
func accountTarget(req *http.Request, path string) (studiomembers.TargetKind, string, error) {
	if isReadOnly(req.Method) {
		return 0, "", errNoTarget
	}
	if m := authAdminUser.FindStringSubmatch(path); m != nil {
		return uuidTarget(studiomembers.TargetUserID, m[1])
	}
	if m := authAdminSession.FindStringSubmatch(path); m != nil {
		return uuidTarget(studiomembers.TargetSessionID, m[1])
	}
	if req.Method == http.MethodPost && emailTargetPaths[path] {
		return emailTarget(req)
	}
	return 0, "", errNoTarget
}

// uuidTarget parses an id segment the way the auth server does.
func uuidTarget(kind studiomembers.TargetKind, segment string) (studiomembers.TargetKind, string, error) {
	decoded, err := url.PathUnescape(segment)
	if err != nil {
		return 0, "", errNoTarget
	}
	id, err := uuid.FromString(decoded)
	if err != nil {
		return 0, "", errNoTarget
	}
	return kind, id.String(), nil
}

// emailTarget reads the `email` field the way the auth server does, with
// encoding/json into a struct, so a key that differs only in case names the
// same field here as there. The body is put back for the upstream.
func emailTarget(req *http.Request) (studiomembers.TargetKind, string, error) {
	body, err := readBodyRestoring(req)
	if err != nil {
		return 0, "", err
	}
	var params struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(body, &params) != nil || params.Email == "" {
		return 0, "", errNoTarget
	}
	return studiomembers.TargetEmail, params.Email, nil
}

// guardBodyLimit bounds the body the guard reads; a larger one is refused.
const guardBodyLimit = 1 << 20

var errBodyTooLarge = errors.New("request body too large to inspect")

// readBodyRestoring reads a request body, at most guardBodyLimit bytes, and
// replaces it with a copy so the upstream still receives it whole.
func readBodyRestoring(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, guardBodyLimit+1))
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) > guardBodyLimit {
		return nil, errBodyTooLarge
	}
	return body, nil
}

// guardAdminAccounts refuses, having written the response, a request by a
// role without the Studio admin role that acts on the account of a user who
// holds it. Failing to tell whose account it is refuses too.
func guardAdminAccounts(w http.ResponseWriter, req *http.Request, c Config, perms StudioPermissions, path string) bool {
	if perms.ManageMembers || !under(path, "/auth/v1") {
		return true
	}
	kind, value, err := accountTarget(req, path)
	if errors.Is(err, errNoTarget) {
		return true
	}
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, refusedUncheckedTarget)
		return false
	}
	roles, err := c.targetRoles()(req.Context(), kind, value)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, refusedUncheckedTarget)
		return false
	}
	if holdsStudioAdmin(roles) {
		writeError(w, http.StatusForbidden, refusedAdminTarget)
		return false
	}
	return true
}

// holdsStudioAdmin reports whether any of roles grants the Studio admin's own
// power, managing Studio members.
func holdsStudioAdmin(roles []string) bool {
	for _, role := range roles {
		if perms, ok := PermissionsForRole(role); ok && perms.ManageMembers {
			return true
		}
	}
	return false
}
