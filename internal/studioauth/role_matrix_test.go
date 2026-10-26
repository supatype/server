package studioauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"testing"
)

// roleMatrixSHA256 pins the shared role document. The control plane and the CLI
// carry the same file and pin the same hash, so a change to what a role may do
// has to be made in all three, or one of them fails.
const roleMatrixSHA256 = "fd20d03babf333ad04fa46f5cddda7c3faa391149239724ab2e5e9c667d56129"

type sharedRoleMatrix struct {
	Roles         []string            `json:"roles"`
	SelfHostRoles []string            `json:"selfHostRoles"`
	Permissions   map[string][]string `json:"permissions"`
}

func readSharedMatrix(t *testing.T) ([]byte, sharedRoleMatrix) {
	t.Helper()
	raw, err := os.ReadFile("role_matrix.json")
	if err != nil {
		t.Fatalf("the shared role document is missing: %v", err)
	}
	var m sharedRoleMatrix
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("role_matrix.json: %v", err)
	}
	return raw, m
}

func TestRoleMatrixIsTheSharedDocument(t *testing.T) {
	raw, _ := readSharedMatrix(t)
	sum := sha256.Sum256(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))
	if got := hex.EncodeToString(sum[:]); got != roleMatrixSHA256 {
		t.Fatalf("role_matrix.json changed (sha256 %s); change it in every host", got)
	}
}

// Self-host grants admin, developer, editor and viewer. Owner and billing are
// Cloud org roles: a membership row naming one grants nothing here.
func TestSelfHostRoleSet(t *testing.T) {
	want := []string{"admin", "developer", "editor", "viewer"}
	if got := KnownStudioRoles(); !slices.Equal(got, want) {
		t.Fatalf("KnownStudioRoles() = %v, want %v", got, want)
	}
	for _, role := range []string{"owner", "billing"} {
		if _, ok := PermissionsForRole(role); ok {
			t.Fatalf("role %q must not be recognised on self-host", role)
		}
	}
}

// Every flag a role holds is exactly its membership in the shared document.
func TestEveryPermissionFollowsTheMatrix(t *testing.T) {
	_, m := readSharedMatrix(t)
	for _, role := range KnownStudioRoles() {
		perms, ok := PermissionsForRole(role)
		if !ok {
			t.Fatalf("role %q missing", role)
		}
		encoded, _ := json.Marshal(perms)
		var flags map[string]bool
		_ = json.Unmarshal(encoded, &flags)
		if len(flags) != len(m.Permissions) {
			t.Fatalf("flags %v do not match the document's permissions", reflect.ValueOf(flags).MapKeys())
		}
		for name, holders := range m.Permissions {
			got, present := flags[name]
			if !present {
				t.Fatalf("permission %q has no flag", name)
			}
			if got != slices.Contains(holders, role) {
				t.Fatalf("role %q permission %q = %v, document says %v", role, name, got, !got)
			}
		}
	}
}

// A viewer reads content and schema and changes nothing, and always acts as
// themselves.
func TestViewerIsReadOnlyAndSelf(t *testing.T) {
	viewer, ok := PermissionsForRole("viewer")
	if !ok {
		t.Fatal("viewer must be a Studio role")
	}
	if !AllowsRequest(viewer, http.MethodGet, "/rest/v1/posts") {
		t.Fatal("viewer must read content rows")
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		if AllowsRequest(viewer, method, "/rest/v1/posts") {
			t.Fatalf("viewer must not %s content rows", method)
		}
	}
	for _, path := range []string{"/sql", "/auth/v1/admin/users", "/platform/v1/schema/push"} {
		if AllowsRequest(viewer, http.MethodPost, path) {
			t.Fatalf("viewer must not reach %s", path)
		}
	}
	if !AllowsRequest(viewer, http.MethodGet, "/admin/studio-members") {
		t.Fatal("viewer has schema view, so may read the membership list")
	}

	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	if mode, ok := resolveActingMode(req, viewer); !ok || mode != ModeSelf {
		t.Fatalf("viewer default mode = %q, %v; want self", mode, ok)
	}
	req.Header.Set(ActingModeHeader, ModeElevated)
	if _, ok := resolveActingMode(req, viewer); ok {
		t.Fatal("viewer must not elevate")
	}
}
