-- Email templates saved through the admin API (Studio's Email templates view).
--
-- An edit used to live only in the running server's memory, so a restart, or
-- any other replica, served the configured or built-in template again and the
-- operator's change was gone without a word. A saved row wins over the
-- SUPATYPE_MAILER_TEMPLATES_* and SUPATYPE_MAILER_SUBJECTS_* configuration;
-- deleting it goes back to that configuration, or to the built-in default.

/* auth_migration: 20261013180000 */
create table if not exists {{ index .Options "Namespace" }}.mail_templates (
    type text not null primary key
        check (type in ('invite', 'confirmation', 'recovery', 'email_change', 'magic_link')),
    subject text not null,
    body text not null,
    updated_at timestamptz not null default now()
);

/* auth_migration: 20261013180000 */
revoke all on table {{ index .Options "Namespace" }}.mail_templates from public;

/* auth_migration: 20261013180000 */
comment on table {{ index .Options "Namespace" }}.mail_templates is
  'Email templates saved through the auth admin API. A row overrides the configured template of its type.';
