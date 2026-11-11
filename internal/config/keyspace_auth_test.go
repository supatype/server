package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The keyspace credentials are read from the SUPATYPE_ spellings only. A
// password turns authentication on and brings the default user with it; a
// username alone does not.
func TestKeyspaceAuthResolution(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want KeyspaceCredentials
	}{
		{"nothing set sends no AUTH", nil, KeyspaceCredentials{}},
		{
			"a username alone sends no AUTH",
			map[string]string{"SUPATYPE_KEYSPACE_USERNAME": "app"},
			KeyspaceCredentials{},
		},
		{"a blank password is unset", map[string]string{"SUPATYPE_KEYSPACE_PASSWORD": "  "}, KeyspaceCredentials{}},
		{
			"a password alone uses the default user",
			map[string]string{"SUPATYPE_KEYSPACE_PASSWORD": "pw"},
			KeyspaceCredentials{Username: DefaultKeyspaceUsername, Password: "pw"},
		},
		{
			"a blank username uses the default user",
			map[string]string{"SUPATYPE_KEYSPACE_PASSWORD": "pw", "SUPATYPE_KEYSPACE_USERNAME": " "},
			KeyspaceCredentials{Username: DefaultKeyspaceUsername, Password: "pw"},
		},
		{
			"both set",
			map[string]string{"SUPATYPE_KEYSPACE_PASSWORD": "pw", "SUPATYPE_KEYSPACE_USERNAME": " app "},
			KeyspaceCredentials{Username: "app", Password: "pw"},
		},
		{
			"the unprefixed spellings are not read",
			map[string]string{"KEYSPACE_PASSWORD": "pw", "KEYSPACE_USERNAME": "app"},
			KeyspaceCredentials{},
		},
		{
			"spaces inside a password are kept",
			map[string]string{"SUPATYPE_KEYSPACE_PASSWORD": " p w "},
			KeyspaceCredentials{Username: DefaultKeyspaceUsername, Password: " p w "},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			platform, _ := loadCredentials(t)
			wantCredentials(t, "platform", platform, tc.want)
		})
	}
}

// The password must not reach a log line by any of the ways a config or its
// credentials usually get printed.
func TestTheKeyspacePasswordIsMaskedWhereverItIsPrinted(t *testing.T) {
	const password = "hunter2-b-ksrv"
	cfg := &Config{KeyspacePassword: password}
	creds, _, err := cfg.LoadKeyspaceCredentials()
	if err != nil {
		t.Fatal(err)
	}

	printed := map[string]string{
		"%v config":       fmt.Sprintf("%v", cfg),
		"%+v config":      fmt.Sprintf("%+v", cfg),
		"%#v config":      fmt.Sprintf("%#v", cfg),
		"Sprint secret":   fmt.Sprint(creds.Password),
		"%q secret":       fmt.Sprintf("%q", creds.Password),
		"%+v credentials": fmt.Sprintf("%+v", creds),
		"%#v credentials": fmt.Sprintf("%#v", creds),
	}
	for _, v := range []interface{}{cfg, creds} {
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		printed[fmt.Sprintf("json %T", v)] = string(out)
	}
	for how, out := range printed {
		if strings.Contains(out, password) {
			t.Errorf("%s prints the password: %s", how, out)
		}
		if !strings.Contains(out, "[redacted]") {
			t.Errorf("%s should show that a password is set: %s", how, out)
		}
	}
	if creds.Password.Reveal() != password {
		t.Error("Reveal must return the password itself")
	}
}

// An unset secret prints as nothing, so a log line still says it was not set.
func TestAnUnsetSecretPrintsAsNothing(t *testing.T) {
	var s Secret
	if s.String() != "" || s.GoString() != "" {
		t.Errorf("unset secret prints as %q / %q", s.String(), s.GoString())
	}
}

