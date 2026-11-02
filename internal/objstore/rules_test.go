package objstore

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// posixRules store every name as written except one holding NUL or starting
// with the marker.
var posixRules = nameRules{}

// windowsRules apply Windows' rules on any OS, so the encoding can be checked
// under them everywhere. Device names are judged by reservedDeviceName.
var windowsRules = nameRules{windows: true, reserved: reservedDeviceName, foldsCase: true}

// bothRules is each OS's rules, by name.
var bothRules = map[string]nameRules{"posix": posixRules, "windows": windowsRules}

// reservedDeviceName reports whether Windows takes name for a device, counting
// every reserved base name, with or without an extension. Over-matching only
// encodes a name that did not need it, which is harmless.
func reservedDeviceName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	for _, device := range []string{"COM", "LPT"} {
		if n, ok := strings.CutPrefix(base, device); ok && utf8.RuneCountInString(n) == 1 && strings.Contains("0123456789¹²³", n) {
			return true
		}
	}
	return false
}

// legacyUnder is where objPath in the bucket "files" and its sidecar were
// stored before names were encoded, when r may look there.
func legacyUnder(r nameRules, objPath string) (fileRel, metaRel string, ok bool) {
	rel, ok := r.legacyRel(objPath)
	if !ok {
		return "", "", false
	}
	fileRel, metaRel = joinPaths("files", rel)
	return fileRel, metaRel, true
}

// The rules of this OS are Windows rules exactly when it is Windows.
func TestHostRulesAreThisOSRules(t *testing.T) {
	if hostRules.windows != onWindows {
		t.Fatalf("hostRules.windows = %t on a host where onWindows = %t", hostRules.windows, onWindows)
	}
	if onWindows && !hostRules.reserved("CON") {
		t.Error("Windows does not reserve CON")
	}
}

// An old location that is where some object is stored now is never looked
// at, under either OS's rules: it is that object's file.
func TestALegacyLocationIsNeverAnotherObjectsCurrentOne(t *testing.T) {
	for name, cases := range map[string][]string{
		"posix":   {"%%%25%25foo", "a/%%%25%25foo", "plain.txt"},
		"windows": {"%%%25%25foo", "%%CON", "%%con", `a\b`, `photos\a.txt`, "%%%25%2a", "%%%25%25FOO"},
	} {
		for _, objPath := range cases {
			if fileRel, _, ok := legacyUnder(bothRules[name], objPath); ok {
				t.Errorf("%s: %q falls back to %q", name, objPath, fileRel)
			}
		}
	}
}

// The sidecar directory is named ".meta". Where the file system ignores
// case, every casing of it opens that directory; elsewhere ".Meta" is a
// folder of the caller's objects like any other.
func TestTheSidecarDirNameFollowsTheFileSystemsCase(t *testing.T) {
	for _, tc := range []struct {
		rules string
		rel   string
		want  bool
	}{
		{"posix", ".meta/a.json", true},
		{"posix", ".Meta/report.pdf", false},
		{"posix", ".META", false},
		{"windows", ".meta/a.json", true},
		{"windows", ".Meta/report.pdf", true},
		{"windows", "a/../.META", true},
	} {
		if got := bothRules[tc.rules].inSidecarTree(tc.rel); got != tc.want {
			t.Errorf("%s: inSidecarTree(%q) = %t, want %t", tc.rules, tc.rel, got, tc.want)
		}
	}
}
