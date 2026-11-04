-- The actor marker for after hooks fed from the change log.
--
-- The change log records rows, not users. PostgREST runs this as its db-pre-request function, inside
-- the request's own transaction and before any write, so the first `supatype.actor` message of an
-- API transaction says who made it. The hook listener trusts only that first message, and honours a
-- seed's `supatype.hooks` skip only in a transaction with no actor at all, so an API request cannot
-- switch its own hooks off.
--
-- Reads are left alone. A GET runs the function too, and a message per read would be WAL written for
-- nothing, since a read cannot fire a hook.
--
-- `depth` carries X-Supatype-Hook-Depth, so a hook whose own write fires another after hook is
-- counted the same way it is on the request path, and a loop is refused at the same limit.
--
-- `requestPath` carries X-Supatype-After-Path, a signed mark the server sets when it fired this
-- write's after hook itself, while the listener was not yet active. The listener verifies it and
-- skips that table, so the write is not delivered twice.
--
-- Wired by setting PostgREST's db-pre-request (PGRST_DB_PRE_REQUEST) to this function.

/* auth_migration: 20261103180000 */
create or replace function {{ index .Options "Namespace" }}.emit_hook_actor()
returns void
language plpgsql
set search_path = ''
as $$
declare
  claims jsonb := coalesce(nullif(current_setting('request.jwt.claims', true), ''), '{}')::jsonb;
  headers jsonb := coalesce(nullif(current_setting('request.headers', true), ''), '{}')::jsonb;
  depth text := headers ->> 'x-supatype-hook-depth';
begin
  if coalesce(current_setting('request.method', true), '') in ('GET', 'HEAD', 'OPTIONS') then
    return;
  end if;
  perform pg_catalog.pg_logical_emit_message(true, 'supatype.actor', pg_catalog.jsonb_build_object(
    'sub', claims ->> 'sub',
    'role', coalesce(claims ->> 'role', current_user),
    'email', claims ->> 'email',
    'requestId', headers ->> 'x-request-id',
    'depth', case when depth ~ '^[0-9]{1,3}$' then depth::int else 0 end,
    'requestPath', headers ->> 'x-supatype-after-path'
  )::text);
end
$$;

/* auth_migration: 20261103180000 */
comment on function {{ index .Options "Namespace" }}.emit_hook_actor() is
  'PostgREST db-pre-request: marks an API write''s transaction with its caller for change-log after hooks.';
