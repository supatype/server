package config

import (
	"fmt"
	"sort"
	"strings"
)

// legacyPrefix is the prefix this service inherited from the project it was
// forked from. Nothing reads it any more.
const legacyPrefix = "GOTRUE_"

// retired holds variables that kept the SUPATYPE_ prefix but changed name, so
// the mechanical rule below cannot derive the replacement.
//
// These are the dangerous ones. A variable under the old prefix is obviously
// stale, but SUPATYPE_SECURITY_SB_FORWARDED_FOR_ENABLED still looks current,
// and a deployment that sets it would silently run with the feature off.
var retired = map[string]string{
	"SUPATYPE_SECURITY_SB_FORWARDED_FOR_ENABLED": "SUPATYPE_SECURITY_ST_FORWARDED_FOR_ENABLED",
}

// Preflight refuses to start when the environment still names a retired variable.
//
// A deployment that sets GOTRUE_JWT_SECRET and nothing else now has no JWT
// secret. Starting anyway would look fine until the first request, and then
// present as unexplained 401s a long way from the cause. Refusing here, naming
// every variable and what it is called now, turns that into a one-line fix.
func Preflight(env Env) error {
	stale := staleNames(env)
	if len(stale) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("configuration names variables that nothing reads any more.\n")
	b.WriteString("Rename these, or unset them if the deployment no longer needs them:\n")
	for _, name := range stale {
		fmt.Fprintf(&b, "    %s -> %s\n", name, Rename(name))
	}
	return fmt.Errorf("%s", b.String())
}

// Rename maps an old variable name to its replacement.
//
// The prefix rename was mechanical, so it is computed rather than tabulated;
// anything that changed name for another reason has to be looked up.
func Rename(old string) string {
	if replacement, ok := retired[old]; ok {
		return replacement
	}
	if !strings.HasPrefix(old, legacyPrefix) {
		return old
	}
	return "SUPATYPE_" + strings.TrimPrefix(old, legacyPrefix)
}

// isStale reports whether nothing reads this variable any more.
func isStale(name string) bool {
	if _, ok := retired[name]; ok {
		return true
	}
	return strings.HasPrefix(name, legacyPrefix)
}

// staleNames returns the retired variables present in env, sorted.
func staleNames(env Env) []string {
	var found []string
	for _, name := range env.Names() {
		if isStale(name) {
			found = append(found, name)
		}
	}
	sort.Strings(found)
	return found
}
