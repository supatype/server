# Changelog

## [1.5.0](https://github.com/supatype/server/compare/v1.4.0...v1.5.0) (2026-08-27)


### Features

* **modelhooks:** run per-field validators, and name the field they refuse ([c33c453](https://github.com/supatype/server/commit/c33c45310b084e04c2ed1cfaaba504a874ac127e))

## [1.4.0](https://github.com/supatype/server/compare/v1.3.0...v1.4.0) (2026-08-15)


### Features

* **fields:** per-column verdicts for Studio and a masked-field header for REST ([5cb2e24](https://github.com/supatype/server/commit/5cb2e24c1002b053d1bbfd418ac15572c26e652c))
* **server:** call a hook and classify its answer ([4fd7c72](https://github.com/supatype/server/commit/4fd7c7280d985640a6af27dc578ff20c30803247))
* **server:** carry the model hook map through tenant config ([037fa68](https://github.com/supatype/server/commit/037fa68e672642ec5c0f61c73598fd50532ab940))
* **server:** cloud gateway wrap — per-request activity, auth MAU, non-prod robots ([0b98fb5](https://github.com/supatype/server/commit/0b98fb5de0bd3f15ab27634fb1abf1c5aaf780f6))
* **server:** make a hook chain count itself, and stop saying service_role skips hooks ([656be4f](https://github.com/supatype/server/commit/656be4fe24f95fd22b2e97da1e9b435acc17564d))
* **server:** model hooks on the write path, Studio as the caller, API keys on the data plane ([c7cd013](https://github.com/supatype/server/commit/c7cd0138e95c0d0ca0d1f593367f527cf240e131))
* **server:** mount model hooks on the REST write path ([995fbd7](https://github.com/supatype/server/commit/995fbd709816f56b6987be24211375af78789a15))
* **server:** previous() — the rows a write is about to change ([170a7af](https://github.com/supatype/server/commit/170a7afc7cf9027e1b0e01544e0978c9a2e9978f))
* **server:** read the hook map, and decide which hooks a REST write implies ([5bc843c](https://github.com/supatype/server/commit/5bc843c84f4e78c2aac0aa04e1c2ce8742a8a550))
* **server:** refuse the hooks namespace on the public functions path ([c243c1e](https://github.com/supatype/server/commit/c243c1ea6f61e3a26f437034ba4f86d4540c5798))
* **server:** require a project API key on the data plane; restrict dev bypass ([e7d2e18](https://github.com/supatype/server/commit/e7d2e1888780d9fa2b22aef20a868eb5db88654d))
* **server:** run a table's hooks around its writes ([2e3d602](https://github.com/supatype/server/commit/2e3d602261098178c62dee78f5657d2c7b268756))
* **studio:** act as the caller, not the service role ([cd0d4a2](https://github.com/supatype/server/commit/cd0d4a2296954fe0eb8bf669d84e722ae29f6a19))
* **studio:** enforce the membership role, not just admission ([78eea33](https://github.com/supatype/server/commit/78eea3380e0ffe39a8f78ac3006083897d64d7bc))
* **studio:** resolve Studio capability from membership, not JWT claims ([03b575f](https://github.com/supatype/server/commit/03b575f40de96603d405a3be5a70ef0a14e1e9db))
* **studio:** resolve Studio capability from the database ([3b75cb0](https://github.com/supatype/server/commit/3b75cb0a577d987c0f98698e754c7fed51c371ca))
* **studio:** schema and session bootstrap endpoints ([b278108](https://github.com/supatype/server/commit/b27810801e0f91b36b50475d50a0cd5df4ac254f))
* **studio:** Studio membership assignment API ([efdc09e](https://github.com/supatype/server/commit/efdc09e91759c724eed2dc9c437be6f0b3418727))


### Bug Fixes

* **auth:** do not require a role named `postgres` to run auth migrations ([2bafa4e](https://github.com/supatype/server/commit/2bafa4e62a2f05d8b5a7d464fbd7f664f1bc0dae))
* **build:** the builder image needs the Go version go.mod now requires ([f724465](https://github.com/supatype/server/commit/f7244652e5551245d784e7d30bf380f91af80552))
* **build:** the builder image needs the Go version go.mod now requires ([a3ef385](https://github.com/supatype/server/commit/a3ef3851429a2669b63754f8f442e3537ad93086))
* **deps:** clear the seven advisories govulncheck fails the build on ([b055844](https://github.com/supatype/server/commit/b055844b207d2401848c44df0bd8ca4809c31b69))
* **restcache:** never share a cache entry across callers by config flag ([6e2907f](https://github.com/supatype/server/commit/6e2907f3dbbd98b96f36a28756187899532f0ecf))
* **server:** encode the previous() response instead of assembling it by hand ([bbcc89b](https://github.com/supatype/server/commit/bbcc89bb5ddfed2664772e9c38b9df66efb2c8d8))
* **server:** resolve a hook's worker from the caller's tenant, not the platform's file ([516ae54](https://github.com/supatype/server/commit/516ae544a2670cade876f9de03ef933eaeb33f95))
* **server:** wait for Postgres instead of refusing to start ([f08d09e](https://github.com/supatype/server/commit/f08d09eda448f7cdb07751d3ab41cef7bc7f3904))


### Performance Improvements

* **dbpool:** raise the shared pool to 10 connections ([9b5d731](https://github.com/supatype/server/commit/9b5d7318079dff0a51d4e89f0b04741b0363906d))

## [1.3.0](https://github.com/supatype/server/compare/v1.2.0...v1.3.0) (2026-07-20)


### Features

* **server:** proxy /realtime/v1 to external WAL realtime ([fce6405](https://github.com/supatype/server/commit/fce640537e01b3e9437c2b0099213e745e52b3b6))
* **server:** proxy /realtime/v1 to external WAL realtime service ([5923d84](https://github.com/supatype/server/commit/5923d841587f3bade07c16e3b5334392bc15913d))


### Bug Fixes

* **deps:** bump Go to 1.25.12 for stdlib vuln fixes ([b3b5660](https://github.com/supatype/server/commit/b3b5660255f88b4097a6bd23cc937406ff4bc74a))
* **proxy:** rewrite WebSocket upgrade path after StripPrefix ([07472e3](https://github.com/supatype/server/commit/07472e3efc31aace7af78616c0e9e61c174dbac9))
* **proxy:** rewrite WebSocket upgrade path after StripPrefix ([fd9e5ca](https://github.com/supatype/server/commit/fd9e5ca68069374b8adf384f964ff88758fd1a50))

## [1.2.0](https://github.com/supatype/server/compare/v1.1.1...v1.2.0) (2026-06-28)


### Features

* **server:** REST GET cache with Valkey, admin API, and table cache config ([3dd0cd9](https://github.com/supatype/server/commit/3dd0cd9e99278deea8c55b6eac8703a6e0d7c6a3))


### Bug Fixes

* **server:** handle db.Close() errors in server.New bootstrap (gosec G104) ([cc44778](https://github.com/supatype/server/commit/cc44778e3b6c1045ddc14c4a3ed67cefb414a25f))

## [1.1.0](https://github.com/supatype/server/compare/v1.0.6...v1.1.0) (2026-06-15)


### Features

* **server:** add studio auth proxy routing and studioauth package ([3d4890f](https://github.com/supatype/server/commit/3d4890f2250186688d0e890aef9f5ff6a07f265f))


### Bug Fixes

* **deps:** bump Go to 1.25.11 for stdlib vuln fixes ([1ba324b](https://github.com/supatype/server/commit/1ba324b96d5de4092d902f013e899d0adb79f265))
* **mux:** proxy GraphQL to PostgREST graphql_public RPC ([60dfa4f](https://github.com/supatype/server/commit/60dfa4ff71f25bc17aa28a3a3e2f056b66392f6b))
* **release:** skip existing RC tags and make upload idempotent ([288f292](https://github.com/supatype/server/commit/288f29279f98e91fd22c201ce7ae4744526a11bd))
* **studioauth:** read admin config via OpenRoot to satisfy gosec G304 ([fc24acc](https://github.com/supatype/server/commit/fc24accf1e236df4bb9dd565b126dc29ccee98b6))
* **studioauth:** use restrictive file modes in roles tests for gosec ([56c3afb](https://github.com/supatype/server/commit/56c3afb4830e3448807232e178b1a2d64271d0d1))

## [1.0.6](https://github.com/supatype/server/compare/v1.0.5...v1.0.6) (2026-06-01)


### Bug Fixes

* **deps:** resolve govulncheck failures ([b510d93](https://github.com/supatype/server/commit/b510d93ad7d0e692391afbccfd9599599f0fb7b4))
* **deps:** resolve govulncheck failures ([d09b6b4](https://github.com/supatype/server/commit/d09b6b490643f5802a013e4779a5cee1c40d0932))
* **deps:** resolve second round of govulncheck failures ([9361833](https://github.com/supatype/server/commit/93618330ba0e728a34e63d5c92b9196471382d08))
* **docker:** bump build image to golang:1.25.10-alpine3.23 ([870211d](https://github.com/supatype/server/commit/870211db3b8c3c6f0e30306d99326c02ace4fba3))
* **test:** repair pre-existing make test failures ([90f5d10](https://github.com/supatype/server/commit/90f5d104633286cfaa8a6bb217537d940cec03d0))

## 1.0.0 (2026-03-13)

The first Supatype release, and the point from which this changelog is this
project's own.

`supatype-server` began as a fork of [supabase/auth](https://github.com/supabase/auth)
(formerly GoTrue) at **v2.187.0**, released 2026-02-24 and carried here as
commit `bdb13e34`. Everything before that tag belongs to that project. Its
changelog is [in its own repository](https://github.com/supabase/auth/blob/master/CHANGELOG.md),
and the entries it lists resolve against its commits, not this repository's.
They used to be reproduced below with their links rewritten to point here,
where they resolve to nothing.

What the fork is now is not what it was. The auth endpoints remain, but the
binary is one coherent service rather than an auth server with things wrapped
around it: a gateway in front of PostgREST, storage, functions, realtime,
Studio and the SQL runner; model hooks and field-level access control on the
write path; one configuration surface under the `SUPATYPE_` prefix, with no
`GOTRUE_` variable read anywhere; one owner for every database and cache
connection. The upstream history describes a different program.

The MIT licence and its attribution stay exactly as they are. See `LICENSE`.
