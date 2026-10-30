package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/pluginwidgets"
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

// An unsigned (source-built) widget runs only on a server whose mode was set to dev,
// as native and Docker dev set it. Never in standalone, and never when the mode was
// left to its dev default, which is what a production server started without one has.
func TestAnUnsignedWidgetIsServedOnlyWhenTheModeIsExplicitlyDev(t *testing.T) {
	for name, tc := range map[string]struct {
		mode     string
		explicit bool
		status   int
	}{
		"dev, set":        {"dev", true, http.StatusOK},
		"dev, by default": {"dev", false, http.StatusInternalServerError},
		"standalone, set": {"standalone", true, http.StatusInternalServerError},
		"managed, set":    {"managed", true, http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			hash := writeUnsignedWidget(t, dir)
			cfg := &config.Config{Mode: tc.mode, ModeSetExplicitly: tc.explicit, PluginWidgetsDir: dir, JWTSecret: testJWTSecret}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, pluginwidgets.BundlePrefix+hash+".js", nil)
			buildPluginWidgetBundles(depsFor(t, cfg, nil)).ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}

// writeUnsignedWidget writes a first-party bundle with no signature and a manifest
// naming it, and returns its hash.
func writeUnsignedWidget(t *testing.T, dir string) string {
	t.Helper()
	body := []byte("export default () => null\n")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	manifest := `{"version":1,"widgets":[{"plugin":"@supatype/plugin-seo","name":"seo","kind":"composite","sha256":"` + hash + `"}]}`
	if err := os.WriteFile(filepath.Join(dir, hash+".js"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return hash
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
