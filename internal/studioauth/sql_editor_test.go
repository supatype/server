package studioauth

import (
	"net/http"
	"testing"
)

// RX1: the SQL editor keeps full access and is open to owners, admins and
// developers. The runner connects as the project's owning role, so row level
// security does not apply to it: a developer can read and change every row
// through it, as the owner chose. It always acts as the service role, whatever
// mode the caller's data requests act in, and says so.

const sqlEditorPath = "/studio/proxy/sql"

func TestSQLEditorActsAsTheServiceRoleForDevelopers(t *testing.T) {
	for _, role := range []string{"admin", "developer"} {
		_, call := proxyCapturing(t, role)
		got := call(http.MethodPost, sqlEditorPath, nil)
		if got.code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", role, got.code)
		}
		if auth := got.header.Get("Authorization"); auth != "Bearer service-role-key" {
			t.Fatalf("%s: the runner must get the service role, got %q", role, auth)
		}
		if mode := got.response.Get(ActingModeHeader); mode != ModeElevated {
			t.Fatalf("%s: mode = %q, want elevated", role, mode)
		}
	}
}

func TestSQLEditorIsClosedToEditorsAndViewers(t *testing.T) {
	for _, role := range []string{"editor", "viewer"} {
		_, call := proxyCapturing(t, role)
		if got := call(http.MethodPost, sqlEditorPath, nil); got.code != http.StatusForbidden {
			t.Fatalf("%s: expected 403, got %d", role, got.code)
		}
	}
}

// The mode switch governs data requests. The SQL editor is elevated by
// definition, so an admin acting as themselves still reaches it, elevated.
func TestSQLEditorIgnoresTheActingMode(t *testing.T) {
	_, call := proxyCapturing(t, "admin")
	got := call(http.MethodPost, sqlEditorPath, http.Header{ActingModeHeader: {ModeSelf}})
	if got.code != http.StatusOK || got.header.Get("Authorization") != "Bearer service-role-key" {
		t.Fatalf("expected the elevated runner, got %d %q", got.code, got.header.Get("Authorization"))
	}
}
