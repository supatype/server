package studioauth

import (
	"testing"

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
		addSessionAccess(body, snapshot, caller, tc.canWrite)
		fields, ok := body["fields"].(map[string]map[string]studiobootstrap.FieldVerdict)
		if !ok {
			t.Fatalf("%s: no fields on the session body: %v", tc.name, body)
		}
		if got := fields["posts"]["title"]; got != tc.want {
			t.Errorf("%s: posts.title = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
