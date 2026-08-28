package config

import (
	"sort"
	"strings"
	"testing"

	"github.com/kelseyhightower/envconfig"
	"github.com/supatype/server/internal/conf"
)

// namesFor returns every variable envconfig would look up for spec, both the
// prefixed key and the bare fallback from an explicit tag.
func namesFor(prefix string, spec interface{}) map[string]bool {
	var sb strings.Builder
	if err := envconfig.Usagef(prefix, spec, &sb, "{{range .}}{{.Key}}\t{{.Alt}}\n{{end}}"); err != nil {
		panic(err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(sb.String()), "\n") {
		key, alt, _ := strings.Cut(strings.TrimSpace(line), "\t")
		for _, name := range []string{key, alt} {
			if name = strings.TrimSpace(name); name != "" {
				out[name] = true
			}
		}
	}
	return out
}

// The service loads two structs from one environment. A name claimed by both is
// a variable whose meaning depends on which struct you ask, and the two would
// drift apart silently.
//
// Two overlaps are deliberate, and both are cases where a second value would be
// a bug rather than a feature:
//
//	DATABASE_URL         the auth service and the admin pool are pointed at the
//	                     same database, and Config.SQLDSN states the precedence.
//	SUPATYPE_JWT_SECRET  the gateway verifies tokens the auth service issues, so
//	                     a second secret would mean rejecting its own tokens.
func TestTheTwoConfigStructsDoNotFightOverAName(t *testing.T) {
	allowed := map[string]bool{
		"DATABASE_URL":        true,
		"SUPATYPE_JWT_SECRET": true,
	}

	gateway := namesFor("", &Config{})
	auth := namesFor(conf.EnvPrefix, &conf.GlobalConfiguration{})

	var collisions []string
	for name := range auth {
		if gateway[name] && !allowed[name] {
			collisions = append(collisions, name)
		}
	}
	sort.Strings(collisions)

	if len(collisions) > 0 {
		t.Errorf("%d name(s) claimed by both config structs:\n  %s",
			len(collisions), strings.Join(collisions, "\n  "))
	}
}

// A guard on the guard: if the allowance stops matching reality, the test above
// is either hiding a collision or carrying a stale exception.
func TestTheAllowedOverlapIsStillReal(t *testing.T) {
	gateway := namesFor("", &Config{})
	auth := namesFor(conf.EnvPrefix, &conf.GlobalConfiguration{})
	for _, name := range []string{"DATABASE_URL", "SUPATYPE_JWT_SECRET"} {
		if !gateway[name] || !auth[name] {
			t.Errorf("%s is no longer claimed by both structs; drop the exception", name)
		}
	}
}
