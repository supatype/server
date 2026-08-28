package config

import "testing"

// TestLoadReadsTheAbsorbedVariables covers the fields that used to be read with
// os.Getenv from inside twelve packages. The point is not that envconfig works;
// it is that each variable is still spelled exactly as the deployment spells it,
// because the surface must not move in this phase.
func TestLoadReadsTheAbsorbedVariables(t *testing.T) {
	env := map[string]string{
		"SUPATYPE_CLOUD_ACTIVITY_ENABLED": "true",
		"SUPATYPE_CLOUD_ACTIVITY_URL":     "http://cp:4001",
		"SUPATYPE_CONTROL_PLANE_URL":      "http://cp:8080",
		"SUPATYPE_INTERNAL_HMAC_SECRET":   "s3cret",
		"SUPATYPE_NONPROD":                "true",
		"SUPATYPE_BLOCK_BOT_UA":           "true",
		"MAU_EMAIL_SALT":                  "pepper",
		"VALKEY_ADDR":                     "valkey:6379",
		"SUPATYPE_SQL_DATABASE_URL":       "postgres://a/b",
		"DATABASE_URL":                    "postgres://c/d",
		"SUPATYPE_DB_SCHEMA":              "app",
		"SUPATYPE_SQLRUNNER_INSECURE":     "yes",
		"POSTGRES_PASSWORD":               "pw",
		"STUDIO_OPEN_DEV":                 "on",
		"STUDIO_ADMIN_ROLES":              "admin,owner",
		"LOG_LEVEL":                       "debug",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	for name, got := range map[string]string{
		"SUPATYPE_CLOUD_ACTIVITY_URL":   cfg.CloudActivityURL,
		"SUPATYPE_CONTROL_PLANE_URL":    cfg.ControlPlaneURL,
		"SUPATYPE_INTERNAL_HMAC_SECRET": cfg.InternalHMACSecret,
		"MAU_EMAIL_SALT":                cfg.MAUEmailSalt,
		"VALKEY_ADDR":                   cfg.ValkeyAddrLegacy,
		"SUPATYPE_SQL_DATABASE_URL":     cfg.SQLDatabaseURL,
		"DATABASE_URL":                  cfg.DatabaseURL,
		"SUPATYPE_DB_SCHEMA":            cfg.SQLSchema,
		"POSTGRES_PASSWORD":             cfg.PostgresPassword,
		"STUDIO_ADMIN_ROLES":            cfg.StudioAdminRoles,
		"LOG_LEVEL":                     cfg.LogLevel,
	} {
		if got != env[name] {
			t.Errorf("%s: got %q, want %q", name, got, env[name])
		}
	}

	// The custom bool types must actually be reached by envconfig; a plain bool
	// would have rejected "yes" and "on" outright.
	for name, got := range map[string]bool{
		"SUPATYPE_CLOUD_ACTIVITY_ENABLED": cfg.CloudActivityEnabled.Bool(),
		"SUPATYPE_NONPROD":                cfg.NonProd.Bool(),
		"SUPATYPE_BLOCK_BOT_UA":           cfg.BlockBotUA.Bool(),
		"SUPATYPE_SQLRUNNER_INSECURE":     cfg.SQLRunnerInsecure.Bool(),
		"STUDIO_OPEN_DEV":                 cfg.StudioOpenDev.Bool(),
	} {
		if !got {
			t.Errorf("%s: want true, got false", name)
		}
	}
}

// The strict switches must keep refusing "1", which is the behaviour a plain
// bool field would have silently widened.
func TestStrictSwitchesStillRefuseOne(t *testing.T) {
	t.Setenv("SUPATYPE_CLOUD_ACTIVITY_ENABLED", "1")
	t.Setenv("SUPATYPE_SQLRUNNER_INSECURE", "1")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CloudActivityEnabled.Bool() {
		t.Error(`SUPATYPE_CLOUD_ACTIVITY_ENABLED="1" must not enable cloud metering`)
	}
	if !cfg.SQLRunnerInsecure.Bool() {
		t.Error(`SUPATYPE_SQLRUNNER_INSECURE="1" must be accepted, as it always has been`)
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][2]string{
		"SUPATYPE_CLOUD_ACTIVITY_URL": {cfg.CloudActivityURL, "http://control-plane:4001"},
		"SUPATYPE_CONTROL_PLANE_URL":  {cfg.ControlPlaneURL, "http://control-plane:8080"},
		"SUPATYPE_MODE":               {cfg.Mode, "dev"},
	} {
		if got[0] != got[1] {
			t.Errorf("%s default: got %q, want %q", name, got[0], got[1])
		}
	}
}

// TestTagDefaultsMatchConstants stops the two spellings of a default drifting.
// The tag is what Load applies; the constant is what a consumer falls back to
// when handed a Config that never went through Load. If they disagree, the
// behaviour depends on how the Config was built, which is exactly the class of
// bug the route-table lock caught when the default was only in the tag.
func TestTagDefaultsMatchConstants(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2]string{
		"ControlPlaneURL":  {cfg.ControlPlaneURL, DefaultControlPlaneURL},
		"CloudActivityURL": {cfg.CloudActivityURL, DefaultCloudActivityURL},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: tag default %q does not match the exported constant %q", name, pair[0], pair[1])
		}
	}
}

func TestSQLDSNPrefersTheSupatypeVariable(t *testing.T) {
	both := &Config{SQLDatabaseURL: "postgres://admin/db", DatabaseURL: "postgres://app/db"}
	if got := both.SQLDSN(); got != "postgres://admin/db" {
		t.Errorf("SUPATYPE_SQL_DATABASE_URL should win, got %q", got)
	}

	fallback := &Config{DatabaseURL: "postgres://app/db"}
	if got := fallback.SQLDSN(); got != "postgres://app/db" {
		t.Errorf("should fall back to DATABASE_URL, got %q", got)
	}

	whitespace := &Config{SQLDatabaseURL: "   ", DatabaseURL: "postgres://app/db"}
	if got := whitespace.SQLDSN(); got != "postgres://app/db" {
		t.Errorf("a blank Supatype DSN should not shadow the fallback, got %q", got)
	}

	if got := (&Config{}).SQLDSN(); got != "" {
		t.Errorf("no DSN configured should be empty, got %q", got)
	}
}

// A value the decoder cannot parse must stop the process at startup rather than
// leaving the field at its zero value, which for a bool would silently mean
// "off" for something the deployment asked to turn on.
func TestLoadRejectsAnUnparseableValue(t *testing.T) {
	t.Setenv("SUPATYPE_APP_SPA_FALLBACK", "notabool")

	if _, err := Load(); err == nil {
		t.Fatal("want an error for a bool field set to a non-boolean")
	}
}

// The two custom bool types never fail to decode: anything unrecognised is
// false. That is deliberate, and it means a typo in one of these switches
// disables the feature rather than refusing to boot.
func TestLoadAcceptsAnyValueForTheCustomBools(t *testing.T) {
	t.Setenv("SUPATYPE_CLOUD_ACTIVITY_ENABLED", "notabool")
	t.Setenv("STUDIO_OPEN_DEV", "notabool")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("custom bools should not fail to decode: %v", err)
	}
	if cfg.CloudActivityEnabled.Bool() || cfg.StudioOpenDev.Bool() {
		t.Error("an unrecognised value must leave the switch off")
	}
}
