package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
)

// The plugin widget manifest says which editors this project has, so it needs a
// Studio session, as the admin config does. The bundles it names do not: a module
// import cannot carry the token, and each bundle is served only by a hash the
// manifest names, from a file checked to hash to it.
func TestThePluginWidgetManifestNeedsAStudioSession(t *testing.T) {
	deps := depsFor(t, &config.Config{Mode: "dev", PluginWidgetsDir: t.TempDir(), JWTSecret: testJWTSecret}, nil)

	anonymous := httptest.NewRecorder()
	buildPluginWidgetManifest(deps).ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/studio/plugins", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("without a session: status = %d, want 401 (%s)", anonymous.Code, anonymous.Body.String())
	}

	signedIn := httptest.NewRecorder()
	buildPluginWidgetManifest(deps).ServeHTTP(signedIn, studioAdminRequest(t, "/studio/plugins"))
	if signedIn.Code != http.StatusOK || signedIn.Body.String() != "{\"widgets\":[]}\n" {
		t.Fatalf("with a session: status = %d, body %q", signedIn.Code, signedIn.Body.String())
	}
}

func TestAPluginWidgetBundleNeedsNoSessionButMustBeNamed(t *testing.T) {
	deps := depsFor(t, &config.Config{Mode: "dev", PluginWidgetsDir: t.TempDir(), JWTSecret: testJWTSecret}, nil)
	rec := httptest.NewRecorder()
	path := "/studio/plugins/widgets/" + "0000000000000000000000000000000000000000000000000000000000000000.js"
	buildPluginWidgetBundles(deps).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a hash no manifest names: status = %d, want 404", rec.Code)
	}
}
