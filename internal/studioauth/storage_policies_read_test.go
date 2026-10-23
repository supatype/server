package studioauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sendMetadata sends one request to target through the proxy as role, and checks
// that whatever reached upstream was not recorded, as no catalogue read is.
func sendMetadata(t *testing.T, role, method, target, body string) (*bodyUpstream, *httptest.ResponseRecorder) {
	t.Helper()
	audit := &fakeAuditor{}
	next, rec := sendThroughProxy(t, proxied{role: role, method: method, target: target, body: body}, audit)
	if len(audit.records) != 0 {
		t.Errorf("%s %s: a catalogue read was recorded: %+v", role, target, audit.records)
	}
	return next, rec
}

const (
	storagePoliciesBody = `{"name":"policies","params":{"schema":"storage"}}`
	storageRLSBody      = `{"name":"rls_status","params":{"schema":"storage"}}`
)

// Owner decision: an editor may see a storage bucket's access rules, read-only,
// as a viewer can. Studio's Storage > Policies view sends exactly these two
// requests, and they reach the catalogue with their body intact.
func TestAnEditorReadsStoragePolicies(t *testing.T) {
	for _, body := range []string{storagePoliciesBody, storageRLSBody, " \n" + storageRLSBody + "\n"} {
		next, rec := sendMetadata(t, "editor", http.MethodPost, "/metadata", body)
		if !next.reached {
			t.Fatalf("%s: editor refused with %d %s", body, rec.Code, rec.Body.String())
		}
		if next.body != body {
			t.Errorf("%s: upstream got body %q", body, next.body)
		}
	}
}

// Everything else in the catalogue stays with schema view.
func TestAnEditorReadsNoOtherMetadata(t *testing.T) {
	for name, body := range map[string]string{
		"public schema":         `{"name":"policies","params":{"schema":"public"}}`,
		"schema case variant":   `{"name":"policies","params":{"schema":"STORAGE"}}`,
		"other query":           `{"name":"tables","params":{"schema":"storage"}}`,
		"query name case":       `{"name":"Policies","params":{"schema":"storage"}}`,
		"duplicate name":        `{"name":"policies","name":"tables","params":{"schema":"storage"}}`,
		"duplicate schema":      `{"name":"policies","params":{"schema":"storage","schema":"public"}}`,
		"case variant name key": `{"Name":"tables","name":"policies","params":{"schema":"storage"}}`,
		"case variant params":   `{"name":"policies","Params":{"schema":"public"},"params":{"schema":"storage"}}`,
		"case variant schema":   `{"name":"policies","params":{"Schema":"storage"}}`,
		"extra param":           `{"name":"policies","params":{"schema":"storage","table":"objects"}}`,
		"extra top-level field": `{"name":"policies","params":{"schema":"storage"},"sql":"select 1"}`,
		"no params":             `{"name":"policies"}`,
		"schema not a string":   `{"name":"policies","params":{"schema":["storage"]}}`,
		"schema null":           `{"name":"policies","params":{"schema":null}}`,
		"trailing data":         storagePoliciesBody + `{"name":"tables"}`,
		"batch":                 `[` + storagePoliciesBody + `]`,
		"not json":              `name=policies&schema=storage`,
		"empty":                 ``,
		"oversized":             `{"name":"policies","params":{"schema":"storage"},"pad":"` + strings.Repeat("x", guardBodyLimit) + `"}`,
	} {
		if next, rec := sendMetadata(t, "editor", http.MethodPost, "/metadata", body); next.reached || rec.Code != http.StatusForbidden {
			t.Errorf("%s: editor got %d, reached %v", name, rec.Code, next.reached)
		}
	}
}

// The gateway answers the catalogue only on POST /metadata and POST
// /metadata/introspect. Any other method or path beneath them falls through to
// the application, and the proxy would have sent it with the service role's
// key, so a viewer or an editor reaches none of them, whatever the body says.
func TestOnlyTheCatalogueRoutesAreOpen(t *testing.T) {
	for _, role := range []string{"viewer", "editor"} {
		for _, target := range []string{"/metadata/", "/metadata/other", "/metadata/introspect/", "/metadata/introspect/x"} {
			if next, rec := sendMetadata(t, role, http.MethodPost, target, storagePoliciesBody); next.reached {
				t.Errorf("%s POST %s: reached upstream with %d", role, target, rec.Code)
			}
		}
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			for _, target := range []string{"/metadata", "/metadata/introspect"} {
				if next, rec := sendMetadata(t, role, method, target, storagePoliciesBody); next.reached {
					t.Errorf("%s %s %s: reached upstream with %d", role, method, target, rec.Code)
				}
			}
		}
	}
}

// Viewers and admins see the catalogue as before, whatever they ask of it;
// roles that may neither write nor view the schema see none of it.
func TestOtherRolesMetadataAccessIsUnchanged(t *testing.T) {
	public := `{"name":"policies","params":{"schema":"public"}}`
	for _, role := range []string{"admin", "developer", "viewer"} {
		for _, body := range []string{storagePoliciesBody, storageRLSBody, public, `not json`} {
			if next, rec := sendMetadata(t, role, http.MethodPost, "/metadata", body); !next.reached {
				t.Errorf("%s %s: refused with %d", role, body, rec.Code)
			}
		}
	}
	for _, role := range []string{"billing", "owner", "nonsense"} {
		if next, rec := sendMetadata(t, role, http.MethodPost, "/metadata", storagePoliciesBody); next.reached {
			t.Errorf("%s: reached upstream with %d", role, rec.Code)
		}
	}
}
