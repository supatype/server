package studioauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/supatype/server/internal/studiobootstrap"
)

// A column the app's own rules would let anyone write is still offered to a
// viewer as read-only: the proxy refuses a viewer's writes, so offering an
// editable input or a create field would show a control that cannot work.
// A role that writes keeps the verdict the rules give it.
func TestSessionFieldsOfferAViewerNoWrite(t *testing.T) {
	snapshot := &studiobootstrap.Snapshot{
		Hash: "h",
		AST: []byte(`{"models":[{"name":"Post","annotations":{"db":{"tableName":"posts"},
			"platform":{"access":{"read":{"type":"public"},
			  "fields":{"title":{"read":{"type":"public"}}}}}}}]}`),
	}
	caller := studiobootstrap.Caller{UserID: "user-1", AppRole: "authenticated"}
	allow, deny := studiobootstrap.VerdictAllow, studiobootstrap.VerdictDeny

	for _, tc := range []struct {
		name     string
		canWrite bool
		want     studiobootstrap.FieldVerdict
	}{
		{"viewer", false, studiobootstrap.FieldVerdict{Read: allow, Write: deny, Create: deny}},
		{"editor", true, studiobootstrap.FieldVerdict{Read: allow, Write: allow, Create: allow}},
	} {
		body := map[string]interface{}{}
		if err := addSessionAccess(body, snapshot, caller, tc.canWrite); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		fields, ok := body["fields"].(map[string]map[string]studiobootstrap.FieldVerdict)
		if !ok {
			t.Fatalf("%s: no fields on the session body: %v", tc.name, body)
		}
		if got := fields["posts"]["title"]; got != tc.want {
			t.Errorf("%s: posts.title = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// A schema state that cannot be read is logged with the route that hit it, and
// only once per distinct error: Studio asks on every load, and a project that is
// broken in the same way each time should not fill the log saying so.
func TestASchemaReadFailureIsLoggedOnce(t *testing.T) {
	hook := logtest.NewGlobal()
	req := httptest.NewRequest(http.MethodGet, "/studio/session", nil)
	failure := errors.New("invalid input syntax for type bytea (log once test)")

	for i := 0; i < 3; i++ {
		markFieldAccess(map[string]interface{}{}, req, failure)
	}

	entries := hook.AllEntries()
	if len(entries) != 1 {
		t.Fatalf("logged %d times, want once", len(entries))
	}
	if entries[0].Data["path"] != "/studio/session" || entries[0].Data[logrus.ErrorKey] != failure {
		t.Errorf("entry = %+v, want the path and the error", entries[0].Data)
	}
}
