-- Backfill and enforce non-empty runtime role for auth users.
update {{ index .Options "Namespace" }}.users
set role = 'authenticated'
where role is null or btrim(role) = '';

alter table {{ index .Options "Namespace" }}.users
  alter column role set default 'authenticated';

alter table {{ index .Options "Namespace" }}.users
  alter column role set not null;

alter table {{ index .Options "Namespace" }}.users
  drop constraint if exists users_role_not_empty;

alter table {{ index .Options "Namespace" }}.users
  add constraint users_role_not_empty check (btrim(role) <> '');
