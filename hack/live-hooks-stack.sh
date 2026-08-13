#!/usr/bin/env bash
# Run the model-hook tests against a real Postgres and a real PostgREST.
#
# Every other test in internal/modelhooks uses a fake upstream, which cannot show the one join the
# middleware exists for: that the request it hands on — body substituted, Content-Length rewritten — is
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

docker run -d --name "$PG" --network "$NET" \
  -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=app -p "$PG_PORT:5432" postgres:17-alpine >/dev/null

for _ in $(seq 1 60); do
  docker exec "$PG" pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$PG" pg_isready -U postgres >/dev/null

# The fixture the tests expect: one table, and an anon role that can read and write it. Deliberately
# permissive — what is under test is the hook path, not the access rules, and RLS here would only make
# a failure ambiguous.
docker exec -i "$PG" psql -U postgres -d app -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
create table if not exists posts (
  id serial primary key,
  title text not null,
  body text not null default ''
);
do $$ begin
  if not exists (select 1 from pg_roles where rolname = 'anon') then
    create role anon nologin;
  end if;
  if not exists (select 1 from pg_roles where rolname = 'authenticator') then
    create role authenticator noinherit login password 'pw';
  end if;
end $$;
grant anon to authenticator;
grant usage on schema public to anon;
grant all on posts to anon;
grant usage, select on all sequences in schema public to anon;
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
