package config

import (
	"fmt"
	"sort"
	"strings"
)

// legacyPrefix is the prefix this service inherited from the project it was
// forked from. Nothing reads it any more.
const legacyPrefix = "GOTRUE_"

// Preflight refuses to start when the environment still names the old prefix.
//
// A deployment that sets GOTRUE_JWT_SECRET and nothing else now has no JWT
// secret. Starting anyway would look fine until the first request, and then
// present as unexplained 401s a long way from the cause. Refusing here, naming
// every variable and what it is called now, turns that into a one-line fix.
//
// The renaming is mechanical: the prefix changed and nothing else did, so the
// message can compute the new name rather than carry a table that would drift.
func Preflight(env Env) error {
	stale := staleNames(env)
	if len(stale) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "configuration still uses the old %s prefix, which nothing reads.\n", legacyPrefix)
	b.WriteString("Rename these, or unset them if the deployment no longer needs them:\n")
	for _, name := range stale {
		fmt.Fprintf(&b, "    %s -> %s\n", name, Rename(name))
	}
	return fmt.Errorf("%s", b.String())
}

// Rename maps an old variable name to its replacement.
func Rename(old string) string {
	if !strings.HasPrefix(old, legacyPrefix) {
		return old
	}
	return "SUPATYPE_" + strings.TrimPrefix(old, legacyPrefix)
}

// staleNames returns the old-prefix variables present in env, sorted.
func staleNames(env Env) []string {
	var found []string
	for _, name := range env.Names() {
		if strings.HasPrefix(name, legacyPrefix) {
			found = append(found, name)
		}
	}
	sort.Strings(found)
	return found
}
