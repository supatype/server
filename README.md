# supatype-server

Unified API gateway for the [Supatype](https://github.com/supatype/supatype) platform: local dev, self-host, and production.

**Product:** [github.com/supatype](https://github.com/supatype) · **Docs:** [supatype.github.io/supatype](https://supatype.github.io/supatype/)

Most users run this via the CLI (`supatype dev` or `supatype self-host compose up`); you only need this repo if you are hacking on the server itself.

Repository: [github.com/supatype/server](https://github.com/supatype/server)

## Overview

`supatype-server` is the unified Supatype runtime process. It includes the Auth API and also serves as the API edge/gateway for other Supatype services.

It can:

- Run Auth (`/auth/v1`) with migrations and background workers.
- Proxy PostgREST (`/rest/v1`) and GraphQL (`/graphql/v1`).
- Serve Storage via built-in local filesystem mode or proxy mode (`/storage/v1`).
- Proxy Edge Functions to a functions worker or in-process Deno (`/functions/v1`).
- Expose Functions admin endpoints (`/admin/v1/functions`). With no functions directory set, the list is
  empty and the env routes answer 409.
- Serve Realtime WebSockets when enabled (`/realtime/v1`).
- Expose admin and Studio support endpoints (`/admin/v1`, `/studio-config`, `/sql`).
- Serve application content from `/` (none/static/proxy modes).

## Route Map

- `/auth/v1/*` -> Auth API
- `/rest/v1/*` -> PostgREST
- `/graphql/v1/*` -> GraphQL
- `/storage/v1/*` -> local storage handler or storage proxy
- `/functions/v1/*` -> Deno functions proxy, including a project function named `admin`
- `/realtime/v1/*` -> realtime WebSocket handler
- `/admin/v1/functions/*` -> functions admin API (service-role key required)
- `/admin/v1/*` -> admin API
- `/studio-config` -> Studio config payload
- `/sql` -> SQL runner endpoint
- `/supatype-env.json`, `/supatype-env.js` -> the public API address and anon key (see `SUPATYPE_PUBLIC_ENV`)
- `/*` -> app runtime (`none`, `static`, or `proxy`)

The functions admin API's old address, `/functions/v1/admin/*`, still answers for a transition: a
request there that carries the service-role key and names one of the API's own routes (`/list`,
`/env`, `/{name}/logs`, ...) goes to the admin API, and every other request there is a call to the
project's function named `admin`. Move callers to `/admin/v1/functions`.

## Runtime Modes

Set `SUPATYPE_MODE` to control gateway behavior:

- `dev` (default): permissive CORS and optional dev-proxy behavior.
- `standalone`: automatic ACME/TLS support for direct hosting.
- `managed`: tenant HMAC verification middleware (hosted multi-tenant gateway).

## Quick Start

> **Using Supatype as a product?** Run `npx supatype@latest init` then `supatype dev` (see the [docs](https://supatype.github.io/supatype/#local-dev)). The steps below are for **server contributors** cloning this repo.

### Prerequisites

- Go 1.26.11+
- Docker (for local Postgres with the provided compose file)

### 1) Configure environment

Create `.env` from `example.env` and set required values (especially DB and JWT values).

### 2) Start local Postgres

```bash
docker-compose -f docker-compose-dev.yml up -d postgres
```

### 3) Build

```bash
make build
```

Or with plain Go:

```bash
go build -o supatype-server .
```

### 4) Run

```bash
./supatype-server
```

Health check:

- [http://localhost:9999/health](http://localhost:9999/health)

## CLI Commands

Default `supatype-server` behavior runs migrations and then starts serving.

Available commands:

- `supatype-server serve`
- `supatype-server migrate`
- `supatype-server version`
- `supatype-server admin createuser <email> <password> [role]`
- `supatype-server admin deleteuser <email-or-uuid>`

## Configuration

`supatype-server` reads:

- Auth/API config from `.env` and `SUPATYPE_*` environment variables.
- Server/gateway config from `SUPATYPE_*` variables.
- Optional route manifest (default `.supatype/manifest.json`).

Service switches. The route manifest switches upstream services on and off with one shape,
`"services": { "<name>": { "enabled": true | false } }`, for `realtime`, `storage` and `functions`. It is
read per request, so a switch takes effect without a restart. A service the manifest says nothing about
keeps the server's start value: storage is on unless `SUPATYPE_STORAGE_DISABLED`, functions are on when
`SUPATYPE_FUNCTIONS_WORKER_URL` is set, and realtime is off. A service switched off is answered by the
gateway itself (realtime and functions 404, storage 503 `storage_unavailable`), and `/health` reports it
as `{"enabled": false, "skipped": true, "status": "disabled"}`, not counted toward readiness. The legacy
`realtime_enabled` and `functions_enabled` keys are still read as the same switches; a `services` entry
wins over its legacy key.

Common `SUPATYPE_*` variables:

- `SUPATYPE_MODE`
- `SUPATYPE_APP_MODE`
- `SUPATYPE_APP_STATIC_DIR`
- `SUPATYPE_APP_UPSTREAM`
- `SUPATYPE_MANIFEST_PATH`
- `SUPATYPE_POSTGREST_URL`
- `SUPATYPE_GRAPHQL_URL` (unset: PostgREST's URL, where pg_graphql is an RPC)
- `SUPATYPE_GRAPHQL_DISABLED` (answer `/graphql/v1` with 501 `graphql_unavailable`; native `supatype dev` sets it. `/health` and `/health/ready` then report `components.graphql.status` as `disabled`, do not probe it, and do not count it toward readiness)
- `SUPATYPE_STORAGE_URL`
- `SUPATYPE_STORAGE_DISABLED` (answer every `/storage/v1` request with 503 `storage_unavailable`, whatever `STORAGE_PROVIDER` or the storage URLs say; native `supatype dev` sets it when its storage service could not start. `/health` and `/health/ready` then report `components.storage.status` as `disabled`, do not probe it, and do not count it toward readiness. It is only the starting value: the route manifest's `services.storage.enabled` overrides it while the server runs, see Service switches below)
- `SUPATYPE_FUNCTIONS_WORKER_URL`
- `SUPATYPE_DENO_PATH`
- `SUPATYPE_DENO_FUNCTIONS_DIR`
- `SUPATYPE_DENO_PORT`
- `SUPATYPE_TENANT_HMAC_SECRET`
- `SUPATYPE_TLS_DOMAIN`
- `SUPATYPE_PUBLIC_ENV` (default `true`). `GET /supatype-env.json` answers `{"url":...,"anonKey":...}` for `supatype build`, and `GET /supatype-env.js` sets `window.__SUPATYPE_ENV__` for a static app's script tag, whatever `SUPATYPE_APP_MODE` is and ahead of a file of the same name in the app. `url` is `SUPATYPE_URL`, else `SUPATYPE_API_EXTERNAL_URL` without a trailing `/auth/v1`; the key is `SUPATYPE_ANON_KEY`. The service role key is never served. A misconfigured deployment names every setting to fix, one per line, key before URL: no anon key, an anon key whose JWT role is `service_role` (refused, never echoed), or neither URL set. `/supatype-env.json` answers that text as a 503 with `no-store`. `/supatype-env.js` answers 200 with `no-store` and `window.__SUPATYPE_ENV__ = {"error":"..."};` carrying the same text and no key, because a browser neither runs a script answered with an error status nor tells the page why, and the client throws that reason on its first call. Set `false` for an API-only project: both paths then answer 404.

SAML single sign-on (the service provider side of `/auth/v1/sso`):

- `SUPATYPE_SAML_ENABLED`, `SUPATYPE_SAML_PRIVATE_KEY` (standard base64, PKCS#1, RSA 2048 or more,
  exponent 65537), `SUPATYPE_SAML_ALLOW_ENCRYPTED_ASSERTIONS`,
  `SUPATYPE_SAML_RELAY_STATE_VALIDITY_PERIOD`, `SUPATYPE_SAML_EXTERNAL_URL`,
  `SUPATYPE_SAML_RATE_LIMIT_ASSERTION`.
- The SP certificate in the metadata (`/auth/v1/sso/saml/metadata`) is derived from the private key
  alone. Changing the host or the encrypted-assertions setting does not change it, so identity
  providers keep trusting it; only a new key does.
- Key rotation: set `SUPATYPE_SAML_NEXT_PRIVATE_KEY` (same format) to the new key. While it is set,
  the metadata lists both certificates, AuthnRequests are still signed with the current key, and an
  assertion encrypted to either key is accepted, by the HTTP-POST and artifact bindings alike. Once
  every identity provider has the new certificate, move the new key to `SUPATYPE_SAML_PRIVATE_KEY`
  and clear `SUPATYPE_SAML_NEXT_PRIVATE_KEY`.
- An identity provider registered by `metadata_url` must use HTTPS, except when `SUPATYPE_MODE=dev`,
  where `http` is accepted too, for a local test identity provider.

Local storage mode:

- `STORAGE_PROVIDER=local`
- `STORAGE_PATH=<directory>`

## Migrations

Migrations are run automatically when starting `supatype-server` directly.

Run manually:

```bash
./supatype-server migrate
```

## Development

```bash
make build
make test
make vet
make static
make format
```

Dev container helpers:

```bash
make dev
make down
```

## License

See `LICENSE`.
