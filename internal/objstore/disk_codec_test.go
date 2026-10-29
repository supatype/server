package objstore

import (
	"strings"
	"testing"
)

// Under Windows rules every encoded segment is a legal Windows file name and
// decodes back to the name it came from. This runs on every OS.
func TestDiskNamesAreLegalOnWindowsAndDecodeBack(t *testing.T) {
	for _, name := range awkwardNames {
		for _, segment := range strings.Split(name, "/") {
			disk := windowsRules.diskSegment(segment)
			if strings.ContainsAny(disk, `<>:"|?*\/`) || strings.ContainsFunc(disk, func(r rune) bool { return r < 0x20 }) {
				t.Errorf("%q is stored as %q, which Windows forbids", segment, disk)
			}
			if strings.HasSuffix(disk, ".") || strings.HasSuffix(disk, " ") || reservedDeviceName(disk) {
				t.Errorf("%q is stored as %q, which Windows renames or reserves", segment, disk)
			}
			if got := windowsRules.objectSegment(disk); got != segment {
				t.Errorf("%q is stored as %q, which reads back as %q", segment, disk, got)
			}
		}
	}
}

// A stored name is decoded only when the decoded name would be stored under
// that same file name, so a listed name always leads back to the file it was
// listed from. A file placed by hand that merely looks encoded, or one written
// under the other OS's rules, keeps its own name.
func TestOnlyANameThatWouldBeStoredThereIsDecoded(t *testing.T) {
	for _, tc := range []struct {
		disk  string
		rules string
		want  string
	}{
		{"%%a%3Ab", "windows", "a:b"},
		{"%%a%3Ab", "posix", "%%a%3Ab"},
		{"%%x", "windows", "%%x"},
		{"%%x", "posix", "%%x"},
		{"%%100%25", "windows", "%%100%25"},
		{"%%100%25", "posix", "%%100%25"},
		{"%%%25%25x", "posix", "%%x"},
		{"%%nul%00", "posix", "nul\x00"},
		{"%%trailing dot%2E", "windows", "trailing dot."},
		{"%%trailing dot%2E", "posix", "%%trailing dot%2E"},
	} {
		if got := bothRules[tc.rules].objectSegment(tc.disk); got != tc.want {
			t.Errorf("objectSegment(%q) under %s rules = %q, want %q", tc.disk, tc.rules, got, tc.want)
		}
	}
}

// Every name stored under either OS's rules reads back as itself under the
// same rules.
func TestEveryStoredNameDecodesBackUnderItsOwnRules(t *testing.T) {
	for rulesName, rules := range bothRules {
		for _, name := range awkwardNames {
			for _, segment := range strings.Split(name, "/") {
				disk := rules.diskSegment(segment)
				if got := rules.objectSegment(disk); got != segment {
					t.Errorf("%s: %q is stored as %q, which reads back as %q", rulesName, segment, disk, got)
				}
			}
		}
	}
}
