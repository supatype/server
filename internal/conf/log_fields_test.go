package conf

import (
	"testing"

	"github.com/supatype/server/internal/envparse"
)

// SUPATYPE_LOG_FIELDS comes from the environment as key:value pairs, so its values are
// strings, and the logging API takes them as such.
func TestLogFieldsLoadFromTheEnvironmentAsStrings(t *testing.T) {
	t.Setenv("SUPATYPE_LOG_FIELDS", "service:auth,region:eu")
	var cfg LoggingConfig
	if err := envparse.Process(EnvPrefix+"_LOG", &cfg); err != nil {
		t.Fatal(err)
	}
	var fields map[string]string = cfg.Fields
	if fields["service"] != "auth" || fields["region"] != "eu" {
		t.Errorf("LOG_FIELDS = %v, want service:auth and region:eu", fields)
	}
}
