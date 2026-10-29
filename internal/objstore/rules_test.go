package objstore

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// posixRules store every name as written except one holding NUL or starting
// with the marker.
var posixRules = nameRules{}

// windowsRules apply Windows' rules on any OS, so the encoding can be checked
// under them everywhere. Device names are judged by reservedDeviceName.
var windowsRules = nameRules{windows: true, reserved: reservedDeviceName}

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

// legacyUnder is where objPath in the bucket "files" was stored before names
// were encoded, under r, judged against where r stores it now.
func legacyUnder(r nameRules, objPath string) (fileRel, metaRel string, ok bool) {
	current := filepath.FromSlash(mapSegments(objPath, r.diskSegment))
	return r.legacyPaths("files", current, objPath)
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
