package restcache

import (
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/supatype/server/internal/data/keyspace"
)

// 0.9.2 release test round 2 (R2-S13): with no keyspace connection, every cached request logged
// "restcache: keyspace GET failed" and "... SET failed", both saying "keyspace: not configured".
// The server said once, at start, that the caches bypass; a request says it in its BYPASS header.
func TestAKeyspaceWithNoConnectionIsNotLoggedPerRequest(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(hook.Reset)

	for range 3 {
		rec := serve(deps(keyspace.Unavailable(), staticStore{cfg: cachingConfig(false)}), &upstream{}, request("/posts", "max-age=30"))
		if got := rec.Header().Get(statusHeader); got != "BYPASS" {
			t.Fatalf("status = %q, want BYPASS", got)
		}
	}
	for _, entry := range hook.AllEntries() {
		if entry.Level <= logrus.WarnLevel && strings.HasPrefix(entry.Message, "restcache: keyspace") {
			t.Errorf("logged per request: %s (%v)", entry.Message, entry.Data[logrus.ErrorKey])
		}
	}
}
