package keyspace

import (
	"encoding/json"
	"testing"

	"github.com/supatype/server/internal/proxy"
)

// Cloud has no manifest file, so the `tables` map arrives through the tenant config like hooks and
// the cache declaration. Without it a managed tenant's after hooks cannot strip restricted columns
// and previous() has no primary key.

func TestTenantConfigCarriesTheTablesMap(t *testing.T) {
	var tc TenantConfig
	raw := `{"tables":{"posts":{"columns":{"afterChange":"all"},"restrictedColumns":["secret"],"primaryKey":["id"]}}}`
	if err := json.Unmarshal([]byte(raw), &tc); err != nil {
		t.Fatal(err)
	}
	base := &proxy.RouteManifest{Schema: "public"}
	tc.mergeRoutingInto(base)

	meta, ok := base.Table("posts")
	if !ok || meta.Columns["afterChange"] != "all" || meta.RestrictedColumns[0] != "secret" || meta.PrimaryKey[0] != "id" {
		t.Fatalf("tables did not reach the manifest: %+v", base.Tables)
	}
}

func TestTheTablesMapIsReplacedWholesale(t *testing.T) {
	base := &proxy.RouteManifest{Tables: map[string]proxy.TableMeta{"drafts": {PrimaryKey: []string{"id"}}}}
	(&TenantConfig{Tables: map[string]proxy.TableMeta{"posts": {PrimaryKey: []string{"id"}}}}).mergeRoutingInto(base)
	if _, stale := base.Tables["drafts"]; stale {
		t.Fatalf("a table the push removed kept its entry: %+v", base.Tables)
	}
}

func TestTenantConfigWithoutTablesLeavesTheFileManifestAlone(t *testing.T) {
	base := &proxy.RouteManifest{Tables: map[string]proxy.TableMeta{"posts": {PrimaryKey: []string{"id"}}}}
	(&TenantConfig{Schema: "public"}).mergeRoutingInto(base)
	if _, ok := base.Tables["posts"]; !ok {
		t.Fatal("an unrelated tenant config dropped the tables map")
	}
}

// Field validators reach a managed pod the same way: cloud has no manifest file, so without this
// a schema's validators never ran on Cloud.
func TestTenantConfigCarriesValidators(t *testing.T) {
	var tc TenantConfig
	raw := `{"validators":{"posts":{"title":{"function":"check-title","timeout":1500}}}}`
	if err := json.Unmarshal([]byte(raw), &tc); err != nil {
		t.Fatal(err)
	}
	base := &proxy.RouteManifest{Schema: "public"}
	tc.mergeRoutingInto(base)
	got := base.Validators["posts"]["title"]
	if got.Function != "check-title" || got.TimeoutMs != 1500 {
		t.Fatalf("validators did not reach the manifest: %+v", base.Validators)
	}
}

func TestValidatorsAreReplacedWholesale(t *testing.T) {
	base := &proxy.RouteManifest{Validators: map[string]proxy.TableValidators{"drafts": {"body": {Function: "v"}}}}
	(&TenantConfig{Validators: map[string]proxy.TableValidators{"posts": {"title": {Function: "v"}}}}).mergeRoutingInto(base)
	if _, stale := base.Validators["drafts"]; stale {
		t.Fatalf("a validator the push removed is still applied: %+v", base.Validators)
	}
}

func TestTenantConfigWithoutValidatorsLeavesTheFileManifestAlone(t *testing.T) {
	base := &proxy.RouteManifest{Validators: map[string]proxy.TableValidators{"posts": {"title": {Function: "v"}}}}
	(&TenantConfig{Schema: "public"}).mergeRoutingInto(base)
	if _, ok := base.Validators["posts"]; !ok {
		t.Fatal("an unrelated tenant config dropped the validators")
	}
}
