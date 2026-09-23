package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace"
	"github.com/supatype/server/internal/platform/mau"
	"github.com/supatype/server/internal/restcache"
)

// Every key this service writes, and the head that decides how long it lives.
//
// pg_keyspace resolves durability by literal longest-prefix match, so the head
// of a key is not a naming convention: it is the tier. A deployment says
// `durability_overrides = 'tenant:=durable'` and everything under tenant:
// survives a restart, at the cost of a WAL write per SET. That rule is only
// correct while nothing ephemeral is filed under the same head, and a cached
// REST GET filed there would put the busiest write in the service through the
// WAL to keep a copy of something that expires in seconds.
//
// The families cannot be enumerated from the keyspace itself — it holds
// whatever was last written — so they are enumerated here, from the functions
// that build them. A new family fails this test until someone has said which
// tier it belongs to.
func TestEveryKeyFamilyIsFiledUnderItsDurabilityHead(t *testing.T) {
	when, err := time.Parse("2006-01-02", "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		family string
		key    string
		head   string
		// why the tier is what it is, in the words someone reviewing a change
		// to this table would need.
		tier string
	}{
		{"tenant config", keyspace.TenantConfigKey("proj-1"), "tenant:", "durable: written by the control plane, and a pod that loses it serves the file defaults to every tenant"},
		{"route manifest", keyspace.RouteManifestKey("proj-1"), "tenant:", "durable: same writer, same consequence"},
		{"db credential meta", metaKey("proj-1"), "tenant:", "durable: losing it strands a rotation half-done"},
		{"db credential secret", secretKey("proj-1", 2), "tenant:", "durable: it is the only copy of a managed password"},
		{"REST response cache", restcache.RestKeyPrefix("proj-1"), "cache:", "ephemeral: it is a cache, and it is the write this service does most"},
		{"MAU day set", mau.DayKey("org-1", when), "mau:", "its own head: the tier is a cost decision, not a correctness one"},
	} {
		t.Run(tc.family, func(t *testing.T) {
			if !strings.HasPrefix(tc.key, tc.head) {
				t.Errorf("%s builds %q, which is not under %q (%s)", tc.family, tc.key, tc.head, tc.tier)
			}
		})
	}
}

// The reason the response cache moved out from under tenant:, stated on its
// own so a future change to the cache key cannot satisfy the table above by
// moving the whole family somewhere else.
func TestTheResponseCacheIsNotUnderTheDurableHead(t *testing.T) {
	// That the keys themselves start with this prefix is asserted where the
	// builder lives, in restcache; what is checked here is the prefix the admin
	// API scans and purges with, which is the other half of the same fact.
	scanned := tenantCachePrefix(&config.Config{ManagedProjectRef: "proj-1"}, httptest.NewRequest(http.MethodGet, "/cache", nil))
	for _, key := range []string{restcache.RestKeyPrefix("proj-1"), scanned} {
		if strings.HasPrefix(key, "tenant:") {
			t.Errorf("%q is under tenant:, where a durability rule would make every cached GET a WAL write", key)
		}
		if !strings.HasPrefix(key, "cache:rest:") {
			t.Errorf("%q is not under cache:rest:, which is the prefix the durability rules are written against", key)
		}
	}
}
