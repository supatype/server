package maskedfields

import "testing"

// Only a bare table path describes this table's columns. An RPC returns whatever its
// function returns, and a nested path is not a PostgREST table route — annotating either
// would be a claim about the wrong column set.
func TestTableFromPath(t *testing.T) {
	cases := map[string]string{
		"/posts":            "posts",
		"posts":             "posts",
		"/posts/":           "posts",
		"/rpc/do_thing":     "",
		"/posts/1":          "",
		"/":                 "",
		"":                  "",
		"/weird.table_name": "weird.table_name",
	}

	for path, want := range cases {
		if got := tableFromPath(path); got != want {
			t.Errorf("tableFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}
