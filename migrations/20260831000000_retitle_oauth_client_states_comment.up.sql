-- The comment on this table has said "where Supabase acts as the OAuth client"
-- since the table was added, and it is written into the operator's own database:
-- it shows in \d+, in Studio's database view, and in anything that reads
-- pg_description. Editing the original migration would not reach a database that
-- has already run it, so correct it here.
comment on table {{ index .Options "Namespace" }}.oauth_client_states is
  'Stores OAuth states for third-party provider authentication flows where Supatype acts as the OAuth client.';
