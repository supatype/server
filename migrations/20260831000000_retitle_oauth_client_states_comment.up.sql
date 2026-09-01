-- This table's comment named the project this service was forked from, and a
-- table comment is written into the operator's own database: it shows in \d+,
-- in Studio's database view, and in anything reading pg_description. Editing
-- the migration that set it would not reach a database that has already run
-- it, so correct it here.
comment on table {{ index .Options "Namespace" }}.oauth_client_states is
  'Stores OAuth states for third-party provider authentication flows where Supatype acts as the OAuth client.';
