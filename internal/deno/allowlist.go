package deno

import "strings"

// What the Deno child inherits from this server's environment.
//
// It used to inherit all of it (e2e 2026-11-21, RB-2). Natively that is the stack's JWT secret,
// the service-role key, the database URLs, everything the server loaded from the project's .env and
// whatever the developer's shell held, and Deno runs with --allow-env, so every function, an npm
// dependency's code included, could read any of it through Deno.env.
//
// The worker applies the same list again before it imports a function
// (supatype packages/functions-worker/env-allowlist.ts); the two are kept in step by hand. A value
// the server means the child to have goes in the explicit extras instead (EdgeSubprocessEnv), which
// are not filtered.

// runtimeEnvNames is what Deno and the operating system need to run, fetch and resolve modules.
var runtimeEnvNames = []string{
	"PATH", "PATHEXT", "HOME", "USER", "USERNAME", "LOGNAME", "TMPDIR", "TEMP", "TMP", "TZ", "LANG",
	"LC_ALL", "LC_CTYPE", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "NO_COLOR",
	// Windows: the profile and cache directories, and what its networking and process APIs read.
	"USERPROFILE", "APPDATA", "LOCALAPPDATA", "SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC",
	"PROGRAMDATA", "HOMEDRIVE", "HOMEPATH",
	// Deno honours these for fetch and for npm: and jsr: imports.
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "NPM_CONFIG_REGISTRY",
}

// workerEnvNames is what the functions worker reads to find its sources, listen, log and build a
// function's ctx. SUPATYPE_SERVICE_ROLE_KEY is not here: it comes through the extras, and the
// worker withholds it from every route not granted it.
var workerEnvNames = []string{
	"SUPATYPE_FUNCTIONS_ROOT", "SUPATYPE_DENO_FUNCTIONS_DIR", "SUPATYPE_HOOKS_ROOT",
	"SUPATYPE_FUNCTION_NAME", "SUPATYPE_FUNCTIONS_HOST", "SUPATYPE_FUNCTIONS_DRAIN_MS",
	"SUPATYPE_SERVICE_ROLE_ROUTES", "SUPATYPE_SERVICE_ROLE_ROUTES_FILE", "SUPATYPE_SERVICE_ROLE_KEY_FILE",
	"SUPATYPE_MANIFEST_PATH", "SUPATYPE_SHARED_ENV_FILE", "SUPATYPE_FUNCTION_ENV_FILE",
	"SUPATYPE_LOG_TAIL_BUFFER", "SUPATYPE_PROJECT_REF", "SUPATYPE_MANAGED_PROJECT_REF",
	"DEDICATED_PROJECT_REF", "SUPATYPE_URL", "SUPATYPE_INTERNAL_URL", "SUPATYPE_PUBLIC_URL",
	"SUPATYPE_ANON_KEY", "SUPATYPE_REGION", "SUPATYPE_DB_URL",
}

// allowedEnvPrefixes are Deno's own settings: its cache directory, certificate store and registry
// tokens.
var allowedEnvPrefixes = []string{"DENO_"}

var allowedEnvNames = func() map[string]struct{} {
	names := make(map[string]struct{}, len(runtimeEnvNames)+len(workerEnvNames))
	for _, list := range [][]string{runtimeEnvNames, workerEnvNames} {
		for _, name := range list {
			names[name] = struct{}{}
		}
	}
	return names
}()

// envNameAllowed reports whether the Deno child may inherit name. Case-insensitive: Windows spells
// PATH as Path, and its environment does not tell the two apart.
func envNameAllowed(name string) bool {
	upper := strings.ToUpper(name)
	if _, ok := allowedEnvNames[upper]; ok {
		return true
	}
	for _, prefix := range allowedEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}