// The project keyspace has a pair of its own and never borrows the platform's:
// its credentials are handed to users, so the platform's must not be them.
func TestProjectKeyspaceAuthNeverFallsBackToThePlatformPair(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want KeyspaceCredentials
	}{
		{"nothing set sends no AUTH", nil, KeyspaceCredentials{}},
		{
			"only the platform pair set still sends no AUTH",
			map[string]string{"SUPATYPE_KEYSPACE_PASSWORD": "platform", "SUPATYPE_KEYSPACE_USERNAME": "ops"},
			KeyspaceCredentials{},
		},
		{
			"a project password alone uses the default user",
			map[string]string{"SUPATYPE_PROJECT_KEYSPACE_PASSWORD": "project"},
			KeyspaceCredentials{Username: DefaultKeyspaceUsername, Password: "project"},
		},
		{
			"a project username alone sends no AUTH",
			map[string]string{"SUPATYPE_PROJECT_KEYSPACE_USERNAME": "app", "SUPATYPE_KEYSPACE_PASSWORD": "platform"},
			KeyspaceCredentials{},
		},
		{
			"the unprefixed spellings are not read",
			map[string]string{"PROJECT_KEYSPACE_PASSWORD": "project", "PROJECT_KEYSPACE_USERNAME": "app"},
			KeyspaceCredentials{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, project := loadCredentials(t)
			wantCredentials(t, "project", project, tc.want)
		})
	}
}

// A project password equal to the platform one would hand users the platform
// keyspace, so it is refused, wherever each was set and whichever user goes
// with it. The refusal names the rule, never either value.
func TestLoadKeyspaceCredentialsRefusesAProjectPasswordEqualToThePlatformOne(t *testing.T) {
	const shared = "shared-b-ksrv"
	for name, cfg := range map[string]*Config{
		"same address":                {KeyspacePassword: shared, ProjectKeyspacePassword: shared},
		"separate address, same pair": {ProjectKeyspaceAddr: "postgres:6379", KeyspacePassword: shared, ProjectKeyspacePassword: shared},
		"different users":             {KeyspacePassword: shared, KeyspaceUsername: "ops", ProjectKeyspacePassword: shared},
	} {
		_, _, err := cfg.LoadKeyspaceCredentials()
		if err == nil {
			t.Errorf("%s: want a refusal", name)
			continue
		}
		if strings.Contains(err.Error(), shared) {
			t.Errorf("%s: the refusal carries the password: %v", name, err)
		}
		if !strings.Contains(err.Error(), "SUPATYPE_PROJECT_KEYSPACE_PASSWORD must differ from SUPATYPE_KEYSPACE_PASSWORD") {
			t.Errorf("%s: the refusal should name the rule: %v", name, err)
		}
	}
}

// Anything else starts, including no passwords at all, which is today.
func TestLoadKeyspaceCredentialsAcceptsDistinctOrAbsentPasswords(t *testing.T) {
	for name, cfg := range map[string]*Config{
		"nothing set":       {},
		"platform only":     {KeyspacePassword: "platform"},
		"project only":      {ProjectKeyspacePassword: "project"},
		"distinct":          {KeyspacePassword: "platform", ProjectKeyspacePassword: "project"},
		"separate, no AUTH": {ProjectKeyspaceAddr: "postgres:6379"},
	} {
		if _, _, err := cfg.LoadKeyspaceCredentials(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// loadCredentials loads the Config from the environment the test set and
// resolves both keyspace pairs.
func loadCredentials(t *testing.T) (platform, project KeyspaceCredentials) {
	t.Helper()
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	platform, project, err = cfg.LoadKeyspaceCredentials()
	if err != nil {
		t.Fatal(err)
	}
	return platform, project
}

// wantCredentials compares credentials without printing a password.
func wantCredentials(t *testing.T, which string, got, want KeyspaceCredentials) {
	t.Helper()
	if got.Username != want.Username || got.Password.Reveal() != want.Password.Reveal() {
		t.Errorf("%s credentials = %q / %d-byte password, want %q / %d bytes",
			which, got.Username, len(got.Password.Reveal()), want.Username, len(want.Password.Reveal()))
	}
}
