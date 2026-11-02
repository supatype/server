package conf

import (
	"strings"
	"testing"
)

// The namespace goes into migrations unquoted (`CREATE SCHEMA IF NOT EXISTS auth`,
// `auth.users`), where Postgres folds it to lower case and refuses a reserved keyword, and
// into the connection URL as search_path. Only a plain lower-case identifier that is not
// reserved means the same schema in both, so anything else is refused at load.
func TestDBNamespaceMustBeAPlainLowerCaseIdentifier(t *testing.T) {
	// action is an unreserved keyword, which Postgres takes anywhere a name goes. The others
	// are refused: reserved ones, those reserved as a type or function name, and column-name
	// keywords (between, int, time), which cannot name the type in <ns>.factor_type.
	for _, ok := range []string{"auth", "_a1", "action", "pgauth", strings.Repeat("a", 63)} {
		cfg := &DBConfiguration{Namespace: ok}
		if err := cfg.Validate(); err != nil {
			t.Errorf("namespace %q refused: %v", ok, err)
		}
	}
	bad := []string{"", " auth ", "Auth", "Auth Ns", "1auth", "au-th", "a$b", strings.Repeat("a", 64),
		"user", "table", "order", "select", "join", "authorization", "pg_auth",
		"between", "int", "time", "values", "none", "char", "bigint"}
	for _, name := range bad {
		cfg := &DBConfiguration{Namespace: name}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "SUPATYPE_DB_NAMESPACE") {
			t.Errorf("namespace %q: want an error naming SUPATYPE_DB_NAMESPACE, got %v", name, err)
		}
	}
}

// Trimming and the default happen in one place, before validation.
func TestApplyDefaultsTrimsTheNamespaceAndDefaultsAnEmptyOne(t *testing.T) {
	for given, want := range map[string]string{"": "auth", "   ": "auth", " auth ": "auth", "mine": "mine"} {
		cfg := &GlobalConfiguration{DB: DBConfiguration{Namespace: given}, JWT: JWTConfiguration{Secret: "a"}}
		if err := cfg.ApplyDefaults(); err != nil {
			t.Fatal(err)
		}
		if cfg.DB.Namespace != want {
			t.Errorf("namespace %q became %q, want %q", given, cfg.DB.Namespace, want)
		}
	}
}
