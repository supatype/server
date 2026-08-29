// Package mau counts monthly active users for billing.
//
// The count is per organisation, not per project, so a customer with several
// projects is billed for people rather than for deployments. A day's users are
// held as a set of dedupe keys, and the month's figure is the union of its days,
// computed elsewhere.
//
// Everything that decides what to count is a pure function here. Only Record
// touches anything.
package mau

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// retention is how long a day's set is kept. Forty days, so a month's rollup can
// still read the last day after the month closes.
const retention = 40 * 24 * time.Hour

// Eligible reports whether a successful auth call represents a user showing up.
//
// Only the calls where somebody proved who they are: a signup, a verification,
// and the token grants that involve a credential. A refresh is deliberately
// absent, since a background token rotation is not a person arriving.
func Eligible(method, path, grantType string) bool {
	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/auth/v1/signup", "/auth/v1/verify":
		return true
	case "/auth/v1/token":
		switch grantType {
		case "password", "id_token", "pkce":
			return true
		}
	}
	return false
}

// ProjectRef strips an environment suffix from a tenant id.
//
// A project's staging and preview stacks bill to the same organisation as the
// project itself, so they must resolve to the same org and share a day's set.
func ProjectRef(tenantID string) string {
	i := strings.LastIndex(tenantID, "-")
	if i <= 0 {
		return tenantID
	}
	switch tenantID[i+1:] {
	case "staging", "preview":
		return tenantID[:i]
	}
	return tenantID
}

// DedupeKey identifies a person as stably as the available evidence allows.
//
// The order matters. A federated identity is the same person across projects and
// devices, so it wins. A salted email hash is next: stable, and salted so the
// tally cannot be turned back into a customer's mailing list. The local user id
// is the last resort and is scoped to the project, because the same person
// signing up to two projects has two ids and must not be silently merged.
//
// The salt is required for the email form. Without one, hashing would produce a
// value an attacker with the tally could confirm addresses against, so an
// unsalted deployment falls through to the local id instead.
func DedupeKey(emailSalt, projectRef string, user map[string]any) string {
	if key, ok := federatedKey(user); ok {
		return key
	}
	if email, ok := user["email"].(string); ok && strings.TrimSpace(email) != "" && emailSalt != "" {
		normalised := strings.ToLower(strings.TrimSpace(email))
		sum := sha256.Sum256([]byte(normalised + "||" + emailSalt))
		return "email:" + hex.EncodeToString(sum[:])
	}
	id, _ := user["id"].(string)
	if id == "" {
		id = "unknown"
	}
	return "local:" + projectRef + ":" + id
}

// federatedKey returns the first usable provider identity on the user.
func federatedKey(user map[string]any) (string, bool) {
	identities, ok := user["identities"].([]any)
	if !ok {
		return "", false
	}
	for _, raw := range identities {
		identity, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		provider, _ := identity["provider"].(string)
		subject, _ := identity["identity_id"].(string)
		if subject == "" {
			subject, _ = identity["id"].(string)
		}
		if provider != "" && subject != "" {
			return "oidc:" + provider + ":" + subject, true
		}
	}
	return "", false
}

// DayKey is where one organisation's users for one day are held.
func DayKey(orgID string, day time.Time) string {
	return "mau:org:" + orgID + ":d:" + day.UTC().Format("2006-01-02")
}

// Store records a member in a set that expires.
type Store interface {
	AddToExpiringSet(ctx context.Context, key, member string, expireAt time.Time) error
}

// Recorder writes the tally.
type Recorder struct {
	Store Store
	Salt  string
	// Now is overridable so a test does not have to wait for midnight to see
	// which day a tally lands in.
	Now func() time.Time
}

// Record adds one user to their organisation's set for today.
func (r Recorder) Record(ctx context.Context, orgID, projectRef string, user map[string]any) error {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	at := now().UTC()
	return r.Store.AddToExpiringSet(
		ctx,
		DayKey(orgID, at),
		DedupeKey(r.Salt, projectRef, user),
		at.Add(retention),
	)
}
