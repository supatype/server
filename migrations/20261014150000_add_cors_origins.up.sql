-- The browser origins saved from Studio's API settings.
--
-- Outside dev mode the gateway allows the project's site URL plus the origins its configuration
-- names (SUPATYPE_CORS_ALLOW_ORIGINS, filled from `api.corsOrigins`). A saved list replaces that
-- default until it is deleted, which goes back to it. One row at most: the list is the setting.
-- Kept in the database rather than the process, so a restart or another replica serves the same
-- list.

/* auth_migration: 20261014150000 */
create table if not exists {{ index .Options "Namespace" }}.cors_origins (
    singleton boolean not null primary key default true check (singleton),
    origins jsonb not null check (jsonb_typeof(origins) = 'array'),
    updated_at timestamptz not null default now()
);

/* auth_migration: 20261014150000 */
revoke all on table {{ index .Options "Namespace" }}.cors_origins from public;

/* auth_migration: 20261014150000 */
comment on table {{ index .Options "Namespace" }}.cors_origins is
  'Browser origins saved through the gateway''s CORS API. A row replaces the configured default.';
