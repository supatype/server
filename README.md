# supatype-server

Runtime server for Supatype local development, self-hosted deployments, and managed environments.

Repository: [github.com/supatype/server](https://github.com/supatype/server)

## Overview

`supatype-server` is the unified Supatype runtime process. It includes the Auth API and also serves as the API edge/gateway for other Supatype services.

It can:

- Run Auth (`/auth/v1`) with migrations and background workers.
- Proxy PostgREST (`/rest/v1`) and GraphQL (`/graphql/v1`).
- Serve Storage via built-in local filesystem mode or proxy mode (`/storage/v1`).
- Run and proxy Edge Functions with Deno (`/functions/v1`).
- Expose Functions admin endpoints (`/functions/v1/admin`).
- Serve Realtime WebSockets when enabled (`/realtime/v1`).
- Expose admin and Studio support endpoints (`/admin/v1`, `/studio-config`, `/sql`).
- Serve application content from `/` (none/static/proxy modes).

## Route Map

- `/auth/v1/*` -> Auth API
- `/rest/v1/*` -> PostgREST
- `/graphql/v1/*` -> GraphQL
- `/storage/v1/*` -> local storage handler or storage proxy
- `/functions/v1/admin/*` -> functions admin API
- `/functions/v1/*` -> Deno functions proxy
- `/realtime/v1/*` -> realtime WebSocket handler
- `/admin/v1/*` -> admin API
- `/studio-config` -> Studio config payload
- `/sql` -> SQL runner endpoint
- `/*` -> app runtime (`none`, `static`, or `proxy`)

## Runtime Modes

Set `SUPATYPE_MODE` to control gateway behavior:

- `dev` (default): permissive CORS and optional dev-proxy behavior.
- `standalone`: automatic ACME/TLS support for direct hosting.
- `managed`: tenant HMAC verification middleware for managed multi-tenant setups.

## Quick Start

### Prerequisites

- Go 1.25+
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

- Auth/API config from `.env` and `GOTRUE_*` environment variables.
- Server/gateway config from `SUPATYPE_*` variables.
- Optional route manifest (default `.supatype/manifest.json`).

Common `SUPATYPE_*` variables:

- `SUPATYPE_MODE`
- `SUPATYPE_APP_MODE`
- `SUPATYPE_APP_STATIC_DIR`
- `SUPATYPE_APP_UPSTREAM`
- `SUPATYPE_MANIFEST_PATH`
- `SUPATYPE_POSTGREST_URL`
- `SUPATYPE_GRAPHQL_URL`
- `SUPATYPE_STORAGE_URL`
- `SUPATYPE_DENO_PATH`
- `SUPATYPE_DENO_FUNCTIONS_DIR`
- `SUPATYPE_DENO_PORT`
- `SUPATYPE_TENANT_HMAC_SECRET`
- `SUPATYPE_TLS_DOMAIN`

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
