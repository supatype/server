package conf

import (
	"strings"
	"testing"
)

// The namespace goes into migrations unquoted, where Postgres folds it to lower case, and into
// the connection URL as search_path. Only a plain lower-case identifier means the same schema
// in both, so anything else is refused at load rather than splitting auth across two schemas.
func TestDBNamespaceMustBeAPlainLowerCaseIdentifier(t *testing.T) {
	for _, ok := range []string{"auth", " auth ", "_a1", "user", strings.Repeat("a", 63)} {
		cfg := &DBConfiguration{Namespace: ok}
		if err := cfg.Validate(); err != nil {
			t.Errorf("namespace %q refused: %v", ok, err)
		}
		if cfg.Namespace != strings.TrimSpace(ok) {
			t.Errorf("namespace %q kept as %q, want it trimmed once here", ok, cfg.Namespace)
		}
	}
	for _, bad := range []string{"", "  ", "Auth", "Auth Ns", "1auth", "au-th", "a$b", strings.Repeat("a", 64)} {
		cfg := &DBConfiguration{Namespace: bad}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "SUPATYPE_DB_NAMESPACE") {
			t.Errorf("namespace %q: want an error naming SUPATYPE_DB_NAMESPACE, got %v", bad, err)
		}
	}
}
