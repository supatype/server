package studioauth

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
)

// roleMatrixDocument is the shared role document: every role, and for each
// permission the roles that hold it. The same file ships in the control plane
// and the CLI.
//
//go:embed role_matrix.json
var roleMatrixDocument []byte

type roleMatrix struct {
	Roles         []string            `json:"roles"`
	SelfHostRoles []string            `json:"selfHostRoles"`
	Permissions   map[string][]string `json:"permissions"`
}

var selfHostRoles, studioRolePermissions = mustLoadRoleMatrix(roleMatrixDocument)

// mustLoadRoleMatrix builds the self-host role table from the document. A
// document that does not parse, or names a permission this server has no field
// for, stops the server at start: a role table that silently dropped a
// permission would grant less, or more, than the other hosts.
func mustLoadRoleMatrix(raw []byte) ([]string, map[string]StudioPermissions) {
	var m roleMatrix
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(fmt.Sprintf("studioauth: role_matrix.json: %v", err))
	}
	table := make(map[string]StudioPermissions, len(m.SelfHostRoles))
	for _, role := range m.SelfHostRoles {
		perms, err := permissionsFromMatrix(m, role)
		if err != nil {
			panic(fmt.Sprintf("studioauth: role_matrix.json: %v", err))
		}
		table[role] = perms
	}
	return m.SelfHostRoles, table
}

// permissionsFromMatrix sets each flag of one role from its membership in the
// document, by the flag's JSON name, and refuses any name with no flag.
func permissionsFromMatrix(m roleMatrix, role string) (StudioPermissions, error) {
	if !slices.Contains(m.Roles, role) {
		return StudioPermissions{}, fmt.Errorf("self-host role %q is not in the role set", role)
	}
	flags := make(map[string]bool, len(m.Permissions))
	for name, holders := range m.Permissions {
		flags[name] = slices.Contains(holders, role)
	}
	// A map from strings to bools has no value json cannot encode, so Marshal
	// has no failure to report here.
	encoded, _ := json.Marshal(flags)
	var perms StudioPermissions
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&perms); err != nil {
		return StudioPermissions{}, fmt.Errorf("permission without a flag: %w", err)
	}
	return perms, nil
}
