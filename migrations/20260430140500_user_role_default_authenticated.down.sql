alter table {{ index .Options "Namespace" }}.users
  drop constraint if exists users_role_not_empty;

alter table {{ index .Options "Namespace" }}.users
  alter column role drop not null;

alter table {{ index .Options "Namespace" }}.users
  alter column role drop default;
