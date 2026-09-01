package config

import (
	"os"
	"strings"
	"testing"
)

func TestPreflightPassesOnACleanEnvironment(t *testing.T) {
	env := MapEnv{
		"SUPATYPE_JWT_SECRET": "s",
		"SUPATYPE_MODE":       "dev",
		"DATABASE_URL":        "postgres://x/y",
		"PATH":                "/usr/bin",
	}
	if err := Preflight(env); err != nil {
		t.Errorf("want no error, got %v", err)
	}
}

// A deployment that sets only the old name now has no value at all. Starting
// anyway would present as unexplained 401s a long way from the cause.
func TestPreflightRefusesTheOldPrefix(t *testing.T) {
	env := MapEnv{
		"GOTRUE_JWT_SECRET": "s",
		"SUPATYPE_MODE":     "dev",
	}
	err := Preflight(env)
	if err == nil {
		t.Fatal("want a refusal")
	}
	message := err.Error()
	if !strings.Contains(message, "GOTRUE_JWT_SECRET -> SUPATYPE_JWT_SECRET") {
		t.Errorf("the message must name the fix:\n%s", message)
	}
	if strings.Contains(message, "SUPATYPE_MODE") {
		t.Errorf("a correctly named variable should not be listed:\n%s", message)
	}
}

// Every stale variable is reported at once. Fixing them one restart at a time
// is the failure mode this is meant to avoid.
func TestPreflightReportsAllOfThemAtOnce(t *testing.T) {
	env := MapEnv{
		"GOTRUE_JWT_SECRET":         "s",
		"GOTRUE_SITE_URL":           "http://localhost",
		"GOTRUE_DB_DATABASE_URL":    "postgres://x/y",
		"GOTRUE_MAILER_AUTOCONFIRM": "true",
	}
	err := Preflight(env)
	if err == nil {
		t.Fatal("want a refusal")
	}
	for name := range env {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s is missing from the message:\n%s", name, err)
		}
	}
}

// The message is sorted so a deployment with many stale names produces a stable,
// readable list rather than a different order every restart.
func TestPreflightSortsWhatItReports(t *testing.T) {
	err := Preflight(MapEnv{"GOTRUE_ZED": "1", "GOTRUE_ALPHA": "1", "GOTRUE_MID": "1"})
	if err == nil {
		t.Fatal("want a refusal")
	}
	message := err.Error()
	alpha, mid, zed := strings.Index(message, "GOTRUE_ALPHA"), strings.Index(message, "GOTRUE_MID"), strings.Index(message, "GOTRUE_ZED")
	if !(alpha < mid && mid < zed) {
		t.Errorf("names should be sorted:\n%s", message)
	}
}

func TestRename(t *testing.T) {
	for old, want := range map[string]string{
		"GOTRUE_JWT_SECRET":      "SUPATYPE_JWT_SECRET",
		"GOTRUE_DB_DATABASE_URL": "SUPATYPE_DB_DATABASE_URL",
		"GOTRUE_":                "SUPATYPE_",
		// Names without the prefix are not this function's business, including
		// ones that merely contain it.
		"DATABASE_URL":    "DATABASE_URL",
		"SUPATYPE_MODE":   "SUPATYPE_MODE",
		"MY_GOTRUE_THING": "MY_GOTRUE_THING",
	} {
		if got := Rename(old); got != want {
			t.Errorf("Rename(%q) = %q, want %q", old, got, want)
		}
	}
}

func TestOSEnvListsTheProcessVariables(t *testing.T) {
	t.Setenv("SUPATYPE_PREFLIGHT_PROBE", "1")

	var found bool
	for _, name := range (OSEnv{}).Names() {
		if name == "SUPATYPE_PREFLIGHT_PROBE" {
			found = true
		}
		if strings.Contains(name, "=") {
			t.Errorf("Names should return names, not assignments: %q", name)
		}
	}
	if !found {
		t.Error("the probe variable was not listed")
	}
	if len((OSEnv{}).Names()) != len(os.Environ()) {
		t.Error("every entry should yield a name")
	}
}

// The preflight runs against the real process environment at startup, so it has
// to agree with OSEnv about what is set.
func TestPreflightAgainstTheProcessEnvironment(t *testing.T) {
	t.Setenv("GOTRUE_SOMETHING_STALE", "x")
	err := Preflight(OSEnv{})
	if err == nil {
		t.Fatal("a stale variable on the process must be caught")
	}
	if !strings.Contains(err.Error(), "GOTRUE_SOMETHING_STALE -> SUPATYPE_SOMETHING_STALE") {
		t.Errorf("message:\n%s", err)
	}
}

// A variable that kept the SUPATYPE_ prefix but changed name is the one a
// reader cannot spot. This one still looks current, so a deployment that sets
// it would start cleanly and run with the forwarded-for header ignored, which
// silently changes which IP address rate limiting counts against.
func TestPreflightRefusesARetiredNameThatKeptThePrefix(t *testing.T) {
	env := MapEnv{
		"SUPATYPE_SECURITY_SB_FORWARDED_FOR_ENABLED": "true",
		"SUPATYPE_MODE": "dev",
	}
	err := Preflight(env)
	if err == nil {
		t.Fatal("want a refusal")
	}
	message := err.Error()
	want := "SUPATYPE_SECURITY_SB_FORWARDED_FOR_ENABLED -> SUPATYPE_SECURITY_ST_FORWARDED_FOR_ENABLED"
	if !strings.Contains(message, want) {
		t.Errorf("the message must name the fix:\n%s", message)
	}
}

// The replacement name must not itself be refused, or the fix the message asks
// for would fail on the next start.
func TestPreflightAcceptsTheReplacementName(t *testing.T) {
	env := MapEnv{"SUPATYPE_SECURITY_ST_FORWARDED_FOR_ENABLED": "true"}
	if err := Preflight(env); err != nil {
		t.Errorf("want no error, got %v", err)
	}
}

// Every retired name has to map to something else, or the message would tell
// the reader to change a variable to itself.
func TestEveryRetiredNameMapsToADifferentName(t *testing.T) {
	for old, replacement := range retired {
		if old == replacement {
			t.Errorf("%s maps to itself", old)
		}
		if replacement == "" {
			t.Errorf("%s maps to nothing", old)
		}
		if isStale(replacement) {
			t.Errorf("%s maps to %s, which is itself retired", old, replacement)
		}
	}
}
