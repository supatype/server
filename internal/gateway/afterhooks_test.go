package gateway

import (
	"testing"

	"github.com/supatype/server/internal/changelog"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/modelhooks"
	"github.com/supatype/server/internal/proxy"
)

func TestTheAfterHookListenerRunsOnlyForASingleProjectWithADatabase(t *testing.T) {
	manifest := func() *proxy.RouteManifest { return nil }
	withDSN := &config.Config{SQLDatabaseURL: "postgres://x/y"}

	if newAfterHookListener(&config.Config{}, manifest, false, false) != nil {
		t.Fatal("no DSN, nothing to read")
	}
	if newAfterHookListener(withDSN, manifest, false, true) != nil {
		t.Fatal("one listener cannot follow every tenant's database")
	}
	l := newAfterHookListener(withDSN, manifest, false, false)
	if l == nil {
		t.Fatal("a single project with a database should get a listener")
	}
	if st := l.Status(); st.Path != changelog.PathUnknown || st.Slot != changelog.SlotName {
		t.Fatalf("a listener that has not run reports unknown on its own slot, got %+v", st)
	}
}

func TestTheRequestPathKeepsAfterHooksWithoutAListener(t *testing.T) {
	d := &Deps{Config: &config.Config{}}
	if newAfterRows(d).ChangeLogActive() {
		t.Fatal("no listener means the request path still fires after hooks")
	}
}

func TestAnEmptyManifestIsNeverNil(t *testing.T) {
	m := orEmptyManifest(func() *proxy.RouteManifest { return nil })()
	if m == nil || m.Schema != "public" {
		t.Fatalf("got %+v", m)
	}
}

func TestTheRequestPathSignsMarksWithTheSecretTheListenerChecks(t *testing.T) {
	d := &Deps{Config: &config.Config{JWTSecret: "shared"}}
	token := newAfterRows(d).Marks.Mint("posts")
	if table, ok := modelhooks.NewRequestPathMarks("shared").Verify(token); !ok || table != "posts" {
		t.Fatal("a mark minted by the middleware must verify with the JWT secret the listener uses")
	}
}
