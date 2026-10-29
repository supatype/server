package pluginwidgets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeWidget puts a bundle in dir and returns the manifest entry naming it.
func writeWidget(t *testing.T, dir, plugin, body string) Entry {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	require.NoError(t, os.WriteFile(filepath.Join(dir, hash+".js"), []byte(body), 0o600))
	return Entry{Plugin: plugin, Name: "phone", Kind: "field", SHA256: hash}
}

func writeManifest(t *testing.T, dir string, entries ...Entry) {
	t.Helper()
	raw, err := json.Marshal(manifest{Version: 1, Widgets: entries})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600))
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestManifestListsOnlyFirstPartyWidgetsWithTheirURLs(t *testing.T) {
	dir := t.TempDir()
	ours := writeWidget(t, dir, "@supatype/plugin-phone-field", "export default () => null\n")
	theirs := writeWidget(t, dir, "@acme/plugin-phone", "export default () => 1\n")
	writeManifest(t, dir, ours, theirs)

	rec := get(ManifestHandler(StoreFor(dir)), "/studio/plugins")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Widgets []published `json:"widgets"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, []published{{
		Plugin: "@supatype/plugin-phone-field", Name: "phone", Kind: "field", URL: BundlePrefix + ours.Bundle(),
	}}, body.Widgets)
}

func TestNoManifestMeansNoWidgets(t *testing.T) {
	rec := get(ManifestHandler(StoreFor(t.TempDir())), "/studio/plugins")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"widgets":[]}`, rec.Body.String())
}

func TestABundleTheManifestNamesIsServedAsAnImmutableModule(t *testing.T) {
	dir := t.TempDir()
	ours := writeWidget(t, dir, "@supatype/plugin-seo", "export default () => null\n")
	writeManifest(t, dir, ours)

	rec := get(BundleHandler(StoreFor(dir)), BundlePrefix+ours.Bundle())
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "export default () => null\n", rec.Body.String())
	assert.Equal(t, "text/javascript; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable")
}

func TestABundleFromAPluginNotOnTheListIsNotServed(t *testing.T) {
	dir := t.TempDir()
	theirs := writeWidget(t, dir, "@acme/plugin-phone", "export default () => 1\n")
	writeManifest(t, dir, theirs)
	assert.Equal(t, http.StatusNotFound, get(BundleHandler(StoreFor(dir)), BundlePrefix+theirs.Bundle()).Code)
}

func TestAFileTheManifestDoesNotNameIsNotServed(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir)
	stray := writeWidget(t, dir, "@supatype/plugin-seo", "export default () => 2\n")
	h := BundleHandler(StoreFor(dir))
	assert.Equal(t, http.StatusNotFound, get(h, BundlePrefix+stray.Bundle()).Code)
	assert.Equal(t, http.StatusNotFound, get(h, BundlePrefix+"..%2fmanifest.json").Code)
	assert.Equal(t, http.StatusNotFound, get(h, BundlePrefix+"manifest").Code)
}

func TestAChangedManifestIsReadAgain(t *testing.T) {
	dir := t.TempDir()
	store := StoreFor(dir)
	writeManifest(t, dir)
	entries, err := store.Entries()
	require.NoError(t, err)
	assert.Empty(t, entries)

	ours := writeWidget(t, dir, "@supatype/plugin-seo", "export default () => null\n")
	writeManifest(t, dir, ours)
	// A later mtime than the first write, whatever the filesystem's timestamp resolution.
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "manifest.json"), later, later))
	entries, err = store.Entries()
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestOnlySupatypesOwnPluginsAreOnTheList(t *testing.T) {
	assert.True(t, FirstParty("@supatype/plugin-seo"))
	assert.True(t, FirstParty("@supatype/plugin-phone-field"))
	// A standalone widget is not attached to fields, so the colour picker ships none Studio loads.
	assert.False(t, FirstParty("@supatype/plugin-color-picker"))
	assert.False(t, FirstParty("@acme/plugin-seo"))
}
