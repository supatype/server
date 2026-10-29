// Package pluginwidgets serves the editor widgets of a project's plugins to Studio.
//
// A widget is code that runs in the browser of everyone signed in to Studio, so this
// package decides which plugins may ship one, in code: Supatype's own, and no others
// for now. The CLI copies each widget into `.supatype/plugin-widgets/` under its
// SHA-256 and writes a manifest naming them; this package reads that manifest, keeps
// only the entries on the list, and serves those bundles and nothing else.
package pluginwidgets

// firstParty is every plugin whose widget Studio may load. The CLI keeps a copy
// (`FIRST_PARTY_WIDGET_PLUGINS` in @supatype/plugin-sdk) to refuse early with a
// message; this one is the one that is enforced.
var firstParty = map[string]bool{
	"@supatype/plugin-phone-field": true,
	"@supatype/plugin-seo":         true,
}

// FirstParty reports whether a plugin's widgets may load in Studio.
func FirstParty(plugin string) bool {
	return firstParty[plugin]
}
