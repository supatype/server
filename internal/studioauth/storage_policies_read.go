package studioauth

import (
	"encoding/json"
	"net/http"
)

// Studio's Storage > Policies view lists the storage schema's access rules
// with two catalogue queries. An editor, who may change files but holds no
// schema view, may see those rules read-only, as a viewer can (owner decision
// 2026-10-23). AllowsRequest cannot see a body, so the admit step reads it
// here, bounded, and restores it for the catalogue. Every other catalogue
// query stays with schema view.

// storagePolicyQueries are the catalogue queries the Policies view sends.
var storagePolicyQueries = map[string]bool{"policies": true, "rls_status": true}

// storageSchema is the one schema an editor may ask them about.
const storageSchema = "storage"

// allowsStoragePolicyRead reports whether one request is a caller who may
// write content reading the storage schema's policies or row level security
// status: a POST to the catalogue whose body is exactly
// {"name": "policies" or "rls_status", "params": {"schema": "storage"}}.
func allowsStoragePolicyRead(req *http.Request, perms StudioPermissions, path string) bool {
	if !perms.Write || req.Method != http.MethodPost || path != "/metadata" {
		return false
	}
	body, err := readBodyRestoring(req)
	return err == nil && isStoragePolicyQuery(body)
}

// isStoragePolicyQuery matches keys exactly and refuses a repeated key or any
// other field. The catalogue decodes its body with encoding/json, which
// matches field names case-insensitively and keeps the last copy of a key, so
// any looser shape could name another query or schema there than here.
func isStoragePolicyQuery(body []byte) bool {
	fields, ok := strictObject(body)
	if !ok || len(fields) != 2 {
		return false
	}
	var name string
	if json.Unmarshal(fields["name"], &name) != nil || !storagePolicyQueries[name] {
		return false
	}
	params, ok := strictObject(fields["params"])
	if !ok || len(params) != 1 {
		return false
	}
	var schema string
	return json.Unmarshal(params["schema"], &schema) == nil && schema == storageSchema
}
