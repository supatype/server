package models

import (
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/pkg/errors"

	"github.com/supatype/server/internal/auth/storage"
)

// SAMLConsumedAssertion records a SAML assertion the ACS has accepted. It is
// kept until the assertion expires, so the same assertion cannot be accepted
// twice.
type SAMLConsumedAssertion struct {
	SSOProviderID uuid.UUID `db:"sso_provider_id"`
	AssertionID   string    `db:"assertion_id"`
	ExpiresAt     time.Time `db:"expires_at"`
	CreatedAt     time.Time `db:"created_at"`
}

func (SAMLConsumedAssertion) TableName() string {
	return "saml_consumed_assertions"
}

// consumeSAMLAssertionQuery records an assertion ID. A row whose assertion
// has expired is replaced rather than refused: the assertion it recorded can
// no longer be accepted, so its ID is free.
var consumeSAMLAssertionQuery = fmt.Sprintf(`insert into %[1]q (sso_provider_id, assertion_id, expires_at)
values (?, ?, ?)
on conflict (sso_provider_id, assertion_id) do update
set expires_at = excluded.expires_at, created_at = now()
where %[1]q.expires_at < now()`, SAMLConsumedAssertion{}.TableName())

// ConsumeSAMLAssertion records that the provider's assertion assertionID has
// been accepted, until expiresAt. It returns SAMLAssertionReplayedError when
// the assertion is already recorded and has not expired. Concurrent calls for
// the same assertion are serialised by the primary key, so only one succeeds.
func ConsumeSAMLAssertion(tx *storage.Connection, ssoProviderID uuid.UUID, assertionID string, expiresAt time.Time) error {
	if assertionID == "" {
		return errors.New("SAML assertion has no ID")
	}

	count, err := tx.RawQuery(consumeSAMLAssertionQuery, ssoProviderID, assertionID, expiresAt).ExecWithCount()
	if err != nil {
		return errors.Wrap(err, "error recording consumed SAML assertion")
	}
	if count == 0 {
		return SAMLAssertionReplayedError{}
	}
	return nil
}
