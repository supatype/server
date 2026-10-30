package pluginwidgets

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"aead.dev/minisign"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A release key pair made for the test; the release pipeline signs with the real one.
func releaseKey(t *testing.T) (minisign.PublicKey, minisign.PrivateKey) {
	t.Helper()
	public, private, err := minisign.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return public, private
}

// trustFor is the policy for a server holding public, as NewTrust builds it from the
// key's text: in production (no unsigned bundles) unless dev.
func trustFor(t *testing.T, public minisign.PublicKey, dev bool) Trust {
	t.Helper()
	text, err := public.MarshalText()
	require.NoError(t, err)
	trust, err := NewTrust(string(text), dev)
	require.NoError(t, err)
	return trust
}

// sign writes the bundle's signature beside it, prehashed as the minisign CLI the
// release pipeline uses signs by default.
func sign(t *testing.T, dir string, e Entry, private minisign.PrivateKey, body string) {
	t.Helper()
	reader := minisign.NewReader(bytes.NewReader([]byte(body)))
	_, err := io.Copy(io.Discard, reader)
	require.NoError(t, err)
	path := filepath.Join(dir, e.Bundle()+SignatureSuffix)
	require.NoError(t, os.WriteFile(path, reader.Sign(private), 0o600))
}

const widgetBody = "export default () => null\n"

func TestABundleSignedWithTheReleaseKeyIsServedInProduction(t *testing.T) {
	public, private := releaseKey(t)
	dir := t.TempDir()
	ours := writeWidget(t, dir, "@supatype/plugin-seo", widgetBody)
	sign(t, dir, ours, private, widgetBody)
	writeManifest(t, dir, ours)

	rec := get(BundleHandler(NewStore(dir, trustFor(t, public, false))), BundlePrefix+ours.Bundle())
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, widgetBody, rec.Body.String())
}

func TestABundleWhoseSignatureIsForOtherBytesIsNotServed(t *testing.T) {
	public, private := releaseKey(t)
	dir := t.TempDir()
	ours := writeWidget(t, dir, "@supatype/plugin-seo", widgetBody)
	// The file hashes to its name, but the signature is for what Supatype released.
	sign(t, dir, ours, private, "export default () => 'released'\n")
	writeManifest(t, dir, ours)

	for _, dev := range []bool{false, true} {
		rec := get(BundleHandler(NewStore(dir, trustFor(t, public, dev))), BundlePrefix+ours.Bundle())
		assert.Equal(t, http.StatusInternalServerError, rec.Code, "dev=%v", dev)
		assert.NotContains(t, rec.Body.String(), "export default", "dev=%v", dev)
	}
}

func TestABundleSignedWithAnotherKeyIsNotServed(t *testing.T) {
	public, _ := releaseKey(t)
	_, other := releaseKey(t)
	dir := t.TempDir()
	ours := writeWidget(t, dir, "@supatype/plugin-seo", widgetBody)
	sign(t, dir, ours, other, widgetBody)
	writeManifest(t, dir, ours)

	rec := get(BundleHandler(NewStore(dir, trustFor(t, public, true))), BundlePrefix+ours.Bundle())
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAnUnsignedBundleIsRefusedInProductionAndServedInDev(t *testing.T) {
	public, _ := releaseKey(t)
	dir := t.TempDir()
	ours := writeWidget(t, dir, "@supatype/plugin-seo", widgetBody)
	writeManifest(t, dir, ours)

	production := get(BundleHandler(NewStore(dir, trustFor(t, public, false))), BundlePrefix+ours.Bundle())
	assert.Equal(t, http.StatusInternalServerError, production.Code)
	dev := get(BundleHandler(NewStore(dir, trustFor(t, public, true))), BundlePrefix+ours.Bundle())
	assert.Equal(t, http.StatusOK, dev.Code)
}

func TestTheManifestListsOnlyBundlesThatMayRun(t *testing.T) {
	public, private := releaseKey(t)
	dir := t.TempDir()
	signed := writeWidget(t, dir, "@supatype/plugin-seo", widgetBody)
	sign(t, dir, signed, private, widgetBody)
	unsigned := writeWidget(t, dir, "@supatype/plugin-phone-field", "export default () => 1\n")
	writeManifest(t, dir, signed, unsigned)

	rec := get(ManifestHandler(NewStore(dir, trustFor(t, public, false))), "/studio/plugins")
	var body struct {
		Widgets []published `json:"widgets"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Widgets, 1)
	assert.Equal(t, BundlePrefix+signed.Bundle(), body.Widgets[0].URL)
}

func TestAKeyThatDoesNotParseIsRefused(t *testing.T) {
	_, err := NewTrust("not a minisign key", false)
	assert.Error(t, err)
}

// A release build embeds the key (-X ...embeddedReleaseKey); a configured key, when set,
// overrides it.
func TestTheEmbeddedKeyIsUsedWhenNoneIsConfigured(t *testing.T) {
	public, private := releaseKey(t)
	text, err := public.MarshalText()
	require.NoError(t, err)
	original := embeddedReleaseKey
	t.Cleanup(func() { embeddedReleaseKey = original })
	embeddedReleaseKey = string(text)

	dir := t.TempDir()
	ours := writeWidget(t, dir, "@supatype/plugin-seo", widgetBody)
	sign(t, dir, ours, private, widgetBody)
	writeManifest(t, dir, ours)
	trust, err := NewTrust("", false)
	require.NoError(t, err)
	rec := get(BundleHandler(NewStore(dir, trust)), BundlePrefix+ours.Bundle())
	assert.Equal(t, http.StatusOK, rec.Code)
}
