package pluginwidgets

import (
	"strings"

	"aead.dev/minisign"
)

// embeddedReleaseKey is the minisign public key Supatype signs its releases with, set
// when a release is built:
//
//	-ldflags "-X github.com/supatype/server/internal/pluginwidgets.embeddedReleaseKey=<key>"
//
// It is the key the CLI verifies its CDN downloads with. Empty in a source build.
var embeddedReleaseKey string

// Trust decides which bundles may run in Studio: one whose minisign signature
// verifies against Supatype's release key, and, on a development server only, one
// with no signature, as a plugin built from source has.
type Trust struct {
	key           *minisign.PublicKey
	allowUnsigned bool
}

// NewTrust is the policy for this server. configuredKey overrides the embedded key
// when set; a key that does not parse is an error, so no bundle is taken as signed by
// a key nobody meant.
func NewTrust(configuredKey string, allowUnsigned bool) (Trust, error) {
	text := strings.TrimSpace(configuredKey)
	if text == "" {
		text = strings.TrimSpace(embeddedReleaseKey)
	}
	trust := Trust{allowUnsigned: allowUnsigned}
	if text == "" {
		return trust, nil
	}
	var key minisign.PublicKey
	if err := key.UnmarshalText([]byte(text)); err != nil {
		return trust, err
	}
	trust.key = &key
	return trust, nil
}

// refusal is why a bundle may not run, or "" when it may. A signature is checked
// whenever there is one and a key to check it with, in every mode: a bad signature
// is a changed bundle, which a development server refuses too.
func (t Trust) refusal(body, signature []byte, signed bool) string {
	if signed && t.key != nil {
		if minisign.Verify(*t.key, body, signature) {
			return ""
		}
		return "its signature does not verify against Supatype's release key"
	}
	if t.allowUnsigned {
		return ""
	}
	if signed {
		return "this server has no release key to check its signature with (set SUPATYPE_RELEASE_PUBLIC_KEY)"
	}
	return "it is not signed, and only a development server (SUPATYPE_MODE=dev) serves an unsigned widget"
}
