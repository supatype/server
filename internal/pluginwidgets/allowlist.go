// Package pluginwidgets serves the editor widgets of a project's plugins to Studio.
//
// A widget is code that runs in the browser of everyone signed in to Studio, so only
// Supatype's own may run there, and that is proved by signature. The CLI copies each
// widget into `.supatype/plugin-widgets/` under its SHA-256, with the minisign
// signature the release shipped beside it, and writes a manifest naming them. This
// package reads that manifest, keeps the entries whose plugin is on the first-party
// list, and serves a bundle only when it hashes to its name and its signature
// verifies against Supatype's release key (trust.go).
//
// The list is the manifest's word for which plugin an entry belongs to, and filters
// out other plugins' widgets early; the signature is what stops someone who can write
// the project's `.supatype` directory from running other code under a first-party name.
package pluginwidgets

// firstParty is every plugin whose widget Studio may load. The CLI keeps a copy
// (`FIRST_PARTY_WIDGET_PLUGINS` in @supatype/plugin-sdk) to refuse early with a
// message; this one is checked again against every manifest entry the server reads.
// It names plugins, not code: a bundle still has to carry Supatype's signature.
var firstParty = map[string]bool{
	"@supatype/plugin-phone-field": true,
	"@supatype/plugin-seo":         true,
}

// FirstParty reports whether a plugin's widgets may load in Studio.
func FirstParty(plugin string) bool {
	return firstParty[plugin]
}
