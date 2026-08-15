# Changelog

## 1.0.0 (2026-08-15)


### Features

* add `.well-known/openid-configuration` ([#2197](https://github.com/supatype/server/issues/2197)) ([24ed669](https://github.com/supatype/server/commit/24ed669225c59b1329807a8f6dfacb4f27f40e43))
* add `auth_migration` annotation for the migrations ([#2234](https://github.com/supatype/server/issues/2234)) ([ddf584a](https://github.com/supatype/server/commit/ddf584a2e7250009daebbaeb027a39680e9edbe5))
* add `password_hash` and `id` fields to admin create user ([#1641](https://github.com/supatype/server/issues/1641)) ([684442a](https://github.com/supatype/server/commit/684442a824164351bfc28b13969bf94bbd6dea3b))
* add `x-sb-error-code` header, show error code in logs ([#1765](https://github.com/supatype/server/issues/1765)) ([c2653c9](https://github.com/supatype/server/commit/c2653c9079a51c5292b5af87b2502e1c97d4deb8))
* add advisor to notify you when to double the max connection pool ([#2167](https://github.com/supatype/server/issues/2167)) ([49dc278](https://github.com/supatype/server/commit/49dc2787b6bb9f864135dc172701ed00dd5b278f))
* add after-user-created hook ([#2169](https://github.com/supatype/server/issues/2169)) ([8086cb1](https://github.com/supatype/server/commit/8086cb1ef4a1a31b7db1d64a501b659813497803))
* add an optional burstable rate limiter ([#1924](https://github.com/supatype/server/issues/1924)) ([6bf3c2e](https://github.com/supatype/server/commit/6bf3c2ef8ca306fd2443162523982e6ef2841637))
* add asymmetric jwt support ([#1674](https://github.com/supatype/server/issues/1674)) ([5549ff1](https://github.com/supatype/server/commit/5549ff18b2df863b25e02c3f9be7cb1d84ca792a))
* add authorized email address support ([#1757](https://github.com/supatype/server/issues/1757)) ([ce45e82](https://github.com/supatype/server/commit/ce45e828e86958264a5320896ffda77f52e67f9c))
* Add custom claims from Keycloak user token ([#1917](https://github.com/supatype/server/issues/1917)) ([b8e0349](https://github.com/supatype/server/commit/b8e0349f29e93e5c6606091ff52ef187e4195f2b))
* Add email send operation metrics ([#2311](https://github.com/supatype/server/issues/2311)) ([a7a9b27](https://github.com/supatype/server/commit/a7a9b27b85fd3784f690f8a9af4c6edbae2e6bbe))
* add email validation function to lower bounce rates ([#1845](https://github.com/supatype/server/issues/1845)) ([fc5100b](https://github.com/supatype/server/commit/fc5100bb96a42407f3a45c5d79bd1d59b2f8bb63))
* add hook log entry with `run_hook` action ([#1684](https://github.com/supatype/server/issues/1684)) ([6d7b88e](https://github.com/supatype/server/commit/6d7b88e37e7504326cc007a24fcd0fd733421266))
* add is_anonymous claim to Auth hook jsonschema ([#1667](https://github.com/supatype/server/issues/1667)) ([06239b8](https://github.com/supatype/server/commit/06239b808f906df6961ad8813871d1f26962a385))
* add mail header support via `GOTRUE_SMTP_HEADERS` with `$messageType` ([#1804](https://github.com/supatype/server/issues/1804)) ([f6723bc](https://github.com/supatype/server/commit/f6723bcd17f250532f1db49ad9bc8c0afc6c946f))
* add max length check for email ([#1508](https://github.com/supatype/server/issues/1508)) ([2640a80](https://github.com/supatype/server/commit/2640a80bd3ff13bc8c6d99ade668e99ccd4df328))
* add metadata field to all hooks ([#2365](https://github.com/supatype/server/issues/2365)) ([872a617](https://github.com/supatype/server/commit/872a617d618acc59d37a05b798507d900c8f1e48))
* add MFA for WebAuthn ([#1775](https://github.com/supatype/server/issues/1775)) ([a0c8701](https://github.com/supatype/server/commit/a0c870125a23551f5acddc4f9eed7d9d9a2ac064))
* add OAuth client type ([#2152](https://github.com/supatype/server/issues/2152)) ([6f5f4db](https://github.com/supatype/server/commit/6f5f4db00e655d6ba35ca61b94f77f2929b9b339))
* add oauth2 client support ([#2098](https://github.com/supatype/server/issues/2098)) ([9b8a87f](https://github.com/supatype/server/commit/9b8a87f7ad705ae0564b9035690fdd2c9bf253ae))
* add option to disable magic links ([#1756](https://github.com/supatype/server/issues/1756)) ([2bbe93d](https://github.com/supatype/server/commit/2bbe93db31d7fe099a7ddf7157283952c6f9f738))
* add option to disable writing to `audit_log_entries` ([#2073](https://github.com/supatype/server/issues/2073)) ([3904926](https://github.com/supatype/server/commit/3904926b860bb4497784a2b1d0006a95f37ce904))
* add phone to sms webhook payload ([#2160](https://github.com/supatype/server/issues/2160)) ([1a1dbde](https://github.com/supatype/server/commit/1a1dbdebae1c3deaf3feeacc004b2ff48ac00b36))
* add SAML specific external URL config ([#1599](https://github.com/supatype/server/issues/1599)) ([523e52c](https://github.com/supatype/server/commit/523e52c61b52bf1c059b97816c74939bff031cf5))
* Add Sb-Forwarded-For header and IP-based rate limiting ([#2295](https://github.com/supatype/server/issues/2295)) ([33001cd](https://github.com/supatype/server/commit/33001cda33402fc987c171c2249db174b0293811))
* add sign in with ethereum ([#2069](https://github.com/supatype/server/issues/2069)) ([165e361](https://github.com/supatype/server/commit/165e361540175994a85a08cc7904ce8e54cdf132))
* add sign in with solana (EIP-4361) support ([#1918](https://github.com/supatype/server/issues/1918)) ([f7b931a](https://github.com/supatype/server/commit/f7b931ab1c89c69c9b4ec5d8989519eda93ecd15))
* add snapchat provider ([#2071](https://github.com/supatype/server/issues/2071)) ([1192864](https://github.com/supatype/server/commit/11928645c7b21e869f11deabd27b0806ae9ec11e))
* add Supabase Auth identifier to OAuth redirect URLs ([#2299](https://github.com/supatype/server/issues/2299)) ([d99135d](https://github.com/supatype/server/commit/d99135d484fed5151f1263f3240fbca1562d700d))
* add support for account changes notifications in email send hook ([#2192](https://github.com/supatype/server/issues/2192)) ([ae9546b](https://github.com/supatype/server/commit/ae9546b5aef6547e9057f61bf8c4235bd0ad97f4))
* add support for managing SSO providers by resource_id ([#2081](https://github.com/supatype/server/issues/2081)) ([a04609a](https://github.com/supatype/server/commit/a04609ad396d0e1b3064be5a0c0ad487cd2477c6))
* add support for migration of firebase scrypt passwords ([#1768](https://github.com/supatype/server/issues/1768)) ([2068420](https://github.com/supatype/server/commit/2068420ef771689985e370fe897fa1b5095e2731))
* add support for saml encrypted assertions ([#1752](https://github.com/supatype/server/issues/1752)) ([976b4ea](https://github.com/supatype/server/commit/976b4ea65efd64435d4cc19b612dbc38e4f7c002))
* add support for Slack OAuth V2 ([#1591](https://github.com/supatype/server/issues/1591)) ([5850985](https://github.com/supatype/server/commit/585098553facd8ec6b8166ddf6cfe27441f741da))
* add support for verifying argon2i and argon2id passwords ([#1597](https://github.com/supatype/server/issues/1597)) ([3a616ee](https://github.com/supatype/server/commit/3a616ee6dfc45eece7bb9fd9c6d7f3ca09b08097))
* add support packages for end-to-end testing ([#2021](https://github.com/supatype/server/issues/2021)) ([250973d](https://github.com/supatype/server/commit/250973d21f411ba8d7ab13c609e14d57e39c170f))
* add webauthn configuration variables ([#1773](https://github.com/supatype/server/issues/1773)) ([35d9461](https://github.com/supatype/server/commit/35d9461480007809e53718e715af63cd037f0b0e))
* allow amr claim to be array of strings or objects ([#2274](https://github.com/supatype/server/issues/2274)) ([121ecc1](https://github.com/supatype/server/commit/121ecc1a5acf986d48ad8ece920ce60638891528))
* allow invalid config directories ([#1969](https://github.com/supatype/server/issues/1969)) ([989356a](https://github.com/supatype/server/commit/989356a999cfc808d978e205ea6690993499b46a))
* allow limiting lifespan of low-aal sessions ([#1942](https://github.com/supatype/server/issues/1942)) ([2683e46](https://github.com/supatype/server/commit/2683e46adf810a5dd528db66d4d5143f5caf7773))
* async, concurrent index creation for users table ([#2239](https://github.com/supatype/server/issues/2239)) ([a9449f5](https://github.com/supatype/server/commit/a9449f525b4327920c71be39e1cba47a3d7535c6))
* background template reloading p1 - baseline decomposition ([#2148](https://github.com/supatype/server/issues/2148)) ([467a18b](https://github.com/supatype/server/commit/467a18b35b0c8229d0e0298af2370da6acc27fa0))
* Block specific outgoing mail servers ([#1971](https://github.com/supatype/server/issues/1971)) ([4f72a1b](https://github.com/supatype/server/commit/4f72a1b8e7541fb295a1a199dcd5ba8ac41585cf))
* cache OIDC discovery documents for providers ([#2389](https://github.com/supatype/server/issues/2389)) ([d8d18c5](https://github.com/supatype/server/commit/d8d18c59ea1bd717ff3faa5e1103658d1822ffac))
* check current password on change ([#2364](https://github.com/supatype/server/issues/2364)) ([941abaa](https://github.com/supatype/server/commit/941abaabb345afb57b1fb1be4feb2072a089ea93))
* **cmd:** console mailer, send-email hook receiver, proxy timeouts ([c9cce37](https://github.com/supatype/server/commit/c9cce37431ed35b9799741823db489933ef22690))
* config reloading ([#1771](https://github.com/supatype/server/issues/1771)) ([6fd7fc1](https://github.com/supatype/server/commit/6fd7fc127278ac21b44d357bad2896382a43a5a7))
* config reloading with fsnotify, poller fallback, and signals ([#2161](https://github.com/supatype/server/issues/2161)) ([1cbb4f3](https://github.com/supatype/server/commit/1cbb4f32933e8d073174751bcc13159d45e506bc))
* configurable email and sms rate limiting ([#1800](https://github.com/supatype/server/issues/1800)) ([82c7968](https://github.com/supatype/server/commit/82c7968f9fc4b897f19ee554d773e634fa886ce0))
* cover 100% of crypto with tests ([#1892](https://github.com/supatype/server/issues/1892)) ([c6a43ce](https://github.com/supatype/server/commit/c6a43ce0edf506ec0b6a867502d3687e5d1981b2))
* email address changed notification ([#2181](https://github.com/supatype/server/issues/2181)) ([151bcb6](https://github.com/supatype/server/commit/151bcb65e844945d0bdc324cbc1232c850ae7785))
* encrypt sensitive columns ([#1593](https://github.com/supatype/server/issues/1593)) ([572c929](https://github.com/supatype/server/commit/572c92960dd06a7d9f20e3034c1dd7025a4c5c12))
* enhance issuer URL validation in OAuth server metadata ([#2164](https://github.com/supatype/server/issues/2164)) ([ef7ab93](https://github.com/supatype/server/commit/ef7ab93bb4bb320ca0ebb87528e105ede97da00c))
* enhance login analytics ([#2078](https://github.com/supatype/server/issues/2078)) ([126175e](https://github.com/supatype/server/commit/126175e9f42c56b3831cae41a5cee13c949db12e))
* experimental own linking domains per provider ([#2119](https://github.com/supatype/server/issues/2119)) ([53ceba4](https://github.com/supatype/server/commit/53ceba43e2f69229fd6428ba2c45b2db32fcfbef))
* fallback to jwt secret if alg is `HS256` and the `kid` is not recognized ([#2072](https://github.com/supatype/server/issues/2072)) ([a1457a7](https://github.com/supatype/server/commit/a1457a70d8ade5c18fa9a03c412df92bddc96305))
* fetch email from snapchat oauth provider if available for consistency ([#2110](https://github.com/supatype/server/issues/2110)) ([cd2c5e9](https://github.com/supatype/server/commit/cd2c5e95a608f01716591801d435b06fc3319b4a))
* fix argon2 parsing and comparison ([#1887](https://github.com/supatype/server/issues/1887)) ([d0ff061](https://github.com/supatype/server/commit/d0ff061acb5b36e872171d21b71a8dbb3b9e4e8d))
* fix large group claim handling in azure id tokens ([#1995](https://github.com/supatype/server/issues/1995)) ([d274362](https://github.com/supatype/server/commit/d274362e25f0119c8dde15a418895edfd26639f0))
* **functions:** proxy invocations to external functions-worker ([5c56671](https://github.com/supatype/server/commit/5c566711ec1a46ad3d1aee421e10a6b3971b3709))
* hooks round 2 - remove indirection and simplify error handling ([#2025](https://github.com/supatype/server/issues/2025)) ([8b96693](https://github.com/supatype/server/commit/8b96693a9c4a644dbb59614db51ded5aa29a599f))
* hooks round 4 - update tests to use require package ([#2030](https://github.com/supatype/server/issues/2030)) ([07ee4af](https://github.com/supatype/server/commit/07ee4afbbc00f559d9e7f78f7c850de5dfe8a998))
* hooks round 5 (Option 2) - add before-user-created hook ([#2034](https://github.com/supatype/server/issues/2034)) ([828cea7](https://github.com/supatype/server/commit/828cea796e57412acb4cc898562993fe50ef1ee6))
* identity linked/unlinked notifications ([#2185](https://github.com/supatype/server/issues/2185)) ([c650db8](https://github.com/supatype/server/commit/c650db83576f081a12aa282f773ba105c68d3708))
* ignore `aud` claim from admin jwt (`service_role` never had one) ([#2070](https://github.com/supatype/server/issues/2070)) ([5ec410b](https://github.com/supatype/server/commit/5ec410bae08e7c884906ac22ca44e9d6b48d31e5))
* implement link identity with oidc / native sign in ([#2108](https://github.com/supatype/server/issues/2108)) ([70ea143](https://github.com/supatype/server/commit/70ea143c1bb2e5c04063eecf94afd7fea79f605b))
* implement OAuth2 authorization endpoint ([#2107](https://github.com/supatype/server/issues/2107)) ([596a0bb](https://github.com/supatype/server/commit/596a0bbacf8c71330a1e0bf6c1a19fb8b539ea06))
* implements email-less accounts with oauth ([#2105](https://github.com/supatype/server/issues/2105)) ([60e17fc](https://github.com/supatype/server/commit/60e17fcf2d8316e2aa4532b761afa630ec27ee30))
* improvements to config reloader, 100% coverage ([#1933](https://github.com/supatype/server/issues/1933)) ([9270f8e](https://github.com/supatype/server/commit/9270f8e860e966d84e2e02f3bb368a3e42631632))
* increase test coverage in conf package to 100% ([#1937](https://github.com/supatype/server/issues/1937)) ([85a2e95](https://github.com/supatype/server/commit/85a2e950b51ae40a1e1e0d50a571984888cf35ee))
* increment refresh token counter by 2 for mfa verify ([#2284](https://github.com/supatype/server/issues/2284)) ([5ad7438](https://github.com/supatype/server/commit/5ad74386930d0be9f13f46c811c47244fd4eef97))
* **indexworker:** add max users threshold for rollout ([#2374](https://github.com/supatype/server/issues/2374)) ([34d6b1a](https://github.com/supatype/server/commit/34d6b1aa874c541909cdd8f3663482014625b174))
* **indexworker:** use `auth_trgm` extension if available ([#2263](https://github.com/supatype/server/issues/2263)) ([65aee8c](https://github.com/supatype/server/commit/65aee8c832026fbf58d769f7175e94ce8473d588))
* introduce request-scoped background tasks & async mail sending ([#2126](https://github.com/supatype/server/issues/2126)) ([3b79504](https://github.com/supatype/server/commit/3b79504ccf38b0d297993e1c489d3d89f7ad0d90))
* introduce v2 refresh token algorithm ([#2216](https://github.com/supatype/server/issues/2216)) ([c4ab9e4](https://github.com/supatype/server/commit/c4ab9e4477ccbb877e82825dd8be970bcd2caddc))
* load template cache at startup for fault tolerance ([#2261](https://github.com/supatype/server/issues/2261)) ([e63bc05](https://github.com/supatype/server/commit/e63bc0589ed9355151f7fe8dac0a84d611c9e4a3))
* log all audit events separately to prevent missing events ([#2086](https://github.com/supatype/server/issues/2086)) ([0421c08](https://github.com/supatype/server/commit/0421c082e2e32ceee71e89bd3b77b3159d6f4741))
* log sb-auth-user-id, sb-auth-session-id, ... on sign in not just refresh token ([#2342](https://github.com/supatype/server/issues/2342)) ([f249845](https://github.com/supatype/server/commit/f249845bf1055fc951b3cbf5d47406cc22ec6938))
* mailer logging ([#1805](https://github.com/supatype/server/issues/1805)) ([38830fc](https://github.com/supatype/server/commit/38830fc92446520a7dae7e17dc89fbfdcc8c3928))
* make the email client explicity set the format to be HTML ([#1149](https://github.com/supatype/server/issues/1149)) ([8c045dc](https://github.com/supatype/server/commit/8c045dcc93a4c90b6b088f7fdd0b613d45095afe))
* **metrics:** added a gauge with version information ([#2375](https://github.com/supatype/server/issues/2375)) ([c0d2ff2](https://github.com/supatype/server/commit/c0d2ff2cbec9c8c6eb2d86e26fa5d189d5fa302b))
* MFA (Phone) ([#1668](https://github.com/supatype/server/issues/1668)) ([f0bd92a](https://github.com/supatype/server/commit/f0bd92ac6153e343d528e1a9086877e1228c5925))
* MFA factor enrollment notifications ([#2183](https://github.com/supatype/server/issues/2183)) ([c4d0c3f](https://github.com/supatype/server/commit/c4d0c3f6d33c9f18802bda8436bbd39873ccad31))
* modernize IsNotFoundError handler to support errors.Is ([#2392](https://github.com/supatype/server/issues/2392)) ([2313eac](https://github.com/supatype/server/commit/2313eac6a9d8e7221edaf68ab28a83e24ade6cc9))
* new timeout writer implementation ([#1584](https://github.com/supatype/server/issues/1584)) ([ec0fdcf](https://github.com/supatype/server/commit/ec0fdcf201a8c3750ecc738e21d7c350ddc48ef0))
* notify users when their phone number has changed ([#2184](https://github.com/supatype/server/issues/2184)) ([e64e98d](https://github.com/supatype/server/commit/e64e98d994ecc1421fccb9b9a1ee1b2ceb1a014b))
* **oauth-server:** allow updating `token_endpoint_auth_method` for OAuth clients ([#2391](https://github.com/supatype/server/issues/2391)) ([01b1411](https://github.com/supatype/server/commit/01b141194c037a6cbaac521d4ecb017b25e70729))
* **oauth-server:** store and enforce token_endpoint_auth_method ([#2300](https://github.com/supatype/server/issues/2300)) ([2c9309f](https://github.com/supatype/server/commit/2c9309f9b54fcce3db6440699733411b0654fe41))
* **oauth2:** add `/oauth/token` endpoint ([#2159](https://github.com/supatype/server/issues/2159)) ([d0a9a2a](https://github.com/supatype/server/commit/d0a9a2a19836c1131e3005864f2dcb9c828f0e00))
* **oauth2:** add admin endpoint to regenerate OAuth client secrets ([#2170](https://github.com/supatype/server/issues/2170)) ([f9f5006](https://github.com/supatype/server/commit/f9f5006d86e9b2a05803a30eca9be5f3850227af))
* **oauth2:** return redirect_uri on GET authorization ([#2175](https://github.com/supatype/server/issues/2175)) ([aefdbf2](https://github.com/supatype/server/commit/aefdbf2e63ea597073dad3159984d37c9557f1c7))
* **oauth2:** use `id` field as the public client_id ([#2154](https://github.com/supatype/server/issues/2154)) ([d2445f2](https://github.com/supatype/server/commit/d2445f241a3e2ca8e6c793490cefc0c0a551b5c3))
* **oauth:** add support for X/Twitter v2 provider ([#2275](https://github.com/supatype/server/issues/2275)) ([1f3deaf](https://github.com/supatype/server/commit/1f3deaf5df99b86ac19288f952871b7076e85f34))
* **oauthserver:** add authorization list and revoke endpoints ([#2232](https://github.com/supatype/server/issues/2232)) ([9b38a55](https://github.com/supatype/server/commit/9b38a5593ef6d6115eb95808f415f289d624aee4))
* **oauthserver:** add OAuth client admin update endpoint ([#2231](https://github.com/supatype/server/issues/2231)) ([d0828b7](https://github.com/supatype/server/commit/d0828b78a943e864cfc02efbbc60c79c80f18fd8))
* **oauthserver:** add OpenID Connect support ([#2250](https://github.com/supatype/server/issues/2250)) ([5027a84](https://github.com/supatype/server/commit/5027a84ff4fe2491323d3650ae90ebcaa50107d3))
* **oauthserver:** update oauth grant list & authorization details response structure ([#2247](https://github.com/supatype/server/issues/2247)) ([e9b2c5c](https://github.com/supatype/server/commit/e9b2c5c0c367d95c88d5fb163657e9d5dcfcf896))
* **oauthserver:** use `NewOAuthServerAuthorizationParams` & configurable ttl for authorization ([#2254](https://github.com/supatype/server/issues/2254)) ([a23d665](https://github.com/supatype/server/commit/a23d665d9d64e9687e7c832026a9fb91cc051036))
* **openapi:** add OAuth 2.1 server endpoints and clarify OAuth modes ([#2165](https://github.com/supatype/server/issues/2165)) ([4bf316e](https://github.com/supatype/server/commit/4bf316e16d0708a67f25636698c8a0d56f2e9f0c))
* **passkeys:** add audit, metering, webauthn primitives ([86498bf](https://github.com/supatype/server/commit/86498bf1575f61bc55978f4a2db32d3c6ab9dd4f))
* **passkeys:** add configuration, error codes, and schemas ([b870832](https://github.com/supatype/server/commit/b8708324ffae62c0d92e39f31b6cf14e55670215))
* **passkeys:** progressive enrollment flow ([309a35e](https://github.com/supatype/server/commit/309a35e4edefc21c26276ba37f295f084a57246f))
* password changed email notification ([#2176](https://github.com/supatype/server/issues/2176)) ([25ce373](https://github.com/supatype/server/commit/25ce3732910dbd2cf913babee6b33da1b039c431))
* preserve rate limiters in memory across configuration reloads ([#1792](https://github.com/supatype/server/issues/1792)) ([d550e60](https://github.com/supatype/server/commit/d550e6062f03c2a98e93442ed4600db2f1f4805e))
* properly handle redirect url fragments and unusual hostnames ([#2200](https://github.com/supatype/server/issues/2200)) ([3c12656](https://github.com/supatype/server/commit/3c12656e09c44c83991eefd89871f783c82439ad))
* refactor hooks out of api package ([#1976](https://github.com/supatype/server/issues/1976)) ([abb11f8](https://github.com/supatype/server/commit/abb11f844f30fee424909308c070d096b52ec223))
* refactor mailer client wiring and add validation wrapper ([#2130](https://github.com/supatype/server/issues/2130)) ([b205bcc](https://github.com/supatype/server/commit/b205bcc53a8c599d03bd070b64c4d02b2742bf03))
* remove legacy lookup in users for one_time_tokens (phase II) ([#1569](https://github.com/supatype/server/issues/1569)) ([255f3f3](https://github.com/supatype/server/commit/255f3f3439fe4b2d8ac52ad72491a93c84841b19))
* replace JWT OAuth state with `flow_state.id` UUID ([#2331](https://github.com/supatype/server/issues/2331)) ([8ce398f](https://github.com/supatype/server/commit/8ce398fd7e944a370605ccc24c1f7c6418f0c531))
* reset main branch to 2.185.0 ([#2325](https://github.com/supatype/server/issues/2325)) ([fdd9979](https://github.com/supatype/server/commit/fdd9979a24fb6cfb51e59c8b3e5b19505a5bf83b))
* return validation failed error if captcha request was not json ([#1815](https://github.com/supatype/server/issues/1815)) ([91a141a](https://github.com/supatype/server/commit/91a141a1cee88a471142a6e0a10c28795f05d6e2))
* separate web3 rate limits from other `/token?grant_type=...` ([#1985](https://github.com/supatype/server/issues/1985)) ([77cbfcc](https://github.com/supatype/server/commit/77cbfcc54c7922754493302f233d5c10ed997489))
* **server:** add studio auth proxy routing and studioauth package ([09a12c4](https://github.com/supatype/server/commit/09a12c409bb7a1318e212fe97f9495fa8a026cb6))
* **server:** proxy /realtime/v1 to external WAL realtime ([0369ac6](https://github.com/supatype/server/commit/0369ac6a09d49e2a1edfaf4c4e1873bfa5bb0ded))
* **server:** proxy /realtime/v1 to external WAL realtime service ([7831b62](https://github.com/supatype/server/commit/7831b621c6d826f7a6d5a49d82549c08151bd083))
* **server:** REST GET cache with Valkey, admin API, and table cache config ([3d3ce32](https://github.com/supatype/server/commit/3d3ce32f0f14f1c1fbb9069c23ddb66c013665f9))
* set `email_verified` to true on all identities with the verified email ([#1902](https://github.com/supatype/server/issues/1902)) ([f167a2d](https://github.com/supatype/server/commit/f167a2d1d1c66908e13f27958d1cf9c44fbe2ae5))
* skip nonce check for Facebook Limited Login auth ([#2082](https://github.com/supatype/server/issues/2082)) ([b7575e3](https://github.com/supatype/server/commit/b7575e3a4959be1560913670f14a3898d0fb2238))
* store latest challenge/attestation data ([#2179](https://github.com/supatype/server/issues/2179)) ([01bba51](https://github.com/supatype/server/commit/01bba5170e26aadc8ce7193b06901b49b77acbfa))
* **supatype-server:** Phase 10.6A outer layer improvements ([60cbe3e](https://github.com/supatype/server/commit/60cbe3e269a6108151d59ba1e2438861195d8a44))
* **supatype-server:** realtime liveness and layered .env loading ([2ed5951](https://github.com/supatype/server/commit/2ed59517ebb8180e49ac50cf0ef19c28c35784da))
* support `transfer_sub` in apple id tokens ([#2162](https://github.com/supatype/server/issues/2162)) ([5760a48](https://github.com/supatype/server/commit/5760a48bf1a298c07fb44915ac85c7d7a58c2a4d))
* support custom oauth & oidc providers ([#2357](https://github.com/supatype/server/issues/2357)) ([f9ce7ee](https://github.com/supatype/server/commit/f9ce7ee56c0686a4ff5f9e8ef34e9941ef53f93c))
* support ledger solana offchain message signing ([#2093](https://github.com/supatype/server/issues/2093)) ([9922415](https://github.com/supatype/server/commit/99224150b3298a2e94e379fcb11570b3fc52a94f))
* support multiple `aud` for the external providers ([#2117](https://github.com/supatype/server/issues/2117)) ([432082d](https://github.com/supatype/server/commit/432082dae95aff7739c6cdd542a2db24ceef3e81))
* support percentage based db limits with reload support ([#2177](https://github.com/supatype/server/issues/2177)) ([1190dd7](https://github.com/supatype/server/commit/1190dd7215aaacd98164cc898e5f7abf3edf3836))
* switch Docker image publishing from GHCR to Docker Hub ([db720ce](https://github.com/supatype/server/commit/db720ce8dd79f0d17e5059e6d6428aa3cfc95699))
* switch to googleapis/release-please-action, bump to 2.166.0 ([#1883](https://github.com/supatype/server/issues/1883)) ([55ab3bf](https://github.com/supatype/server/commit/55ab3bfc68af0efe263ee68295d29556db75beae))
* Treat rate limit header value as comma-separated list ([#2282](https://github.com/supatype/server/issues/2282)) ([d110674](https://github.com/supatype/server/commit/d110674aeab6c027c5352666300bc684c3db40c8))
* update chi version ([#1581](https://github.com/supatype/server/issues/1581)) ([8d63c52](https://github.com/supatype/server/commit/8d63c52ee16c632d34ab12753f800b8190071f9d))
* upgrade existing sessions to v2 refresh tokens though config value ([#2356](https://github.com/supatype/server/issues/2356)) ([a0e1e66](https://github.com/supatype/server/commit/a0e1e668cee2785404683c15ac3b58521b194a36))
* upgrade otel to v1.26 ([#1585](https://github.com/supatype/server/issues/1585)) ([2c8a02d](https://github.com/supatype/server/commit/2c8a02da1a6107e3e8813f43462120d970cfbc23))
* use `global_user_id` over `sub` for `vercel_marketplace` issuer ([#1990](https://github.com/supatype/server/issues/1990)) ([54b8749](https://github.com/supatype/server/commit/54b8749e06bf4eb197dc2b54051b141968586c85))
* use `slices.Contains` instead of for loops ([#2111](https://github.com/supatype/server/issues/2111)) ([ecc23dd](https://github.com/supatype/server/commit/ecc23dd9897e321218c82e34305e0259e80de155))
* use embedded migrations for `migrate` command ([#1843](https://github.com/supatype/server/issues/1843)) ([044d5d3](https://github.com/supatype/server/commit/044d5d309cea291944870c154c0ff43e5d96a1dc))
* use largest avatar from spotify instead ([#1210](https://github.com/supatype/server/issues/1210)) ([96c0585](https://github.com/supatype/server/commit/96c0585955ef57ded7fe6e55e217086b3231fcb1)), closes [#1209](https://github.com/supatype/server/issues/1209)
* Vercel marketplace OIDC ([#1731](https://github.com/supatype/server/issues/1731)) ([3f643e5](https://github.com/supatype/server/commit/3f643e5bef47b96f28cb4a990840efb1024a366d))
* webauthn support schema changes, update openapi.yaml ([#2163](https://github.com/supatype/server/issues/2163)) ([8bacb37](https://github.com/supatype/server/commit/8bacb377cc0c27b400ab19d175fcfd9c074bf09a))


### Bug Fixes

* accept ID tokens from all `account.apple.com` and `appleid.apple.com` ([#2050](https://github.com/supatype/server/issues/2050)) ([1dd62e5](https://github.com/supatype/server/commit/1dd62e5ebf7a12abf04b85399cbeef05efaf5bae))
* add `id-token` permission to ci ([#2143](https://github.com/supatype/server/issues/2143)) ([08a9a17](https://github.com/supatype/server/commit/08a9a17bd8f29ecf2458b9b58e06685ebbef86b1))
* add `supafast` tarball for upgrading auth via supabase-admin-api ([#2009](https://github.com/supatype/server/issues/2009)) ([9b065e6](https://github.com/supatype/server/commit/9b065e6f9e216af63816c7ff7aa4b22a8e88d10f))
* add additional information around errors for missing content type header ([#1576](https://github.com/supatype/server/issues/1576)) ([9768e6b](https://github.com/supatype/server/commit/9768e6b8ccda6fa172eb8a78fbe6ec8aea103b7b))
* add error codes to password login flow ([#1721](https://github.com/supatype/server/issues/1721)) ([7ff389d](https://github.com/supatype/server/commit/7ff389d5447028ce1770d6716b11becc69aa3b11))
* add error codes to refresh token flow ([#1824](https://github.com/supatype/server/issues/1824)) ([2c566fd](https://github.com/supatype/server/commit/2c566fd79cbc543a5e94e9fcf8aaedcf712d009a))
* add ip based limiter ([#1622](https://github.com/supatype/server/issues/1622)) ([0ee43ee](https://github.com/supatype/server/commit/0ee43eef674d89c3054ed0c423e2a9af9f0820c6))
* add last_challenged_at field to mfa factors ([#1705](https://github.com/supatype/server/issues/1705)) ([a658954](https://github.com/supatype/server/commit/a65895415534c25c3947996a805bcb90e448e796))
* add MaxBytesReader middleware to limit request body size to 1MB ([#2402](https://github.com/supatype/server/issues/2402)) ([4296e1d](https://github.com/supatype/server/commit/4296e1d9b0ae6b6c58409ad373e0ca6969161671))
* add missing param ([#2125](https://github.com/supatype/server/issues/2125)) ([db16e29](https://github.com/supatype/server/commit/db16e295d6f4d59a985905739fa4d8c4c002070e))
* add missing provider info to signedup audit logs ([#2061](https://github.com/supatype/server/issues/2061)) ([d570068](https://github.com/supatype/server/commit/d570068ff34daf5b8e2d0322a145bd0ae1baa0a4))
* add test coverage for rate limits with 0 permitted events ([#1834](https://github.com/supatype/server/issues/1834)) ([675a230](https://github.com/supatype/server/commit/675a230d95e96e1bb07b495bbcd0b19f247fbb54))
* add token to hook payload for non-secure email change ([#1763](https://github.com/supatype/server/issues/1763)) ([725089e](https://github.com/supatype/server/commit/725089ef9177161b742c686dfc3ca65cc1ff8d1e))
* add twilio verify support on mfa ([#1714](https://github.com/supatype/server/issues/1714)) ([88331eb](https://github.com/supatype/server/commit/88331eb91ff7a6a68a9a8c9ada50a394a7ef2c99))
* additional provider and issuer checks ([#2326](https://github.com/supatype/server/issues/2326)) ([1aef05a](https://github.com/supatype/server/commit/1aef05a827ea1d3cb9dbb6220ca1a2bb4ad6f54b))
* admin user update should update is_anonymous field ([#1623](https://github.com/supatype/server/issues/1623)) ([bd13120](https://github.com/supatype/server/commit/bd13120b148b1d6db5fc5840a1bf721fcd1257ee))
* allow anonymous user to update password ([#1739](https://github.com/supatype/server/issues/1739)) ([8eedad9](https://github.com/supatype/server/commit/8eedad9d7d70be31c8f3b53a392f873f9d701001))
* allow enabling sms hook without setting up sms provider ([#1704](https://github.com/supatype/server/issues/1704)) ([d641586](https://github.com/supatype/server/commit/d641586edb5d858967b27f0d646271122818790c))
* allow HTTP with localhost in solana ([#2027](https://github.com/supatype/server/issues/2027)) ([da2c313](https://github.com/supatype/server/commit/da2c3138683ffcc7a579efba8d1a97be64946e30))
* amr claim should contain provider_id for sso method ([#2033](https://github.com/supatype/server/issues/2033)) ([70f2030](https://github.com/supatype/server/commit/70f20300c79a0151d64427af169d3d84a7b9b6e3))
* apply authorized email restriction to non-admin routes ([#1778](https://github.com/supatype/server/issues/1778)) ([2addd28](https://github.com/supatype/server/commit/2addd2801f15857ac212eea5c94a4dba0cc06325))
* apply mailer autoconfirm config to update user email ([#1646](https://github.com/supatype/server/issues/1646)) ([148db1a](https://github.com/supatype/server/commit/148db1a90a993997c6aee9862e906367fa234b5e))
* apply shared limiters before email / sms is sent ([#1748](https://github.com/supatype/server/issues/1748)) ([6d9a23b](https://github.com/supatype/server/commit/6d9a23b033ec5788c0c68b7fcf9fcd984c0ccef6))
* **auditlog:** keep writing to logs even postgres is disabled ([#2076](https://github.com/supatype/server/issues/2076)) ([570a2c6](https://github.com/supatype/server/commit/570a2c6e3635914b744fec46532b3ffe568ce43e))
* azure overage claims start with single `_` not two ([#1999](https://github.com/supatype/server/issues/1999)) ([16f3f50](https://github.com/supatype/server/commit/16f3f500b964008e3e9ffdd5b76f6c975106f551))
* bypass check for token & verify endpoints ([#1785](https://github.com/supatype/server/issues/1785)) ([e9c9fdc](https://github.com/supatype/server/commit/e9c9fdcaf679de6929b9d570570f8ca59ae5e56b))
* call write header in write if not written ([#1598](https://github.com/supatype/server/issues/1598)) ([b5c47da](https://github.com/supatype/server/commit/b5c47da517326d9478e69190d0428b37048d17dd))
* case-insensitive Bearer token scheme matching ([#2387](https://github.com/supatype/server/issues/2387)) ([7855c06](https://github.com/supatype/server/commit/7855c06197672095e2818b0befe64360b835a6ff))
* change phone constraint to per user ([#1713](https://github.com/supatype/server/issues/1713)) ([e0e5473](https://github.com/supatype/server/commit/e0e54738b0562955c95095a837a8efd049fba60f))
* change s3 artifact upload role ([#2145](https://github.com/supatype/server/issues/2145)) ([a7d3d9f](https://github.com/supatype/server/commit/a7d3d9f43aa8d9255a1006009de06287d61d82a1))
* check each type independently ([#2290](https://github.com/supatype/server/issues/2290)) ([b655660](https://github.com/supatype/server/commit/b6556606223bc70e2ce14b12afe8cd40ac800d20))
* check for empty aud string ([#1649](https://github.com/supatype/server/issues/1649)) ([cf9e3fd](https://github.com/supatype/server/commit/cf9e3fd54326bff36b103323b70a7bd2fb33bf0e))
* check if session is nil ([#1873](https://github.com/supatype/server/issues/1873)) ([9043256](https://github.com/supatype/server/commit/90432561d68bd87c58f0ef89d6044397a8a20e61))
* check password max length in checkPasswordStrength ([#1659](https://github.com/supatype/server/issues/1659)) ([4415111](https://github.com/supatype/server/commit/44151111fd99bc51b5dc3ae2a63a79aed207008a))
* **ci:** build server release from package main and verify ELF ([0627c7e](https://github.com/supatype/server/commit/0627c7e3a2ec8cdc40e7ff7f4c60cac2f1302879))
* clear staticcheck warnings in proxy and realtime ([42067a2](https://github.com/supatype/server/commit/42067a24807f95d02afb4273ac89c572b711822d))
* **cmd:** outer access logger deadlock and configurable level ([2dd3e60](https://github.com/supatype/server/commit/2dd3e6010f101ea89dd1b73fa694a3507f626a06))
* convert refreshed_at to UTC before updating ([#1916](https://github.com/supatype/server/issues/1916)) ([ca7b550](https://github.com/supatype/server/commit/ca7b550588788295a6279c9d58d0bcc28b139f48))
* correct casing of API key authentication in openapi.yaml ([965e4fb](https://github.com/supatype/server/commit/965e4fbd212d94a00c95236ef69fe8466db65d63))
* correct web authn aaguid column naming ([#1826](https://github.com/supatype/server/issues/1826)) ([4cd82ec](https://github.com/supatype/server/commit/4cd82ecac8cd8835a3f6b5109577e7dd2980f3b7))
* correctly parse JWT ValidMethods from env by enabling split_words ([#2334](https://github.com/supatype/server/issues/2334)) ([9d42ee0](https://github.com/supatype/server/commit/9d42ee000e56ee325ad9cebbf8ff29e6162a5eba))
* custom SMS does not work with Twilio Verify ([#1733](https://github.com/supatype/server/issues/1733)) ([e8d4d7b](https://github.com/supatype/server/commit/e8d4d7ba41f950e4ee6ae38885f784e4ccee0e84))
* deadlock issue with timeout middleware write ([#1595](https://github.com/supatype/server/issues/1595)) ([5470bb3](https://github.com/supatype/server/commit/5470bb3bad71527ab0fa075927fbeaf442546ded))
* default to files:read scope for Figma provider ([#1831](https://github.com/supatype/server/issues/1831)) ([0165835](https://github.com/supatype/server/commit/016583548bab83aee56ed5c3609e64f8895a5a55))
* define search path in auth functions ([#1616](https://github.com/supatype/server/issues/1616)) ([a5a7cfc](https://github.com/supatype/server/commit/a5a7cfc29a9d63165899fbebd70cbd487dc967c8))
* **deps:** bump Go to 1.25.11 for stdlib vuln fixes ([71f3290](https://github.com/supatype/server/commit/71f32900d5e5aaab07c29d71c1f4d617ffc42f41))
* **deps:** bump Go to 1.25.12 for stdlib vuln fixes ([e8c88a2](https://github.com/supatype/server/commit/e8c88a2630ad43c1c3c623bf5016642aa688e20c))
* **deps:** bump golang.org/x/text to v0.39.0 (GO-2026-5970) ([13d58c6](https://github.com/supatype/server/commit/13d58c6ff59439263ef939a9f89873e7fb4440a6))
* **deps:** resolve govulncheck failures ([a3e0cc7](https://github.com/supatype/server/commit/a3e0cc7a1da081fceafc3d1dff7690391f7d6eb6))
* **deps:** resolve govulncheck failures ([82183ea](https://github.com/supatype/server/commit/82183eac2ebbe099a6392373c092b204ce902129))
* **deps:** resolve second round of govulncheck failures ([ea513ce](https://github.com/supatype/server/commit/ea513cebe84a94e03dcf0f2f904d41a7491661c3))
* do not log fatal when http server successfully closes ([#2065](https://github.com/supatype/server/issues/2065)) ([7e4ef4a](https://github.com/supatype/server/commit/7e4ef4adc882a4d21b49bc9bd16afddaabcaaa2b))
* **docker:** bump build image to golang:1.25.10-alpine3.23 ([a735ae1](https://github.com/supatype/server/commit/a735ae14c12e026d7c6318d572a15ffd02f87c95))
* don't update attribute mapping if nil ([#1665](https://github.com/supatype/server/issues/1665)) ([126950c](https://github.com/supatype/server/commit/126950c53c6306100e1b6e5b4f822a7593e9ab38))
* drop the MFA_ENABLED config ([#1701](https://github.com/supatype/server/issues/1701)) ([9ec7d29](https://github.com/supatype/server/commit/9ec7d29f01bdae9614dff385dbed35df62957624))
* email header setting no longer misleading ([#1802](https://github.com/supatype/server/issues/1802)) ([ca56118](https://github.com/supatype/server/commit/ca56118762343276fc1efc3f16152542216e16f3))
* email_verified field not being updated on signup confirmation ([#1868](https://github.com/supatype/server/issues/1868)) ([ae14be6](https://github.com/supatype/server/commit/ae14be6225ad695fcfd37cd93fadb9d3a2e3bc70))
* email-sendhook - bug in email change verification ([#2044](https://github.com/supatype/server/issues/2044)) ([a57417b](https://github.com/supatype/server/commit/a57417b87da3336b5937aac44336962aa5a9e0f8))
* enable rls & update grants for auth tables ([#1617](https://github.com/supatype/server/issues/1617)) ([e5c1ea6](https://github.com/supatype/server/commit/e5c1ea68c8eaffa03e07a1033e8f09031e6fdcf7))
* enable SO_REUSEPORT in listener config ([#1936](https://github.com/supatype/server/issues/1936)) ([8559893](https://github.com/supatype/server/commit/85598931b7455d8fd8d9f27f667710809697e000))
* enforce authorized address checks on send email only ([#1806](https://github.com/supatype/server/issues/1806)) ([f1195b5](https://github.com/supatype/server/commit/f1195b5b1d7674c40243fed953a0e82d35fb545b))
* enforce uniqueness on verified phone numbers ([#1693](https://github.com/supatype/server/issues/1693)) ([c38d99d](https://github.com/supatype/server/commit/c38d99d6e55eb2d24adb114cfa1786e0ff58ad85))
* ensure request context exists in API db operations ([#2171](https://github.com/supatype/server/issues/2171)) ([d1da96f](https://github.com/supatype/server/commit/d1da96fd55a0325a010c302ef3d8a89aafde442d))
* explicit permisions on actions ([#1978](https://github.com/supatype/server/issues/1978)) ([a730198](https://github.com/supatype/server/commit/a730198263c766156d14b8068f89bc2042c2fc4d))
* expose `X-Supabase-Api-Version` header in CORS ([#1612](https://github.com/supatype/server/issues/1612)) ([eee8487](https://github.com/supatype/server/commit/eee8487b0dd83bd5d58ae5d7851a9412876d50d4))
* expose factor type on challenge ([#1709](https://github.com/supatype/server/issues/1709)) ([57484b1](https://github.com/supatype/server/commit/57484b1026e874e86120538ae1bff9e8cf8ec2e9))
* external host validation ([#1808](https://github.com/supatype/server/issues/1808)) ([03b658d](https://github.com/supatype/server/commit/03b658df4e57655625775cd96911464c453bcb44)), closes [#1228](https://github.com/supatype/server/issues/1228)
* fallback on btree indexes when hash is unavailable ([#1856](https://github.com/supatype/server/issues/1856)) ([f01eb6b](https://github.com/supatype/server/commit/f01eb6b651b9200792fc2a4614b561c14a5e8097))
* fix `getExcludedColumns` slice allocation ([#1788](https://github.com/supatype/server/issues/1788)) ([a55c044](https://github.com/supatype/server/commit/a55c0443ed72a01e0e991eb869b9a144109c3776))
* fix `supafast` tarball generation ([#2011](https://github.com/supatype/server/issues/2011)) ([042defa](https://github.com/supatype/server/commit/042defa3bd4bacc628fe3cd63c12b01347762125))
* Fix reqPath for bypass check for verify EP ([#1789](https://github.com/supatype/server/issues/1789)) ([dbe4213](https://github.com/supatype/server/commit/dbe4213e38a62ec23e5e6deca8f27866ca655faf))
* fix the wrong error return value ([#1950](https://github.com/supatype/server/issues/1950)) ([9d28e89](https://github.com/supatype/server/commit/9d28e893947775aed55f29f069a34109ff6decde))
* flaky index worker test ([#2366](https://github.com/supatype/server/issues/2366)) ([664beb7](https://github.com/supatype/server/commit/664beb7f4ce338499a7dc1889dc6612d4b6a7064))
* gosec incorrectly warns about accessing signature[64] ([#2222](https://github.com/supatype/server/issues/2222)) ([a3ed70e](https://github.com/supatype/server/commit/a3ed70e53e16d2c7e58413d29fc0af4e075e31c9))
* handle user banned error code ([#1851](https://github.com/supatype/server/issues/1851)) ([31135c0](https://github.com/supatype/server/commit/31135c0a174e89576da04c2735615d6351fe626b))
* hide hook name ([#1743](https://github.com/supatype/server/issues/1743)) ([780c115](https://github.com/supatype/server/commit/780c115621450c26ac8eb1a11655066664f898bd))
* **hooks:** propagate error objects from hook calls ([#2380](https://github.com/supatype/server/issues/2380)) ([25b7847](https://github.com/supatype/server/commit/25b784787764da91452104edf413c7b0a8811046))
* hostname can be empty with redirect urls ([#2241](https://github.com/supatype/server/issues/2241)) ([4c23081](https://github.com/supatype/server/commit/4c23081947c06bbbe614d222bf511d0540151fad))
* ignore errors if transaction has closed already ([#1726](https://github.com/supatype/server/issues/1726)) ([bf1bc43](https://github.com/supatype/server/commit/bf1bc4391dcce3e342e2e219a3b97572df25b75d))
* ignore not found error to check for pkce prefix later ([#1929](https://github.com/supatype/server/issues/1929)) ([12a0a6d](https://github.com/supatype/server/commit/12a0a6dacba083108dd3c2b60089f1a645787035))
* ignore rate limits for autoconfirm ([#1810](https://github.com/supatype/server/issues/1810)) ([9968467](https://github.com/supatype/server/commit/9968467199695dbcb57437086bd9115b2ea665ca))
* improve error messaging for http hooks ([#1821](https://github.com/supatype/server/issues/1821)) ([08090d5](https://github.com/supatype/server/commit/08090d525aee3e8d59702bfeae6a0207449a8c2c))
* improve invalid channel error message returned ([#1908](https://github.com/supatype/server/issues/1908)) ([2dc886b](https://github.com/supatype/server/commit/2dc886bbb22fd1c50e51a3523981487d3bbb9746))
* improve mfa verify logs ([#1635](https://github.com/supatype/server/issues/1635)) ([8c919a0](https://github.com/supatype/server/commit/8c919a02ff1c1593478e4d36e8530659beffb83e))
* improve saml assertion logging ([#1915](https://github.com/supatype/server/issues/1915)) ([1a4cfe5](https://github.com/supatype/server/commit/1a4cfe57b5c26c58f04562411236b2364f4cc1b3))
* improve session error logging ([#1655](https://github.com/supatype/server/issues/1655)) ([6f4cbee](https://github.com/supatype/server/commit/6f4cbee416c8ecbc0d12319199e2423327dda5e5))
* improve token OIDC logging ([#1606](https://github.com/supatype/server/issues/1606)) ([346d6ac](https://github.com/supatype/server/commit/346d6ac29120b2b740d7008e07403686725322ed))
* include factor_id in query ([#1702](https://github.com/supatype/server/issues/1702)) ([15fca1d](https://github.com/supatype/server/commit/15fca1d3c3b156afd7573d10c62b5dfeb188addc))
* **indexworker:** detect which schema `pg_trgm` exists in ([#2260](https://github.com/supatype/server/issues/2260)) ([badaf95](https://github.com/supatype/server/commit/badaf95ed8bccebeddf1411b16231d3cd78f27ee))
* **indexworker:** remove pg_trgm extension ([#2301](https://github.com/supatype/server/issues/2301)) ([c2e9be0](https://github.com/supatype/server/commit/c2e9be0f9686b23939786c2976f2ac62f38f7210))
* inline mailme package for easy development ([#1803](https://github.com/supatype/server/issues/1803)) ([6e14101](https://github.com/supatype/server/commit/6e141012f43b3be7de3f35b740ee835c03a6e590))
* invited users should have a temporary password generated ([#1644](https://github.com/supatype/server/issues/1644)) ([12be1f1](https://github.com/supatype/server/commit/12be1f1574c4840a541d02cb83ab36ebd9ff9f90))
* invites should send another email when user exists ([#2058](https://github.com/supatype/server/issues/2058)) ([42f5b7b](https://github.com/supatype/server/commit/42f5b7bd8134906a54dac0212d05f157dfdc87f6))
* japanese dot example fix ([#2243](https://github.com/supatype/server/issues/2243)) ([3f529e5](https://github.com/supatype/server/commit/3f529e56cf448042bcfd1dd53dd754782d1c693d))
* log version & migration count ([#1934](https://github.com/supatype/server/issues/1934)) ([345f477](https://github.com/supatype/server/commit/345f4774b679f2918620a18373b6e78095ffc64d))
* look for refresh token on mfa verification only in v1 ([#2249](https://github.com/supatype/server/issues/2249)) ([1348931](https://github.com/supatype/server/commit/134893163d28f2195523781626a8a7b06a1626b5))
* magiclink failing due to passwordStrength check ([#1769](https://github.com/supatype/server/issues/1769)) ([9e82eb1](https://github.com/supatype/server/commit/9e82eb162df28022864247e67760999e35a13ec8))
* maintain backward compatibility for asymmetric JWTs ([#1690](https://github.com/supatype/server/issues/1690)) ([0dae60c](https://github.com/supatype/server/commit/0dae60c4662890eb85d1b18e0c49441f06aedde0))
* make drop_uniqueness_constraint_on_phone idempotent ([#1817](https://github.com/supatype/server/issues/1817)) ([592f7b0](https://github.com/supatype/server/commit/592f7b0dc3055560e202aa53c9ab3385b4717257))
* **makefile:** remove invalid @ symbol from shell commands ([#2168](https://github.com/supatype/server/issues/2168)) ([ce7e52a](https://github.com/supatype/server/commit/ce7e52ae289c4590cede3006bbc7d64a68cf21c9))
* MFA NewFactor to default to creating unverfied factors ([#1692](https://github.com/supatype/server/issues/1692)) ([b26426b](https://github.com/supatype/server/commit/b26426b052ad74ff39e7609500cd7864e544fb71))
* mfa verify now works with refresh token algorithm v2 ([#2246](https://github.com/supatype/server/issues/2246)) ([5a9b3fc](https://github.com/supatype/server/commit/5a9b3fc9956c3dd7c3e7b8daede93a52f282e429))
* minor spelling errors ([#1688](https://github.com/supatype/server/issues/1688)) ([3fc992c](https://github.com/supatype/server/commit/3fc992ceb8a726b57de3ae6974a9b5f08e76e5d1)), closes [#1682](https://github.com/supatype/server/issues/1682)
* move is owned by check to load factor ([#1703](https://github.com/supatype/server/issues/1703)) ([5b96139](https://github.com/supatype/server/commit/5b961398eadd2098b91cdacf101f1545b6e299c4))
* **mux:** proxy GraphQL to PostgREST graphql_public RPC ([2c9c1a5](https://github.com/supatype/server/commit/2c9c1a58cec6b49cceda61859ce7853ffba0477e))
* new `odic.Provider` for apple with insecure issuer url context ([#2055](https://github.com/supatype/server/issues/2055)) ([7c43ee0](https://github.com/supatype/server/commit/7c43ee0ca05a87edac18714c517b7dfe936925b5))
* **oauth-server:** allow custom URI schemes in client redirect URIs ([#2298](https://github.com/supatype/server/issues/2298)) ([a653f22](https://github.com/supatype/server/commit/a653f222139aa07baa213b9a6c0c0575d61b0f50))
* **oauth2:** switch to Origin header for request validation ([#2174](https://github.com/supatype/server/issues/2174)) ([029abf5](https://github.com/supatype/server/commit/029abf5809e92615549358403a96dae64c9aa0e6))
* omit empty string from name & use case-insensitive equality for comparing SAML attributes ([#1654](https://github.com/supatype/server/issues/1654)) ([89d7765](https://github.com/supatype/server/commit/89d776539916b7629f9d50a4e5b52107dc777a12))
* **openapi:** add missing OAuth client registration fields ([#2227](https://github.com/supatype/server/issues/2227)) ([ac57314](https://github.com/supatype/server/commit/ac5731428dcd3f67081fd57667eafa847e80ec02))
* package server release artifacts consistently ([bd63290](https://github.com/supatype/server/commit/bd6329036ffb193711c5c83c26ebff0d79123665))
* **passkeys:** construct configuration env var correctly ([1a8e950](https://github.com/supatype/server/commit/1a8e950becd184ce1a1a32e5b0347432462d62d5))
* **passkeys:** enforce passkey cap during registration verify ([358b0e1](https://github.com/supatype/server/commit/358b0e140cbba8498153dd2de2b9c330c4a19128))
* **passkeys:** sign_count should be uint32 ([29d740e](https://github.com/supatype/server/commit/29d740e15613ae8f0dbd5e488610e83ead4e926e))
* possible panic if refresh token has a null session_id ([#1822](https://github.com/supatype/server/issues/1822)) ([61887e6](https://github.com/supatype/server/commit/61887e6e75acd6702b5c73c7769e8ca85ea31348))
* propagate error when when confirming phone ([#1939](https://github.com/supatype/server/issues/1939)) ([938fb15](https://github.com/supatype/server/commit/938fb1583088426c510c009627426313dac4a1d5))
* **proxy:** rewrite WebSocket upgrade path after StripPrefix ([cdfb7fb](https://github.com/supatype/server/commit/cdfb7fb7f76a486d2648906e7cf07506f7fdcd1f))
* **proxy:** rewrite WebSocket upgrade path after StripPrefix ([6f74c8a](https://github.com/supatype/server/commit/6f74c8a679255b3725558532d63bc232e774e392))
* publish to ghcr.io/supabase/auth ([#1626](https://github.com/supatype/server/issues/1626)) ([a259356](https://github.com/supatype/server/commit/a259356fac0133ed4f40c149ce78aca55eb7a186)), closes [#1625](https://github.com/supatype/server/issues/1625)
* rate limits of 0 take precedence over MAILER_AUTO_CONFIRM ([#1837](https://github.com/supatype/server/issues/1837)) ([67c22b4](https://github.com/supatype/server/commit/67c22b40df10c06ac7b72eb2c3a40ee28efa596b))
* redirect invalid state errors to site url ([#1722](https://github.com/supatype/server/issues/1722)) ([f4191b4](https://github.com/supatype/server/commit/f4191b4c86640591b24d5d6bd8b285ad09e6ecc9))
* redirects must not be to ip addresses ([#1984](https://github.com/supatype/server/issues/1984)) ([c40d924](https://github.com/supatype/server/commit/c40d92472f06c5e7a08ab8823840b57f1e95d2cc))
* refactor mfa models and add observability to loadFactor ([#1669](https://github.com/supatype/server/issues/1669)) ([a974db5](https://github.com/supatype/server/commit/a974db5d8b6ec5ebc202418b09056d0509be1435))
* refactor mfa validation into functions ([#1780](https://github.com/supatype/server/issues/1780)) ([ebd2ab6](https://github.com/supatype/server/commit/ebd2ab62749e9254b90a9780eb91ec22db674399))
* refactor TOTP MFA into separate methods ([#1698](https://github.com/supatype/server/issues/1698)) ([4e212fb](https://github.com/supatype/server/commit/4e212fb3d042c0d0aeec331c897fb7ccdcdaf5db))
* **release:** skip existing RC tags and make upload idempotent ([1101d78](https://github.com/supatype/server/commit/1101d7882a7b3ea1142ccba2ed3219eb1b6499e2))
* reloader unittest races on writeWg ([#2352](https://github.com/supatype/server/issues/2352)) ([1570eac](https://github.com/supatype/server/commit/1570eac2856efcae986578c82e44a1bfae1c789e))
* remove azure claim overage code. ([#2005](https://github.com/supatype/server/issues/2005)) ([e3710f8](https://github.com/supatype/server/commit/e3710f84faaf0a5269e2d3cadaa7513392c4ed09))
* remove check for content-length ([#1700](https://github.com/supatype/server/issues/1700)) ([77b86ab](https://github.com/supatype/server/commit/77b86ab6b7ce33701ed4361dff7008ec7b39032d))
* remove FindFactorsByUser ([#1707](https://github.com/supatype/server/issues/1707)) ([226c982](https://github.com/supatype/server/commit/226c9827fd807674ecf1f148f4a180273289cc6f))
* remove requirement of empty content-type on 204 ([#2128](https://github.com/supatype/server/issues/2128)) ([19966fa](https://github.com/supatype/server/commit/19966fa778e93e54d9398120de65be8c47b072a9))
* remove server side cookie token methods ([#1742](https://github.com/supatype/server/issues/1742)) ([973f7b6](https://github.com/supatype/server/commit/973f7b601e6c49bc8e285a9f54d9d5d21c4dfccf))
* remove TOTP field for phone enroll response ([#1717](https://github.com/supatype/server/issues/1717)) ([1b44f79](https://github.com/supatype/server/commit/1b44f79e9d7ec35358684357c2f54f3c354ad630))
* remove unused object storage path helpers ([c31caab](https://github.com/supatype/server/commit/c31caab8e0a9e41dd9d93a2bf560da132a92994b))
* resolving azure overage claim should include `api-version=1.6` query parameter ([#2000](https://github.com/supatype/server/issues/2000)) ([c0b71cc](https://github.com/supatype/server/commit/c0b71cc13d42e1d4552db28e01559b8e7c6cf0c2))
* restrict autoconfirm email change to anonymous users ([#1679](https://github.com/supatype/server/issues/1679)) ([b4fd4f0](https://github.com/supatype/server/commit/b4fd4f03b52e383ab0b3af4b33c7e71f58121c45))
* return oauth identity when user is created ([#1736](https://github.com/supatype/server/issues/1736)) ([61bd3c7](https://github.com/supatype/server/commit/61bd3c7cc3bb1b6448cb6e78e5864db7ca3a0dd6))
* return proper error if sms rate limit is exceeded ([#1647](https://github.com/supatype/server/issues/1647)) ([059fdd0](https://github.com/supatype/server/commit/059fdd01b021970d73a7963f9bf796da6e534a88))
* return the error code instead of status code ([#1855](https://github.com/supatype/server/issues/1855)) ([7a908dd](https://github.com/supatype/server/commit/7a908dd17fe18dd4f5ece050de7ade1d9dd9679a))
* Revert "fix: revert fallback on btree indexes when hash is unavailable" ([#1859](https://github.com/supatype/server/issues/1859)) ([f8f7a73](https://github.com/supatype/server/commit/f8f7a73eb0d304c3d749e2dc974fb41068662360))
* revert define search path in auth functions ([#1634](https://github.com/supatype/server/issues/1634)) ([11d8111](https://github.com/supatype/server/commit/11d8111ebd55556584b7f1771ae0328ee7e54093))
* revert fallback on btree indexes when hash is unavailable ([#1858](https://github.com/supatype/server/issues/1858)) ([8b1f8f3](https://github.com/supatype/server/commit/8b1f8f3dc884306c558e27e30489af7770c1b936))
* run release-please again ([#2144](https://github.com/supatype/server/issues/2144)) ([56c9a14](https://github.com/supatype/server/commit/56c9a1406139b30b0c46718a47b0cb3f5650f846))
* sanitize redirect URL (remove fragment, query) before pattern matching ([#1974](https://github.com/supatype/server/issues/1974)) ([b057e74](https://github.com/supatype/server/commit/b057e74a1b3f53fdef1454c36032b3d7db9b74cc))
* satisfy release security checks ([a64ef51](https://github.com/supatype/server/commit/a64ef5191de42e40fb16393ba94bfb235b119a7d))
* serialize jwt as string ([#1657](https://github.com/supatype/server/issues/1657)) ([f842ddc](https://github.com/supatype/server/commit/f842ddc7275d194820f810853be34efdcb726f1a))
* **server:** handle db.Close() errors in server.New bootstrap (gosec G104) ([711b4bf](https://github.com/supatype/server/commit/711b4bf33a229d200227a3bbc693db13da4334a8))
* session upgrade percentage should be based on session, not request ([#2371](https://github.com/supatype/server/issues/2371)) ([acef167](https://github.com/supatype/server/commit/acef167323f262b05b40659d937542bf6230da14))
* set rate limit log level to warn ([#1652](https://github.com/supatype/server/issues/1652)) ([15ceaa1](https://github.com/supatype/server/commit/15ceaa1881873b6ec95a54c427bcaa0d06e86fae))
* simplify WaitForCleanup ([#1747](https://github.com/supatype/server/issues/1747)) ([3687783](https://github.com/supatype/server/commit/36877835ae16aac17a69f1ca3fbe31240c861ed3))
* skip apple oidc issuer check ([#2053](https://github.com/supatype/server/issues/2053)) ([c100935](https://github.com/supatype/server/commit/c10093566e2cbdc2744822d3aa855177ef550aa0))
* skip cleanup for non-2xx status ([#1877](https://github.com/supatype/server/issues/1877)) ([86e8034](https://github.com/supatype/server/commit/86e8034611bb7d59fa68c519a113af7ec7cac86e))
* **social-auth:** default to current_user:read for Figma provider ([#2195](https://github.com/supatype/server/issues/2195)) ([affc1b3](https://github.com/supatype/server/commit/affc1b33eca8f17735eda0e4c5a2758fe210c377))
* stripped binary now includes version ([#2147](https://github.com/supatype/server/issues/2147)) ([3f865fa](https://github.com/supatype/server/commit/3f865faeeb61d06d3d7521e285d77008ff62ff12))
* **studioauth:** read admin config via OpenRoot to satisfy gosec G304 ([5c719a8](https://github.com/supatype/server/commit/5c719a827b2ae8bc954e9b1db271e6fa949155ac))
* **studioauth:** use restrictive file modes in roles tests for gosec ([6f11bbf](https://github.com/supatype/server/commit/6f11bbf12afeb8251a6a2d1ff67cd8aeaa7a1555))
* **test:** repair pre-existing make test failures ([8b490e4](https://github.com/supatype/server/commit/8b490e4a829cfc24a468ce4113cd19a49c0c1126))
* tighten email validation rules ([#2304](https://github.com/supatype/server/issues/2304)) ([6ccfa52](https://github.com/supatype/server/commit/6ccfa52691dd02f18f2e2467c901f8ab424382f9))
* treat `GOTRUE_MFA_ENABLED` as meaning TOTP enabled on enroll and verify ([#1694](https://github.com/supatype/server/issues/1694)) ([3783577](https://github.com/supatype/server/commit/3783577ac8ea09253af9e82d22db329038b1ed9a))
* treat empty string as nil in `encrypted_password` ([#1663](https://github.com/supatype/server/issues/1663)) ([44b6e0c](https://github.com/supatype/server/commit/44b6e0c7d99c917fe41b7b84214d74c6e9e354db))
* update aal requirements to update user ([#1766](https://github.com/supatype/server/issues/1766)) ([b71a30b](https://github.com/supatype/server/commit/b71a30b137eeed8a26733d38350b8efb7c416d29))
* update contributing to use v1.22 ([#1609](https://github.com/supatype/server/issues/1609)) ([3073845](https://github.com/supatype/server/commit/30738458350fa0863d00b366ba3e7696634d7fb9))
* update copyright year in LICENSE ([#2142](https://github.com/supatype/server/issues/2142)) ([ad70b14](https://github.com/supatype/server/commit/ad70b148dc6e56f943ecb464d60651e6eaca619d))
* update figma token endpoint ([#1952](https://github.com/supatype/server/issues/1952)) ([645b8c9](https://github.com/supatype/server/commit/645b8c906a4137ba03c963c2150a9a7e2a9312a7))
* update ip mismatch error message ([#1849](https://github.com/supatype/server/issues/1849)) ([a19bfd0](https://github.com/supatype/server/commit/a19bfd071dc077cf356fdaf603f45f25f6e90ac7))
* update MaxFrequency error message to reflect number of seconds ([#1540](https://github.com/supatype/server/issues/1540)) ([ca7e25e](https://github.com/supatype/server/commit/ca7e25ee73f84c647cfda4092f9163d554f33317))
* update mfa admin methods ([#1774](https://github.com/supatype/server/issues/1774)) ([1671023](https://github.com/supatype/server/commit/1671023e4e9a1e273a2ef984bf5f03e3509e235b))
* update mfa phone migration to be idempotent ([#1687](https://github.com/supatype/server/issues/1687)) ([7444b28](https://github.com/supatype/server/commit/7444b28fd407ec869c002ea7a841d148d8671065))
* update migration version ([#2343](https://github.com/supatype/server/issues/2343)) ([e730121](https://github.com/supatype/server/commit/e730121fc1a5cc6a0889fc48f96121d92245c9d1))
* update OpenAPI schema to use 'minimum' instead of 'min' for integer ([69d22c6](https://github.com/supatype/server/commit/69d22c6a32ad599cd8facdc3f360f0426780329b))
* update openapi spec for MFA (Phone)  ([#1689](https://github.com/supatype/server/issues/1689)) ([4314bce](https://github.com/supatype/server/commit/4314bce0b4dc9b5f6caa16a80a668093ea3d09a4))
* upgrade ci Go version ([#1782](https://github.com/supatype/server/issues/1782)) ([b880217](https://github.com/supatype/server/commit/b880217545e4b0747f534ad457c996b233960c1c))
* upgrade godotenv to v1.5.1 to fix multiline file loading ([#1997](https://github.com/supatype/server/issues/1997)) ([0b0034c](https://github.com/supatype/server/commit/0b0034c3e19ede9e85312030ea31f88046c34f36))
* upgrade golang-jwt to v5 ([#1639](https://github.com/supatype/server/issues/1639)) ([1de01f6](https://github.com/supatype/server/commit/1de01f6fd42a2d38b16f0fb93beaeb7c0535de60))
* use `appleid.apple.com` as default issuer ([#2068](https://github.com/supatype/server/issues/2068)) ([8676872](https://github.com/supatype/server/commit/8676872ffe8a63794fb18ccb312208f3c727f6b6))
* use `split_words` config option for `AuditLog` ([#2075](https://github.com/supatype/server/issues/2075)) ([bb0e781](https://github.com/supatype/server/commit/bb0e781497cbeadf59b88709d624ad8bafd1049f))
* use deep equal ([#1672](https://github.com/supatype/server/issues/1672)) ([913c5c8](https://github.com/supatype/server/commit/913c5c853dc0d10c73d98733dbd9f4ae5b2f9001))
* use pointer for `user.EncryptedPassword` ([#1637](https://github.com/supatype/server/issues/1637)) ([814e0b2](https://github.com/supatype/server/commit/814e0b2f5d46616b30187c900f824194a7a25ef7))
* use redirect URL as-is for mobile apps ([#2007](https://github.com/supatype/server/issues/2007)) ([a53f0b2](https://github.com/supatype/server/commit/a53f0b275709d9dbcae98af72f59b6310a6e0897))
* use signing jwk to sign oauth state ([#1728](https://github.com/supatype/server/issues/1728)) ([5be0a9e](https://github.com/supatype/server/commit/5be0a9ebd0f88875e13ae36c618a4fc46527157f))
* use sys/unix instead of syscall ([#1953](https://github.com/supatype/server/issues/1953)) ([0e0f3c9](https://github.com/supatype/server/commit/0e0f3c9c3de8bccc5fe633e1bab18334fdc5bc73))
* user sanitization should clean up email change info too ([#1759](https://github.com/supatype/server/issues/1759)) ([00c09c2](https://github.com/supatype/server/commit/00c09c2032d953bffc6bea63165e19f57339f608))
* validateEmail should normalise emails ([#1790](https://github.com/supatype/server/issues/1790)) ([619b90d](https://github.com/supatype/server/commit/619b90d21076eab3dca951c7043c8434476d4fd0))

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


### Features

* add `.well-known/openid-configuration` ([#2197](https://github.com/supatype/auth/issues/2197)) ([9a8d0df](https://github.com/supatype/auth/commit/9a8d0df63bb5089e1705f9d970669bfc97ed345e))
* add `auth_migration` annotation for the migrations ([#2234](https://github.com/supatype/auth/issues/2234)) ([b276d0b](https://github.com/supatype/auth/commit/b276d0bcf4d1ee08fce8c2f7146423e9aaf34dfb))
* add `password_hash` and `id` fields to admin create user ([#1641](https://github.com/supatype/auth/issues/1641)) ([20d59f1](https://github.com/supatype/auth/commit/20d59f10b601577683d05bcd7d2128ff4bc462a0))
* add `x-sb-error-code` header, show error code in logs ([#1765](https://github.com/supatype/auth/issues/1765)) ([ed91c59](https://github.com/supatype/auth/commit/ed91c59aa332738bd0ac4b994aeec2cdf193a068))
* add advisor to notify you when to double the max connection pool ([#2167](https://github.com/supatype/auth/issues/2167)) ([a72f5d9](https://github.com/supatype/auth/commit/a72f5d95795ac070e248007c0c38196f47ea5046))
* add after-user-created hook ([#2169](https://github.com/supatype/auth/issues/2169)) ([bd80df8](https://github.com/supatype/auth/commit/bd80df8a888a7de023557a97b65b21419d3029e7))
* add an optional burstable rate limiter ([#1924](https://github.com/supatype/auth/issues/1924)) ([1f06f58](https://github.com/supatype/auth/commit/1f06f58e1434b91612c0d96c8c0435d26570f3e2))
* add array attribute mapping for SAML ([#1526](https://github.com/supatype/auth/issues/1526)) ([7326285](https://github.com/supatype/auth/commit/7326285c8af5c42e5c0c2d729ab224cf33ac3a1f))
* add asymmetric jwt support ([#1674](https://github.com/supatype/auth/issues/1674)) ([c7a2be3](https://github.com/supatype/auth/commit/c7a2be347b301b666e99adc3d3fed78c5e287c82))
* add authorized email address support ([#1757](https://github.com/supatype/auth/issues/1757)) ([f3a28d1](https://github.com/supatype/auth/commit/f3a28d182d193cf528cc72a985dfeaf7ecb67056))
* Add custom claims from Keycloak user token ([#1917](https://github.com/supatype/auth/issues/1917)) ([1365aaa](https://github.com/supatype/auth/commit/1365aaa45569fc9e7c3497e744e0e80cf237d617))
* add custom sms hook ([#1474](https://github.com/supatype/auth/issues/1474)) ([0f6b29a](https://github.com/supatype/auth/commit/0f6b29a46f1dcbf92aa1f7cb702f42e7640f5f93))
* Add email send operation metrics ([#2311](https://github.com/supatype/auth/issues/2311)) ([0096575](https://github.com/supatype/auth/commit/00965758762301875df2d7e4e552b2346bc09236))
* add email validation function to lower bounce rates ([#1845](https://github.com/supatype/auth/issues/1845)) ([2c291f0](https://github.com/supatype/auth/commit/2c291f0356f3e91063b6b43bf2a21625b0ce0ebd))
* add error codes ([#1377](https://github.com/supatype/auth/issues/1377)) ([e4beea1](https://github.com/supatype/auth/commit/e4beea1cdb80544b0581f1882696a698fdf64938))
* add hook log entry with `run_hook` action ([#1684](https://github.com/supatype/auth/issues/1684)) ([46491b8](https://github.com/supatype/auth/commit/46491b867a4f5896494417391392a373a453fa5f))
* add is_anonymous claim to Auth hook jsonschema ([#1667](https://github.com/supatype/auth/issues/1667)) ([f9df65c](https://github.com/supatype/auth/commit/f9df65c91e226084abfa2e868ab6bab892d16d2f))
* add kakao OIDC ([#1381](https://github.com/supatype/auth/issues/1381)) ([b5566e7](https://github.com/supatype/auth/commit/b5566e7ac001cc9f2bac128de0fcb908caf3a5ed))
* add mail header support via `GOTRUE_SMTP_HEADERS` with `$messageType` ([#1804](https://github.com/supatype/auth/issues/1804)) ([99d6a13](https://github.com/supatype/auth/commit/99d6a134c44554a8ad06695e1dff54c942c8335d))
* add max length check for email ([#1508](https://github.com/supatype/auth/issues/1508)) ([f9c13c0](https://github.com/supatype/auth/commit/f9c13c0ad5c556bede49d3e0f6e5f58ca26161c3))
* add metadata field to all hooks ([#2365](https://github.com/supatype/auth/issues/2365)) ([c675749](https://github.com/supatype/auth/commit/c67574946d1e11c7986d2c868336df0cefbe3452))
* add MFA for WebAuthn ([#1775](https://github.com/supatype/auth/issues/1775)) ([8cc2f0e](https://github.com/supatype/auth/commit/8cc2f0e14d06d0feb56b25a0278fda9e213b6b5a))
* add OAuth client type ([#2152](https://github.com/supatype/auth/issues/2152)) ([b118f1f](https://github.com/supatype/auth/commit/b118f1f00c3c846095c25c34092e38aeebfdf2db))
* add oauth2 client support ([#2098](https://github.com/supatype/auth/issues/2098)) ([8fae015](https://github.com/supatype/auth/commit/8fae01581d122bba95a3742dc212284f9a21dc4d))
* add option to disable magic links ([#1756](https://github.com/supatype/auth/issues/1756)) ([2ad0737](https://github.com/supatype/auth/commit/2ad07373aa9239eba94abdabbb01c9abfa8c48de))
* add option to disable writing to `audit_log_entries` ([#2073](https://github.com/supatype/auth/issues/2073)) ([80758dd](https://github.com/supatype/auth/commit/80758dd880b82e9b96d7185d9d0a0850b8c6f19d))
* add phone to sms webhook payload ([#2160](https://github.com/supatype/auth/issues/2160)) ([d475ac1](https://github.com/supatype/auth/commit/d475ac1f20a0814f59d4bc1370801f915a9ba4d4))
* add SAML specific external URL config ([#1599](https://github.com/supatype/auth/issues/1599)) ([b352719](https://github.com/supatype/auth/commit/b3527190560381fafe9ba2fae4adc3b73703024a))
* Add Sb-Forwarded-For header and IP-based rate limiting ([#2295](https://github.com/supatype/auth/issues/2295)) ([e8f679b](https://github.com/supatype/auth/commit/e8f679b9e8fcd8cb543ed43cd9cd6a73bbbf4fa7))
* add send email Hook ([#1512](https://github.com/supatype/auth/issues/1512)) ([cf42e02](https://github.com/supatype/auth/commit/cf42e02ec63779f52b1652a7413f64994964c82d))
* add sign in with ethereum ([#2069](https://github.com/supatype/auth/issues/2069)) ([079b242](https://github.com/supatype/auth/commit/079b2427b8ed312880b60e89cc79b716fe9ae73d))
* add sign in with solana (EIP-4361) support ([#1918](https://github.com/supatype/auth/issues/1918)) ([d121546](https://github.com/supatype/auth/commit/d1215464d4c81bb6e2e210df81ba0263d90ffb64))
* add snapchat provider ([#2071](https://github.com/supatype/auth/issues/2071)) ([fca8ea4](https://github.com/supatype/auth/commit/fca8ea4a701eafb587438a159e19f5488c82a178))
* add Supabase Auth identifier to OAuth redirect URLs ([#2299](https://github.com/supatype/auth/issues/2299)) ([2d3dbc6](https://github.com/supatype/auth/commit/2d3dbc652c1beb47c2eade28b45e94f6e2c56982))
* add support for account changes notifications in email send hook ([#2192](https://github.com/supatype/auth/issues/2192)) ([6b382ae](https://github.com/supatype/auth/commit/6b382ae3a96bbe052395bdfa30fb49f717e5ad68))
* add support for Azure CIAM login ([#1541](https://github.com/supatype/auth/issues/1541)) ([1cb4f96](https://github.com/supatype/auth/commit/1cb4f96bdc7ef3ef995781b4cf3c4364663a2bf3))
* add support for managing SSO providers by resource_id ([#2081](https://github.com/supatype/auth/issues/2081)) ([5ca4489](https://github.com/supatype/auth/commit/5ca44893964d3b12a24ea26302b23f4976f768a0))
* add support for migration of firebase scrypt passwords ([#1768](https://github.com/supatype/auth/issues/1768)) ([ba00f75](https://github.com/supatype/auth/commit/ba00f75c28d6708ddf8ee151ce18f2d6193689ef))
* add support for saml encrypted assertions ([#1752](https://github.com/supatype/auth/issues/1752)) ([c5480ef](https://github.com/supatype/auth/commit/c5480ef83248ec2e7e3d3d87f92f43f17161ed25))
* add support for Slack OAuth V2 ([#1591](https://github.com/supatype/auth/issues/1591)) ([bb99251](https://github.com/supatype/auth/commit/bb992519cdf7578dc02cd7de55e2e6aa09b4c0f3))
* add support for verifying argon2i and argon2id passwords ([#1597](https://github.com/supatype/auth/issues/1597)) ([55409f7](https://github.com/supatype/auth/commit/55409f797bea55068a3fafdddd6cfdb78feba1b4))
* add support packages for end-to-end testing ([#2021](https://github.com/supatype/auth/issues/2021)) ([269ddfe](https://github.com/supatype/auth/commit/269ddfe18718ae74535f7227eb75f67667275140))
* add timeout middleware ([#1529](https://github.com/supatype/auth/issues/1529)) ([f96ff31](https://github.com/supatype/auth/commit/f96ff31040b28e3a7373b4fd41b7334eda1b413e))
* add webauthn configuration variables ([#1773](https://github.com/supatype/auth/issues/1773)) ([77d5897](https://github.com/supatype/auth/commit/77d58976ae624dbb7f8abee041dd4557aab81109))
* allow amr claim to be array of strings or objects ([#2274](https://github.com/supatype/auth/issues/2274)) ([607da43](https://github.com/supatype/auth/commit/607da43b697b0af1de0da5f966f5b63ff033fefb))
* allow for postgres and http functions on each extensibility point ([#1528](https://github.com/supatype/auth/issues/1528)) ([348a1da](https://github.com/supatype/auth/commit/348a1daee24f6e44b14c018830b748e46d34b4c2))
* allow invalid config directories ([#1969](https://github.com/supatype/auth/issues/1969)) ([6b842f6](https://github.com/supatype/auth/commit/6b842f6b304bba5f886c6bf8b5675d914f881a2d))
* allow limiting lifespan of low-aal sessions ([#1942](https://github.com/supatype/auth/issues/1942)) ([d7a9ca6](https://github.com/supatype/auth/commit/d7a9ca62a7a09edd864f0b968c1882f5e464e662))
* async, concurrent index creation for users table ([#2239](https://github.com/supatype/auth/issues/2239)) ([a1146bf](https://github.com/supatype/auth/commit/a1146bf7eecb35e237350dda7ae62328cbb5acfe))
* background template reloading p1 - baseline decomposition ([#2148](https://github.com/supatype/auth/issues/2148)) ([746c937](https://github.com/supatype/auth/commit/746c937f7c57ba256d942df334ab9ee354509587))
* Block specific outgoing mail servers ([#1971](https://github.com/supatype/auth/issues/1971)) ([091aef9](https://github.com/supatype/auth/commit/091aef945a764ee8d3b80ae8c5ed5d88dd582d03))
* cache OIDC discovery documents for providers ([#2389](https://github.com/supatype/auth/issues/2389)) ([40d07b5](https://github.com/supatype/auth/commit/40d07b5f50ec4dce5c5a27e405097bc90c027000))
* check current password on change ([#2364](https://github.com/supatype/auth/issues/2364)) ([33b87ae](https://github.com/supatype/auth/commit/33b87ae0671aba2e9b4df0ef1d5d1e7906c32129))
* clean up expired factors ([#1371](https://github.com/supatype/auth/issues/1371)) ([5c94207](https://github.com/supatype/auth/commit/5c9420743a9aef0675f823c30aa4525b4933836e))
* config reloading ([#1771](https://github.com/supatype/auth/issues/1771)) ([6ee0091](https://github.com/supatype/auth/commit/6ee009163bfe451e2a0b923705e073928a12c004))
* config reloading with fsnotify, poller fallback, and signals ([#2161](https://github.com/supatype/auth/issues/2161)) ([c77d512](https://github.com/supatype/auth/commit/c77d51203fc52c1c9a9f7dc56ca1c076e018fc54))
* configurable email and sms rate limiting ([#1800](https://github.com/supatype/auth/issues/1800)) ([5e94047](https://github.com/supatype/auth/commit/5e9404717e1c962ab729cde150ef5b40ea31a6e8))
* configurable NameID format for SAML provider ([#1481](https://github.com/supatype/auth/issues/1481)) ([ef405d8](https://github.com/supatype/auth/commit/ef405d89e69e008640f275bc37f8ec02ad32da40))
* cover 100% of crypto with tests ([#1892](https://github.com/supatype/auth/issues/1892)) ([174198e](https://github.com/supatype/auth/commit/174198e56f8e9b8470a717d0021c626130288d2e))
* email address changed notification ([#2181](https://github.com/supatype/auth/issues/2181)) ([047f851](https://github.com/supatype/auth/commit/047f85136c9223ca99cb0169ba82343088fbbfd8))
* encrypt sensitive columns ([#1593](https://github.com/supatype/auth/issues/1593)) ([e4a4758](https://github.com/supatype/auth/commit/e4a475820b2dc1f985bd37df15a8ab9e781626f5))
* enhance issuer URL validation in OAuth server metadata ([#2164](https://github.com/supatype/auth/issues/2164)) ([a9424d2](https://github.com/supatype/auth/commit/a9424d25909e074db395b620dc9999724bf4a03c))
* enhance login analytics ([#2078](https://github.com/supatype/auth/issues/2078)) ([1aed4a2](https://github.com/supatype/auth/commit/1aed4a27fdc54d9c4d01f17d49dcaadb25400f18))
* experimental own linking domains per provider ([#2119](https://github.com/supatype/auth/issues/2119)) ([747bf3b](https://github.com/supatype/auth/commit/747bf3b15fd9e371c9330e75fe2e5de8b89ce14d))
* fallback to jwt secret if alg is `HS256` and the `kid` is not recognized ([#2072](https://github.com/supatype/auth/issues/2072)) ([8fa99bd](https://github.com/supatype/auth/commit/8fa99bd6cab91c0bf093fdcdb912054113ea66ba))
* fetch email from snapchat oauth provider if available for consistency ([#2110](https://github.com/supatype/auth/issues/2110)) ([7507822](https://github.com/supatype/auth/commit/750782246e736093131ba2eb1015fc73083d99ab))
* fix argon2 parsing and comparison ([#1887](https://github.com/supatype/auth/issues/1887)) ([9dbe6ef](https://github.com/supatype/auth/commit/9dbe6ef931ae94e621d55a5f7aea4b7ee0449949))
* fix large group claim handling in azure id tokens ([#1995](https://github.com/supatype/auth/issues/1995)) ([2f323fe](https://github.com/supatype/auth/commit/2f323fe3ce2c1d24343d822ac093f28fdda3a4a9))
* forbid generating an access token without a session ([#1504](https://github.com/supatype/auth/issues/1504)) ([795e93d](https://github.com/supatype/auth/commit/795e93d0afbe94bcd78489a3319a970b7bf8e8bc))
* hooks round 2 - remove indirection and simplify error handling ([#2025](https://github.com/supatype/auth/issues/2025)) ([26e23f0](https://github.com/supatype/auth/commit/26e23f05acd1e1a959c3e04764a569ea0364d947))
* hooks round 4 - update tests to use require package ([#2030](https://github.com/supatype/auth/issues/2030)) ([aaf93df](https://github.com/supatype/auth/commit/aaf93df50ebfb489c6335e2c1b846dc5cee18767))
* hooks round 5 (Option 2) - add before-user-created hook ([#2034](https://github.com/supatype/auth/issues/2034)) ([b53f6b0](https://github.com/supatype/auth/commit/b53f6b0d0e056bf3e84884847ab4608ffc9efd61))
* HTTP Hook - Add custom envconfig decoding for HTTP Hook Secrets ([#1467](https://github.com/supatype/auth/issues/1467)) ([5b24c4e](https://github.com/supatype/auth/commit/5b24c4eb05b2b52c4177d5f41cba30cb68495c8c))
* identity linked/unlinked notifications ([#2185](https://github.com/supatype/auth/issues/2185)) ([7d46936](https://github.com/supatype/auth/commit/7d46936e145479be1e508b52549c7fca3c59fc2f))
* ignore `aud` claim from admin jwt (`service_role` never had one) ([#2070](https://github.com/supatype/auth/issues/2070)) ([57eddcb](https://github.com/supatype/auth/commit/57eddcb45ce97004c26f6d65351447d7dc654162))
* implement link identity with oidc / native sign in ([#2108](https://github.com/supatype/auth/issues/2108)) ([5f0ec87](https://github.com/supatype/auth/commit/5f0ec8709231c57b57aa06160e18bc9e52ec9002))
* implement OAuth2 authorization endpoint ([#2107](https://github.com/supatype/auth/issues/2107)) ([5318552](https://github.com/supatype/auth/commit/53185526b07cb2c27f6a81782a6c24610e39d6fe))
* implements email-less accounts with oauth ([#2105](https://github.com/supatype/auth/issues/2105)) ([9a61dae](https://github.com/supatype/auth/commit/9a61dae788311a086ce8e72b52c21e031857adf7))
* improvements to config reloader, 100% coverage ([#1933](https://github.com/supatype/auth/issues/1933)) ([21c2256](https://github.com/supatype/auth/commit/21c2256806ab4950e9bfc0af0472a64f7d9112a7))
* increase test coverage in conf package to 100% ([#1937](https://github.com/supatype/auth/issues/1937)) ([bc57c1c](https://github.com/supatype/auth/commit/bc57c1c25769905b29bfc9e89bf3d6b65b1030ea))
* increment refresh token counter by 2 for mfa verify ([#2284](https://github.com/supatype/auth/issues/2284)) ([2a38668](https://github.com/supatype/auth/commit/2a3866854fe7cb58a6cb84e7a82ce5d07bb920ee))
* **indexworker:** add max users threshold for rollout ([#2374](https://github.com/supatype/auth/issues/2374)) ([a2066c6](https://github.com/supatype/auth/commit/a2066c6a340fd3ebcaa0a816ab06ee3d6b1afad7))
* **indexworker:** use `auth_trgm` extension if available ([#2263](https://github.com/supatype/auth/issues/2263)) ([05daa43](https://github.com/supatype/auth/commit/05daa437131bd220e01a0e33df75f4b9afa72bb6))
* introduce request-scoped background tasks & async mail sending ([#2126](https://github.com/supatype/auth/issues/2126)) ([2c8ea61](https://github.com/supatype/auth/commit/2c8ea6113ae7381106ed7c67d7a45f7ef87195c7))
* introduce v2 refresh token algorithm ([#2216](https://github.com/supatype/auth/issues/2216)) ([dea5b8e](https://github.com/supatype/auth/commit/dea5b8e5353ea240c658b030325432ce512f18a8))
* load template cache at startup for fault tolerance ([#2261](https://github.com/supatype/auth/issues/2261)) ([511c3a4](https://github.com/supatype/auth/commit/511c3a4e12819d313840cd5342ae6a76d4708cfc))
* log all audit events separately to prevent missing events ([#2086](https://github.com/supatype/auth/issues/2086)) ([3b666f5](https://github.com/supatype/auth/commit/3b666f51f56db778848730d74ac140f02b0cb522))
* log sb-auth-user-id, sb-auth-session-id, ... on sign in not just refresh token ([#2342](https://github.com/supatype/auth/issues/2342)) ([a486ada](https://github.com/supatype/auth/commit/a486ada3683bb078b8f396a5ba2e606826f0044b))
* mailer logging ([#1805](https://github.com/supatype/auth/issues/1805)) ([9354b83](https://github.com/supatype/auth/commit/9354b83a48a3edcb49197c997a1e96efc80c5383))
* make the email client explicity set the format to be HTML ([#1149](https://github.com/supatype/auth/issues/1149)) ([53e223a](https://github.com/supatype/auth/commit/53e223abdf29f4abcad13f99baf00daedcb00c3f))
* merge provider metadata on link account ([#1552](https://github.com/supatype/auth/issues/1552)) ([bd8b5c4](https://github.com/supatype/auth/commit/bd8b5c41dd544575e1a52ccf1ef3f0fdee67458c))
* **metrics:** added a gauge with version information ([#2375](https://github.com/supatype/auth/issues/2375)) ([911ad0b](https://github.com/supatype/auth/commit/911ad0bae0b65b878acd05208e733f480c76b22f))
* MFA (Phone) ([#1668](https://github.com/supatype/auth/issues/1668)) ([ae091aa](https://github.com/supatype/auth/commit/ae091aa942bdc5bc97481037508ec3bb4079d859))
* MFA factor enrollment notifications ([#2183](https://github.com/supatype/auth/issues/2183)) ([53db712](https://github.com/supatype/auth/commit/53db712f0c3ffae6d61ea3ddcff5e8d7a33639b9))
* modernize IsNotFoundError handler to support errors.Is ([#2392](https://github.com/supatype/auth/issues/2392)) ([ab7c9f9](https://github.com/supatype/auth/commit/ab7c9f98a7fd98f0ff29d1f97784fc9e4dbfc87c))
* new timeout writer implementation ([#1584](https://github.com/supatype/auth/issues/1584)) ([72614a1](https://github.com/supatype/auth/commit/72614a1fce27888f294772b512f8e31c55a36d87))
* notify users when their phone number has changed ([#2184](https://github.com/supatype/auth/issues/2184)) ([21f3070](https://github.com/supatype/auth/commit/21f30702a62d722bce32972d4b2fcef1da6e2177))
* **oauth-server:** allow updating `token_endpoint_auth_method` for OAuth clients ([#2391](https://github.com/supatype/auth/issues/2391)) ([1280dc1](https://github.com/supatype/auth/commit/1280dc1ad75fce6e69bfa15c262c4b637c0720b5))
* **oauth-server:** store and enforce token_endpoint_auth_method ([#2300](https://github.com/supatype/auth/issues/2300)) ([bcd6cd5](https://github.com/supatype/auth/commit/bcd6cd590a47e963b7afe615c889f62d28cb94a2))
* **oauth2:** add `/oauth/token` endpoint ([#2159](https://github.com/supatype/auth/issues/2159)) ([a89a0b0](https://github.com/supatype/auth/commit/a89a0b054e87fee4e193aab4fff7677b56775386))
* **oauth2:** add admin endpoint to regenerate OAuth client secrets ([#2170](https://github.com/supatype/auth/issues/2170)) ([0bd1c28](https://github.com/supatype/auth/commit/0bd1c285aaf3bbb3f3d6e2e131aabfe5cabf0fa5))
* **oauth2:** return redirect_uri on GET authorization ([#2175](https://github.com/supatype/auth/issues/2175)) ([b0a0c3e](https://github.com/supatype/auth/commit/b0a0c3e48c8c8686d4cc3f82abd2ed326c297614))
* **oauth2:** use `id` field as the public client_id ([#2154](https://github.com/supatype/auth/issues/2154)) ([86b7de4](https://github.com/supatype/auth/commit/86b7de45c9432ea6ee9bd7c7e9cfe96e038fe2bc))
* **oauth:** add support for X/Twitter v2 provider ([#2275](https://github.com/supatype/auth/issues/2275)) ([7f36eb0](https://github.com/supatype/auth/commit/7f36eb053286038d01ba1650dd48a15508550ce0))
* **oauthserver:** add authorization list and revoke endpoints ([#2232](https://github.com/supatype/auth/issues/2232)) ([cc640b2](https://github.com/supatype/auth/commit/cc640b277989d57b39f3805cd9433ef4fe16bf83))
* **oauthserver:** add OAuth client admin update endpoint ([#2231](https://github.com/supatype/auth/issues/2231)) ([6296a5a](https://github.com/supatype/auth/commit/6296a5a226b3c60bcd9d20786750a808af9cd529))
* **oauthserver:** add OpenID Connect support ([#2250](https://github.com/supatype/auth/issues/2250)) ([162788f](https://github.com/supatype/auth/commit/162788ff960c060318324f11f673c09c0da41d5e))
* **oauthserver:** update oauth grant list & authorization details response structure ([#2247](https://github.com/supatype/auth/issues/2247)) ([137ea92](https://github.com/supatype/auth/commit/137ea92c00a0c1a7654fb8bcf0c1b5313901349f))
* **oauthserver:** use `NewOAuthServerAuthorizationParams` & configurable ttl for authorization ([#2254](https://github.com/supatype/auth/issues/2254)) ([61632f8](https://github.com/supatype/auth/commit/61632f8c0401b6c816ea7427d351ec623ce5258f))
* **openapi:** add OAuth 2.1 server endpoints and clarify OAuth modes ([#2165](https://github.com/supatype/auth/issues/2165)) ([1f804a2](https://github.com/supatype/auth/commit/1f804a2795012a1a165ff07afdb9dd98ad8ff291))
* **passkeys:** add audit, metering, webauthn primitives ([039b569](https://github.com/supatype/auth/commit/039b569cd2cb1541d9b7d1b93bfb7b4d8996e820))
* **passkeys:** add configuration, error codes, and schemas ([0a5eb95](https://github.com/supatype/auth/commit/0a5eb957407f007099608a032e540401fc186d0f))
* **passkeys:** progressive enrollment flow ([61ae2aa](https://github.com/supatype/auth/commit/61ae2aa17bdf9f234d61a631d71467cbf1d12f4e))
* password changed email notification ([#2176](https://github.com/supatype/auth/issues/2176)) ([fe0fd04](https://github.com/supatype/auth/commit/fe0fd04c9f5558d0165a94c7c080fb15c036d08f))
* preserve rate limiters in memory across configuration reloads ([#1792](https://github.com/supatype/auth/issues/1792)) ([0a3968b](https://github.com/supatype/auth/commit/0a3968b02b9f044bfb7e5ebc71dca970d2bb7807))
* properly handle redirect url fragments and unusual hostnames ([#2200](https://github.com/supatype/auth/issues/2200)) ([aa0ac5b](https://github.com/supatype/auth/commit/aa0ac5b9a8af26d4b779e48ec4da2ab06a6dc15e))
* refactor generate accesss token to take in request ([#1531](https://github.com/supatype/auth/issues/1531)) ([e4f2b59](https://github.com/supatype/auth/commit/e4f2b59e8e1f8158b6461a384349f1a32cc1bf9a))
* refactor hooks out of api package ([#1976](https://github.com/supatype/auth/issues/1976)) ([c5904c0](https://github.com/supatype/auth/commit/c5904c05d9dce4366e6527aa40e439a3c8c460bb))
* refactor mailer client wiring and add validation wrapper ([#2130](https://github.com/supatype/auth/issues/2130)) ([68c40a6](https://github.com/supatype/auth/commit/68c40a6a494029d8d704b14abbe85171a7dc8d12))
* refactor one-time tokens for performance ([#1558](https://github.com/supatype/auth/issues/1558)) ([d1cf8d9](https://github.com/supatype/auth/commit/d1cf8d9096e9183d7772b73031de8ecbd66e912b))
* refactor PKCE FlowState to reduce duplicate code ([#1446](https://github.com/supatype/auth/issues/1446)) ([b8d0337](https://github.com/supatype/auth/commit/b8d0337922c6712380f6dc74f7eac9fb71b1ae48))
* remove legacy lookup in users for one_time_tokens (phase II) ([#1569](https://github.com/supatype/auth/issues/1569)) ([39ca026](https://github.com/supatype/auth/commit/39ca026035f6c61d206d31772c661b326c2a424c))
* replace JWT OAuth state with `flow_state.id` UUID ([#2331](https://github.com/supatype/auth/issues/2331)) ([645654d](https://github.com/supatype/auth/commit/645654df63a3da7929840659c065f6a9cdd4ba96))
* reset main branch to 2.185.0 ([#2325](https://github.com/supatype/auth/issues/2325)) ([b9d0500](https://github.com/supatype/auth/commit/b9d050029ce90efc083f08a1e8df629faf20e8cd))
* return validation failed error if captcha request was not json ([#1815](https://github.com/supatype/auth/issues/1815)) ([26d2e36](https://github.com/supatype/auth/commit/26d2e36bba29eb8a6ddba556acfd0820f3bfde5d))
* send over user in SendSMS Hook instead of UserID ([#1551](https://github.com/supatype/auth/issues/1551)) ([d4d743c](https://github.com/supatype/auth/commit/d4d743c2ae9490e1b3249387e3b0d60df6913c68))
* separate web3 rate limits from other `/token?grant_type=...` ([#1985](https://github.com/supatype/auth/issues/1985)) ([8b23382](https://github.com/supatype/auth/commit/8b233820e41fedd18338eb37345ecbb0beb350ce))
* set `email_verified` to true on all identities with the verified email ([#1902](https://github.com/supatype/auth/issues/1902)) ([307892f](https://github.com/supatype/auth/commit/307892f85b39150074fbb80b9c8f45ac3312aae2))
* skip nonce check for Facebook Limited Login auth ([#2082](https://github.com/supatype/auth/issues/2082)) ([f1b15ff](https://github.com/supatype/auth/commit/f1b15ffdb9b1f1af873a147fdb5d039382becb2e))
* store latest challenge/attestation data ([#2179](https://github.com/supatype/auth/issues/2179)) ([01ebce1](https://github.com/supatype/auth/commit/01ebce1bf01b563105d653ff168a16e72c12d481))
* support `transfer_sub` in apple id tokens ([#2162](https://github.com/supatype/auth/issues/2162)) ([8a71006](https://github.com/supatype/auth/commit/8a71006486027c0850a58ec6e94f62a1607d1d48))
* support custom oauth & oidc providers ([#2357](https://github.com/supatype/auth/issues/2357)) ([53021f6](https://github.com/supatype/auth/commit/53021f66597439c14ebb869e567ab4742afd0142))
* support ledger solana offchain message signing ([#2093](https://github.com/supatype/auth/issues/2093)) ([4c94443](https://github.com/supatype/auth/commit/4c944431558aaca3c945c472dc5a27077f6dfa75))
* support multiple `aud` for the external providers ([#2117](https://github.com/supatype/auth/issues/2117)) ([ca5792e](https://github.com/supatype/auth/commit/ca5792e41a48f20a395646015c28ce272355bf63))
* support percentage based db limits with reload support ([#2177](https://github.com/supatype/auth/issues/2177)) ([1731466](https://github.com/supatype/auth/commit/1731466903539569ec5b308db4e39eb33c653b94))
* switch Docker image publishing from GHCR to Docker Hub ([25b8ca4](https://github.com/supatype/auth/commit/25b8ca4085425736100c9cf2e7a65cf4e269d8d8))
* switch to googleapis/release-please-action, bump to 2.166.0 ([#1883](https://github.com/supatype/auth/issues/1883)) ([11a312f](https://github.com/supatype/auth/commit/11a312fcf77771b3732f2f439078225895df7a85))
* Treat rate limit header value as comma-separated list ([#2282](https://github.com/supatype/auth/issues/2282)) ([5f2e279](https://github.com/supatype/auth/commit/5f2e2792560d57dd14fbf3e69c133a7ec8518c4d))
* update chi version ([#1581](https://github.com/supatype/auth/issues/1581)) ([c64ae3d](https://github.com/supatype/auth/commit/c64ae3dd775e8fb3022239252c31b4ee73893237))
* update openapi spec with identity and is_anonymous fields ([#1573](https://github.com/supatype/auth/issues/1573)) ([86a79df](https://github.com/supatype/auth/commit/86a79df9ecfcf09fda0b8e07afbc41154fbb7d9d))
* upgrade existing sessions to v2 refresh tokens though config value ([#2356](https://github.com/supatype/auth/issues/2356)) ([6fb0e8a](https://github.com/supatype/auth/commit/6fb0e8adc104e3b9119b79506997e29bbb2ca9a2))
* upgrade otel to v1.26 ([#1585](https://github.com/supatype/auth/issues/1585)) ([cdd13ad](https://github.com/supatype/auth/commit/cdd13adec02eb0c9401bc55a2915c1005d50dea1))
* use `global_user_id` over `sub` for `vercel_marketplace` issuer ([#1990](https://github.com/supatype/auth/issues/1990)) ([f94f97e](https://github.com/supatype/auth/commit/f94f97e8d3e530d730d9352a14b477fd33548df2))
* use `slices.Contains` instead of for loops ([#2111](https://github.com/supatype/auth/issues/2111)) ([9f22682](https://github.com/supatype/auth/commit/9f2268263118713d3390ce4617ccf21bc2c031eb))
* use embedded migrations for `migrate` command ([#1843](https://github.com/supatype/auth/issues/1843)) ([e358da5](https://github.com/supatype/auth/commit/e358da5f0e267725a77308461d0a4126436fc537))
* use largest avatar from spotify instead ([#1210](https://github.com/supatype/auth/issues/1210)) ([4f9994b](https://github.com/supatype/auth/commit/4f9994bf792c3887f2f45910b11a9c19ee3a896b)), closes [#1209](https://github.com/supatype/auth/issues/1209)
* Vercel marketplace OIDC ([#1731](https://github.com/supatype/auth/issues/1731)) ([a9ff361](https://github.com/supatype/auth/commit/a9ff3612196af4a228b53a8bfb9c11785bcfba8d))
* webauthn support schema changes, update openapi.yaml ([#2163](https://github.com/supatype/auth/issues/2163)) ([68cb8d2](https://github.com/supatype/auth/commit/68cb8d2ba3ded878c68d7cb76465bfaaac58436a))


### Bug Fixes

* accept ID tokens from all `account.apple.com` and `appleid.apple.com` ([#2050](https://github.com/supatype/auth/issues/2050)) ([82aa167](https://github.com/supatype/auth/commit/82aa167cae01658b5319914f3412d78876955106))
* add `id-token` permission to ci ([#2143](https://github.com/supatype/auth/issues/2143)) ([79209c0](https://github.com/supatype/auth/commit/79209c0e35afa82ec8822a343108d6a690e14229))
* add `supafast` tarball for upgrading auth via supabase-admin-api ([#2009](https://github.com/supatype/auth/issues/2009)) ([9b55785](https://github.com/supatype/auth/commit/9b557855a3ab80ee93ab95159055a444bff53f01))
* add additional information around errors for missing content type header ([#1576](https://github.com/supatype/auth/issues/1576)) ([c2b2f96](https://github.com/supatype/auth/commit/c2b2f96f07c97c15597cd972b1cd672238d87cdc))
* add cleanup statement for anonymous users ([#1497](https://github.com/supatype/auth/issues/1497)) ([cf2372a](https://github.com/supatype/auth/commit/cf2372a177796b829b72454e7491ce768bf5a42f))
* add db conn max idle time setting ([#1555](https://github.com/supatype/auth/issues/1555)) ([2caa7b4](https://github.com/supatype/auth/commit/2caa7b4d75d2ff54af20f3e7a30a8eeec8cbcda9))
* add error codes to password login flow ([#1721](https://github.com/supatype/auth/issues/1721)) ([4351226](https://github.com/supatype/auth/commit/435122627a0784f1c5cb76d7e08caa1f6259423b))
* add error codes to refresh token flow ([#1824](https://github.com/supatype/auth/issues/1824)) ([4614dc5](https://github.com/supatype/auth/commit/4614dc54ab1dcb5390cfed05441e7888af017d92))
* add http support for https hooks on localhost ([#1484](https://github.com/supatype/auth/issues/1484)) ([5c04104](https://github.com/supatype/auth/commit/5c04104bf77a9c2db46d009764ec3ec3e484fc09))
* add ip based limiter ([#1622](https://github.com/supatype/auth/issues/1622)) ([06464c0](https://github.com/supatype/auth/commit/06464c013571253d1f18f7ae5e840826c4bd84a7))
* add last_challenged_at field to mfa factors ([#1705](https://github.com/supatype/auth/issues/1705)) ([29cbeb7](https://github.com/supatype/auth/commit/29cbeb799ff35ce528bfbd01b7103a24903d8061))
* add MaxBytesReader middleware to limit request body size to 1MB ([#2402](https://github.com/supatype/auth/issues/2402)) ([6f0b2eb](https://github.com/supatype/auth/commit/6f0b2ebc8c7bb96735cb6432923b3618ffb81a5c))
* add missing param ([#2125](https://github.com/supatype/auth/issues/2125)) ([c0b75f6](https://github.com/supatype/auth/commit/c0b75f66229410e6e5fbc7cd1ae9066cec54c5d7))
* add missing provider info to signedup audit logs ([#2061](https://github.com/supatype/auth/issues/2061)) ([c6e0cbe](https://github.com/supatype/auth/commit/c6e0cbefe5b609ac3362c23d0f7cb9d9bb04abc9))
* add test coverage for rate limits with 0 permitted events ([#1834](https://github.com/supatype/auth/issues/1834)) ([7c3cf26](https://github.com/supatype/auth/commit/7c3cf26cfe2a3e4de579d10509945186ad719855))
* add token to hook payload for non-secure email change ([#1763](https://github.com/supatype/auth/issues/1763)) ([7e472ad](https://github.com/supatype/auth/commit/7e472ad72042e86882dab3fddce9fafa66a8236c))
* add twilio verify support on mfa ([#1714](https://github.com/supatype/auth/issues/1714)) ([aeb5d8f](https://github.com/supatype/auth/commit/aeb5d8f8f18af60ce369cab5714979ac0c208308))
* add validation and proper decoding on send email hook ([#1520](https://github.com/supatype/auth/issues/1520)) ([e19e762](https://github.com/supatype/auth/commit/e19e762e3e29729a1d1164c65461427822cc87f1))
* additional provider and issuer checks ([#2326](https://github.com/supatype/auth/issues/2326)) ([cb79a74](https://github.com/supatype/auth/commit/cb79a7414e8b2bff30113bdf2b9ec6d6e93c1146))
* admin user update should update is_anonymous field ([#1623](https://github.com/supatype/auth/issues/1623)) ([f5c6fcd](https://github.com/supatype/auth/commit/f5c6fcd9c3fee0f793f96880a8caebc5b5cb0916))
* allow anonymous user to update password ([#1739](https://github.com/supatype/auth/issues/1739)) ([2d51956](https://github.com/supatype/auth/commit/2d519569d7b8540886d0a64bf3e561ef5f91eb63))
* allow enabling sms hook without setting up sms provider ([#1704](https://github.com/supatype/auth/issues/1704)) ([575e88a](https://github.com/supatype/auth/commit/575e88ac345adaeb76ab6aae077307fdab9cda3c))
* allow HTTP with localhost in solana ([#2027](https://github.com/supatype/auth/issues/2027)) ([3ee02f0](https://github.com/supatype/auth/commit/3ee02f085df206dcd3e6fa79f2d583148ebc52b8))
* amr claim should contain provider_id for sso method ([#2033](https://github.com/supatype/auth/issues/2033)) ([33741e1](https://github.com/supatype/auth/commit/33741e18d2e0adb691e650355337924f9ccfd91f))
* apply authorized email restriction to non-admin routes ([#1778](https://github.com/supatype/auth/issues/1778)) ([1af203f](https://github.com/supatype/auth/commit/1af203f92372e6db12454a0d319aad8ce3d149e7))
* apply mailer autoconfirm config to update user email ([#1646](https://github.com/supatype/auth/issues/1646)) ([a518505](https://github.com/supatype/auth/commit/a5185058e72509b0781e0eb59910ecdbb8676fee))
* apply shared limiters before email / sms is sent ([#1748](https://github.com/supatype/auth/issues/1748)) ([bf276ab](https://github.com/supatype/auth/commit/bf276ab49753642793471815727559172fea4efc))
* **auditlog:** keep writing to logs even postgres is disabled ([#2076](https://github.com/supatype/auth/issues/2076)) ([b89bc32](https://github.com/supatype/auth/commit/b89bc32de5adc9d458e7f95ad9b08a99604c70d8))
* azure overage claims start with single `_` not two ([#1999](https://github.com/supatype/auth/issues/1999)) ([29f3440](https://github.com/supatype/auth/commit/29f3440d6376fac22568284d5b417836bf335a74))
* bypass check for token & verify endpoints ([#1785](https://github.com/supatype/auth/issues/1785)) ([9ac2ea0](https://github.com/supatype/auth/commit/9ac2ea0180826cd2f65e679524aabfb10666e973))
* call write header in write if not written ([#1598](https://github.com/supatype/auth/issues/1598)) ([0ef7eb3](https://github.com/supatype/auth/commit/0ef7eb30619d4c365e06a94a79b9cb0333d792da))
* case-insensitive Bearer token scheme matching ([#2387](https://github.com/supatype/auth/issues/2387)) ([36d712d](https://github.com/supatype/auth/commit/36d712d27f66721adf58a93ffb9e43d5cc915eca))
* change phone constraint to per user ([#1713](https://github.com/supatype/auth/issues/1713)) ([b9bc769](https://github.com/supatype/auth/commit/b9bc769b93b6e700925fcbc1ebf8bf9678034205))
* change s3 artifact upload role ([#2145](https://github.com/supatype/auth/issues/2145)) ([767e371](https://github.com/supatype/auth/commit/767e37131aa01bf6cb27dbc62b2928e7cc701893))
* check each type independently ([#2290](https://github.com/supatype/auth/issues/2290)) ([d9de0af](https://github.com/supatype/auth/commit/d9de0af3a173ae3e9ab0219c07652675f8be1761))
* check for empty aud string ([#1649](https://github.com/supatype/auth/issues/1649)) ([42c1d45](https://github.com/supatype/auth/commit/42c1d4526b98203664d4a22c23014ecd0b4951f9))
* check if session is nil ([#1873](https://github.com/supatype/auth/issues/1873)) ([fd82601](https://github.com/supatype/auth/commit/fd82601917adcd9f8c38263953eb1ef098b26b7f))
* check password max length in checkPasswordStrength ([#1659](https://github.com/supatype/auth/issues/1659)) ([1858c93](https://github.com/supatype/auth/commit/1858c93bba6f5bc41e4c65489f12c1a0786a1f2b))
* cleanup panics due to bad inactivity timeout code ([#1471](https://github.com/supatype/auth/issues/1471)) ([548edf8](https://github.com/supatype/auth/commit/548edf898161c9ba9a136fc99ec2d52a8ba1f856))
* convert refreshed_at to UTC before updating ([#1916](https://github.com/supatype/auth/issues/1916)) ([a4c692f](https://github.com/supatype/auth/commit/a4c692f6cb1b8bf4c47ea012872af5ce93382fbf))
* correct casing of API key authentication in openapi.yaml ([0cfd177](https://github.com/supatype/auth/commit/0cfd177b8fb1df8f62e84fbd3761ef9f90c384de))
* correct web authn aaguid column naming ([#1826](https://github.com/supatype/auth/issues/1826)) ([0a589d0](https://github.com/supatype/auth/commit/0a589d04e1cd9310cb260d329bc8beb050adf8da))
* correctly parse JWT ValidMethods from env by enabling split_words ([#2334](https://github.com/supatype/auth/issues/2334)) ([a6076bc](https://github.com/supatype/auth/commit/a6076bc39f63cfca94e2330957031d4f63a4b68e))
* custom SMS does not work with Twilio Verify ([#1733](https://github.com/supatype/auth/issues/1733)) ([dc2391d](https://github.com/supatype/auth/commit/dc2391d15f2c0725710aa388cd32a18797e6769c))
* deadlock issue with timeout middleware write ([#1595](https://github.com/supatype/auth/issues/1595)) ([6c9fbd4](https://github.com/supatype/auth/commit/6c9fbd4bd5623c729906fca7857ab508166a3056))
* default to files:read scope for Figma provider ([#1831](https://github.com/supatype/auth/issues/1831)) ([9ce2857](https://github.com/supatype/auth/commit/9ce28570bf3da9571198d44d693c7ad7038cde33))
* define search path in auth functions ([#1616](https://github.com/supatype/auth/issues/1616)) ([357bda2](https://github.com/supatype/auth/commit/357bda23cb2abd12748df80a9d27288aa548534d))
* do call send sms hook when SMS autoconfirm is enabled ([#1562](https://github.com/supatype/auth/issues/1562)) ([bfe4d98](https://github.com/supatype/auth/commit/bfe4d988f3768b0407526bcc7979fb21d8cbebb3))
* do not log fatal when http server successfully closes ([#2065](https://github.com/supatype/auth/issues/2065)) ([1f7de6c](https://github.com/supatype/auth/commit/1f7de6c65f31ef0bbb80899369989b13ab5a517f))
* **docs:** remove bracket on file name for broken link ([#1493](https://github.com/supatype/auth/issues/1493)) ([96f7a68](https://github.com/supatype/auth/commit/96f7a68a5479825e31106c2f55f82d5b2c007c0f))
* don't update attribute mapping if nil ([#1665](https://github.com/supatype/auth/issues/1665)) ([7e67f3e](https://github.com/supatype/auth/commit/7e67f3edbf81766df297a66f52a8e472583438c6))
* drop the MFA_ENABLED config ([#1701](https://github.com/supatype/auth/issues/1701)) ([078c3a8](https://github.com/supatype/auth/commit/078c3a8adcd51e57b68ab1b582549f5813cccd14))
* email header setting no longer misleading ([#1802](https://github.com/supatype/auth/issues/1802)) ([3af03be](https://github.com/supatype/auth/commit/3af03be6b65c40f3f4f62ce9ab989a20d75ae53a))
* email_verified field not being updated on signup confirmation ([#1868](https://github.com/supatype/auth/issues/1868)) ([483463e](https://github.com/supatype/auth/commit/483463e49eec7b2974cca05eadca6b933b2145b5))
* email-sendhook - bug in email change verification ([#2044](https://github.com/supatype/auth/issues/2044)) ([be20654](https://github.com/supatype/auth/commit/be20654ec3af21b93a8d7482a5673b5c8c60ac8a))
* enable rls & update grants for auth tables ([#1617](https://github.com/supatype/auth/issues/1617)) ([28967aa](https://github.com/supatype/auth/commit/28967aa4b5db2363cc581c9da0d64e974eb7b64c))
* enable SO_REUSEPORT in listener config ([#1936](https://github.com/supatype/auth/issues/1936)) ([a474b80](https://github.com/supatype/auth/commit/a474b80cc1075eb32a7e72a05b0cdb561e61770b))
* enforce authorized address checks on send email only ([#1806](https://github.com/supatype/auth/issues/1806)) ([c0c5b23](https://github.com/supatype/auth/commit/c0c5b23728c8fb633dae23aa4b29ed60e2691a2b))
* enforce uniqueness on verified phone numbers ([#1693](https://github.com/supatype/auth/issues/1693)) ([70446cc](https://github.com/supatype/auth/commit/70446cc11d70b0493d742fe03f272330bb5b633e))
* ensure request context exists in API db operations ([#2171](https://github.com/supatype/auth/issues/2171)) ([060a992](https://github.com/supatype/auth/commit/060a99278d8e3ec4a78ca61b95a9acf0e7052948))
* explicit permisions on actions ([#1978](https://github.com/supatype/auth/issues/1978)) ([06e9ead](https://github.com/supatype/auth/commit/06e9ead3e09e77631597a953a535cb93dd006c7f))
* expose `X-Supabase-Api-Version` header in CORS ([#1612](https://github.com/supatype/auth/issues/1612)) ([6ccd814](https://github.com/supatype/auth/commit/6ccd814309dca70a9e3585543887194b05d725d3))
* expose factor type on challenge ([#1709](https://github.com/supatype/auth/issues/1709)) ([e1a21a3](https://github.com/supatype/auth/commit/e1a21a34779ca4b2254caf8b7578db4a50172751))
* external host validation ([#1808](https://github.com/supatype/auth/issues/1808)) ([4f6a461](https://github.com/supatype/auth/commit/4f6a4617074e61ba3b31836ccb112014904ce97c)), closes [#1228](https://github.com/supatype/auth/issues/1228)
* fallback on btree indexes when hash is unavailable ([#1856](https://github.com/supatype/auth/issues/1856)) ([b33bc31](https://github.com/supatype/auth/commit/b33bc31c07549dc9dc221100995d6f6b6754fd3a))
* fix `getExcludedColumns` slice allocation ([#1788](https://github.com/supatype/auth/issues/1788)) ([7f006b6](https://github.com/supatype/auth/commit/7f006b63c8d7e28e55a6d471881e9c118df80585))
* fix `supafast` tarball generation ([#2011](https://github.com/supatype/auth/issues/2011)) ([88bb2c0](https://github.com/supatype/auth/commit/88bb2c0638863f94f9f0d7f4ca88ba04929dfd55))
* Fix reqPath for bypass check for verify EP ([#1789](https://github.com/supatype/auth/issues/1789)) ([646dc66](https://github.com/supatype/auth/commit/646dc66ea8d59a7f78bf5a5e55d9b5065a718c23))
* fix the wrong error return value ([#1950](https://github.com/supatype/auth/issues/1950)) ([e2dfb5d](https://github.com/supatype/auth/commit/e2dfb5d4222e5edc569b54d057db9ed4375a19d8))
* flaky index worker test ([#2366](https://github.com/supatype/auth/issues/2366)) ([961a7e6](https://github.com/supatype/auth/commit/961a7e620109d554ae81ca8227a5107671679982))
* format test otps ([#1567](https://github.com/supatype/auth/issues/1567)) ([434a59a](https://github.com/supatype/auth/commit/434a59ae387c35fd6629ec7c674d439537e344e5))
* generate signup link should not error ([#1514](https://github.com/supatype/auth/issues/1514)) ([4fc3881](https://github.com/supatype/auth/commit/4fc388186ac7e7a9a32ca9b963a83d6ac2eb7603))
* gosec incorrectly warns about accessing signature[64] ([#2222](https://github.com/supatype/auth/issues/2222)) ([bca6626](https://github.com/supatype/auth/commit/bca66268dc4f81821c194a26dcf76209d1c696de))
* handle user banned error code ([#1851](https://github.com/supatype/auth/issues/1851)) ([a6918f4](https://github.com/supatype/auth/commit/a6918f49baee42899b3ae1b7b6bc126d84629c99))
* hide hook name ([#1743](https://github.com/supatype/auth/issues/1743)) ([7e38f4c](https://github.com/supatype/auth/commit/7e38f4cf37768fe2adf92bbd0723d1d521b3d74c))
* **hooks:** propagate error objects from hook calls ([#2380](https://github.com/supatype/auth/issues/2380)) ([3ca1e88](https://github.com/supatype/auth/commit/3ca1e88df06e7096c8ebb3e1bedf291654f4c66e))
* hostname can be empty with redirect urls ([#2241](https://github.com/supatype/auth/issues/2241)) ([f5a4cba](https://github.com/supatype/auth/commit/f5a4cbac73de28cc4b04c5c9725b70517cb131d3))
* ignore errors if transaction has closed already ([#1726](https://github.com/supatype/auth/issues/1726)) ([53c11d1](https://github.com/supatype/auth/commit/53c11d173a79ae5c004871b1b5840c6f9425a080))
* ignore not found error to check for pkce prefix later ([#1929](https://github.com/supatype/auth/issues/1929)) ([fbbebcc](https://github.com/supatype/auth/commit/fbbebccd5da21ea22323e6f8f853df9168c4c41e))
* ignore rate limits for autoconfirm ([#1810](https://github.com/supatype/auth/issues/1810)) ([9ce2340](https://github.com/supatype/auth/commit/9ce23409f960a8efa55075931138624cb681eca5))
* impose expiry on auth code instead of magic link ([#1440](https://github.com/supatype/auth/issues/1440)) ([35aeaf1](https://github.com/supatype/auth/commit/35aeaf1b60dd27a22662a6d1955d60cc907b55dd))
* improve error messaging for http hooks ([#1821](https://github.com/supatype/auth/issues/1821)) ([fa020d0](https://github.com/supatype/auth/commit/fa020d0fc292d5c381c57ecac6666d9ff657e4c4))
* improve invalid channel error message returned ([#1908](https://github.com/supatype/auth/issues/1908)) ([f72f0ee](https://github.com/supatype/auth/commit/f72f0eee328fa0aa041155f5f5dc305f0874d2bf))
* improve logging structure ([#1583](https://github.com/supatype/auth/issues/1583)) ([c22fc15](https://github.com/supatype/auth/commit/c22fc15d2a8383e95a2364f383dfa7dce5f5df88))
* improve mfa verify logs ([#1635](https://github.com/supatype/auth/issues/1635)) ([d8b47f9](https://github.com/supatype/auth/commit/d8b47f9d3f0dc8f97ad1de49e45f452ebc726481))
* improve saml assertion logging ([#1915](https://github.com/supatype/auth/issues/1915)) ([d6030cc](https://github.com/supatype/auth/commit/d6030ccd271a381e2a6ababa11a5beae4b79e5c3))
* improve session error logging ([#1655](https://github.com/supatype/auth/issues/1655)) ([5a6793e](https://github.com/supatype/auth/commit/5a6793ee8fce7a089750fe10b3b63bb0a19d6d21))
* improve token OIDC logging ([#1606](https://github.com/supatype/auth/issues/1606)) ([5262683](https://github.com/supatype/auth/commit/526268311844467664e89c8329e5aaee817dbbaf))
* include factor_id in query ([#1702](https://github.com/supatype/auth/issues/1702)) ([ac14e82](https://github.com/supatype/auth/commit/ac14e82b33545466184da99e99b9d3fe5f3876d9))
* **indexworker:** detect which schema `pg_trgm` exists in ([#2260](https://github.com/supatype/auth/issues/2260)) ([4be12b3](https://github.com/supatype/auth/commit/4be12b3e7c0a30b1e289ab81348548f72ab32ba5))
* **indexworker:** remove pg_trgm extension ([#2301](https://github.com/supatype/auth/issues/2301)) ([c553b10](https://github.com/supatype/auth/commit/c553b10e5f3b7a8c430b20babe0e7c96178b1c91))
* inline mailme package for easy development ([#1803](https://github.com/supatype/auth/issues/1803)) ([fa6f729](https://github.com/supatype/auth/commit/fa6f729a027eff551db104550fa626088e00bc15))
* invalidate email, phone OTPs on password change ([#1489](https://github.com/supatype/auth/issues/1489)) ([960a4f9](https://github.com/supatype/auth/commit/960a4f94f5500e33a0ec2f6afe0380bbc9562500))
* invited users should have a temporary password generated ([#1644](https://github.com/supatype/auth/issues/1644)) ([3f70d9d](https://github.com/supatype/auth/commit/3f70d9d8974d0e9c437c51e1312ad17ce9056ec9))
* invites should send another email when user exists ([#2058](https://github.com/supatype/auth/issues/2058)) ([96469bd](https://github.com/supatype/auth/commit/96469bd01b9c37f938aabdb0434a054a111cf963))
* japanese dot example fix ([#2243](https://github.com/supatype/auth/issues/2243)) ([3a5f4b2](https://github.com/supatype/auth/commit/3a5f4b211a0f50bd1957f5a41467fc5aa6a01ca6))
* linkedin_oidc provider error ([#1534](https://github.com/supatype/auth/issues/1534)) ([4f5e8e5](https://github.com/supatype/auth/commit/4f5e8e5120531e5a103fbdda91b51cabcb4e1a8c))
* log final writer error instead of handling ([#1564](https://github.com/supatype/auth/issues/1564)) ([170bd66](https://github.com/supatype/auth/commit/170bd6615405afc852c7107f7358dfc837bad737))
* log version & migration count ([#1934](https://github.com/supatype/auth/issues/1934)) ([8078cdc](https://github.com/supatype/auth/commit/8078cdc6f275c97d84c0ba20963327af900b84d0))
* look for refresh token on mfa verification only in v1 ([#2249](https://github.com/supatype/auth/issues/2249)) ([2906b24](https://github.com/supatype/auth/commit/2906b2424d0aa804031e66cf92f008289b8a9c77))
* magiclink failing due to passwordStrength check ([#1769](https://github.com/supatype/auth/issues/1769)) ([7a5411f](https://github.com/supatype/auth/commit/7a5411f1d4247478f91027bc4969cbbe95b7774c))
* maintain backward compatibility for asymmetric JWTs ([#1690](https://github.com/supatype/auth/issues/1690)) ([0ad1402](https://github.com/supatype/auth/commit/0ad1402444348e47e1e42be186b3f052d31be824))
* make drop_uniqueness_constraint_on_phone idempotent ([#1817](https://github.com/supatype/auth/issues/1817)) ([158e473](https://github.com/supatype/auth/commit/158e4732afa17620cdd89c85b7b57569feea5c21))
* **makefile:** remove invalid @ symbol from shell commands ([#2168](https://github.com/supatype/auth/issues/2168)) ([e6afe45](https://github.com/supatype/auth/commit/e6afe4529859e1ee92ed5c259e04c9fe56de22cf))
* MFA NewFactor to default to creating unverfied factors ([#1692](https://github.com/supatype/auth/issues/1692)) ([3d448fa](https://github.com/supatype/auth/commit/3d448fa73cb77eb8511dbc47bfafecce4a4a2150))
* mfa verify now works with refresh token algorithm v2 ([#2246](https://github.com/supatype/auth/issues/2246)) ([4e8275f](https://github.com/supatype/auth/commit/4e8275f915c4d84186d17b41c86a9277055a55e4))
* minor spelling errors ([#1688](https://github.com/supatype/auth/issues/1688)) ([6aca52b](https://github.com/supatype/auth/commit/6aca52b56f8a6254de7709c767b9a5649f1da248)), closes [#1682](https://github.com/supatype/auth/issues/1682)
* move all EmailActionTypes to mailer package ([#1510](https://github.com/supatype/auth/issues/1510)) ([765db08](https://github.com/supatype/auth/commit/765db08582669a1b7f054217fa8f0ed45804c0b5))
* move creation of flow state into function ([#1470](https://github.com/supatype/auth/issues/1470)) ([4392a08](https://github.com/supatype/auth/commit/4392a08d68d18828005d11382730117a7b143635))
* move is owned by check to load factor ([#1703](https://github.com/supatype/auth/issues/1703)) ([701a779](https://github.com/supatype/auth/commit/701a779cf092e777dd4ad4954dc650164b09ab32))
* new `odic.Provider` for apple with insecure issuer url context ([#2055](https://github.com/supatype/auth/issues/2055)) ([23d69f1](https://github.com/supatype/auth/commit/23d69f1c450b4a24a262cb25112e68408857a3b2))
* **oauth-server:** allow custom URI schemes in client redirect URIs ([#2298](https://github.com/supatype/auth/issues/2298)) ([ea72f57](https://github.com/supatype/auth/commit/ea72f57f99633b33cc7b30b4a0b74ed8314b71e6))
* **oauth2:** switch to Origin header for request validation ([#2174](https://github.com/supatype/auth/issues/2174)) ([42bc9ab](https://github.com/supatype/auth/commit/42bc9ab7db24ce1902fef21ba5e90a2128617669))
* omit empty string from name & use case-insensitive equality for comparing SAML attributes ([#1654](https://github.com/supatype/auth/issues/1654)) ([bf5381a](https://github.com/supatype/auth/commit/bf5381a6b1c686955dc4e39fe5fb806ffd309563))
* **openapi:** add missing OAuth client registration fields ([#2227](https://github.com/supatype/auth/issues/2227)) ([cf39a8a](https://github.com/supatype/auth/commit/cf39a8ae2cc386f2672f0ecbb8d84dd77f04e56f))
* **passkeys:** construct configuration env var correctly ([dba676e](https://github.com/supatype/auth/commit/dba676ef9c1087e509006c01893d9f8d9d3bbb37))
* **passkeys:** enforce passkey cap during registration verify ([9868df6](https://github.com/supatype/auth/commit/9868df617af0cccd9f88ba71600058eeb31024ea))
* **passkeys:** sign_count should be uint32 ([e509e3a](https://github.com/supatype/auth/commit/e509e3a80e075ccb92f738000bb592f475487a3c))
* possible panic if refresh token has a null session_id ([#1822](https://github.com/supatype/auth/issues/1822)) ([a7129df](https://github.com/supatype/auth/commit/a7129df4e1d91a042b56ff1f041b9c6598825475))
* prevent user email side-channel leak on verify ([#1472](https://github.com/supatype/auth/issues/1472)) ([311cde8](https://github.com/supatype/auth/commit/311cde8d1e82f823ae26a341e068034d60273864))
* propagate error when when confirming phone ([#1939](https://github.com/supatype/auth/issues/1939)) ([e882b42](https://github.com/supatype/auth/commit/e882b42f3929ab2e587a41ba6593edaf237e5535))
* publish to ghcr.io/supabase/auth ([#1626](https://github.com/supatype/auth/issues/1626)) ([930aa3e](https://github.com/supatype/auth/commit/930aa3edb633823d4510c2aff675672df06f1211)), closes [#1625](https://github.com/supatype/auth/issues/1625)
* rate limits of 0 take precedence over MAILER_AUTO_CONFIRM ([#1837](https://github.com/supatype/auth/issues/1837)) ([cb7894e](https://github.com/supatype/auth/commit/cb7894e1119d27d527dedcca22d8b3d433beddac))
* redirect invalid state errors to site url ([#1722](https://github.com/supatype/auth/issues/1722)) ([b2b1123](https://github.com/supatype/auth/commit/b2b11239dc9f9bd3c85d76f6c23ee94beb3330bb))
* redirects must not be to ip addresses ([#1984](https://github.com/supatype/auth/issues/1984)) ([347e23a](https://github.com/supatype/auth/commit/347e23a98c2ee362620d2711d12a76d7bc266a8f))
* refactor email sending functions ([#1495](https://github.com/supatype/auth/issues/1495)) ([285c290](https://github.com/supatype/auth/commit/285c290adf231fea7ca1dff954491dc427cf18e2))
* refactor factor_test to centralize setup ([#1473](https://github.com/supatype/auth/issues/1473)) ([c86007e](https://github.com/supatype/auth/commit/c86007e59684334b5e8c2285c36094b6eec89442))
* refactor mfa and aal update methods ([#1503](https://github.com/supatype/auth/issues/1503)) ([31a5854](https://github.com/supatype/auth/commit/31a585429bf248aa919d94c82c7c9e0c1c695461))
* refactor mfa challenge and tests ([#1469](https://github.com/supatype/auth/issues/1469)) ([6c76f21](https://github.com/supatype/auth/commit/6c76f21cee5dbef0562c37df6a546939affb2f8d))
* refactor mfa models and add observability to loadFactor ([#1669](https://github.com/supatype/auth/issues/1669)) ([822fb93](https://github.com/supatype/auth/commit/822fb93faab325ba3d4bb628dff43381d68d0b5d))
* refactor mfa validation into functions ([#1780](https://github.com/supatype/auth/issues/1780)) ([410b8ac](https://github.com/supatype/auth/commit/410b8acdd659fc4c929fe57a9e9dba4c76da305d))
* refactor TOTP MFA into separate methods ([#1698](https://github.com/supatype/auth/issues/1698)) ([250d92f](https://github.com/supatype/auth/commit/250d92f9a18d38089d1bf262ef9088022a446965))
* reloader unittest races on writeWg ([#2352](https://github.com/supatype/auth/issues/2352)) ([088b714](https://github.com/supatype/auth/commit/088b7149d6857cfe65e4338c1ee9e079688f8c92))
* remove azure claim overage code. ([#2005](https://github.com/supatype/auth/issues/2005)) ([63dce14](https://github.com/supatype/auth/commit/63dce14488f92d9e0e67028cd0ae6e002ebf532a))
* remove check for content-length ([#1700](https://github.com/supatype/auth/issues/1700)) ([81b332d](https://github.com/supatype/auth/commit/81b332d2f48622008469d2c5a9b130465a65f2a3))
* remove deprecated LogoutAllRefreshTokens ([#1519](https://github.com/supatype/auth/issues/1519)) ([35533ea](https://github.com/supatype/auth/commit/35533ea100669559e1209ecc7b091db3657234d9))
* remove FindFactorsByUser ([#1707](https://github.com/supatype/auth/issues/1707)) ([af8e2dd](https://github.com/supatype/auth/commit/af8e2dda15a1234a05e7d2d34d316eaa029e0912))
* remove requirement of empty content-type on 204 ([#2128](https://github.com/supatype/auth/issues/2128)) ([ecc97e0](https://github.com/supatype/auth/commit/ecc97e0fac7cb1bd736ef6db435a0a5fb224e954))
* remove server side cookie token methods ([#1742](https://github.com/supatype/auth/issues/1742)) ([c6efec4](https://github.com/supatype/auth/commit/c6efec4cbc950e01e1fd06d45ed821bd27c2ad08))
* remove TOTP field for phone enroll response ([#1717](https://github.com/supatype/auth/issues/1717)) ([4b04327](https://github.com/supatype/auth/commit/4b043275dd2d94600a8138d4ebf4638754ed926b))
* rename from CustomSMSProvider to SendSMS ([#1513](https://github.com/supatype/auth/issues/1513)) ([c0bc37b](https://github.com/supatype/auth/commit/c0bc37b44effaebb62ba85102f072db07fe57e48))
* Resend SMS when duplicate SMS sign ups are made ([#1490](https://github.com/supatype/auth/issues/1490)) ([73240a0](https://github.com/supatype/auth/commit/73240a0b096977703e3c7d24a224b5641ce47c81))
* resolving azure overage claim should include `api-version=1.6` query parameter ([#2000](https://github.com/supatype/auth/issues/2000)) ([44890d0](https://github.com/supatype/auth/commit/44890d0a6df903e765bcde509231a78f61890bec))
* restrict autoconfirm email change to anonymous users ([#1679](https://github.com/supatype/auth/issues/1679)) ([b57e223](https://github.com/supatype/auth/commit/b57e2230102280ed873acf70be1aeb5a2f6f7a4f))
* return error if session id does not exist ([#1538](https://github.com/supatype/auth/issues/1538)) ([91e9eca](https://github.com/supatype/auth/commit/91e9ecabe33a1c022f8e82a6050c22a7ca42de48))
* return oauth identity when user is created ([#1736](https://github.com/supatype/auth/issues/1736)) ([60cfb60](https://github.com/supatype/auth/commit/60cfb6063afa574dfe4993df6b0e087d4df71309))
* return proper error if sms rate limit is exceeded ([#1647](https://github.com/supatype/auth/issues/1647)) ([3c8d765](https://github.com/supatype/auth/commit/3c8d7656431ac4b2e80726b7c37adb8f0c778495))
* return the error code instead of status code ([#1855](https://github.com/supatype/auth/issues/1855)) ([834a380](https://github.com/supatype/auth/commit/834a380d803ae9ce59ce5ee233fa3a78a984fe68))
* Revert "fix: revert fallback on btree indexes when hash is unavailable" ([#1859](https://github.com/supatype/auth/issues/1859)) ([9fe5b1e](https://github.com/supatype/auth/commit/9fe5b1eebfafb385d6b5d10196aeb2a1964ab296))
* revert define search path in auth functions ([#1634](https://github.com/supatype/auth/issues/1634)) ([155e87e](https://github.com/supatype/auth/commit/155e87ef8129366d665968f64d1fc66676d07e16))
* revert fallback on btree indexes when hash is unavailable ([#1858](https://github.com/supatype/auth/issues/1858)) ([1c7202f](https://github.com/supatype/auth/commit/1c7202ff835856562ee66b33be131eca769acf1d))
* revert patch for linkedin_oidc provider error ([#1535](https://github.com/supatype/auth/issues/1535)) ([58ef4af](https://github.com/supatype/auth/commit/58ef4af0b4224b78cd9e59428788d16a8d31e562))
* run release-please again ([#2144](https://github.com/supatype/auth/issues/2144)) ([2560f14](https://github.com/supatype/auth/commit/2560f14ef6ee35f84b7c592290647e0d1c8a3932))
* sanitize redirect URL (remove fragment, query) before pattern matching ([#1974](https://github.com/supatype/auth/issues/1974)) ([ccf20d7](https://github.com/supatype/auth/commit/ccf20d724f31871b71292e0ea867c48e2cdfdbcb))
* serialize jwt as string ([#1657](https://github.com/supatype/auth/issues/1657)) ([98d8324](https://github.com/supatype/auth/commit/98d83245e40d606438eb0afdbf474276179fd91d))
* session upgrade percentage should be based on session, not request ([#2371](https://github.com/supatype/auth/issues/2371)) ([510e68b](https://github.com/supatype/auth/commit/510e68b803ba9110df969c7548ccad85c84f0eb6))
* set rate limit log level to warn ([#1652](https://github.com/supatype/auth/issues/1652)) ([10ca9c8](https://github.com/supatype/auth/commit/10ca9c806e4b67a371897f1b3f93c515764c4240))
* simplify WaitForCleanup ([#1747](https://github.com/supatype/auth/issues/1747)) ([0084625](https://github.com/supatype/auth/commit/0084625ad0790dd7c14b412d932425f4b84bb4c8))
* skip apple oidc issuer check ([#2053](https://github.com/supatype/auth/issues/2053)) ([1c6f18e](https://github.com/supatype/auth/commit/1c6f18e6e573ae1da6875f51d8613992ced057a2))
* skip cleanup for non-2xx status ([#1877](https://github.com/supatype/auth/issues/1877)) ([f572ced](https://github.com/supatype/auth/commit/f572ced3699c7f920deccce1a3539299541ec94c))
* sms verify should update is_anonymous field ([#1580](https://github.com/supatype/auth/issues/1580)) ([e5f98cb](https://github.com/supatype/auth/commit/e5f98cb9e24ecebb0b7dc88c495fd456cc73fcba))
* **social-auth:** default to current_user:read for Figma provider ([#2195](https://github.com/supatype/auth/issues/2195)) ([f409d11](https://github.com/supatype/auth/commit/f409d118ebb958c12f2395c0bf4fb9590ab6c0af))
* stripped binary now includes version ([#2147](https://github.com/supatype/auth/issues/2147)) ([609f169](https://github.com/supatype/auth/commit/609f169f505a1f5750fbbf5e9d477cfb4d879eff))
* tighten email validation rules ([#2304](https://github.com/supatype/auth/issues/2304)) ([33bb372](https://github.com/supatype/auth/commit/33bb37203ae54c7ddecb6373122fae4b4fd38682))
* treat `GOTRUE_MFA_ENABLED` as meaning TOTP enabled on enroll and verify ([#1694](https://github.com/supatype/auth/issues/1694)) ([8015251](https://github.com/supatype/auth/commit/8015251400bd52cbdad3ea28afb83b1cdfe816dd))
* treat empty string as nil in `encrypted_password` ([#1663](https://github.com/supatype/auth/issues/1663)) ([f99286e](https://github.com/supatype/auth/commit/f99286eaed505daf3db6f381265ef6024e7e36d2))
* unlink identity bugs ([#1475](https://github.com/supatype/auth/issues/1475)) ([73e8d87](https://github.com/supatype/auth/commit/73e8d8742de3575b3165a707b5d2f486b2598d9d))
* update aal requirements to update user ([#1766](https://github.com/supatype/auth/issues/1766)) ([25d9874](https://github.com/supatype/auth/commit/25d98743f6cc2cca2b490a087f468c8556ec5e44))
* update contributing to use v1.22 ([#1609](https://github.com/supatype/auth/issues/1609)) ([5894d9e](https://github.com/supatype/auth/commit/5894d9e41e7681512a9904ad47082a705e948c98))
* update copyright year in LICENSE ([#2142](https://github.com/supatype/auth/issues/2142)) ([67fe0b0](https://github.com/supatype/auth/commit/67fe0b0230b147048dc2b9f546df72af5b3bc362))
* update figma token endpoint ([#1952](https://github.com/supatype/auth/issues/1952)) ([18fbbb5](https://github.com/supatype/auth/commit/18fbbb53de04c024b6de829e390145a8452d7ab2))
* update ip mismatch error message ([#1849](https://github.com/supatype/auth/issues/1849)) ([49fbbf0](https://github.com/supatype/auth/commit/49fbbf03917a1085c58e9a1ff76c247ae6bb9ca7))
* update linkedin issuer url ([#1536](https://github.com/supatype/auth/issues/1536)) ([10d6d8b](https://github.com/supatype/auth/commit/10d6d8b1eafa504da2b2a351d1f64a3a832ab1b9))
* update MaxFrequency error message to reflect number of seconds ([#1540](https://github.com/supatype/auth/issues/1540)) ([e81c25d](https://github.com/supatype/auth/commit/e81c25d19551fdebfc5197d96bc220ddb0f8227b))
* update mfa admin methods ([#1774](https://github.com/supatype/auth/issues/1774)) ([567ea7e](https://github.com/supatype/auth/commit/567ea7ebd18eacc5e6daea8adc72e59e94459991))
* update mfa phone migration to be idempotent ([#1687](https://github.com/supatype/auth/issues/1687)) ([fdff1e7](https://github.com/supatype/auth/commit/fdff1e703bccf93217636266f1862bd0a9205edb))
* update migration version ([#2343](https://github.com/supatype/auth/issues/2343)) ([61ef4db](https://github.com/supatype/auth/commit/61ef4dbb5146c4379d495c2fb77c7ade753d1f3b))
* update OpenAPI schema to use 'minimum' instead of 'min' for integer ([5c1deb2](https://github.com/supatype/auth/commit/5c1deb2572143d14c309a1695fe2391e3c52388d))
* update openapi spec for MFA (Phone)  ([#1689](https://github.com/supatype/auth/issues/1689)) ([a3da4b8](https://github.com/supatype/auth/commit/a3da4b89820c37f03ea128889616aca598d99f68))
* upgrade ci Go version ([#1782](https://github.com/supatype/auth/issues/1782)) ([97a48f6](https://github.com/supatype/auth/commit/97a48f6daaa2edda5b568939cbb1007ccdf33cfc))
* upgrade godotenv to v1.5.1 to fix multiline file loading ([#1997](https://github.com/supatype/auth/issues/1997)) ([f2af4b2](https://github.com/supatype/auth/commit/f2af4b250dc7d351ee8d0ede3a814439cac43fee))
* upgrade golang-jwt to v5 ([#1639](https://github.com/supatype/auth/issues/1639)) ([2cb97f0](https://github.com/supatype/auth/commit/2cb97f080fa4695766985cc4792d09476534be68))
* use `appleid.apple.com` as default issuer ([#2068](https://github.com/supatype/auth/issues/2068)) ([963a781](https://github.com/supatype/auth/commit/963a781ee525ef893ec545583e7d385c02995518))
* use `split_words` config option for `AuditLog` ([#2075](https://github.com/supatype/auth/issues/2075)) ([7ecb234](https://github.com/supatype/auth/commit/7ecb234c3d66459c92ba16fd69ed7eb933c4b8a7))
* use api_external_url domain as localname ([#1575](https://github.com/supatype/auth/issues/1575)) ([ed2b490](https://github.com/supatype/auth/commit/ed2b4907244281e4c54aaef74b1f4c8a8e3d97c9))
* use deep equal ([#1672](https://github.com/supatype/auth/issues/1672)) ([8efd57d](https://github.com/supatype/auth/commit/8efd57dab40346762a04bac61b314ce05d6fa69c))
* use pointer for `user.EncryptedPassword` ([#1637](https://github.com/supatype/auth/issues/1637)) ([bbecbd6](https://github.com/supatype/auth/commit/bbecbd61a46b0c528b1191f48d51f166c06f4b16))
* use redirect URL as-is for mobile apps ([#2007](https://github.com/supatype/auth/issues/2007)) ([b36cdcd](https://github.com/supatype/auth/commit/b36cdcdb90b8f0a96aba9572e2643c0dee3bdd9c))
* use signing jwk to sign oauth state ([#1728](https://github.com/supatype/auth/issues/1728)) ([66fd0c8](https://github.com/supatype/auth/commit/66fd0c8434388bbff1e1bf02f40517aca0e9d339))
* use sys/unix instead of syscall ([#1953](https://github.com/supatype/auth/issues/1953)) ([4a6d9bc](https://github.com/supatype/auth/commit/4a6d9bcade28db3c7a6c2c610600665190c9a925))
* user sanitization should clean up email change info too ([#1759](https://github.com/supatype/auth/issues/1759)) ([9d419b4](https://github.com/supatype/auth/commit/9d419b400f0637b10e5c235b8fd5bac0d69352bd))
* validateEmail should normalise emails ([#1790](https://github.com/supatype/auth/issues/1790)) ([2e9b144](https://github.com/supatype/auth/commit/2e9b144a0cbf2d26d3c4c2eafbff1899a36aeb3b))

## [2.187.0](https://github.com/supabase/auth/compare/v2.186.0...v2.187.0) (2026-02-23)


### Features

* add metadata field to all hooks ([#2365](https://github.com/supabase/auth/issues/2365)) ([c675749](https://github.com/supabase/auth/commit/c67574946d1e11c7986d2c868336df0cefbe3452))
* check current password on change ([#2364](https://github.com/supabase/auth/issues/2364)) ([33b87ae](https://github.com/supabase/auth/commit/33b87ae0671aba2e9b4df0ef1d5d1e7906c32129))
* **indexworker:** add max users threshold for rollout ([#2374](https://github.com/supabase/auth/issues/2374)) ([a2066c6](https://github.com/supabase/auth/commit/a2066c6a340fd3ebcaa0a816ab06ee3d6b1afad7))
* **metrics:** added a gauge with version information ([#2375](https://github.com/supabase/auth/issues/2375)) ([911ad0b](https://github.com/supabase/auth/commit/911ad0bae0b65b878acd05208e733f480c76b22f))
* support custom oauth & oidc providers ([#2357](https://github.com/supabase/auth/issues/2357)) ([53021f6](https://github.com/supabase/auth/commit/53021f66597439c14ebb869e567ab4742afd0142))


### Bug Fixes

* case-insensitive Bearer token scheme matching ([#2387](https://github.com/supabase/auth/issues/2387)) ([36d712d](https://github.com/supabase/auth/commit/36d712d27f66721adf58a93ffb9e43d5cc915eca))
* correctly parse JWT ValidMethods from env by enabling split_words ([#2334](https://github.com/supabase/auth/issues/2334)) ([a6076bc](https://github.com/supabase/auth/commit/a6076bc39f63cfca94e2330957031d4f63a4b68e))
* flaky index worker test ([#2366](https://github.com/supabase/auth/issues/2366)) ([961a7e6](https://github.com/supabase/auth/commit/961a7e620109d554ae81ca8227a5107671679982))
* **hooks:** propagate error objects from hook calls ([#2380](https://github.com/supabase/auth/issues/2380)) ([3ca1e88](https://github.com/supabase/auth/commit/3ca1e88df06e7096c8ebb3e1bedf291654f4c66e))
* session upgrade percentage should be based on session, not request ([#2371](https://github.com/supabase/auth/issues/2371)) ([510e68b](https://github.com/supabase/auth/commit/510e68b803ba9110df969c7548ccad85c84f0eb6))

## [2.186.0](https://github.com/supabase/auth/compare/v2.185.0...v2.186.0) (2026-01-28)


### Features

* Add email send operation metrics ([#2311](https://github.com/supabase/auth/issues/2311)) ([0096575](https://github.com/supabase/auth/commit/00965758762301875df2d7e4e552b2346bc09236))
* add Supatype Auth identifier to OAuth redirect URLs ([#2299](https://github.com/supabase/auth/issues/2299)) ([2d3dbc6](https://github.com/supabase/auth/commit/2d3dbc652c1beb47c2eade28b45e94f6e2c56982))
* log sb-auth-user-id, sb-auth-session-id, ... on sign in not just refresh token ([#2342](https://github.com/supabase/auth/issues/2342)) ([a486ada](https://github.com/supabase/auth/commit/a486ada3683bb078b8f396a5ba2e606826f0044b))
* **oauth-server:** store and enforce token_endpoint_auth_method ([#2300](https://github.com/supabase/auth/issues/2300)) ([bcd6cd5](https://github.com/supabase/auth/commit/bcd6cd590a47e963b7afe615c889f62d28cb94a2))
* replace JWT OAuth state with `flow_state.id` UUID ([#2331](https://github.com/supabase/auth/issues/2331)) ([645654d](https://github.com/supabase/auth/commit/645654df63a3da7929840659c065f6a9cdd4ba96))
* upgrade existing sessions to v2 refresh tokens though config value ([#2356](https://github.com/supabase/auth/issues/2356)) ([6fb0e8a](https://github.com/supabase/auth/commit/6fb0e8adc104e3b9119b79506997e29bbb2ca9a2))


### Bug Fixes

* reloader unittest races on writeWg ([#2352](https://github.com/supabase/auth/issues/2352)) ([088b714](https://github.com/supabase/auth/commit/088b7149d6857cfe65e4338c1ee9e079688f8c92))
* update migration version ([#2343](https://github.com/supabase/auth/issues/2343)) ([61ef4db](https://github.com/supabase/auth/commit/61ef4dbb5146c4379d495c2fb77c7ade753d1f3b))

## [2.185.0](https://github.com/supabase/auth/compare/v2.184.0...v2.185.0) (2026-01-12)


### Features

* Add Sb-Forwarded-For header and IP-based rate limiting ([#2295](https://github.com/supabase/auth/issues/2295)) ([e8f679b](https://github.com/supabase/auth/commit/e8f679b9e8fcd8cb543ed43cd9cd6a73bbbf4fa7))
* allow amr claim to be array of strings or objects ([#2274](https://github.com/supabase/auth/issues/2274)) ([607da43](https://github.com/supabase/auth/commit/607da43b697b0af1de0da5f966f5b63ff033fefb))
* reset main branch to 2.185.0 ([#2325](https://github.com/supabase/auth/issues/2325)) ([b9d0500](https://github.com/supabase/auth/commit/b9d050029ce90efc083f08a1e8df629faf20e8cd))
* Treat rate limit header value as comma-separated list ([#2282](https://github.com/supabase/auth/issues/2282)) ([5f2e279](https://github.com/supabase/auth/commit/5f2e2792560d57dd14fbf3e69c133a7ec8518c4d))


### Bug Fixes

* additional provider and issuer checks ([#2326](https://github.com/supabase/auth/issues/2326)) ([cb79a74](https://github.com/supabase/auth/commit/cb79a7414e8b2bff30113bdf2b9ec6d6e93c1146))
* check each type independently ([#2290](https://github.com/supabase/auth/issues/2290)) ([d9de0af](https://github.com/supabase/auth/commit/d9de0af3a173ae3e9ab0219c07652675f8be1761))
* fix the wrong error return value ([#1950](https://github.com/supabase/auth/issues/1950)) ([e2dfb5d](https://github.com/supabase/auth/commit/e2dfb5d4222e5edc569b54d057db9ed4375a19d8))
* **indexworker:** remove pg_trgm extension ([#2301](https://github.com/supabase/auth/issues/2301)) ([c553b10](https://github.com/supabase/auth/commit/c553b10e5f3b7a8c430b20babe0e7c96178b1c91))
* **oauth-server:** allow custom URI schemes in client redirect URIs ([#2298](https://github.com/supabase/auth/issues/2298)) ([ea72f57](https://github.com/supabase/auth/commit/ea72f57f99633b33cc7b30b4a0b74ed8314b71e6))
* tighten email validation rules ([#2304](https://github.com/supabase/auth/issues/2304)) ([33bb372](https://github.com/supabase/auth/commit/33bb37203ae54c7ddecb6373122fae4b4fd38682))

## [2.184.0](https://github.com/supabase/auth/compare/v2.183.0...v2.184.0) (2025-12-08)


### Features

* increment refresh token counter by 2 for mfa verify ([#2284](https://github.com/supabase/auth/issues/2284)) ([2a38668](https://github.com/supabase/auth/commit/2a3866854fe7cb58a6cb84e7a82ce5d07bb920ee))
* load template cache at startup for fault tolerance ([#2261](https://github.com/supabase/auth/issues/2261)) ([511c3a4](https://github.com/supabase/auth/commit/511c3a4e12819d313840cd5342ae6a76d4708cfc))
* **oauth:** add support for X/Twitter v2 provider ([#2275](https://github.com/supabase/auth/issues/2275)) ([7f36eb0](https://github.com/supabase/auth/commit/7f36eb053286038d01ba1650dd48a15508550ce0))

## [2.183.0](https://github.com/supabase/auth/compare/v2.182.1...v2.183.0) (2025-11-20)


### Features

* async, concurrent index creation for users table ([#2239](https://github.com/supabase/auth/issues/2239)) ([a1146bf](https://github.com/supabase/auth/commit/a1146bf7eecb35e237350dda7ae62328cbb5acfe))
* **indexworker:** use `auth_trgm` extension if available ([#2263](https://github.com/supabase/auth/issues/2263)) ([05daa43](https://github.com/supabase/auth/commit/05daa437131bd220e01a0e33df75f4b9afa72bb6))
* **oauthserver:** add OpenID Connect support ([#2250](https://github.com/supabase/auth/issues/2250)) ([162788f](https://github.com/supabase/auth/commit/162788ff960c060318324f11f673c09c0da41d5e))
* **oauthserver:** update oauth grant list & authorization details response structure ([#2247](https://github.com/supabase/auth/issues/2247)) ([137ea92](https://github.com/supabase/auth/commit/137ea92c00a0c1a7654fb8bcf0c1b5313901349f))
* **oauthserver:** use `NewOAuthServerAuthorizationParams` & configurable ttl for authorization ([#2254](https://github.com/supabase/auth/issues/2254)) ([61632f8](https://github.com/supabase/auth/commit/61632f8c0401b6c816ea7427d351ec623ce5258f))


### Bug Fixes

* **indexworker:** detect which schema `pg_trgm` exists in ([#2260](https://github.com/supabase/auth/issues/2260)) ([4be12b3](https://github.com/supabase/auth/commit/4be12b3e7c0a30b1e289ab81348548f72ab32ba5))
* look for refresh token on mfa verification only in v1 ([#2249](https://github.com/supabase/auth/issues/2249)) ([2906b24](https://github.com/supabase/auth/commit/2906b2424d0aa804031e66cf92f008289b8a9c77))
* mfa verify now works with refresh token algorithm v2 ([#2246](https://github.com/supabase/auth/issues/2246)) ([4e8275f](https://github.com/supabase/auth/commit/4e8275f915c4d84186d17b41c86a9277055a55e4))
* **social-auth:** default to current_user:read for Figma provider ([#2195](https://github.com/supabase/auth/issues/2195)) ([f409d11](https://github.com/supabase/auth/commit/f409d118ebb958c12f2395c0bf4fb9590ab6c0af))

## [2.182.1](https://github.com/supabase/auth/compare/v2.182.0...v2.182.1) (2025-11-05)


### Bug Fixes

* japanese dot example fix ([#2243](https://github.com/supabase/auth/issues/2243)) ([3a5f4b2](https://github.com/supabase/auth/commit/3a5f4b211a0f50bd1957f5a41467fc5aa6a01ca6))

## [2.182.0](https://github.com/supabase/auth/compare/v2.181.0...v2.182.0) (2025-11-05)


### Features

* **oauthserver:** add authorization list and revoke endpoints ([#2232](https://github.com/supabase/auth/issues/2232)) ([cc640b2](https://github.com/supabase/auth/commit/cc640b277989d57b39f3805cd9433ef4fe16bf83))


### Bug Fixes

* hostname can be empty with redirect urls ([#2241](https://github.com/supabase/auth/issues/2241)) ([f5a4cba](https://github.com/supabase/auth/commit/f5a4cbac73de28cc4b04c5c9725b70517cb131d3))

## [2.181.0](https://github.com/supabase/auth/compare/v2.180.0...v2.181.0) (2025-10-31)


### Features

* add `.well-known/openid-configuration` ([#2197](https://github.com/supabase/auth/issues/2197)) ([9a8d0df](https://github.com/supabase/auth/commit/9a8d0df63bb5089e1705f9d970669bfc97ed345e))
* add `auth_migration` annotation for the migrations ([#2234](https://github.com/supabase/auth/issues/2234)) ([b276d0b](https://github.com/supabase/auth/commit/b276d0bcf4d1ee08fce8c2f7146423e9aaf34dfb))
* add advisor to notify you when to double the max connection pool ([#2167](https://github.com/supabase/auth/issues/2167)) ([a72f5d9](https://github.com/supabase/auth/commit/a72f5d95795ac070e248007c0c38196f47ea5046))
* add after-user-created hook ([#2169](https://github.com/supabase/auth/issues/2169)) ([bd80df8](https://github.com/supabase/auth/commit/bd80df8a888a7de023557a97b65b21419d3029e7))
* add support for account changes notifications in email send hook ([#2192](https://github.com/supabase/auth/issues/2192)) ([6b382ae](https://github.com/supabase/auth/commit/6b382ae3a96bbe052395bdfa30fb49f717e5ad68))
* email address changed notification ([#2181](https://github.com/supabase/auth/issues/2181)) ([047f851](https://github.com/supabase/auth/commit/047f85136c9223ca99cb0169ba82343088fbbfd8))
* identity linked/unlinked notifications ([#2185](https://github.com/supabase/auth/issues/2185)) ([7d46936](https://github.com/supabase/auth/commit/7d46936e145479be1e508b52549c7fca3c59fc2f))
* introduce v2 refresh token algorithm ([#2216](https://github.com/supabase/auth/issues/2216)) ([dea5b8e](https://github.com/supabase/auth/commit/dea5b8e5353ea240c658b030325432ce512f18a8))
* MFA factor enrollment notifications ([#2183](https://github.com/supabase/auth/issues/2183)) ([53db712](https://github.com/supabase/auth/commit/53db712f0c3ffae6d61ea3ddcff5e8d7a33639b9))
* notify users when their phone number has changed ([#2184](https://github.com/supabase/auth/issues/2184)) ([21f3070](https://github.com/supabase/auth/commit/21f30702a62d722bce32972d4b2fcef1da6e2177))
* **oauthserver:** add OAuth client admin update endpoint ([#2231](https://github.com/supabase/auth/issues/2231)) ([6296a5a](https://github.com/supabase/auth/commit/6296a5a226b3c60bcd9d20786750a808af9cd529))
* properly handle redirect url fragments and unusual hostnames ([#2200](https://github.com/supabase/auth/issues/2200)) ([aa0ac5b](https://github.com/supabase/auth/commit/aa0ac5b9a8af26d4b779e48ec4da2ab06a6dc15e))
* store latest challenge/attestation data ([#2179](https://github.com/supabase/auth/issues/2179)) ([01ebce1](https://github.com/supabase/auth/commit/01ebce1bf01b563105d653ff168a16e72c12d481))
* support percentage based db limits with reload support ([#2177](https://github.com/supabase/auth/issues/2177)) ([1731466](https://github.com/supabase/auth/commit/1731466903539569ec5b308db4e39eb33c653b94))
* webauthn support schema changes, update openapi.yaml ([#2163](https://github.com/supabase/auth/issues/2163)) ([68cb8d2](https://github.com/supabase/auth/commit/68cb8d2ba3ded878c68d7cb76465bfaaac58436a))


### Bug Fixes

* gosec incorrectly warns about accessing signature[64] ([#2222](https://github.com/supabase/auth/issues/2222)) ([bca6626](https://github.com/supabase/auth/commit/bca66268dc4f81821c194a26dcf76209d1c696de))
* **openapi:** add missing OAuth client registration fields ([#2227](https://github.com/supabase/auth/issues/2227)) ([cf39a8a](https://github.com/supabase/auth/commit/cf39a8ae2cc386f2672f0ecbb8d84dd77f04e56f))

## [2.180.0](https://github.com/supabase/auth/compare/v2.179.0...v2.180.0) (2025-09-23)


### Features

* add OAuth client type ([#2152](https://github.com/supabase/auth/issues/2152)) ([b118f1f](https://github.com/supabase/auth/commit/b118f1f00c3c846095c25c34092e38aeebfdf2db))
* add phone to sms webhook payload ([#2160](https://github.com/supabase/auth/issues/2160)) ([d475ac1](https://github.com/supabase/auth/commit/d475ac1f20a0814f59d4bc1370801f915a9ba4d4))
* background template reloading p1 - baseline decomposition ([#2148](https://github.com/supabase/auth/issues/2148)) ([746c937](https://github.com/supabase/auth/commit/746c937f7c57ba256d942df334ab9ee354509587))
* config reloading with fsnotify, poller fallback, and signals ([#2161](https://github.com/supabase/auth/issues/2161)) ([c77d512](https://github.com/supabase/auth/commit/c77d51203fc52c1c9a9f7dc56ca1c076e018fc54))
* enhance issuer URL validation in OAuth server metadata ([#2164](https://github.com/supabase/auth/issues/2164)) ([a9424d2](https://github.com/supabase/auth/commit/a9424d25909e074db395b620dc9999724bf4a03c))
* implement OAuth2 authorization endpoint ([#2107](https://github.com/supabase/auth/issues/2107)) ([5318552](https://github.com/supabase/auth/commit/53185526b07cb2c27f6a81782a6c24610e39d6fe))
* **oauth2:** add `/oauth/token` endpoint ([#2159](https://github.com/supabase/auth/issues/2159)) ([a89a0b0](https://github.com/supabase/auth/commit/a89a0b054e87fee4e193aab4fff7677b56775386))
* **oauth2:** add admin endpoint to regenerate OAuth client secrets ([#2170](https://github.com/supabase/auth/issues/2170)) ([0bd1c28](https://github.com/supabase/auth/commit/0bd1c285aaf3bbb3f3d6e2e131aabfe5cabf0fa5))
* **oauth2:** return redirect_uri on GET authorization ([#2175](https://github.com/supabase/auth/issues/2175)) ([b0a0c3e](https://github.com/supabase/auth/commit/b0a0c3e48c8c8686d4cc3f82abd2ed326c297614))
* **oauth2:** use `id` field as the public client_id ([#2154](https://github.com/supabase/auth/issues/2154)) ([86b7de4](https://github.com/supabase/auth/commit/86b7de45c9432ea6ee9bd7c7e9cfe96e038fe2bc))
* **openapi:** add OAuth 2.1 server endpoints and clarify OAuth modes ([#2165](https://github.com/supabase/auth/issues/2165)) ([1f804a2](https://github.com/supabase/auth/commit/1f804a2795012a1a165ff07afdb9dd98ad8ff291))
* password changed email notification ([#2176](https://github.com/supabase/auth/issues/2176)) ([fe0fd04](https://github.com/supabase/auth/commit/fe0fd04c9f5558d0165a94c7c080fb15c036d08f))
* support `transfer_sub` in apple id tokens ([#2162](https://github.com/supabase/auth/issues/2162)) ([8a71006](https://github.com/supabase/auth/commit/8a71006486027c0850a58ec6e94f62a1607d1d48))


### Bug Fixes

* ensure request context exists in API db operations ([#2171](https://github.com/supabase/auth/issues/2171)) ([060a992](https://github.com/supabase/auth/commit/060a99278d8e3ec4a78ca61b95a9acf0e7052948))
* **makefile:** remove invalid @ symbol from shell commands ([#2168](https://github.com/supabase/auth/issues/2168)) ([e6afe45](https://github.com/supabase/auth/commit/e6afe4529859e1ee92ed5c259e04c9fe56de22cf))
* **oauth2:** switch to Origin header for request validation ([#2174](https://github.com/supabase/auth/issues/2174)) ([42bc9ab](https://github.com/supabase/auth/commit/42bc9ab7db24ce1902fef21ba5e90a2128617669))

## [2.179.0](https://github.com/supabase/auth/compare/v2.178.0...v2.179.0) (2025-08-28)


### Features

* add oauth2 client support ([#2098](https://github.com/supabase/auth/issues/2098)) ([8fae015](https://github.com/supabase/auth/commit/8fae01581d122bba95a3742dc212284f9a21dc4d))
* experimental own linking domains per provider ([#2119](https://github.com/supabase/auth/issues/2119)) ([747bf3b](https://github.com/supabase/auth/commit/747bf3b15fd9e371c9330e75fe2e5de8b89ce14d))
* fetch email from snapchat oauth provider if available for consistency ([#2110](https://github.com/supabase/auth/issues/2110)) ([7507822](https://github.com/supabase/auth/commit/750782246e736093131ba2eb1015fc73083d99ab))
* implement link identity with oidc / native sign in ([#2108](https://github.com/supabase/auth/issues/2108)) ([5f0ec87](https://github.com/supabase/auth/commit/5f0ec8709231c57b57aa06160e18bc9e52ec9002))
* implements email-less accounts with oauth ([#2105](https://github.com/supabase/auth/issues/2105)) ([9a61dae](https://github.com/supabase/auth/commit/9a61dae788311a086ce8e72b52c21e031857adf7))
* introduce request-scoped background tasks & async mail sending ([#2126](https://github.com/supabase/auth/issues/2126)) ([2c8ea61](https://github.com/supabase/auth/commit/2c8ea6113ae7381106ed7c67d7a45f7ef87195c7))
* refactor mailer client wiring and add validation wrapper ([#2130](https://github.com/supabase/auth/issues/2130)) ([68c40a6](https://github.com/supabase/auth/commit/68c40a6a494029d8d704b14abbe85171a7dc8d12))
* support multiple `aud` for the external providers ([#2117](https://github.com/supabase/auth/issues/2117)) ([ca5792e](https://github.com/supabase/auth/commit/ca5792e41a48f20a395646015c28ce272355bf63))
* use `slices.Contains` instead of for loops ([#2111](https://github.com/supabase/auth/issues/2111)) ([9f22682](https://github.com/supabase/auth/commit/9f2268263118713d3390ce4617ccf21bc2c031eb))


### Bug Fixes

* add `id-token` permission to ci ([#2143](https://github.com/supabase/auth/issues/2143)) ([79209c0](https://github.com/supabase/auth/commit/79209c0e35afa82ec8822a343108d6a690e14229))
* add missing param ([#2125](https://github.com/supabase/auth/issues/2125)) ([c0b75f6](https://github.com/supabase/auth/commit/c0b75f66229410e6e5fbc7cd1ae9066cec54c5d7))
* change s3 artifact upload role ([#2145](https://github.com/supabase/auth/issues/2145)) ([767e371](https://github.com/supabase/auth/commit/767e37131aa01bf6cb27dbc62b2928e7cc701893))
* remove requirement of empty content-type on 204 ([#2128](https://github.com/supabase/auth/issues/2128)) ([ecc97e0](https://github.com/supabase/auth/commit/ecc97e0fac7cb1bd736ef6db435a0a5fb224e954))
* run release-please again ([#2144](https://github.com/supabase/auth/issues/2144)) ([2560f14](https://github.com/supabase/auth/commit/2560f14ef6ee35f84b7c592290647e0d1c8a3932))
* stripped binary now includes version ([#2147](https://github.com/supabase/auth/issues/2147)) ([609f169](https://github.com/supabase/auth/commit/609f169f505a1f5750fbbf5e9d477cfb4d879eff))
* update copyright year in LICENSE ([#2142](https://github.com/supabase/auth/issues/2142)) ([67fe0b0](https://github.com/supabase/auth/commit/67fe0b0230b147048dc2b9f546df72af5b3bc362))

## [2.178.0](https://github.com/supabase/auth/compare/v2.177.0...v2.178.0) (2025-08-05)


### Features

* add sign in with ethereum ([#2069](https://github.com/supabase/auth/issues/2069)) ([079b242](https://github.com/supabase/auth/commit/079b2427b8ed312880b60e89cc79b716fe9ae73d))
* add support for managing SSO providers by resource_id ([#2081](https://github.com/supabase/auth/issues/2081)) ([5ca4489](https://github.com/supabase/auth/commit/5ca44893964d3b12a24ea26302b23f4976f768a0))
* log all audit events separately to prevent missing events ([#2086](https://github.com/supabase/auth/issues/2086)) ([3b666f5](https://github.com/supabase/auth/commit/3b666f51f56db778848730d74ac140f02b0cb522))
* skip nonce check for Facebook Limited Login auth ([#2082](https://github.com/supabase/auth/issues/2082)) ([f1b15ff](https://github.com/supabase/auth/commit/f1b15ffdb9b1f1af873a147fdb5d039382becb2e))
* support ledger solana offchain message signing ([#2093](https://github.com/supabase/auth/issues/2093)) ([4c94443](https://github.com/supabase/auth/commit/4c944431558aaca3c945c472dc5a27077f6dfa75))

## [2.177.0](https://github.com/supabase/auth/compare/v2.176.1...v2.177.0) (2025-07-05)


### Features

* add option to disable writing to `audit_log_entries` ([#2073](https://github.com/supabase/auth/issues/2073)) ([80758dd](https://github.com/supabase/auth/commit/80758dd880b82e9b96d7185d9d0a0850b8c6f19d))
* add snapchat provider ([#2071](https://github.com/supabase/auth/issues/2071)) ([fca8ea4](https://github.com/supabase/auth/commit/fca8ea4a701eafb587438a159e19f5488c82a178))
* enhance login analytics ([#2078](https://github.com/supabase/auth/issues/2078)) ([1aed4a2](https://github.com/supabase/auth/commit/1aed4a27fdc54d9c4d01f17d49dcaadb25400f18))
* fallback to jwt secret if alg is `HS256` and the `kid` is not recognized ([#2072](https://github.com/supabase/auth/issues/2072)) ([8fa99bd](https://github.com/supabase/auth/commit/8fa99bd6cab91c0bf093fdcdb912054113ea66ba))
* ignore `aud` claim from admin jwt (`service_role` never had one) ([#2070](https://github.com/supabase/auth/issues/2070)) ([57eddcb](https://github.com/supabase/auth/commit/57eddcb45ce97004c26f6d65351447d7dc654162))


### Bug Fixes

* add missing provider info to signedup audit logs ([#2061](https://github.com/supabase/auth/issues/2061)) ([c6e0cbe](https://github.com/supabase/auth/commit/c6e0cbefe5b609ac3362c23d0f7cb9d9bb04abc9))
* **auditlog:** keep writing to logs even postgres is disabled ([#2076](https://github.com/supabase/auth/issues/2076)) ([b89bc32](https://github.com/supabase/auth/commit/b89bc32de5adc9d458e7f95ad9b08a99604c70d8))
* do not log fatal when http server successfully closes ([#2065](https://github.com/supabase/auth/issues/2065)) ([1f7de6c](https://github.com/supabase/auth/commit/1f7de6c65f31ef0bbb80899369989b13ab5a517f))
* invites should send another email when user exists ([#2058](https://github.com/supabase/auth/issues/2058)) ([96469bd](https://github.com/supabase/auth/commit/96469bd01b9c37f938aabdb0434a054a111cf963))
* use `appleid.apple.com` as default issuer ([#2068](https://github.com/supabase/auth/issues/2068)) ([963a781](https://github.com/supabase/auth/commit/963a781ee525ef893ec545583e7d385c02995518))
* use `split_words` config option for `AuditLog` ([#2075](https://github.com/supabase/auth/issues/2075)) ([7ecb234](https://github.com/supabase/auth/commit/7ecb234c3d66459c92ba16fd69ed7eb933c4b8a7))

## [2.176.1](https://github.com/supabase/auth/compare/v2.176.0...v2.176.1) (2025-06-11)


### Bug Fixes

* new `odic.Provider` for apple with insecure issuer url context ([#2055](https://github.com/supabase/auth/issues/2055)) ([23d69f1](https://github.com/supabase/auth/commit/23d69f1c450b4a24a262cb25112e68408857a3b2))
* skip apple oidc issuer check ([#2053](https://github.com/supabase/auth/issues/2053)) ([1c6f18e](https://github.com/supabase/auth/commit/1c6f18e6e573ae1da6875f51d8613992ced057a2))

## [2.176.0](https://github.com/supabase/auth/compare/v2.175.0...v2.176.0) (2025-06-11)


### Features

* Add custom claims from Keycloak user token ([#1917](https://github.com/supabase/auth/issues/1917)) ([1365aaa](https://github.com/supabase/auth/commit/1365aaa45569fc9e7c3497e744e0e80cf237d617))


### Bug Fixes

* accept ID tokens from all `account.apple.com` and `appleid.apple.com` ([#2050](https://github.com/supabase/auth/issues/2050)) ([82aa167](https://github.com/supabase/auth/commit/82aa167cae01658b5319914f3412d78876955106))

## [2.175.0](https://github.com/supabase/auth/compare/v2.174.0...v2.175.0) (2025-06-03)


### Features

* hooks round 5 (Option 2) - add before-user-created hook ([#2034](https://github.com/supabase/auth/issues/2034)) ([b53f6b0](https://github.com/supabase/auth/commit/b53f6b0d0e056bf3e84884847ab4608ffc9efd61))


### Bug Fixes

* email-sendhook - bug in email change verification ([#2044](https://github.com/supabase/auth/issues/2044)) ([be20654](https://github.com/supabase/auth/commit/be20654ec3af21b93a8d7482a5673b5c8c60ac8a))

## [2.174.0](https://github.com/supabase/auth/compare/v2.173.0...v2.174.0) (2025-05-23)


### Features

* hooks round 2 - remove indirection and simplify error handling ([#2025](https://github.com/supabase/auth/issues/2025)) ([26e23f0](https://github.com/supabase/auth/commit/26e23f05acd1e1a959c3e04764a569ea0364d947))
* hooks round 4 - update tests to use require package ([#2030](https://github.com/supabase/auth/issues/2030)) ([aaf93df](https://github.com/supabase/auth/commit/aaf93df50ebfb489c6335e2c1b846dc5cee18767))


### Bug Fixes

* amr claim should contain provider_id for sso method ([#2033](https://github.com/supabase/auth/issues/2033)) ([33741e1](https://github.com/supabase/auth/commit/33741e18d2e0adb691e650355337924f9ccfd91f))

## [2.173.0](https://github.com/supabase/auth/compare/v2.172.1...v2.173.0) (2025-05-17)


### Features

* add support packages for end-to-end testing ([#2021](https://github.com/supabase/auth/issues/2021)) ([269ddfe](https://github.com/supabase/auth/commit/269ddfe18718ae74535f7227eb75f67667275140))


### Bug Fixes

* add `supafast` tarball for upgrading auth via supabase-admin-api ([#2009](https://github.com/supabase/auth/issues/2009)) ([9b55785](https://github.com/supabase/auth/commit/9b557855a3ab80ee93ab95159055a444bff53f01))
* allow HTTP with localhost in solana ([#2027](https://github.com/supabase/auth/issues/2027)) ([3ee02f0](https://github.com/supabase/auth/commit/3ee02f085df206dcd3e6fa79f2d583148ebc52b8))
* fix `supafast` tarball generation ([#2011](https://github.com/supabase/auth/issues/2011)) ([88bb2c0](https://github.com/supabase/auth/commit/88bb2c0638863f94f9f0d7f4ca88ba04929dfd55))

## [2.172.1](https://github.com/supabase/auth/compare/v2.172.0...v2.172.1) (2025-05-05)


### Bug Fixes

* use redirect URL as-is for mobile apps ([#2007](https://github.com/supabase/auth/issues/2007)) ([b36cdcd](https://github.com/supabase/auth/commit/b36cdcdb90b8f0a96aba9572e2643c0dee3bdd9c))

## [2.172.0](https://github.com/supabase/auth/compare/v2.171.0...v2.172.0) (2025-05-04)


### Features

* fix large group claim handling in azure id tokens ([#1995](https://github.com/supabase/auth/issues/1995)) ([2f323fe](https://github.com/supabase/auth/commit/2f323fe3ce2c1d24343d822ac093f28fdda3a4a9))
* use `global_user_id` over `sub` for `vercel_marketplace` issuer ([#1990](https://github.com/supabase/auth/issues/1990)) ([f94f97e](https://github.com/supabase/auth/commit/f94f97e8d3e530d730d9352a14b477fd33548df2))


### Bug Fixes

* azure overage claims start with single `_` not two ([#1999](https://github.com/supabase/auth/issues/1999)) ([29f3440](https://github.com/supabase/auth/commit/29f3440d6376fac22568284d5b417836bf335a74))
* remove azure claim overage code. ([#2005](https://github.com/supabase/auth/issues/2005)) ([63dce14](https://github.com/supabase/auth/commit/63dce14488f92d9e0e67028cd0ae6e002ebf532a))
* resolving azure overage claim should include `api-version=1.6` query parameter ([#2000](https://github.com/supabase/auth/issues/2000)) ([44890d0](https://github.com/supabase/auth/commit/44890d0a6df903e765bcde509231a78f61890bec))
* upgrade godotenv to v1.5.1 to fix multiline file loading ([#1997](https://github.com/supabase/auth/issues/1997)) ([f2af4b2](https://github.com/supabase/auth/commit/f2af4b250dc7d351ee8d0ede3a814439cac43fee))

## [2.171.0](https://github.com/supabase/auth/compare/v2.170.0...v2.171.0) (2025-04-14)


### Features

* add sign in with solana (EIP-4361) support ([#1918](https://github.com/supabase/auth/issues/1918)) ([d121546](https://github.com/supabase/auth/commit/d1215464d4c81bb6e2e210df81ba0263d90ffb64))
* allow invalid config directories ([#1969](https://github.com/supabase/auth/issues/1969)) ([6b842f6](https://github.com/supabase/auth/commit/6b842f6b304bba5f886c6bf8b5675d914f881a2d))
* allow limiting lifespan of low-aal sessions ([#1942](https://github.com/supabase/auth/issues/1942)) ([d7a9ca6](https://github.com/supabase/auth/commit/d7a9ca62a7a09edd864f0b968c1882f5e464e662))
* Block specific outgoing mail servers ([#1971](https://github.com/supabase/auth/issues/1971)) ([091aef9](https://github.com/supabase/auth/commit/091aef945a764ee8d3b80ae8c5ed5d88dd582d03))
* refactor hooks out of api package ([#1976](https://github.com/supabase/auth/issues/1976)) ([c5904c0](https://github.com/supabase/auth/commit/c5904c05d9dce4366e6527aa40e439a3c8c460bb))
* separate web3 rate limits from other `/token?grant_type=...` ([#1985](https://github.com/supabase/auth/issues/1985)) ([8b23382](https://github.com/supabase/auth/commit/8b233820e41fedd18338eb37345ecbb0beb350ce))


### Bug Fixes

* explicit permisions on actions ([#1978](https://github.com/supabase/auth/issues/1978)) ([06e9ead](https://github.com/supabase/auth/commit/06e9ead3e09e77631597a953a535cb93dd006c7f))
* propagate error when when confirming phone ([#1939](https://github.com/supabase/auth/issues/1939)) ([e882b42](https://github.com/supabase/auth/commit/e882b42f3929ab2e587a41ba6593edaf237e5535))
* redirects must not be to ip addresses ([#1984](https://github.com/supabase/auth/issues/1984)) ([347e23a](https://github.com/supabase/auth/commit/347e23a98c2ee362620d2711d12a76d7bc266a8f))
* sanitize redirect URL (remove fragment, query) before pattern matching ([#1974](https://github.com/supabase/auth/issues/1974)) ([ccf20d7](https://github.com/supabase/auth/commit/ccf20d724f31871b71292e0ea867c48e2cdfdbcb))

## [2.170.0](https://github.com/supabase/auth/compare/v2.169.0...v2.170.0) (2025-03-06)


### Features

* improvements to config reloader, 100% coverage ([#1933](https://github.com/supabase/auth/issues/1933)) ([21c2256](https://github.com/supabase/auth/commit/21c2256806ab4950e9bfc0af0472a64f7d9112a7))
* increase test coverage in conf package to 100% ([#1937](https://github.com/supabase/auth/issues/1937)) ([bc57c1c](https://github.com/supabase/auth/commit/bc57c1c25769905b29bfc9e89bf3d6b65b1030ea))


### Bug Fixes

* enable SO_REUSEPORT in listener config ([#1936](https://github.com/supabase/auth/issues/1936)) ([a474b80](https://github.com/supabase/auth/commit/a474b80cc1075eb32a7e72a05b0cdb561e61770b))
* ignore not found error to check for pkce prefix later ([#1929](https://github.com/supabase/auth/issues/1929)) ([fbbebcc](https://github.com/supabase/auth/commit/fbbebccd5da21ea22323e6f8f853df9168c4c41e))
* log version & migration count ([#1934](https://github.com/supabase/auth/issues/1934)) ([8078cdc](https://github.com/supabase/auth/commit/8078cdc6f275c97d84c0ba20963327af900b84d0))
* update figma token endpoint ([#1952](https://github.com/supabase/auth/issues/1952)) ([18fbbb5](https://github.com/supabase/auth/commit/18fbbb53de04c024b6de829e390145a8452d7ab2))
* use sys/unix instead of syscall ([#1953](https://github.com/supabase/auth/issues/1953)) ([4a6d9bc](https://github.com/supabase/auth/commit/4a6d9bcade28db3c7a6c2c610600665190c9a925))

## [2.169.0](https://github.com/supabase/auth/compare/v2.168.0...v2.169.0) (2025-01-27)


### Features

* add an optional burstable rate limiter ([#1924](https://github.com/supabase/auth/issues/1924)) ([1f06f58](https://github.com/supabase/auth/commit/1f06f58e1434b91612c0d96c8c0435d26570f3e2))
* cover 100% of crypto with tests ([#1892](https://github.com/supabase/auth/issues/1892)) ([174198e](https://github.com/supabase/auth/commit/174198e56f8e9b8470a717d0021c626130288d2e))


### Bug Fixes

* convert refreshed_at to UTC before updating ([#1916](https://github.com/supabase/auth/issues/1916)) ([a4c692f](https://github.com/supabase/auth/commit/a4c692f6cb1b8bf4c47ea012872af5ce93382fbf))
* correct casing of API key authentication in openapi.yaml ([0cfd177](https://github.com/supabase/auth/commit/0cfd177b8fb1df8f62e84fbd3761ef9f90c384de))
* improve invalid channel error message returned ([#1908](https://github.com/supabase/auth/issues/1908)) ([f72f0ee](https://github.com/supabase/auth/commit/f72f0eee328fa0aa041155f5f5dc305f0874d2bf))
* improve saml assertion logging ([#1915](https://github.com/supabase/auth/issues/1915)) ([d6030cc](https://github.com/supabase/auth/commit/d6030ccd271a381e2a6ababa11a5beae4b79e5c3))

## [2.168.0](https://github.com/supabase/auth/compare/v2.167.0...v2.168.0) (2025-01-06)


### Features

* set `email_verified` to true on all identities with the verified email ([#1902](https://github.com/supabase/auth/issues/1902)) ([307892f](https://github.com/supabase/auth/commit/307892f85b39150074fbb80b9c8f45ac3312aae2))

## [2.167.0](https://github.com/supabase/auth/compare/v2.166.0...v2.167.0) (2024-12-24)


### Features

* fix argon2 parsing and comparison ([#1887](https://github.com/supabase/auth/issues/1887)) ([9dbe6ef](https://github.com/supabase/auth/commit/9dbe6ef931ae94e621d55a5f7aea4b7ee0449949))

## [2.166.0](https://github.com/supabase/auth/compare/v2.165.0...v2.166.0) (2024-12-23)


### Features

* switch to googleapis/release-please-action, bump to 2.166.0 ([#1883](https://github.com/supabase/auth/issues/1883)) ([11a312f](https://github.com/supabase/auth/commit/11a312fcf77771b3732f2f439078225895df7a85))


### Bug Fixes

* check if session is nil ([#1873](https://github.com/supabase/auth/issues/1873)) ([fd82601](https://github.com/supabase/auth/commit/fd82601917adcd9f8c38263953eb1ef098b26b7f))
* email_verified field not being updated on signup confirmation ([#1868](https://github.com/supabase/auth/issues/1868)) ([483463e](https://github.com/supabase/auth/commit/483463e49eec7b2974cca05eadca6b933b2145b5))
* handle user banned error code ([#1851](https://github.com/supabase/auth/issues/1851)) ([a6918f4](https://github.com/supabase/auth/commit/a6918f49baee42899b3ae1b7b6bc126d84629c99))
* Revert "fix: revert fallback on btree indexes when hash is unavailable" ([#1859](https://github.com/supabase/auth/issues/1859)) ([9fe5b1e](https://github.com/supabase/auth/commit/9fe5b1eebfafb385d6b5d10196aeb2a1964ab296))
* skip cleanup for non-2xx status ([#1877](https://github.com/supabase/auth/issues/1877)) ([f572ced](https://github.com/supabase/auth/commit/f572ced3699c7f920deccce1a3539299541ec94c))

## [2.165.1](https://github.com/supabase/auth/compare/v2.165.0...v2.165.1) (2024-12-06)


### Bug Fixes

* allow setting the mailer service headers as strings ([#1861](https://github.com/supabase/auth/issues/1861)) ([7907b56](https://github.com/supabase/auth/commit/7907b566228f7e2d76049b44cfe0cc808c109100))

## [2.165.0](https://github.com/supabase/auth/compare/v2.164.0...v2.165.0) (2024-12-05)


### Features

* add email validation function to lower bounce rates ([#1845](https://github.com/supabase/auth/issues/1845)) ([2c291f0](https://github.com/supabase/auth/commit/2c291f0356f3e91063b6b43bf2a21625b0ce0ebd))
* use embedded migrations for `migrate` command ([#1843](https://github.com/supabase/auth/issues/1843)) ([e358da5](https://github.com/supabase/auth/commit/e358da5f0e267725a77308461d0a4126436fc537))


### Bug Fixes

* fallback on btree indexes when hash is unavailable ([#1856](https://github.com/supabase/auth/issues/1856)) ([b33bc31](https://github.com/supabase/auth/commit/b33bc31c07549dc9dc221100995d6f6b6754fd3a))
* return the error code instead of status code ([#1855](https://github.com/supabase/auth/issues/1855)) ([834a380](https://github.com/supabase/auth/commit/834a380d803ae9ce59ce5ee233fa3a78a984fe68))
* revert fallback on btree indexes when hash is unavailable ([#1858](https://github.com/supabase/auth/issues/1858)) ([1c7202f](https://github.com/supabase/auth/commit/1c7202ff835856562ee66b33be131eca769acf1d))
* update ip mismatch error message ([#1849](https://github.com/supabase/auth/issues/1849)) ([49fbbf0](https://github.com/supabase/auth/commit/49fbbf03917a1085c58e9a1ff76c247ae6bb9ca7))

## [2.164.0](https://github.com/supabase/auth/compare/v2.163.2...v2.164.0) (2024-11-13)


### Features

* return validation failed error if captcha request was not json ([#1815](https://github.com/supabase/auth/issues/1815)) ([26d2e36](https://github.com/supabase/auth/commit/26d2e36bba29eb8a6ddba556acfd0820f3bfde5d))


### Bug Fixes

* add error codes to refresh token flow ([#1824](https://github.com/supabase/auth/issues/1824)) ([4614dc5](https://github.com/supabase/auth/commit/4614dc54ab1dcb5390cfed05441e7888af017d92))
* add test coverage for rate limits with 0 permitted events ([#1834](https://github.com/supabase/auth/issues/1834)) ([7c3cf26](https://github.com/supabase/auth/commit/7c3cf26cfe2a3e4de579d10509945186ad719855))
* correct web authn aaguid column naming ([#1826](https://github.com/supabase/auth/issues/1826)) ([0a589d0](https://github.com/supabase/auth/commit/0a589d04e1cd9310cb260d329bc8beb050adf8da))
* default to files:read scope for Figma provider ([#1831](https://github.com/supabase/auth/issues/1831)) ([9ce2857](https://github.com/supabase/auth/commit/9ce28570bf3da9571198d44d693c7ad7038cde33))
* improve error messaging for http hooks ([#1821](https://github.com/supabase/auth/issues/1821)) ([fa020d0](https://github.com/supabase/auth/commit/fa020d0fc292d5c381c57ecac6666d9ff657e4c4))
* make drop_uniqueness_constraint_on_phone idempotent ([#1817](https://github.com/supabase/auth/issues/1817)) ([158e473](https://github.com/supabase/auth/commit/158e4732afa17620cdd89c85b7b57569feea5c21))
* possible panic if refresh token has a null session_id ([#1822](https://github.com/supabase/auth/issues/1822)) ([a7129df](https://github.com/supabase/auth/commit/a7129df4e1d91a042b56ff1f041b9c6598825475))
* rate limits of 0 take precedence over MAILER_AUTO_CONFIRM ([#1837](https://github.com/supabase/auth/issues/1837)) ([cb7894e](https://github.com/supabase/auth/commit/cb7894e1119d27d527dedcca22d8b3d433beddac))

## [2.163.2](https://github.com/supabase/auth/compare/v2.163.1...v2.163.2) (2024-10-22)


### Bug Fixes

* ignore rate limits for autoconfirm ([#1810](https://github.com/supabase/auth/issues/1810)) ([9ce2340](https://github.com/supabase/auth/commit/9ce23409f960a8efa55075931138624cb681eca5))

## [2.163.1](https://github.com/supabase/auth/compare/v2.163.0...v2.163.1) (2024-10-22)


### Bug Fixes

* external host validation ([#1808](https://github.com/supabase/auth/issues/1808)) ([4f6a461](https://github.com/supabase/auth/commit/4f6a4617074e61ba3b31836ccb112014904ce97c)), closes [#1228](https://github.com/supabase/auth/issues/1228)

## [2.163.0](https://github.com/supabase/auth/compare/v2.162.2...v2.163.0) (2024-10-15)


### Features

* add mail header support via `GOTRUE_SMTP_HEADERS` with `$messageType` ([#1804](https://github.com/supabase/auth/issues/1804)) ([99d6a13](https://github.com/supabase/auth/commit/99d6a134c44554a8ad06695e1dff54c942c8335d))
* add MFA for WebAuthn ([#1775](https://github.com/supabase/auth/issues/1775)) ([8cc2f0e](https://github.com/supabase/auth/commit/8cc2f0e14d06d0feb56b25a0278fda9e213b6b5a))
* configurable email and sms rate limiting ([#1800](https://github.com/supabase/auth/issues/1800)) ([5e94047](https://github.com/supabase/auth/commit/5e9404717e1c962ab729cde150ef5b40ea31a6e8))
* mailer logging ([#1805](https://github.com/supabase/auth/issues/1805)) ([9354b83](https://github.com/supabase/auth/commit/9354b83a48a3edcb49197c997a1e96efc80c5383))
* preserve rate limiters in memory across configuration reloads ([#1792](https://github.com/supabase/auth/issues/1792)) ([0a3968b](https://github.com/supabase/auth/commit/0a3968b02b9f044bfb7e5ebc71dca970d2bb7807))


### Bug Fixes

* add twilio verify support on mfa ([#1714](https://github.com/supabase/auth/issues/1714)) ([aeb5d8f](https://github.com/supabase/auth/commit/aeb5d8f8f18af60ce369cab5714979ac0c208308))
* email header setting no longer misleading ([#1802](https://github.com/supabase/auth/issues/1802)) ([3af03be](https://github.com/supabase/auth/commit/3af03be6b65c40f3f4f62ce9ab989a20d75ae53a))
* enforce authorized address checks on send email only ([#1806](https://github.com/supabase/auth/issues/1806)) ([c0c5b23](https://github.com/supabase/auth/commit/c0c5b23728c8fb633dae23aa4b29ed60e2691a2b))
* fix `getExcludedColumns` slice allocation ([#1788](https://github.com/supabase/auth/issues/1788)) ([7f006b6](https://github.com/supabase/auth/commit/7f006b63c8d7e28e55a6d471881e9c118df80585))
* Fix reqPath for bypass check for verify EP ([#1789](https://github.com/supabase/auth/issues/1789)) ([646dc66](https://github.com/supabase/auth/commit/646dc66ea8d59a7f78bf5a5e55d9b5065a718c23))
* inline mailme package for easy development ([#1803](https://github.com/supabase/auth/issues/1803)) ([fa6f729](https://github.com/supabase/auth/commit/fa6f729a027eff551db104550fa626088e00bc15))

## [2.162.2](https://github.com/supabase/auth/compare/v2.162.1...v2.162.2) (2024-10-05)


### Bug Fixes

* refactor mfa validation into functions ([#1780](https://github.com/supabase/auth/issues/1780)) ([410b8ac](https://github.com/supabase/auth/commit/410b8acdd659fc4c929fe57a9e9dba4c76da305d))
* upgrade ci Go version ([#1782](https://github.com/supabase/auth/issues/1782)) ([97a48f6](https://github.com/supabase/auth/commit/97a48f6daaa2edda5b568939cbb1007ccdf33cfc))
* validateEmail should normalise emails ([#1790](https://github.com/supabase/auth/issues/1790)) ([2e9b144](https://github.com/supabase/auth/commit/2e9b144a0cbf2d26d3c4c2eafbff1899a36aeb3b))

## [2.162.1](https://github.com/supabase/auth/compare/v2.162.0...v2.162.1) (2024-10-03)


### Bug Fixes

* bypass check for token & verify endpoints ([#1785](https://github.com/supabase/auth/issues/1785)) ([9ac2ea0](https://github.com/supabase/auth/commit/9ac2ea0180826cd2f65e679524aabfb10666e973))

## [2.162.0](https://github.com/supabase/auth/compare/v2.161.0...v2.162.0) (2024-09-27)


### Features

* add support for migration of firebase scrypt passwords ([#1768](https://github.com/supabase/auth/issues/1768)) ([ba00f75](https://github.com/supabase/auth/commit/ba00f75c28d6708ddf8ee151ce18f2d6193689ef))


### Bug Fixes

* apply authorized email restriction to non-admin routes ([#1778](https://github.com/supabase/auth/issues/1778)) ([1af203f](https://github.com/supabase/auth/commit/1af203f92372e6db12454a0d319aad8ce3d149e7))
* magiclink failing due to passwordStrength check ([#1769](https://github.com/supabase/auth/issues/1769)) ([7a5411f](https://github.com/supabase/auth/commit/7a5411f1d4247478f91027bc4969cbbe95b7774c))

## [2.161.0](https://github.com/supabase/auth/compare/v2.160.0...v2.161.0) (2024-09-24)


### Features

* add `x-sb-error-code` header, show error code in logs ([#1765](https://github.com/supabase/auth/issues/1765)) ([ed91c59](https://github.com/supabase/auth/commit/ed91c59aa332738bd0ac4b994aeec2cdf193a068))
* add webauthn configuration variables ([#1773](https://github.com/supabase/auth/issues/1773)) ([77d5897](https://github.com/supabase/auth/commit/77d58976ae624dbb7f8abee041dd4557aab81109))
* config reloading ([#1771](https://github.com/supabase/auth/issues/1771)) ([6ee0091](https://github.com/supabase/auth/commit/6ee009163bfe451e2a0b923705e073928a12c004))


### Bug Fixes

* add additional information around errors for missing content type header ([#1576](https://github.com/supabase/auth/issues/1576)) ([c2b2f96](https://github.com/supabase/auth/commit/c2b2f96f07c97c15597cd972b1cd672238d87cdc))
* add token to hook payload for non-secure email change ([#1763](https://github.com/supabase/auth/issues/1763)) ([7e472ad](https://github.com/supabase/auth/commit/7e472ad72042e86882dab3fddce9fafa66a8236c))
* update aal requirements to update user ([#1766](https://github.com/supabase/auth/issues/1766)) ([25d9874](https://github.com/supabase/auth/commit/25d98743f6cc2cca2b490a087f468c8556ec5e44))
* update mfa admin methods ([#1774](https://github.com/supabase/auth/issues/1774)) ([567ea7e](https://github.com/supabase/auth/commit/567ea7ebd18eacc5e6daea8adc72e59e94459991))
* user sanitization should clean up email change info too ([#1759](https://github.com/supabase/auth/issues/1759)) ([9d419b4](https://github.com/supabase/auth/commit/9d419b400f0637b10e5c235b8fd5bac0d69352bd))

## [2.160.0](https://github.com/supabase/auth/compare/v2.159.2...v2.160.0) (2024-09-02)


### Features

* add authorized email address support ([#1757](https://github.com/supabase/auth/issues/1757)) ([f3a28d1](https://github.com/supabase/auth/commit/f3a28d182d193cf528cc72a985dfeaf7ecb67056))
* add option to disable magic links ([#1756](https://github.com/supabase/auth/issues/1756)) ([2ad0737](https://github.com/supabase/auth/commit/2ad07373aa9239eba94abdabbb01c9abfa8c48de))
* add support for saml encrypted assertions ([#1752](https://github.com/supabase/auth/issues/1752)) ([c5480ef](https://github.com/supabase/auth/commit/c5480ef83248ec2e7e3d3d87f92f43f17161ed25))


### Bug Fixes

* apply shared limiters before email / sms is sent ([#1748](https://github.com/supabase/auth/issues/1748)) ([bf276ab](https://github.com/supabase/auth/commit/bf276ab49753642793471815727559172fea4efc))
* simplify WaitForCleanup ([#1747](https://github.com/supabase/auth/issues/1747)) ([0084625](https://github.com/supabase/auth/commit/0084625ad0790dd7c14b412d932425f4b84bb4c8))

## [2.159.2](https://github.com/supabase/auth/compare/v2.159.1...v2.159.2) (2024-08-28)


### Bug Fixes

* allow anonymous user to update password ([#1739](https://github.com/supabase/auth/issues/1739)) ([2d51956](https://github.com/supabase/auth/commit/2d519569d7b8540886d0a64bf3e561ef5f91eb63))
* hide hook name ([#1743](https://github.com/supabase/auth/issues/1743)) ([7e38f4c](https://github.com/supabase/auth/commit/7e38f4cf37768fe2adf92bbd0723d1d521b3d74c))
* remove server side cookie token methods ([#1742](https://github.com/supabase/auth/issues/1742)) ([c6efec4](https://github.com/supabase/auth/commit/c6efec4cbc950e01e1fd06d45ed821bd27c2ad08))

## [2.159.1](https://github.com/supabase/auth/compare/v2.159.0...v2.159.1) (2024-08-23)


### Bug Fixes

* return oauth identity when user is created ([#1736](https://github.com/supabase/auth/issues/1736)) ([60cfb60](https://github.com/supabase/auth/commit/60cfb6063afa574dfe4993df6b0e087d4df71309))

## [2.159.0](https://github.com/supabase/auth/compare/v2.158.1...v2.159.0) (2024-08-21)


### Features

* Vercel marketplace OIDC ([#1731](https://github.com/supabase/auth/issues/1731)) ([a9ff361](https://github.com/supabase/auth/commit/a9ff3612196af4a228b53a8bfb9c11785bcfba8d))


### Bug Fixes

* add error codes to password login flow ([#1721](https://github.com/supabase/auth/issues/1721)) ([4351226](https://github.com/supabase/auth/commit/435122627a0784f1c5cb76d7e08caa1f6259423b))
* change phone constraint to per user ([#1713](https://github.com/supabase/auth/issues/1713)) ([b9bc769](https://github.com/supabase/auth/commit/b9bc769b93b6e700925fcbc1ebf8bf9678034205))
* custom SMS does not work with Twilio Verify ([#1733](https://github.com/supabase/auth/issues/1733)) ([dc2391d](https://github.com/supabase/auth/commit/dc2391d15f2c0725710aa388cd32a18797e6769c))
* ignore errors if transaction has closed already ([#1726](https://github.com/supabase/auth/issues/1726)) ([53c11d1](https://github.com/supabase/auth/commit/53c11d173a79ae5c004871b1b5840c6f9425a080))
* redirect invalid state errors to site url ([#1722](https://github.com/supabase/auth/issues/1722)) ([b2b1123](https://github.com/supabase/auth/commit/b2b11239dc9f9bd3c85d76f6c23ee94beb3330bb))
* remove TOTP field for phone enroll response ([#1717](https://github.com/supabase/auth/issues/1717)) ([4b04327](https://github.com/supabase/auth/commit/4b043275dd2d94600a8138d4ebf4638754ed926b))
* use signing jwk to sign oauth state ([#1728](https://github.com/supabase/auth/issues/1728)) ([66fd0c8](https://github.com/supabase/auth/commit/66fd0c8434388bbff1e1bf02f40517aca0e9d339))

## [2.158.1](https://github.com/supabase/auth/compare/v2.158.0...v2.158.1) (2024-08-05)


### Bug Fixes

* add last_challenged_at field to mfa factors ([#1705](https://github.com/supabase/auth/issues/1705)) ([29cbeb7](https://github.com/supabase/auth/commit/29cbeb799ff35ce528bfbd01b7103a24903d8061))
* allow enabling sms hook without setting up sms provider ([#1704](https://github.com/supabase/auth/issues/1704)) ([575e88a](https://github.com/supabase/auth/commit/575e88ac345adaeb76ab6aae077307fdab9cda3c))
* drop the MFA_ENABLED config ([#1701](https://github.com/supabase/auth/issues/1701)) ([078c3a8](https://github.com/supabase/auth/commit/078c3a8adcd51e57b68ab1b582549f5813cccd14))
* enforce uniqueness on verified phone numbers ([#1693](https://github.com/supabase/auth/issues/1693)) ([70446cc](https://github.com/supabase/auth/commit/70446cc11d70b0493d742fe03f272330bb5b633e))
* expose `X-Supabase-Api-Version` header in CORS ([#1612](https://github.com/supabase/auth/issues/1612)) ([6ccd814](https://github.com/supabase/auth/commit/6ccd814309dca70a9e3585543887194b05d725d3))
* include factor_id in query ([#1702](https://github.com/supabase/auth/issues/1702)) ([ac14e82](https://github.com/supabase/auth/commit/ac14e82b33545466184da99e99b9d3fe5f3876d9))
* move is owned by check to load factor ([#1703](https://github.com/supabase/auth/issues/1703)) ([701a779](https://github.com/supabase/auth/commit/701a779cf092e777dd4ad4954dc650164b09ab32))
* refactor TOTP MFA into separate methods ([#1698](https://github.com/supabase/auth/issues/1698)) ([250d92f](https://github.com/supabase/auth/commit/250d92f9a18d38089d1bf262ef9088022a446965))
* remove check for content-length ([#1700](https://github.com/supabase/auth/issues/1700)) ([81b332d](https://github.com/supabase/auth/commit/81b332d2f48622008469d2c5a9b130465a65f2a3))
* remove FindFactorsByUser ([#1707](https://github.com/supabase/auth/issues/1707)) ([af8e2dd](https://github.com/supabase/auth/commit/af8e2dda15a1234a05e7d2d34d316eaa029e0912))
* update openapi spec for MFA (Phone)  ([#1689](https://github.com/supabase/auth/issues/1689)) ([a3da4b8](https://github.com/supabase/auth/commit/a3da4b89820c37f03ea128889616aca598d99f68))

## [2.158.0](https://github.com/supabase/auth/compare/v2.157.0...v2.158.0) (2024-07-31)


### Features

* add hook log entry with `run_hook` action ([#1684](https://github.com/supabase/auth/issues/1684)) ([46491b8](https://github.com/supabase/auth/commit/46491b867a4f5896494417391392a373a453fa5f))
* MFA (Phone) ([#1668](https://github.com/supabase/auth/issues/1668)) ([ae091aa](https://github.com/supabase/auth/commit/ae091aa942bdc5bc97481037508ec3bb4079d859))


### Bug Fixes

* maintain backward compatibility for asymmetric JWTs ([#1690](https://github.com/supabase/auth/issues/1690)) ([0ad1402](https://github.com/supabase/auth/commit/0ad1402444348e47e1e42be186b3f052d31be824))
* MFA NewFactor to default to creating unverfied factors ([#1692](https://github.com/supabase/auth/issues/1692)) ([3d448fa](https://github.com/supabase/auth/commit/3d448fa73cb77eb8511dbc47bfafecce4a4a2150))
* minor spelling errors ([#1688](https://github.com/supabase/auth/issues/1688)) ([6aca52b](https://github.com/supabase/auth/commit/6aca52b56f8a6254de7709c767b9a5649f1da248)), closes [#1682](https://github.com/supabase/auth/issues/1682)
* treat `GOTRUE_MFA_ENABLED` as meaning TOTP enabled on enroll and verify ([#1694](https://github.com/supabase/auth/issues/1694)) ([8015251](https://github.com/supabase/auth/commit/8015251400bd52cbdad3ea28afb83b1cdfe816dd))
* update mfa phone migration to be idempotent ([#1687](https://github.com/supabase/auth/issues/1687)) ([fdff1e7](https://github.com/supabase/auth/commit/fdff1e703bccf93217636266f1862bd0a9205edb))

## [2.157.0](https://github.com/supabase/auth/compare/v2.156.0...v2.157.0) (2024-07-26)


### Features

* add asymmetric jwt support ([#1674](https://github.com/supabase/auth/issues/1674)) ([c7a2be3](https://github.com/supabase/auth/commit/c7a2be347b301b666e99adc3d3fed78c5e287c82))

## [2.156.0](https://github.com/supabase/auth/compare/v2.155.6...v2.156.0) (2024-07-25)


### Features

* add is_anonymous claim to Auth hook jsonschema ([#1667](https://github.com/supabase/auth/issues/1667)) ([f9df65c](https://github.com/supabase/auth/commit/f9df65c91e226084abfa2e868ab6bab892d16d2f))


### Bug Fixes

* restrict autoconfirm email change to anonymous users ([#1679](https://github.com/supabase/auth/issues/1679)) ([b57e223](https://github.com/supabase/auth/commit/b57e2230102280ed873acf70be1aeb5a2f6f7a4f))

## [2.155.6](https://github.com/supabase/auth/compare/v2.155.5...v2.155.6) (2024-07-22)


### Bug Fixes

* use deep equal ([#1672](https://github.com/supabase/auth/issues/1672)) ([8efd57d](https://github.com/supabase/auth/commit/8efd57dab40346762a04bac61b314ce05d6fa69c))

## [2.155.5](https://github.com/supabase/auth/compare/v2.155.4...v2.155.5) (2024-07-19)


### Bug Fixes

* check password max length in checkPasswordStrength ([#1659](https://github.com/supabase/auth/issues/1659)) ([1858c93](https://github.com/supabase/auth/commit/1858c93bba6f5bc41e4c65489f12c1a0786a1f2b))
* don't update attribute mapping if nil ([#1665](https://github.com/supabase/auth/issues/1665)) ([7e67f3e](https://github.com/supabase/auth/commit/7e67f3edbf81766df297a66f52a8e472583438c6))
* refactor mfa models and add observability to loadFactor ([#1669](https://github.com/supabase/auth/issues/1669)) ([822fb93](https://github.com/supabase/auth/commit/822fb93faab325ba3d4bb628dff43381d68d0b5d))

## [2.155.4](https://github.com/supabase/auth/compare/v2.155.3...v2.155.4) (2024-07-17)


### Bug Fixes

* treat empty string as nil in `encrypted_password` ([#1663](https://github.com/supabase/auth/issues/1663)) ([f99286e](https://github.com/supabase/auth/commit/f99286eaed505daf3db6f381265ef6024e7e36d2))

## [2.155.3](https://github.com/supabase/auth/compare/v2.155.2...v2.155.3) (2024-07-12)


### Bug Fixes

* serialize jwt as string ([#1657](https://github.com/supabase/auth/issues/1657)) ([98d8324](https://github.com/supabase/auth/commit/98d83245e40d606438eb0afdbf474276179fd91d))

## [2.155.2](https://github.com/supabase/auth/compare/v2.155.1...v2.155.2) (2024-07-12)


### Bug Fixes

* improve session error logging ([#1655](https://github.com/supabase/auth/issues/1655)) ([5a6793e](https://github.com/supabase/auth/commit/5a6793ee8fce7a089750fe10b3b63bb0a19d6d21))
* omit empty string from name & use case-insensitive equality for comparing SAML attributes ([#1654](https://github.com/supabase/auth/issues/1654)) ([bf5381a](https://github.com/supabase/auth/commit/bf5381a6b1c686955dc4e39fe5fb806ffd309563))
* set rate limit log level to warn ([#1652](https://github.com/supabase/auth/issues/1652)) ([10ca9c8](https://github.com/supabase/auth/commit/10ca9c806e4b67a371897f1b3f93c515764c4240))

## [2.155.1](https://github.com/supabase/auth/compare/v2.155.0...v2.155.1) (2024-07-04)


### Bug Fixes

* apply mailer autoconfirm config to update user email ([#1646](https://github.com/supabase/auth/issues/1646)) ([a518505](https://github.com/supabase/auth/commit/a5185058e72509b0781e0eb59910ecdbb8676fee))
* check for empty aud string ([#1649](https://github.com/supabase/auth/issues/1649)) ([42c1d45](https://github.com/supabase/auth/commit/42c1d4526b98203664d4a22c23014ecd0b4951f9))
* return proper error if sms rate limit is exceeded ([#1647](https://github.com/supabase/auth/issues/1647)) ([3c8d765](https://github.com/supabase/auth/commit/3c8d7656431ac4b2e80726b7c37adb8f0c778495))

## [2.155.0](https://github.com/supabase/auth/compare/v2.154.2...v2.155.0) (2024-07-03)


### Features

* add `password_hash` and `id` fields to admin create user ([#1641](https://github.com/supabase/auth/issues/1641)) ([20d59f1](https://github.com/supabase/auth/commit/20d59f10b601577683d05bcd7d2128ff4bc462a0))


### Bug Fixes

* improve mfa verify logs ([#1635](https://github.com/supabase/auth/issues/1635)) ([d8b47f9](https://github.com/supabase/auth/commit/d8b47f9d3f0dc8f97ad1de49e45f452ebc726481))
* invited users should have a temporary password generated ([#1644](https://github.com/supabase/auth/issues/1644)) ([3f70d9d](https://github.com/supabase/auth/commit/3f70d9d8974d0e9c437c51e1312ad17ce9056ec9))
* upgrade golang-jwt to v5 ([#1639](https://github.com/supabase/auth/issues/1639)) ([2cb97f0](https://github.com/supabase/auth/commit/2cb97f080fa4695766985cc4792d09476534be68))
* use pointer for `user.EncryptedPassword` ([#1637](https://github.com/supabase/auth/issues/1637)) ([bbecbd6](https://github.com/supabase/auth/commit/bbecbd61a46b0c528b1191f48d51f166c06f4b16))

## [2.154.2](https://github.com/supabase/auth/compare/v2.154.1...v2.154.2) (2024-06-24)


### Bug Fixes

* publish to ghcr.io/supabase/auth ([#1626](https://github.com/supabase/auth/issues/1626)) ([930aa3e](https://github.com/supabase/auth/commit/930aa3edb633823d4510c2aff675672df06f1211)), closes [#1625](https://github.com/supabase/auth/issues/1625)
* revert define search path in auth functions ([#1634](https://github.com/supabase/auth/issues/1634)) ([155e87e](https://github.com/supabase/auth/commit/155e87ef8129366d665968f64d1fc66676d07e16))
* update MaxFrequency error message to reflect number of seconds ([#1540](https://github.com/supabase/auth/issues/1540)) ([e81c25d](https://github.com/supabase/auth/commit/e81c25d19551fdebfc5197d96bc220ddb0f8227b))

## [2.154.1](https://github.com/supabase/auth/compare/v2.154.0...v2.154.1) (2024-06-17)


### Bug Fixes

* add ip based limiter ([#1622](https://github.com/supabase/auth/issues/1622)) ([06464c0](https://github.com/supabase/auth/commit/06464c013571253d1f18f7ae5e840826c4bd84a7))
* admin user update should update is_anonymous field ([#1623](https://github.com/supabase/auth/issues/1623)) ([f5c6fcd](https://github.com/supabase/auth/commit/f5c6fcd9c3fee0f793f96880a8caebc5b5cb0916))

## [2.154.0](https://github.com/supabase/auth/compare/v2.153.0...v2.154.0) (2024-06-12)


### Features

* add max length check for email ([#1508](https://github.com/supabase/auth/issues/1508)) ([f9c13c0](https://github.com/supabase/auth/commit/f9c13c0ad5c556bede49d3e0f6e5f58ca26161c3))
* add support for Slack OAuth V2 ([#1591](https://github.com/supabase/auth/issues/1591)) ([bb99251](https://github.com/supabase/auth/commit/bb992519cdf7578dc02cd7de55e2e6aa09b4c0f3))
* encrypt sensitive columns ([#1593](https://github.com/supabase/auth/issues/1593)) ([e4a4758](https://github.com/supabase/auth/commit/e4a475820b2dc1f985bd37df15a8ab9e781626f5))
* upgrade otel to v1.26 ([#1585](https://github.com/supabase/auth/issues/1585)) ([cdd13ad](https://github.com/supabase/auth/commit/cdd13adec02eb0c9401bc55a2915c1005d50dea1))
* use largest avatar from spotify instead ([#1210](https://github.com/supabase/auth/issues/1210)) ([4f9994b](https://github.com/supabase/auth/commit/4f9994bf792c3887f2f45910b11a9c19ee3a896b)), closes [#1209](https://github.com/supabase/auth/issues/1209)


### Bug Fixes

* define search path in auth functions ([#1616](https://github.com/supabase/auth/issues/1616)) ([357bda2](https://github.com/supabase/auth/commit/357bda23cb2abd12748df80a9d27288aa548534d))
* enable rls & update grants for auth tables ([#1617](https://github.com/supabase/auth/issues/1617)) ([28967aa](https://github.com/supabase/auth/commit/28967aa4b5db2363cc581c9da0d64e974eb7b64c))

## [2.153.0](https://github.com/supabase/auth/compare/v2.152.0...v2.153.0) (2024-06-04)


### Features

* add SAML specific external URL config ([#1599](https://github.com/supabase/auth/issues/1599)) ([b352719](https://github.com/supabase/auth/commit/b3527190560381fafe9ba2fae4adc3b73703024a))
* add support for verifying argon2i and argon2id passwords ([#1597](https://github.com/supabase/auth/issues/1597)) ([55409f7](https://github.com/supabase/auth/commit/55409f797bea55068a3fafdddd6cfdb78feba1b4))
* make the email client explicity set the format to be HTML ([#1149](https://github.com/supabase/auth/issues/1149)) ([53e223a](https://github.com/supabase/auth/commit/53e223abdf29f4abcad13f99baf00daedcb00c3f))


### Bug Fixes

* call write header in write if not written ([#1598](https://github.com/supabase/auth/issues/1598)) ([0ef7eb3](https://github.com/supabase/auth/commit/0ef7eb30619d4c365e06a94a79b9cb0333d792da))
* deadlock issue with timeout middleware write ([#1595](https://github.com/supabase/auth/issues/1595)) ([6c9fbd4](https://github.com/supabase/auth/commit/6c9fbd4bd5623c729906fca7857ab508166a3056))
* improve token OIDC logging ([#1606](https://github.com/supabase/auth/issues/1606)) ([5262683](https://github.com/supabase/auth/commit/526268311844467664e89c8329e5aaee817dbbaf))
* update contributing to use v1.22 ([#1609](https://github.com/supabase/auth/issues/1609)) ([5894d9e](https://github.com/supabase/auth/commit/5894d9e41e7681512a9904ad47082a705e948c98))

## [2.152.0](https://github.com/supabase/auth/compare/v2.151.0...v2.152.0) (2024-05-22)


### Features

* new timeout writer implementation ([#1584](https://github.com/supabase/auth/issues/1584)) ([72614a1](https://github.com/supabase/auth/commit/72614a1fce27888f294772b512f8e31c55a36d87))
* remove legacy lookup in users for one_time_tokens (phase II) ([#1569](https://github.com/supabase/auth/issues/1569)) ([39ca026](https://github.com/supabase/auth/commit/39ca026035f6c61d206d31772c661b326c2a424c))
* update chi version ([#1581](https://github.com/supabase/auth/issues/1581)) ([c64ae3d](https://github.com/supabase/auth/commit/c64ae3dd775e8fb3022239252c31b4ee73893237))
* update openapi spec with identity and is_anonymous fields ([#1573](https://github.com/supabase/auth/issues/1573)) ([86a79df](https://github.com/supabase/auth/commit/86a79df9ecfcf09fda0b8e07afbc41154fbb7d9d))


### Bug Fixes

* improve logging structure ([#1583](https://github.com/supabase/auth/issues/1583)) ([c22fc15](https://github.com/supabase/auth/commit/c22fc15d2a8383e95a2364f383dfa7dce5f5df88))
* sms verify should update is_anonymous field ([#1580](https://github.com/supabase/auth/issues/1580)) ([e5f98cb](https://github.com/supabase/auth/commit/e5f98cb9e24ecebb0b7dc88c495fd456cc73fcba))
* use api_external_url domain as localname ([#1575](https://github.com/supabase/auth/issues/1575)) ([ed2b490](https://github.com/supabase/auth/commit/ed2b4907244281e4c54aaef74b1f4c8a8e3d97c9))

## [2.151.0](https://github.com/supabase/auth/compare/v2.150.1...v2.151.0) (2024-05-06)


### Features

* refactor one-time tokens for performance ([#1558](https://github.com/supabase/auth/issues/1558)) ([d1cf8d9](https://github.com/supabase/auth/commit/d1cf8d9096e9183d7772b73031de8ecbd66e912b))


### Bug Fixes

* do call send sms hook when SMS autoconfirm is enabled ([#1562](https://github.com/supabase/auth/issues/1562)) ([bfe4d98](https://github.com/supabase/auth/commit/bfe4d988f3768b0407526bcc7979fb21d8cbebb3))
* format test otps ([#1567](https://github.com/supabase/auth/issues/1567)) ([434a59a](https://github.com/supabase/auth/commit/434a59ae387c35fd6629ec7c674d439537e344e5))
* log final writer error instead of handling ([#1564](https://github.com/supabase/auth/issues/1564)) ([170bd66](https://github.com/supabase/auth/commit/170bd6615405afc852c7107f7358dfc837bad737))

## [2.150.1](https://github.com/supabase/auth/compare/v2.150.0...v2.150.1) (2024-04-28)


### Bug Fixes

* add db conn max idle time setting ([#1555](https://github.com/supabase/auth/issues/1555)) ([2caa7b4](https://github.com/supabase/auth/commit/2caa7b4d75d2ff54af20f3e7a30a8eeec8cbcda9))

## [2.150.0](https://github.com/supabase/auth/compare/v2.149.0...v2.150.0) (2024-04-25)


### Features

* add support for Azure CIAM login ([#1541](https://github.com/supabase/auth/issues/1541)) ([1cb4f96](https://github.com/supabase/auth/commit/1cb4f96bdc7ef3ef995781b4cf3c4364663a2bf3))
* add timeout middleware ([#1529](https://github.com/supabase/auth/issues/1529)) ([f96ff31](https://github.com/supabase/auth/commit/f96ff31040b28e3a7373b4fd41b7334eda1b413e))
* allow for postgres and http functions on each extensibility point ([#1528](https://github.com/supabase/auth/issues/1528)) ([348a1da](https://github.com/supabase/auth/commit/348a1daee24f6e44b14c018830b748e46d34b4c2))
* merge provider metadata on link account ([#1552](https://github.com/supabase/auth/issues/1552)) ([bd8b5c4](https://github.com/supabase/auth/commit/bd8b5c41dd544575e1a52ccf1ef3f0fdee67458c))
* send over user in SendSMS Hook instead of UserID ([#1551](https://github.com/supabase/auth/issues/1551)) ([d4d743c](https://github.com/supabase/auth/commit/d4d743c2ae9490e1b3249387e3b0d60df6913c68))


### Bug Fixes

* return error if session id does not exist ([#1538](https://github.com/supabase/auth/issues/1538)) ([91e9eca](https://github.com/supabase/auth/commit/91e9ecabe33a1c022f8e82a6050c22a7ca42de48))

## [2.149.0](https://github.com/supabase/auth/compare/v2.148.0...v2.149.0) (2024-04-15)


### Features

* refactor generate accesss token to take in request ([#1531](https://github.com/supabase/auth/issues/1531)) ([e4f2b59](https://github.com/supabase/auth/commit/e4f2b59e8e1f8158b6461a384349f1a32cc1bf9a))


### Bug Fixes

* linkedin_oidc provider error ([#1534](https://github.com/supabase/auth/issues/1534)) ([4f5e8e5](https://github.com/supabase/auth/commit/4f5e8e5120531e5a103fbdda91b51cabcb4e1a8c))
* revert patch for linkedin_oidc provider error ([#1535](https://github.com/supabase/auth/issues/1535)) ([58ef4af](https://github.com/supabase/auth/commit/58ef4af0b4224b78cd9e59428788d16a8d31e562))
* update linkedin issuer url ([#1536](https://github.com/supabase/auth/issues/1536)) ([10d6d8b](https://github.com/supabase/auth/commit/10d6d8b1eafa504da2b2a351d1f64a3a832ab1b9))

## [2.148.0](https://github.com/supabase/auth/compare/v2.147.1...v2.148.0) (2024-04-10)


### Features

* add array attribute mapping for SAML ([#1526](https://github.com/supabase/auth/issues/1526)) ([7326285](https://github.com/supabase/auth/commit/7326285c8af5c42e5c0c2d729ab224cf33ac3a1f))

## [2.147.1](https://github.com/supabase/auth/compare/v2.147.0...v2.147.1) (2024-04-09)


### Bug Fixes

* add validation and proper decoding on send email hook ([#1520](https://github.com/supabase/auth/issues/1520)) ([e19e762](https://github.com/supabase/auth/commit/e19e762e3e29729a1d1164c65461427822cc87f1))
* remove deprecated LogoutAllRefreshTokens ([#1519](https://github.com/supabase/auth/issues/1519)) ([35533ea](https://github.com/supabase/auth/commit/35533ea100669559e1209ecc7b091db3657234d9))

## [2.147.0](https://github.com/supabase/auth/compare/v2.146.0...v2.147.0) (2024-04-05)


### Features

* add send email Hook ([#1512](https://github.com/supabase/auth/issues/1512)) ([cf42e02](https://github.com/supabase/auth/commit/cf42e02ec63779f52b1652a7413f64994964c82d))

## [2.146.0](https://github.com/supabase/auth/compare/v2.145.0...v2.146.0) (2024-04-03)


### Features

* add custom sms hook ([#1474](https://github.com/supabase/auth/issues/1474)) ([0f6b29a](https://github.com/supabase/auth/commit/0f6b29a46f1dcbf92aa1f7cb702f42e7640f5f93))
* forbid generating an access token without a session ([#1504](https://github.com/supabase/auth/issues/1504)) ([795e93d](https://github.com/supabase/auth/commit/795e93d0afbe94bcd78489a3319a970b7bf8e8bc))


### Bug Fixes

* add cleanup statement for anonymous users ([#1497](https://github.com/supabase/auth/issues/1497)) ([cf2372a](https://github.com/supabase/auth/commit/cf2372a177796b829b72454e7491ce768bf5a42f))
* generate signup link should not error ([#1514](https://github.com/supabase/auth/issues/1514)) ([4fc3881](https://github.com/supabase/auth/commit/4fc388186ac7e7a9a32ca9b963a83d6ac2eb7603))
* move all EmailActionTypes to mailer package ([#1510](https://github.com/supabase/auth/issues/1510)) ([765db08](https://github.com/supabase/auth/commit/765db08582669a1b7f054217fa8f0ed45804c0b5))
* refactor mfa and aal update methods ([#1503](https://github.com/supabase/auth/issues/1503)) ([31a5854](https://github.com/supabase/auth/commit/31a585429bf248aa919d94c82c7c9e0c1c695461))
* rename from CustomSMSProvider to SendSMS ([#1513](https://github.com/supabase/auth/issues/1513)) ([c0bc37b](https://github.com/supabase/auth/commit/c0bc37b44effaebb62ba85102f072db07fe57e48))

## [2.145.0](https://github.com/supabase/gotrue/compare/v2.144.0...v2.145.0) (2024-03-26)


### Features

* add error codes ([#1377](https://github.com/supabase/gotrue/issues/1377)) ([e4beea1](https://github.com/supabase/gotrue/commit/e4beea1cdb80544b0581f1882696a698fdf64938))
* add kakao OIDC ([#1381](https://github.com/supabase/gotrue/issues/1381)) ([b5566e7](https://github.com/supabase/gotrue/commit/b5566e7ac001cc9f2bac128de0fcb908caf3a5ed))
* clean up expired factors ([#1371](https://github.com/supabase/gotrue/issues/1371)) ([5c94207](https://github.com/supabase/gotrue/commit/5c9420743a9aef0675f823c30aa4525b4933836e))
* configurable NameID format for SAML provider ([#1481](https://github.com/supabase/gotrue/issues/1481)) ([ef405d8](https://github.com/supabase/gotrue/commit/ef405d89e69e008640f275bc37f8ec02ad32da40))
* HTTP Hook - Add custom envconfig decoding for HTTP Hook Secrets ([#1467](https://github.com/supabase/gotrue/issues/1467)) ([5b24c4e](https://github.com/supabase/gotrue/commit/5b24c4eb05b2b52c4177d5f41cba30cb68495c8c))
* refactor PKCE FlowState to reduce duplicate code ([#1446](https://github.com/supabase/gotrue/issues/1446)) ([b8d0337](https://github.com/supabase/gotrue/commit/b8d0337922c6712380f6dc74f7eac9fb71b1ae48))


### Bug Fixes

* add http support for https hooks on localhost ([#1484](https://github.com/supabase/gotrue/issues/1484)) ([5c04104](https://github.com/supabase/gotrue/commit/5c04104bf77a9c2db46d009764ec3ec3e484fc09))
* cleanup panics due to bad inactivity timeout code ([#1471](https://github.com/supabase/gotrue/issues/1471)) ([548edf8](https://github.com/supabase/gotrue/commit/548edf898161c9ba9a136fc99ec2d52a8ba1f856))
* **docs:** remove bracket on file name for broken link ([#1493](https://github.com/supabase/gotrue/issues/1493)) ([96f7a68](https://github.com/supabase/gotrue/commit/96f7a68a5479825e31106c2f55f82d5b2c007c0f))
* impose expiry on auth code instead of magic link ([#1440](https://github.com/supabase/gotrue/issues/1440)) ([35aeaf1](https://github.com/supabase/gotrue/commit/35aeaf1b60dd27a22662a6d1955d60cc907b55dd))
* invalidate email, phone OTPs on password change ([#1489](https://github.com/supabase/gotrue/issues/1489)) ([960a4f9](https://github.com/supabase/gotrue/commit/960a4f94f5500e33a0ec2f6afe0380bbc9562500))
* move creation of flow state into function ([#1470](https://github.com/supabase/gotrue/issues/1470)) ([4392a08](https://github.com/supabase/gotrue/commit/4392a08d68d18828005d11382730117a7b143635))
* prevent user email side-channel leak on verify ([#1472](https://github.com/supabase/gotrue/issues/1472)) ([311cde8](https://github.com/supabase/gotrue/commit/311cde8d1e82f823ae26a341e068034d60273864))
* refactor email sending functions ([#1495](https://github.com/supabase/gotrue/issues/1495)) ([285c290](https://github.com/supabase/gotrue/commit/285c290adf231fea7ca1dff954491dc427cf18e2))
* refactor factor_test to centralize setup ([#1473](https://github.com/supabase/gotrue/issues/1473)) ([c86007e](https://github.com/supabase/gotrue/commit/c86007e59684334b5e8c2285c36094b6eec89442))
* refactor mfa challenge and tests ([#1469](https://github.com/supabase/gotrue/issues/1469)) ([6c76f21](https://github.com/supabase/gotrue/commit/6c76f21cee5dbef0562c37df6a546939affb2f8d))
* Resend SMS when duplicate SMS sign ups are made ([#1490](https://github.com/supabase/gotrue/issues/1490)) ([73240a0](https://github.com/supabase/gotrue/commit/73240a0b096977703e3c7d24a224b5641ce47c81))
* unlink identity bugs ([#1475](https://github.com/supabase/gotrue/issues/1475)) ([73e8d87](https://github.com/supabase/gotrue/commit/73e8d8742de3575b3165a707b5d2f486b2598d9d))

## [2.144.0](https://github.com/supabase/gotrue/compare/v2.143.0...v2.144.0) (2024-03-04)


### Features

* add configuration for custom sms sender hook ([#1428](https://github.com/supabase/gotrue/issues/1428)) ([1ea56b6](https://github.com/supabase/gotrue/commit/1ea56b62d47edb0766d9e445406ecb43d387d920))
* anonymous sign-ins  ([#1460](https://github.com/supabase/gotrue/issues/1460)) ([130df16](https://github.com/supabase/gotrue/commit/130df165270c69c8e28aaa1b9421342f997c1ff3))
* clean up test setup in MFA tests ([#1452](https://github.com/supabase/gotrue/issues/1452)) ([7185af8](https://github.com/supabase/gotrue/commit/7185af8de4a269cdde2629054d222333d3522ebe))
* pass transaction to `invokeHook`, fixing pool exhaustion ([#1465](https://github.com/supabase/gotrue/issues/1465)) ([b536d36](https://github.com/supabase/gotrue/commit/b536d368f35adb31f937169e3f093d28352fa7be))
* refactor resource owner password grant ([#1443](https://github.com/supabase/gotrue/issues/1443)) ([e63ad6f](https://github.com/supabase/gotrue/commit/e63ad6ff0f67d9a83456918a972ecb5109125628))
* use dummy instance id to improve performance on refresh token queries ([#1454](https://github.com/supabase/gotrue/issues/1454)) ([656474e](https://github.com/supabase/gotrue/commit/656474e1b9ff3d5129190943e8c48e456625afe5))


### Bug Fixes

* expose `provider` under `amr` in access token ([#1456](https://github.com/supabase/gotrue/issues/1456)) ([e9f38e7](https://github.com/supabase/gotrue/commit/e9f38e76d8a7b93c5c2bb0de918a9b156155f018))
* improve MFA QR Code resilience so as to support providers like 1Password ([#1455](https://github.com/supabase/gotrue/issues/1455)) ([6522780](https://github.com/supabase/gotrue/commit/652278046c9dd92f5cecd778735b058ef3fb41c7))
* refactor request params to use generics ([#1464](https://github.com/supabase/gotrue/issues/1464)) ([e1cdf5c](https://github.com/supabase/gotrue/commit/e1cdf5c4b5c1bf467094f4bdcaa2e42a5cc51c20))
* revert refactor resource owner password grant ([#1466](https://github.com/supabase/gotrue/issues/1466)) ([fa21244](https://github.com/supabase/gotrue/commit/fa21244fa929709470c2e1fc4092a9ce947399e7))
* update file name so migration to Drop IP Address is applied ([#1447](https://github.com/supabase/gotrue/issues/1447)) ([f29e89d](https://github.com/supabase/gotrue/commit/f29e89d7d2c48ee8fd5bf8279a7fa3db0ad4d842))

## [2.143.0](https://github.com/supabase/gotrue/compare/v2.142.0...v2.143.0) (2024-02-19)


### Features

* calculate aal without transaction ([#1437](https://github.com/supabase/gotrue/issues/1437)) ([8dae661](https://github.com/supabase/gotrue/commit/8dae6614f1a2b58819f94894cef01e9f99117769))


### Bug Fixes

* deprecate hooks  ([#1421](https://github.com/supabase/gotrue/issues/1421)) ([effef1b](https://github.com/supabase/gotrue/commit/effef1b6ecc448b7927eff23df8d5b509cf16b5c))
* error should be an IsNotFoundError ([#1432](https://github.com/supabase/gotrue/issues/1432)) ([7f40047](https://github.com/supabase/gotrue/commit/7f40047aec3577d876602444b1d88078b2237d66))
* populate password verification attempt hook ([#1436](https://github.com/supabase/gotrue/issues/1436)) ([f974bdb](https://github.com/supabase/gotrue/commit/f974bdb58340395955ca27bdd26d57062433ece9))
* restrict mfa enrollment to aal2 if verified factors are present ([#1439](https://github.com/supabase/gotrue/issues/1439)) ([7e10d45](https://github.com/supabase/gotrue/commit/7e10d45e54010d38677f4c3f2f224127688eb9a2))
* update phone if autoconfirm is enabled ([#1431](https://github.com/supabase/gotrue/issues/1431)) ([95db770](https://github.com/supabase/gotrue/commit/95db770c5d2ecca4a1e960a8cb28ded37cccc100))
* use email change email in identity ([#1429](https://github.com/supabase/gotrue/issues/1429)) ([4d3b9b8](https://github.com/supabase/gotrue/commit/4d3b9b8841b1a5fa8f3244825153cc81a73ba300))

## [2.142.0](https://github.com/supabase/gotrue/compare/v2.141.0...v2.142.0) (2024-02-14)


### Features

* alter tag to use raw ([#1427](https://github.com/supabase/gotrue/issues/1427)) ([53cfe5d](https://github.com/supabase/gotrue/commit/53cfe5de57d4b5ab6e8e2915493856ecd96f4ede))
* update README.md to trigger release ([#1425](https://github.com/supabase/gotrue/issues/1425)) ([91e0e24](https://github.com/supabase/gotrue/commit/91e0e245f5957ebce13370f79fd4a6be8108ed80))

## [2.141.0](https://github.com/supabase/gotrue/compare/v2.140.0...v2.141.0) (2024-02-13)


### Features

* drop sha hash tag ([#1422](https://github.com/supabase/gotrue/issues/1422)) ([76853ce](https://github.com/supabase/gotrue/commit/76853ce6d45064de5608acc8100c67a8337ba791))
* prefix release with v ([#1424](https://github.com/supabase/gotrue/issues/1424)) ([9d398cd](https://github.com/supabase/gotrue/commit/9d398cd75fca01fb848aa88b4f545552e8b5751a))

## [2.140.0](https://github.com/supabase/gotrue/compare/v2.139.2...v2.140.0) (2024-02-13)


### Features

* deprecate existing webhook implementation ([#1417](https://github.com/supabase/gotrue/issues/1417)) ([5301e48](https://github.com/supabase/gotrue/commit/5301e481b0c7278c18b4578a5b1aa8d2256c2f5d))
* update publish.yml checkout repository so there is access to Dockerfile ([#1419](https://github.com/supabase/gotrue/issues/1419)) ([7cce351](https://github.com/supabase/gotrue/commit/7cce3518e8c9f1f3f93e4f6a0658ee08771c4f1c))

## [2.139.2](https://github.com/supabase/gotrue/compare/v2.139.1...v2.139.2) (2024-02-08)


### Bug Fixes

* improve perf in account linking ([#1394](https://github.com/supabase/gotrue/issues/1394)) ([8eedb95](https://github.com/supabase/gotrue/commit/8eedb95dbaa310aac464645ec91d6a374813ab89))
* OIDC provider validation log message ([#1380](https://github.com/supabase/gotrue/issues/1380)) ([27e6b1f](https://github.com/supabase/gotrue/commit/27e6b1f9a4394c5c4f8dff9a8b5529db1fc67af9))
* only create or update the email / phone identity after it's been verified ([#1403](https://github.com/supabase/gotrue/issues/1403)) ([2d20729](https://github.com/supabase/gotrue/commit/2d207296ec22dd6c003c89626d255e35441fd52d))
* only create or update the email / phone identity after it's been verified (again) ([#1409](https://github.com/supabase/gotrue/issues/1409)) ([bc6a5b8](https://github.com/supabase/gotrue/commit/bc6a5b884b43fe6b8cb924d3f79999fe5bfe7c5f))
* unmarshal is_private_email correctly ([#1402](https://github.com/supabase/gotrue/issues/1402)) ([47df151](https://github.com/supabase/gotrue/commit/47df15113ce8d86666c0aba3854954c24fe39f7f))
* use `pattern` for semver docker image tags ([#1411](https://github.com/supabase/gotrue/issues/1411)) ([14a3aeb](https://github.com/supabase/gotrue/commit/14a3aeb6c3f46c8d38d98cc840112dfd0278eeda))


### Reverts

* "fix: only create or update the email / phone identity after i… ([#1407](https://github.com/supabase/gotrue/issues/1407)) ([ff86849](https://github.com/supabase/gotrue/commit/ff868493169a0d9ac18b66058a735197b1df5b9b))
