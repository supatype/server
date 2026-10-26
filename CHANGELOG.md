# Changelog

## [2.2.0](https://github.com/supatype/server/compare/v2.1.1...v2.2.0) (2026-10-26)


### Features

* **auth:** list and revoke sessions through the admin API ([55ef3ce](https://github.com/supatype/server/commit/55ef3ce17f6df9236dd64e6ffb91c9628d5f2ca8))
* **auth:** return a Studio sign-in to the server's own Studio address ([a5c9f79](https://github.com/supatype/server/commit/a5c9f79fe1000ab4a98fbf952ac4ff9cb2403edf))
* **gateway:** allow-listed admin endpoints for extensions, RLS and policies (SQ2) ([d461607](https://github.com/supatype/server/commit/d4616072cc9f3ec3ec0dcc618fd5dd9a78b22755))
* **gateway:** name supatype.create_record when an insert into a drafts model is refused ([e5e8360](https://github.com/supatype/server/commit/e5e8360035098a63b0c6cbaf3af43f32ee1ba937))
* **sqlrunner:** a read-only metadata catalogue for Studio's schema views (RQ3) ([4693bfa](https://github.com/supatype/server/commit/4693bfa9fbdc29f9d857f7affe0f194dba2ba1ae))
* **sqlrunner:** policies, ledgers and settings in the metadata catalogue (RQ3b) ([670ea9f](https://github.com/supatype/server/commit/670ea9f4953ec0aeb345199706a6be37dd1bc4aa))
* **sqlrunner:** read each migration's outcome and the newest push's status ([67f1806](https://github.com/supatype/server/commit/67f18067cd3ccb396a9461f38e4bd3652a428d20))
* **sqlrunner:** table introspection for every role that may read rows ([bcab52a](https://github.com/supatype/server/commit/bcab52a1270b519dfea898b183a09a64d4cde93e))
* **sqlrunner:** the SQL editor connects on its own non-superuser DSN (SEC-SQL) ([de91178](https://github.com/supatype/server/commit/de91178d82145b06d84136d6d0a9740640d52968))
* **studio:** a Studio-mode writer may create every field, masked ones included ([ff0b2e9](https://github.com/supatype/server/commit/ff0b2e92db1f96bee40153a4cda69a5511cbc2a8))
* **studioauth:** let editors read the storage schema's policies ([590957e](https://github.com/supatype/server/commit/590957efc119d96fc0e1001d3d3027738c18e61d))
* **studioauth:** offer a viewer no writes in the Studio session ([532b255](https://github.com/supatype/server/commit/532b2554bc4f3aaabd67e09867bf2337d91e83ae))
* **studioauth:** one shared role set, with a read-only viewer ([989863b](https://github.com/supatype/server/commit/989863b24e4f02052579445d6a1eb73c82ce4cae))
* **studioauth:** open the SQL editor to every role holding sqlEditor (RX1) ([9de5dfb](https://github.com/supatype/server/commit/9de5dfb4448df92333e4f4921df4d8b70b54f28a))
* **studioauth:** realtimeInspect capability and mediated forwarding for the inspector ([c659f4a](https://github.com/supatype/server/commit/c659f4a15a85c6fb021d214b23ede8a2f024ea29))
* **studioauth:** report the acting mode a request asks for (RX4) ([9347dd9](https://github.com/supatype/server/commit/9347dd9ec5b8e7eef34c227a233be9b1a285ccae))
* **studiomembers:** trust CLI and first-push seed membership changes ([03d2864](https://github.com/supatype/server/commit/03d2864700c0d8c80f8df4105e763c99212bc7f7))
* **studio:** Studio roles decide what Studio users may edit ([356fad2](https://github.com/supatype/server/commit/356fad21d09fc8b1296b5440ef0c22a3693066f5))
* **studio:** Studio writers may supply every field on create ([a89a9d4](https://github.com/supatype/server/commit/a89a9d4d76a17ea52129850cfa6d67662e10aa60))
* user filters and sessions, per-function logs, folder listing ([6988a5c](https://github.com/supatype/server/commit/6988a5c1d1c0309900d8f6a2e4546a124ffa8f96))


### Bug Fixes

* **admin:** report the database's host-side connection details for Studio ([44a9263](https://github.com/supatype/server/commit/44a92631ba28ffeb7818229d0fa8e9f9b6dd1f82))
* **auth:** explain a user delete refused by an onDelete restrict link ([13b8cd0](https://github.com/supatype/server/commit/13b8cd0bf442a45e5d7cf86e4b0ac5e36e9629fb))
* **auth:** include identities in the admin users list ([951fb3e](https://github.com/supatype/server/commit/951fb3ebcaceb15be6509053d0bd9cbff76bcecb))
* **auth:** keep email templates saved from Studio in the database ([c189419](https://github.com/supatype/server/commit/c18941929419f386d62151012e20fa0fc2ad4706))
* **auth:** put custom OAuth provider callbacks under /auth/v1 ([19774c6](https://github.com/supatype/server/commit/19774c663936a985382750294b3575c92cd02aca))
* **auth:** read a user's identities oldest first, so the primary provider is stable ([9b25902](https://github.com/supatype/server/commit/9b25902d0f015dc688cc23f8bc93eaed16cb223f))
* **auth:** remove a deleted user's Studio membership, keep the last admin, lock the guard and audit the caller ([80129dd](https://github.com/supatype/server/commit/80129ddee88bf7712dd2de0d27d92bdf4457b3c1))
* **auth:** request the openid scope from Keycloak ([fc35b0f](https://github.com/supatype/server/commit/fc35b0f6c73d63e6646916c4b99770926631d415))
* **ci:** name the cloud and server repositories as they are now called ([246ee09](https://github.com/supatype/server/commit/246ee09be230e067a328affb2b4dc031e068ee35))
* **config:** let the container environment win over the project .env again ([cd57bfa](https://github.com/supatype/server/commit/cd57bfa8145b6ed7ec95320a125838fbbdab671a))
* **deps:** bump Go to 1.26.10 for a reachable crypto/x509 vulnerability ([c7ede13](https://github.com/supatype/server/commit/c7ede13cfa7254791efa35bf4f6dfd8f95628dd2))
* **deps:** bump Go to 1.26.9 and x/net, x/text, otel for reachable vulns ([167d0df](https://github.com/supatype/server/commit/167d0df14d7aab82ed96580c104137a4f812dece))
* **functions:** require the service-role key in dev mode too (SC1) ([b4b6e98](https://github.com/supatype/server/commit/b4b6e985c8e6fad1d4d532daae486cfacb864b77))
* **gateway:** always allow the site URL, whatever list is saved ([5109bff](https://github.com/supatype/server/commit/5109bffff9afc0b2c9828bb25fc71301daf25b93))
* **gateway:** answer /graphql/v1 with 501 graphql_unavailable where Postgres has no pg_graphql ([32f0928](https://github.com/supatype/server/commit/32f0928eeb63e252fece4c0f835983f4f863d4d9))
* **gateway:** decide CORS at the gateway, defaulting to the site URL ([b73c96e](https://github.com/supatype/server/commit/b73c96e432ef302036dad97b8a37ef9468504919))
* **gateway:** name create_records too in the drafts insert hint ([3ab9ee5](https://github.com/supatype/server/commit/3ab9ee5e9616e51702afe951dd87c2a0b8bca62d))
* **gateway:** run GraphQL as the caller, not as the service role ([2e3b0be](https://github.com/supatype/server/commit/2e3b0bee9d56d9f9ba4c8afe59076bb87aef31dc))
* **health:** report GraphQL as disabled when SUPATYPE_GRAPHQL_DISABLED is set ([9ccf9f4](https://github.com/supatype/server/commit/9ccf9f4e170fe25b474c2b97f414e07d3b7718b1))
* **keyspace:** refuse an expiry a set cannot carry before writing it ([fc4806f](https://github.com/supatype/server/commit/fc4806f248ffa7494dd2cd0832ddb2ce603cd60c))
* **metadata:** match policies and rls_status on the exact schema name ([8faa048](https://github.com/supatype/server/commit/8faa0486a9bc8479371796e1f3072a7e6d698387))
* **objstore:** answer list and remove in the storage service's shape ([5d85cb7](https://github.com/supatype/server/commit/5d85cb721059a50df43b89d4887181b8529f4a1e))
* **platformproxy:** present the control plane secret on every forwarded request ([8d29334](https://github.com/supatype/server/commit/8d293345075cad1b74deb65389b9fb8e01398d03))
* **proxy:** apply a manifest write that lands between the startup load and Watch ([dd99226](https://github.com/supatype/server/commit/dd99226c27ec5f5efb6cc4ed6d67a6cb759d5d23))
* **proxy:** forward each hop once by moving from Director to Rewrite ([a8b5ea4](https://github.com/supatype/server/commit/a8b5ea4ca673e41d474945cc6d1de0b3e3c59542))
* **proxy:** never drop a manifest push that lands during a reload ([0efcbb6](https://github.com/supatype/server/commit/0efcbb6878ffdabf5f869f075adfb173a8bed5c2))
* **proxy:** notice a manifest change the OS never reported ([87a0939](https://github.com/supatype/server/commit/87a0939d39d504599e0aa945904488bdc24e0eb0))
* **reloader:** report a watcher's own error, not the cancellation it causes ([a459d12](https://github.com/supatype/server/commit/a459d12559b55425fbeb3170788badbc906497c6))
* **storage:** serve every object sandboxed with a type it could parse ([9e295df](https://github.com/supatype/server/commit/9e295df0800d29d653d152b4b7c5288481a62024))
* **studio-members:** a failed membership change is a 503, not the caller's fault ([1867d3b](https://github.com/supatype/server/commit/1867d3b8781cd8ef9de274e78661cedf65fd604f))
* **studio-members:** trust the seed's source mark only on rows the seed wrote ([c7d0fd3](https://github.com/supatype/server/commit/c7d0fd32da2f999123b4c77829779c4fa92bf608))
* **studio:** acting as yourself keeps administration and the session ([62d8ff5](https://github.com/supatype/server/commit/62d8ff544dfeca870987d935be50615e781682b7))
* **studio:** answer a field's create verdict per row, as the database judges it ([f818473](https://github.com/supatype/server/commit/f818473d5f2aa679d74667d2cb7feffab769708f))
* **studio:** audit chunked bodies, the routed path and SQL editor statements ([7cde47a](https://github.com/supatype/server/commit/7cde47a94e6502f42083c787cde48cd33e1ab7b1))
* **studioauth:** proxy refusals carry the status code in error and the sentence in message ([6bb5dd3](https://github.com/supatype/server/commit/6bb5dd37ae6a9544078250c381b6457b9b8695e2))
* **studioauth:** refuse disabling, deleting or re-roling your own account ([15af697](https://github.com/supatype/server/commit/15af697d9b2d130c106b5bebd9b9ce466ed99b01))
* **studioauth:** refuse disabling, deleting or re-roling your own account ([ea8f04f](https://github.com/supatype/server/commit/ea8f04fd63209e7303198fcdcf34d2c1c602c7a5))
* **studiobootstrap:** compare numeric claims as Postgres prints them ([080ac60](https://github.com/supatype/server/commit/080ac60afd742b5223b5afda5d4593f14b16c1dd))
* **studio:** give every Studio API error a code that matches its status ([b6b103b](https://github.com/supatype/server/commit/b6b103bda0b17bc8991f14b2e62a0da617580059))
* **studio:** judge GraphQL requests by their operations, not their method ([6d54902](https://github.com/supatype/server/commit/6d54902e8f30be08426d072e94295371d7b93ccd))
* **studio:** read the schema state as text, and say when field access is unavailable ([04366c5](https://github.com/supatype/server/commit/04366c596057ba7234665e985593f690af45a500))
* **studio:** refuse non-admin Studio roles acting on a Studio admin's account ([1d58753](https://github.com/supatype/server/commit/1d58753fb95313fa8c0eb6930ec40a08f1403439))
* **studio:** remove the STUDIO_OPEN_DEV bypass ([97daed8](https://github.com/supatype/server/commit/97daed881308efe0ba158f901bad2841758fbd10))
* **studio:** require admin for every non-content route through the Studio proxy ([1614dbe](https://github.com/supatype/server/commit/1614dbe9f32cd1ab79462c16891d666c78a84831))
* **studio:** send Studio's Anonymous choice as a real anon client ([cfe307e](https://github.com/supatype/server/commit/cfe307e635a090f5eb33b2fc3d5c7a8dad4070ed))
* **studio:** sign URLs only from exact body keys, and cap Studio signing in the store ([002a45c](https://github.com/supatype/server/commit/002a45ce356dc771270ac916f5273ba0b981c6f9))
* **test:** re-exec the dotenv precedence test from os.Executable, not argv[0] ([ef0c576](https://github.com/supatype/server/commit/ef0c57673850495d6dd0728d51b14498e6e5ccdc))

## [2.1.1](https://github.com/supatype/server/compare/v2.1.0...v2.1.1) (2026-09-23)


### Bug Fixes

* **admin:** scan registrations_loaded as the boolean it is ([83b2e03](https://github.com/supatype/server/commit/83b2e039dbff24a725b7a50a5aa2b422d0681d08))
* **admin:** scan registrations_loaded as the boolean it is ([5b701e6](https://github.com/supatype/server/commit/5b701e65140b47d4ded5486393244a7227fe508e))

## [2.1.0](https://github.com/supatype/server/compare/v2.0.1...v2.1.0) (2026-09-23)


### Features

* **admin:** serve the keyspace and row-cache views to Studio ([8dfdafc](https://github.com/supatype/server/commit/8dfdafc434599bbb6b170d35ffc838477e3f4b9b))
* **cacheceiling:** the schema is a ceiling the runtime may only narrow ([8e41044](https://github.com/supatype/server/commit/8e410440ce71abae4ea6038a39879b3c04853c6d))
* **cache:** enforce the schema's ceiling on the read path, and show it ([691ab57](https://github.com/supatype/server/commit/691ab5742329e43125a09af2403e3a52e71eb4f2))
* **cache:** enforce the schema's ceiling on the read path, and show it ([94b84f1](https://github.com/supatype/server/commit/94b84f17dbc1f30db8fb2ec61c6a35b8e4e6456a))
* **functions:** relay the worker's log stream, and read its structured lines ([8a64034](https://github.com/supatype/server/commit/8a64034a26cfad3be0792ec25d1528e1763a8abe))
* **keyspace:** hold two keyspaces, and read each thing from the right one ([cebc456](https://github.com/supatype/server/commit/cebc456160d9eb467fc83c2ddcc138d8231a6499))
* **keyspace:** rename the cache client to keyspace and add SUPATYPE_KEYSPACE_ADDR ([58a24a8](https://github.com/supatype/server/commit/58a24a833bcd767b8754a900730da83fc160138f))
* **keyspace:** rename the cache client to keyspace, add SUPATYPE_KEYSPACE_ADDR ([1ce75fd](https://github.com/supatype/server/commit/1ce75fd333ff2e8c477fb8afe72fb383d7c21bf6))
* **proxy:** carry the schema's cache ceiling on the route manifest ([7d971d8](https://github.com/supatype/server/commit/7d971d8f10709265e59fdf956e039e0549e4037f))
* **restcache:** count cache outcomes per table and serve them on the admin API ([2159e1a](https://github.com/supatype/server/commit/2159e1ad5a6513bd7d3a5b25e78803d8ae407411))
* **restcache:** file the response cache under cache:rest:{ref}:{hash} ([5d957d3](https://github.com/supatype/server/commit/5d957d35000e477da02b5eb07f216973d4dfd38e))
* **rowcache:** register tables for the row cache from the REST allowlist ([c9031d6](https://github.com/supatype/server/commit/c9031d6908456b025db2d75a125eee59741edd23))
* **studioauth:** signed, expiring preview links ([17a8b39](https://github.com/supatype/server/commit/17a8b396915747b4d86ec083373906964d611414))
* **studioauth:** tell Studio whether this caller sees drafts ([48c3aba](https://github.com/supatype/server/commit/48c3aba88adc0c3a0f3f4ff93d1e7711ea167453))
* **studio:** preview links that can be revoked one at a time ([79df06f](https://github.com/supatype/server/commit/79df06fd396b9f3cd44fbb76d17b96ca402caa44))


### Bug Fixes

* **apiconfig:** keep a managed pod's API config somewhere that survives a restart ([1e981b6](https://github.com/supatype/server/commit/1e981b60384a30ae94d38913b8cbc8a3b48219ce))
* **ci:** pin govulncheck, and take the grpc fix it found ([6e17660](https://github.com/supatype/server/commit/6e17660b71928970d10ac5c4e2b560049f15f639))
* **ci:** pin govulncheck, which [@latest](https://github.com/latest) stopped building ([30e5c0a](https://github.com/supatype/server/commit/30e5c0a9b186895b40eff82e67a35b16595c9400))
* **ci:** pin govulncheck, which [@latest](https://github.com/latest) stopped building ([951812b](https://github.com/supatype/server/commit/951812bacaa1ad72167173957725a25bc1a95a4a))
* **coverage:** keep the studioauth note in the one comment key the gate allows ([0c1935c](https://github.com/supatype/server/commit/0c1935c42c81c842eb6bfa60248189ba92d5b996))
* **deps:** take the grpc fix for GO-2026-6348 ([1e92f89](https://github.com/supatype/server/commit/1e92f89aaaa2d5e9e548cd2786dd1887605e24c2))
* **deps:** take the grpc fix for GO-2026-6348 ([d09c21e](https://github.com/supatype/server/commit/d09c21e1ec85577737eaf12acb3fc1e835b0536d))
* **functions:** list only what the worker will actually serve ([411d9d5](https://github.com/supatype/server/commit/411d9d5c39f79c2d7a14072e41754c8ecfb456e5))
* **proxy:** merge an overlay's validators, which were being dropped entirely ([4b4a9a9](https://github.com/supatype/server/commit/4b4a9a9d874c1d8cbb484eeb048b135882ae2dac))

## [2.0.1](https://github.com/supatype/server/compare/v2.0.0...v2.0.1) (2026-09-03)


### Bug Fixes

* **migrate:** wait for a database that is still starting instead of exiting ([5bbbb2f](https://github.com/supatype/server/commit/5bbbb2f36dfb9c69f433591e4fe1808500566dbf))
* **migrate:** wait for a database that is still starting instead of exiting ([e596f0e](https://github.com/supatype/server/commit/e596f0ea2e51e1d5aa376bb7c671a79212d8597b))

## [2.0.0](https://github.com/supatype/server/compare/v1.5.0...v2.0.0) (2026-09-01)


### ⚠ BREAKING CHANGES

* ten wire names change. The seven sb-auth-* response headers set on every token issue and refresh become st-auth-*, x-sb-error-code on every error response becomes x-st-error-code, the sb-forwarded-for request header becomes st-forwarded-for, and X-Supabase-Api-Version becomes X-Supatype-Api-Version. A client that sent the old version header to select the 2024-01-01 error shape will now get the initial shape instead.
* every GOTRUE_ variable is now SUPATYPE_, 573 of them. A deployment that still sets only the old names has no configuration at all, and the server refuses to start rather than running on defaults: it lists each stale variable with the name to use instead. Two variables are gone rather than renamed, because nothing ever read them under the names deployments were setting: SUPATYPE_API_API_EXTERNAL_URL is SUPATYPE_API_EXTERNAL_URL, and SUPATYPE_DB_DB_NAMESPACE is SUPATYPE_DB_NAMESPACE.

### Features

* name every header and variable after this service, not its ancestor ([c0f9d97](https://github.com/supatype/server/commit/c0f9d97ff67e3f4d3da79d8711c4f8ed246865c8))
* one configuration prefix, and no GoTrue left in the service ([81331f6](https://github.com/supatype/server/commit/81331f6039a69815ad63ad2eff05565cc9bb9e84))


### Bug Fixes

* **auth:** stop warning that two supported settings are unsupported ([9632849](https://github.com/supatype/server/commit/96328492522047bd8b9f932648fa68b26005fbbe))
* **config:** actually run the preflight that refuses the old prefix ([de41515](https://github.com/supatype/server/commit/de4151539601384683546ddf8eb0accf8f380892))
* **functions:** write an env file through the directory it lives in ([0e481ad](https://github.com/supatype/server/commit/0e481ad4280eaf4b74538a794cb7ddf6ef5e981e))
* **gateway:** keep the old prefix out of everything but the test that needs it ([26f1dbf](https://github.com/supatype/server/commit/26f1dbf7a253b082e6c18c86ae0f47d53b5587a7))
* **gateway:** load the test environment rather than assume it is exported ([3103805](https://github.com/supatype/server/commit/3103805f2bf8cacc13f00a503599c9394d9ccdc4))
* **hibp:** vendor the pwned-password client, and fix two concurrency defects ([125bd46](https://github.com/supatype/server/commit/125bd46fcca51c65d36b6cca9ed0966d6fee1e08))
* **hooks:** one clock for a hook's time budget, and a timeout test that holds ([f80e9a0](https://github.com/supatype/server/commit/f80e9a0a16ea6c6cb9f8fb2d482cf279ae364945))
* stop ignoring internal/auth ([f2d2319](https://github.com/supatype/server/commit/f2d23198b0b60a567807d07da1757abee8860b18))
* stop the service naming its ancestor on the wire and in the database ([8c48b8b](https://github.com/supatype/server/commit/8c48b8b67e5d4c82180cc029b82e4603cfe6ab53))
* **storage:** actually contact the database when dialling it ([e75bdf5](https://github.com/supatype/server/commit/e75bdf5319c0a1a269a3b1ae4e69f1f33f6983fc))

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
