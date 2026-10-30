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
			disk := diskSegment(segment, true)
			if strings.ContainsAny(disk, `<>:"|?*\/`) || strings.ContainsFunc(disk, func(r rune) bool { return r < 0x20 }) {
				t.Errorf("%q is stored as %q, which Windows forbids", segment, disk)
			}
			if strings.HasSuffix(disk, ".") || strings.HasSuffix(disk, " ") || windowsReserved(disk, true) {
				t.Errorf("%q is stored as %q, which Windows renames or reserves", segment, disk)
			}
			if got := objectSegment(disk, true); got != segment {
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
		disk    string
		windows bool
		want    string
	}{
		{"%%a%3Ab", true, "a:b"},
		{"%%a%3Ab", false, "%%a%3Ab"},
		{"%%x", true, "%%x"},
		{"%%x", false, "%%x"},
		{"%%100%25", true, "%%100%25"},
		{"%%100%25", false, "%%100%25"},
		{"%%%25%25x", false, "%%x"},
		{"%%nul%00", false, "nul\x00"},
		{"%%trailing dot%2E", true, "trailing dot."},
		{"%%trailing dot%2E", false, "%%trailing dot%2E"},
	} {
		if got := objectSegment(tc.disk, tc.windows); got != tc.want {
			t.Errorf("objectSegment(%q, windows=%t) = %q, want %q", tc.disk, tc.windows, got, tc.want)
		}
	}
}

// Every name stored under either OS's rules reads back as itself under the
// same rules.
func TestEveryStoredNameDecodesBackUnderItsOwnRules(t *testing.T) {
	for _, windows := range []bool{false, true} {
		for _, name := range awkwardNames {
			for _, segment := range strings.Split(name, "/") {
				disk := diskSegment(segment, windows)
				if got := objectSegment(disk, windows); got != segment {
					t.Errorf("windows=%t: %q is stored as %q, which reads back as %q", windows, segment, disk, got)
				}
			}
		}
	}
}
