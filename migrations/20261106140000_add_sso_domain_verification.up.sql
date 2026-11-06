-- Verification of the email domains an SSO provider claims.
--
-- An administrator proves a domain by publishing a DNS TXT record
-- _supatype-challenge.<domain> holding verification_token. Once the record is
-- seen the domain is verified and stays verified: there is no periodic
-- re-check. A SAML login on a verified domain links to an existing account with
-- the same email; on an unverified one it is refused when such an account
-- exists, so no second account is created for the same person.

/* auth_migration: 20261106140000 */
alter table {{ index .Options "Namespace" }}.sso_domains
	add column if not exists verification_token text null,
	add column if not exists verified_at timestamptz null;

/* auth_migration: 20261106140000 */
comment on column {{ index .Options "Namespace" }}.sso_domains.verification_token is
  'Auth: Value the DNS TXT record _supatype-challenge.<domain> must hold to verify the domain.';

/* auth_migration: 20261106140000 */
comment on column {{ index .Options "Namespace" }}.sso_domains.verified_at is
  'Auth: When the TXT record was first seen; null while the domain is unverified.';
