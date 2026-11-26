package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// A managed (Cloud) project has no `supatype dev` and no compose stack: telling
// its user to read "the storage pane of supatype dev" sent them to a tool they
// are not running. The answer keeps its code and status and names where this
// kind of project's storage status lives.
func TestStorageOffOnAManagedProjectDoesNotPointAtSupatypeDev(t *testing.T) {
	cfg := config.Config{Mode: "managed", StorageDisabled: true}
	d := depsFor(t, &cfg, &proxy.RouteManifest{Schema: "public"})

	rec := httptest.NewRecorder()
	buildStorage(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/object/public/avatars/a.txt", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d %q, want 503", rec.Code, rec.Body.String())
	}
	var body struct{ Code, Message string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	if body.Code != "storage_unavailable" {
		t.Errorf("code %q, want storage_unavailable", body.Code)
	}
	for _, stale := range []string{"supatype dev", "self-host", "compose"} {
		if strings.Contains(body.Message, stale) {
			t.Errorf("message %q names %q, which a managed project does not run", body.Message, stale)
		}
	}
	if !strings.Contains(body.Message, "dashboard") {
		t.Errorf("message %q does not say where to look", body.Message)
	}
}
