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
			if strings.HasSuffix(disk, ".") || strings.HasSuffix(disk, " ") || windowsReserved(disk) {
				t.Errorf("%q is stored as %q, which Windows renames or reserves", segment, disk)
			}
			if got := objectSegment(disk); got != segment {
				t.Errorf("%q is stored as %q, which reads back as %q", segment, disk, got)
			}
		}
	}
}
