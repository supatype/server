package gateway

import (
	"bytes"
	"strings"
	"testing"

	"github.com/supatype/server/internal/observability"
)

// The access logger is its own logrus instance, so LOG_FIELDS reach it only if it asks.
func TestOuterAccessLoggerCarriesTheConfiguredFields(t *testing.T) {
	observability.SetLogFields(map[string]string{"service": "auth"})
	t.Cleanup(func() { observability.SetLogFields(nil) })
	var buf bytes.Buffer
	configureOuterAccessLoggingWriter(&buf, "info")
	t.Cleanup(func() { configureOuterAccessLogging("info") })

	outerAccessLog().Info("request")
	if !strings.Contains(buf.String(), `"service":"auth"`) {
		t.Errorf("want the configured field on the access line, got %q", buf.String())
	}
}
