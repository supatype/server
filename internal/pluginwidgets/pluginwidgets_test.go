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
	return Entry{Widget: Widget{Plugin: plugin, Name: "phone", Kind: "field"}, SHA256: hash}
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

	rec := get(ManifestHandler(NewStore(dir)), "/studio/plugins")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Widgets []published `json:"widgets"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, []published{{Widget: ours.Widget, URL: BundlePrefix + ours.Bundle()}}, body.Widgets)
	assert.NotContains(t, rec.Body.String(), "sha256")
}

func TestNoManifestMeansNoWidgets(t *testing.T) {
	rec := get(ManifestHandler(NewStore(t.TempDir())), "/studio/plugins")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"widgets":[]}`, rec.Body.String())
}

func TestABundleTheManifestNamesIsServedAsAnImmutableModule(t *testing.T) {
	dir := t.TempDir()
	ours := writeWidget(t, dir, "@supatype/plugin-seo", "export default () => null\n")
	writeManifest(t, dir, ours)

	rec := get(BundleHandler(NewStore(dir)), BundlePrefix+ours.Bundle())
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
	assert.Equal(t, http.StatusNotFound, get(BundleHandler(NewStore(dir)), BundlePrefix+theirs.Bundle()).Code)
}

func TestAFileTheManifestDoesNotNameIsNotServed(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir)
	stray := writeWidget(t, dir, "@supatype/plugin-seo", "export default () => 2\n")
	h := BundleHandler(NewStore(dir))
	assert.Equal(t, http.StatusNotFound, get(h, BundlePrefix+stray.Bundle()).Code)
	assert.Equal(t, http.StatusNotFound, get(h, BundlePrefix+"..%2fmanifest.json").Code)
	assert.Equal(t, http.StatusNotFound, get(h, BundlePrefix+"manifest").Code)
}

func TestAChangedManifestIsReadAgain(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
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

func TestAManifestThatCannotBeReadIsAServerErrorForABundle(t *testing.T) {
	dir := t.TempDir()
	ours := writeWidget(t, dir, "@supatype/plugin-seo", "export default () => null\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{not json"), 0o600))
	rec := get(BundleHandler(NewStore(dir)), BundlePrefix+ours.Bundle())
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// A rewrite inside the filesystem's timestamp resolution keeps the mtime; the size
// still tells the two versions apart.
func TestARewrittenManifestWithTheSameMtimeIsReadAgainWhenItsSizeChanged(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	path := filepath.Join(dir, "manifest.json")
	writeManifest(t, dir)
	info, err := os.Stat(path)
	require.NoError(t, err)
	entries, err := store.Entries()
	require.NoError(t, err)
	assert.Empty(t, entries)

	writeManifest(t, dir, writeWidget(t, dir, "@supatype/plugin-seo", "export default () => null\n"))
	require.NoError(t, os.Chtimes(path, info.ModTime(), info.ModTime()))
	entries, err = store.Entries()
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// A browser revalidating a bundle it holds gets 304, and a Range request its slice.
func TestABundleAnswersConditionalAndRangeRequests(t *testing.T) {
	dir := t.TempDir()
	ours := writeWidget(t, dir, "@supatype/plugin-seo", "export default () => null\n")
	writeManifest(t, dir, ours)
	h := BundleHandler(NewStore(dir))

	revalidate := httptest.NewRequest(http.MethodGet, BundlePrefix+ours.Bundle(), nil)
	revalidate.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, revalidate)
	assert.Equal(t, http.StatusNotModified, rec.Code)

	slice := httptest.NewRequest(http.MethodGet, BundlePrefix+ours.Bundle(), nil)
	slice.Header.Set("Range", "bytes=0-5")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, slice)
	assert.Equal(t, http.StatusPartialContent, rec.Code)
	assert.Equal(t, "export", rec.Body.String())
	assert.Equal(t, "text/javascript; charset=utf-8", rec.Header().Get("Content-Type"))
}
