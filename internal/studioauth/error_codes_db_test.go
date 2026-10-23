package studioauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
)

// Every refusal from the Studio APIs says what happened in `error`, a stable code
// a client can branch on, and keeps the sentence for a person in `message`. The
// code used to be "forbidden" whatever happened, so a project with no schema
// pushed, or a database that could not be read, looked like an access refusal.
//
// The database cases skip without SUPATYPE_TEST_DSN; run the package with -p 1.

type errorCodeCase struct {
	name   string
	call   func(t *testing.T) *httptest.ResponseRecorder
	status int
	code   string
}

func TestEachFailureCarriesItsOwnCode(t *testing.T) {
	cases := append(membersErrorCases(), bootstrapErrorCases()...)
	cases = append(cases, gatewayFreeErrorCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.call(t)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}
			var body struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("not the error shape: %q", rec.Body.String())
			}
			if body.Error != tc.code {
				t.Errorf("error = %q, want %q (%s)", body.Error, tc.code, rec.Body.String())
			}
			if body.Message == "" {
				t.Errorf("no message for a person: %s", rec.Body.String())
			}
		})
	}
}

func membersErrorCases() []errorCodeCase {
	path := "/admin/studio-members/"
	return []errorCodeCase{
		{"members, no token", func(t *testing.T) *httptest.ResponseRecorder {
			return membersCall(t, membersConfig(t, "admin"), http.MethodGet, "/admin/studio-members", "", "")
		}, http.StatusUnauthorized, "unauthorized"},
		{"members, a role that may not change membership", func(t *testing.T) *httptest.ResponseRecorder {
			return membersCall(t, membersConfig(t, "developer"), http.MethodDelete, path+memberSecond, asAdmin(t), "")
		}, http.StatusForbidden, "forbidden"},
		{"members, a body that is not JSON", func(t *testing.T) *httptest.ResponseRecorder {
			return membersCall(t, membersConfig(t, "admin"), http.MethodPatch, path+memberPlain, asAdmin(t), "{")
		}, http.StatusBadRequest, "validation_error"},
		{"members, a user who does not exist", func(t *testing.T) *httptest.ResponseRecorder {
			return membersCall(t, membersConfig(t, "admin"), http.MethodPatch,
				path+"dddddddd-dddd-dddd-dddd-dddddddddddd", asAdmin(t), `{"role":"editor"}`)
		}, http.StatusNotFound, "not_found"},
		{"members, the last admin", demoteTheLastAdmin, http.StatusConflict, "conflict"},
		{"members, membership that cannot be read", listWithNoMembershipTable, http.StatusServiceUnavailable, "unavailable"},
		{"members, a method the API does not have", func(t *testing.T) *httptest.ResponseRecorder {
			return membersCall(t, membersConfig(t, "admin"), http.MethodPost, "/admin/studio-members", asAdmin(t), "")
		}, http.StatusMethodNotAllowed, "method_not_allowed"},
	}
}

// demoteTheLastAdmin leaves one admin and asks somebody else to demote them.
func demoteTheLastAdmin(t *testing.T) *httptest.ResponseRecorder {
	c := membersConfig(t, "admin")
	if rec := membersCall(t, c, http.MethodDelete, "/admin/studio-members/"+memberSecond, asAdmin(t), ""); rec.Code != http.StatusOK {
		t.Fatalf("revoking the second admin: %d %s", rec.Code, rec.Body.String())
	}
	asSecond := bearer(t, jwt.MapClaims{"sub": memberSecond, "role": "authenticated"})
	return membersCall(t, c, http.MethodPatch, "/admin/studio-members/"+memberAdmin, asSecond, `{"role":"editor"}`)
}

// listWithNoMembershipTable lists membership after its table has gone.
func listWithNoMembershipTable(t *testing.T) *httptest.ResponseRecorder {
	c := membersConfig(t, "developer")
	dropTable(t, c, "_supatype.studio_members")
	return membersCall(t, c, http.MethodGet, "/admin/studio-members", asAdmin(t), "")
}

func bootstrapErrorCases() []errorCodeCase {
	return []errorCodeCase{
		{"schema, not a member", func(t *testing.T) *httptest.ResponseRecorder {
			return getSchema(t, bootstrapConfig(t, schemaWithAMaskedColumn, ""))
		}, http.StatusForbidden, "forbidden"},
		{"schema, nothing pushed yet", func(t *testing.T) *httptest.ResponseRecorder {
			return getSchema(t, bootstrapConfig(t, "", "admin"))
		}, http.StatusNotFound, "not_found"},
		{"schema, schema state that cannot be read", func(t *testing.T) *httptest.ResponseRecorder {
			c := bootstrapConfig(t, schemaWithAMaskedColumn, "admin")
			dropTable(t, c, "_supatype.schema_state")
			return getSchema(t, c)
		}, http.StatusServiceUnavailable, "unavailable"},
		{"schema, an AST that will not parse", func(t *testing.T) *httptest.ResponseRecorder {
			return getSchema(t, bootstrapConfig(t, `{"models":"not a list"}`, "admin"))
		}, http.StatusInternalServerError, "internal_error"},
	}
}

func getSchema(t *testing.T, c Config) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	SchemaHandler(c)(rec, studioRequest(http.MethodGet, "/studio/schema", signedIn(t)))
	return rec
}

func dropTable(t *testing.T, c Config, table string) {
	t.Helper()
	pool, err := c.Resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "DROP TABLE "+table); err != nil {
		t.Fatal(err)
	}
}

// gatewayFreeErrorCases need no database.
func gatewayFreeErrorCases() []errorCodeCase {
	return []errorCodeCase{
		{"verify, no token", func(t *testing.T) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			VerifyHandler(membershipConfig("admin"))(rec, studioRequest(http.MethodGet, "/studio/auth/verify", ""))
			return rec
		}, http.StatusUnauthorized, "unauthorized"},
		{"verify, a method it does not take", func(t *testing.T) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			VerifyHandler(membershipConfig("admin"))(rec, studioRequest(http.MethodPost, "/studio/auth/verify", ""))
			return rec
		}, http.StatusMethodNotAllowed, "method_not_allowed"},
		{"preview, a project with no versioned models", func(t *testing.T) *httptest.ResponseRecorder {
			c := membershipConfig("admin")
			c.AdminConfigPath = filepath.Join(t.TempDir(), "admin-config.json")
			rec := httptest.NewRecorder()
			PreviewAPI(c).ServeHTTP(rec, studioRequest(http.MethodPost, "/admin/preview-links", ""))
			return rec
		}, http.StatusNotImplemented, "not_implemented"},
	}
}
