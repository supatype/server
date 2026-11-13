package keyspace

import "testing"

// Each layout names the same key where its login will find it.
func TestKeyLayoutNamesEachKeyWhereItsLoginFindsIt(t *testing.T) {
	for _, tc := range []struct {
		layout         KeyLayout
		tenant, shared string
	}{
		{LayoutLegacy, "tenant:abc:config", "mau:org:o:d:2026-01-02"},
		{LayoutScoped, "config", "mau:org:o:d:2026-01-02"},
		{LayoutPrefixed, "abc:config", "abc:mau:org:o:d:2026-01-02"},
	} {
		if got := tc.layout.Tenant("abc", KeyConfig); got != tc.tenant {
			t.Errorf("%q Tenant = %q, want %q", tc.layout, got, tc.tenant)
		}
		if got := tc.layout.Shared("abc", "mau:org:o:d:2026-01-02"); got != tc.shared {
			t.Errorf("%q Shared = %q, want %q", tc.layout, got, tc.shared)
		}
	}
}

// The wrapped password keeps its generation in the name.
func TestKeyDBCredSecretNamesTheGeneration(t *testing.T) {
	if got := LayoutLegacy.Tenant("p", KeyDBCredSecret(2)); got != "tenant:p:dbcred:secret:v2" {
		t.Errorf("got %q", got)
	}
}

// An unknown layout is refused rather than guessed.
func TestParseKeyLayout(t *testing.T) {
	for in, want := range map[string]KeyLayout{"": LayoutLegacy, "legacy": LayoutLegacy, " Scoped ": LayoutScoped, "prefixed": LayoutPrefixed} {
		got, err := ParseKeyLayout(in)
		if err != nil || got != want {
			t.Errorf("ParseKeyLayout(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseKeyLayout("tenant"); err == nil {
		t.Error("want an error for an unknown layout")
	}
}
