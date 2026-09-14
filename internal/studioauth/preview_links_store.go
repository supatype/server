package studioauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// A preview link is an id and a secret, and only the id is ever stored in the clear.
//
// # Why it is two parts
//
// Revocation used to mean bumping a global counter, which withdrew every outstanding link in the
// project at once: sending one link to the wrong person broke every other link anyone held. The
// objection to keeping a table of links was that it would be a database of secrets. Splitting the
// credential answers that. The id is public and identifies a row; the secret is never written
// down, only its hash. The table can be read end to end without opening a single draft.
//
// The id is what makes a link listable and revocable. Studio can say "made at 14:02, expires at
// 15:02, by you" and offer a Revoke button, without ever holding the credential a second time.
//
// # Why SHA-256 rather than a password hash
//
// Password hashes are slow on purpose because passwords are low entropy and guessable. A secret
// here is 24 random bytes from the system CSPRNG, so there is nothing to guess: an attacker
// holding the table faces the full keyspace either way, and a slow hash would only tax the honest
// path, which runs on every preview page load.
const (
	previewIDPrefix    = "pl_"
	previewIDBytes     = 8
	previewSecretBytes = 24
)

var (
	// ErrPreviewLinkNotFound covers every reason a code will not open a draft: no such id, the
	// wrong secret, revoked, expired, or stranded by an epoch bump.
	//
	// One error for all of them, deliberately. Telling a caller that the id existed but the secret
	// was wrong turns a guessed id into a confirmed one, and telling them a link was revoked
	// rather than never issued leaks that a draft exists.
	ErrPreviewLinkNotFound = errors.New("no live preview link matches that code")
	errNoPreviewDatabase   = errors.New("no database")
)

// PreviewLink is one row, as Studio lists it. It carries no secret.
type PreviewLink struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	Model     string `json:"model,omitempty"`
	RecordID  string `json:"recordId,omitempty"`
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// previewStore is the slice of the pool these queries need, named so the pool type does not have
// to be repeated at every call site.
type previewStore interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func previewPool(c Config) (previewStore, error) {
	if c.Resources == nil {
		return nil, errNoPreviewDatabase
	}
	return c.Resources.AdminPool()
}

// randRead is crypto/rand.Read, named so a test can make it fail. A CSPRNG that will not produce
// bytes must abort minting rather than fall back to anything, and the only way to hold that line is
// to exercise it.
var randRead = rand.Read

// newPreviewCode mints an id and a secret, and returns the code that joins them.
//
// The separator is a dot so the two halves split without knowing either length, which means
// changing a length later does not invalidate codes already in the wild.
func newPreviewCode() (id string, secret string, code string, err error) {
	idBytes := make([]byte, previewIDBytes)
	if _, err = randRead(idBytes); err != nil {
		return "", "", "", err
	}
	secretBytes := make([]byte, previewSecretBytes)
	if _, err = randRead(secretBytes); err != nil {
		return "", "", "", err
	}
	enc := base64.RawURLEncoding
	id = previewIDPrefix + enc.EncodeToString(idBytes)
	secret = enc.EncodeToString(secretBytes)
	return id, secret, id + "." + secret, nil
}

// splitPreviewCode takes a code apart without deciding whether it is valid.
func splitPreviewCode(code string) (id string, secret string, ok bool) {
	id, secret, found := strings.Cut(strings.TrimSpace(code), ".")
	if !found || !strings.HasPrefix(id, previewIDPrefix) || secret == "" {
		return "", "", false
	}
	return id, secret, true
}

func hashPreviewSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// storePreviewLink writes the row a code resolves against.
func storePreviewLink(
	ctx context.Context,
	c Config,
	link PreviewLink,
	secretHash string,
	epoch int,
	expires time.Time,
) error {
	pool, err := previewPool(c)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err = pool.Exec(ctx,
		`INSERT INTO _supatype.preview_links
		   (id, secret_hash, scope, model, record_id, epoch, created_by, expires_at)
		 VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, NULLIF($7, '')::uuid, $8)`,
		link.ID, secretHash, link.Scope, link.Model, link.RecordID, epoch, link.CreatedBy, expires)
	return err
}

// resolvePreviewLink turns a code into the claim it stands for, or refuses.
//
// Liveness is decided in the query rather than in Go: expiry, revocation and the epoch are all
// conditions on the row, so there is one definition of "live" and no window between reading a row
// and judging it.
func resolvePreviewLink(ctx context.Context, c Config, code string) (PreviewLink, int, time.Time, error) {
	id, secret, ok := splitPreviewCode(code)
	if !ok {
		return PreviewLink{}, 0, time.Time{}, ErrPreviewLinkNotFound
	}

	pool, err := previewPool(c)
	if err != nil {
		return PreviewLink{}, 0, time.Time{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var (
		storedHash string
		link       PreviewLink
		model      *string
		record     *string
		epoch      int
		expires    time.Time
	)
	err = pool.QueryRow(ctx,
		`SELECT l.secret_hash, l.scope, l.model, l.record_id, l.epoch, l.expires_at
		   FROM _supatype.preview_links l
		   JOIN _supatype.publishing_settings s ON s.singleton
		  WHERE l.id = $1
		    AND l.revoked_at IS NULL
		    AND l.expires_at > now()
		    AND l.epoch = s.preview_epoch`,
		id).Scan(&storedHash, &link.Scope, &model, &record, &epoch, &expires)
	if err != nil {
		return PreviewLink{}, 0, time.Time{}, ErrPreviewLinkNotFound
	}

	// Constant time: a comparison that returns on the first wrong byte leaks, through timing, how
	// much of a guessed secret was right, which is enough to find the rest one byte at a time.
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hashPreviewSecret(secret))) != 1 {
		return PreviewLink{}, 0, time.Time{}, ErrPreviewLinkNotFound
	}

	link.ID = id
	if model != nil {
		link.Model = *model
	}
	if record != nil {
		link.RecordID = *record
	}
	return link, epoch, expires, nil
}

// listPreviewLinks returns the live links for one record, newest first.
func listPreviewLinks(ctx context.Context, c Config, model, recordID string) ([]PreviewLink, error) {
	pool, err := previewPool(c)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx,
		`SELECT l.id, l.scope, COALESCE(l.model, ''), COALESCE(l.record_id, ''),
		        COALESCE(l.created_by::text, ''), l.created_at, l.expires_at
		   FROM _supatype.preview_links l
		   JOIN _supatype.publishing_settings s ON s.singleton
		  WHERE l.model = $1 AND l.record_id = $2
		    AND l.revoked_at IS NULL
		    AND l.expires_at > now()
		    AND l.epoch = s.preview_epoch
		  ORDER BY l.created_at DESC`,
		model, recordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := []PreviewLink{}
	for rows.Next() {
		var (
			link      PreviewLink
			createdAt time.Time
			expiresAt time.Time
		)
		if err := rows.Scan(&link.ID, &link.Scope, &link.Model, &link.RecordID,
			&link.CreatedBy, &createdAt, &expiresAt); err != nil {
			return nil, err
		}
		link.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		link.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
		links = append(links, link)
	}
	return links, rows.Err()
}

// revokePreviewLink withdraws one link.
//
// Idempotent: revoking an already revoked link is not an error, because the caller's intent is
// satisfied either way and a second click should not report a failure.
func revokePreviewLink(ctx context.Context, c Config, id string) error {
	pool, err := previewPool(c)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err = pool.Exec(ctx,
		`UPDATE _supatype.preview_links
		    SET revoked_at = now()
		  WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}
