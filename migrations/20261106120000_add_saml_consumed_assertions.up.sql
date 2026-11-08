-- The SAML assertions the ACS has already accepted.
--
-- A signed assertion stays valid until its Conditions and SubjectConfirmationData expire, usually a
-- few minutes. An SP-initiated login is bound to its RelayState, which is deleted when used, but an
-- IdP-initiated one has nothing to bind to, so a captured response could be posted again until it
-- expired. Each accepted assertion's ID is recorded here per provider until it expires, and an
-- assertion whose ID is already recorded is refused.
--
-- Rows are deleted by the cleanup task once expires_at has passed.

/* auth_migration: 20261106120000 */
create table if not exists {{ index .Options "Namespace" }}.saml_consumed_assertions (
	sso_provider_id uuid not null,
	assertion_id text not null,
	expires_at timestamptz not null,
	created_at timestamptz not null default now(),
	primary key (sso_provider_id, assertion_id),
	foreign key (sso_provider_id) references {{ index .Options "Namespace" }}.sso_providers (id) on delete cascade,
	constraint "assertion_id not empty" check (char_length(assertion_id) > 0)
);

/* auth_migration: 20261106120000 */
create index if not exists saml_consumed_assertions_expires_at_idx
	on {{ index .Options "Namespace" }}.saml_consumed_assertions (expires_at);

/* auth_migration: 20261106120000 */
alter table {{ index .Options "Namespace" }}.saml_consumed_assertions enable row level security;

/* auth_migration: 20261106120000 */
revoke all on table {{ index .Options "Namespace" }}.saml_consumed_assertions from public;

/* auth_migration: 20261106120000 */
comment on table {{ index .Options "Namespace" }}.saml_consumed_assertions is
  'Auth: IDs of SAML assertions already accepted, kept until they expire so they cannot be replayed.';
