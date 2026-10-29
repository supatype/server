// Package pluginwidgets serves the editor widgets of a project's plugins to Studio.
//
// A widget is code that runs in the browser of everyone signed in to Studio, so which
// plugins may ship one is decided in code: Supatype's own, and no others for now. The
// CLI copies each widget into `.supatype/plugin-widgets/` under its SHA-256 and writes
// a manifest naming them; this package reads that manifest, keeps only the entries
// whose plugin is on the list, and serves those bundles, each checked to hash to its
// name, and nothing else.
//
// The plugin an entry names is the manifest's word for it. The list stops the CLI
// shipping another plugin's widget; it does not stop someone who can write the
// project's `.supatype` directory from naming a first-party plugin over other code.
package pluginwidgets

// firstParty is every plugin whose widget Studio may load. The CLI keeps a copy
// (`FIRST_PARTY_WIDGET_PLUGINS` in @supatype/plugin-sdk) to refuse early with a
// message; this one is checked again against every manifest entry the server reads.
var firstParty = map[string]bool{
	"@supatype/plugin-phone-field": true,
	"@supatype/plugin-seo":         true,
}

// FirstParty reports whether a plugin's widgets may load in Studio.
func FirstParty(plugin string) bool {
	return firstParty[plugin]
}
