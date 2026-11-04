#!/usr/bin/env bash
# Run the model-hook tests against a real Postgres and a real PostgREST.
#
# Every other test in internal/modelhooks uses a fake upstream, which cannot show the one join the
# middleware exists for: that the request it hands on (body substituted, Content-Length rewritten) is
# a request PostgREST actually accepts, and that previous() reads the rows really stored. A fake proxy
# accepts anything, so both failures look like passes.
#
# Usage: hack/live-hooks-stack.sh [--keep]
#   --keep  leave the containers running (re-run the Go test yourself against them)
set -euo pipefail

PG=st-hook-pg
PRST=st-hook-prst
NET=st-hook-net
PG_PORT=55433
PRST_PORT=55434
KEEP=${1:-}

cleanup() {
  if [ "$KEEP" != "--keep" ]; then
    docker rm -f "$PG" "$PRST" >/dev/null 2>&1 || true
    docker network rm "$NET" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

docker rm -f "$PG" "$PRST" >/dev/null 2>&1 || true
docker network create "$NET" >/dev/null 2>&1 || true

# Supatype's own Postgres image, because previous() has to be proven against real field masking:
# it ships supatype_mask preloaded. Its superuser is supatype_admin, reachable over local TCP.
docker run -d --name "$PG" --network "$NET" \
  -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=app -p "$PG_PORT:5432" supatype/postgres:17-latest >/dev/null

admin_psql() {
  docker exec -i "$PG" psql -h 127.0.0.1 -U supatype_admin -d app -v ON_ERROR_STOP=1 "$@"
}

for _ in $(seq 1 90); do
  admin_psql -tAc 'select 1' >/dev/null 2>&1 && break
  sleep 1
done
admin_psql -tAc 'select 1' >/dev/null

# The fixture the tests expect: one table, and an anon role that can read and write it. Deliberately
# permissive for anon, since what is under test is the hook path, and RLS here would only make a
# failure ambiguous.
#
# previous() needs two more things, shaped like what the schema engine generates: the
# can_update_posts / can_delete_posts checks (owner = caller) it filters the caller's read by, and a
# masked column. `secret` carries a supatype_mask READ label that no caller passes, so it can only
# come back through the service role's read.
admin_psql >/dev/null <<'SQL'
create extension if not exists supatype_mask;
create table if not exists posts (
  id serial primary key,
  title text not null,
  body text not null default '',
  owner text,
  secret text
);
alter role authenticator with login password 'pw';
grant anon, authenticated, service_role to authenticator;
grant usage on schema public to anon, authenticated, service_role;
grant all on posts to anon, authenticated, service_role;
grant usage, select on all sequences in schema public to anon, authenticated, service_role;

create or replace function can_update_posts(posts posts) returns boolean
  language sql stable as $$
    select posts.owner = (nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'sub')
  $$;
create or replace function can_delete_posts(posts posts) returns boolean
  language sql stable as $$ select can_update_posts(posts) $$;
create or replace function can_read_posts__secret(posts posts) returns boolean
  language sql stable as $$ select false $$;
grant execute on function can_update_posts(posts), can_delete_posts(posts), can_read_posts__secret(posts)
  to anon, authenticated, service_role;
security label for supatype on column public.posts.secret
  is 'MASK READ public.can_read_posts__secret';
SQL

docker run -d --name "$PRST" --network "$NET" -p "$PRST_PORT:3000" \
  -e PGRST_DB_URI="postgres://authenticator:pw@$PG:5432/app" \
  -e PGRST_DB_ANON_ROLE=anon \
  -e PGRST_DB_SCHEMAS=public \
  -e PGRST_JWT_SECRET="0123456789abcdef0123456789abcdef" \
  postgrest/postgrest:v12.2.3 >/dev/null

for _ in $(seq 1 60); do
  curl -sf "http://127.0.0.1:$PRST_PORT/posts" >/dev/null 2>&1 && break
  sleep 1
done
curl -sf "http://127.0.0.1:$PRST_PORT/posts" >/dev/null

echo "PostgREST is serving on http://127.0.0.1:$PRST_PORT"
SUPATYPE_LIVE_POSTGREST="http://127.0.0.1:$PRST_PORT" \
  go test ./internal/modelhooks -run Live -v -timeout 180s
