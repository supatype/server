package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/supatype/server/internal/apiconfig"
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

// The listener reads everything per batch from the manifest the bootstrap holds, and sends hooks
// to the same worker the request path does.
func TestTheListenerConfigReadsTheLiveManifest(t *testing.T) {
	cfg := &config.Config{FunctionsWorkerURL: "http://worker:8001", JWTSecret: "s"}
	m := &proxy.RouteManifest{
		Schema: "app",
		Hooks:  map[string]proxy.TableHooks{"posts": {"afterChange": {Function: "index-post"}}},
		Tables: map[string]proxy.TableMeta{"posts": {PrimaryKey: []string{"id"}}},
	}
	lc := listenerConfig(cfg, "postgres://x/y", func() *proxy.RouteManifest { return m }, false)
	if lc.Schema() != "app" || lc.Hooks()["posts"]["afterChange"].Function != "index-post" {
		t.Fatalf("schema %q hooks %+v", lc.Schema(), lc.Hooks())
	}
	if shape, ok := lc.Shapes()("posts"); !ok || shape.PrimaryKey[0] != "id" {
		t.Fatalf("shape %+v %v", shape, ok)
	}
	url, err := lc.Resolve("index-post")
	if err != nil || url != "http://worker:8001/"+modelhooks.HooksRoutePrefix+"index-post" {
		t.Fatalf("hook URL = %q, %v", url, err)
	}
}

// The request path reads written rows back from the tenant's own PostgREST, as the service role.
func TestTheRequestPathReadsBackFromTheManifestsPostgREST(t *testing.T) {
	var auth string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[{"id":1}]`))
	}))
	defer rest.Close()
	d := &Deps{
		Config:      &config.Config{ServiceRoleKey: "service"},
		ManifestFor: func(*http.Request) *proxy.RouteManifest { return &proxy.RouteManifest{PostgRESTURL: rest.URL} },
		APIStore:    apiconfig.NewFileStore(filepath.Join(t.TempDir(), "api-config.json")),
	}
	req := httptest.NewRequest(http.MethodPost, "/rest/v1/posts", nil)
	rows, err := newAfterRows(d).Reader.ByKeys(context.Background(), req, "posts", []string{"id"}, []map[string]string{{"id": "1"}})
	if err != nil || len(rows) != 1 || auth != "Bearer service" {
		t.Fatalf("rows %v err %v auth %q", rows, err, auth)
	}
}
