// Package filestamp identifies a version of a file without reading it, so a
// reader can tell whether a file it already parsed has changed since.
package filestamp

import (
	"os"
	"time"
)

// Stamp identifies a version of a file by its modification time and size. The
// zero value stands for a file that did not exist.
type Stamp struct {
	mod  time.Time
	size int64
}

// Of is the stamp of the file at path as it is now.
func Of(path string) (Stamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Stamp{}, err
	}
	return Stamp{mod: info.ModTime(), size: info.Size()}, nil
}

// Same reports whether s and other stamp the same version. ModTime is compared
// with Equal rather than ==, because == on time.Time also compares the
// monotonic reading and the location pointer.
func (s Stamp) Same(other Stamp) bool {
	return s.size == other.size && s.mod.Equal(other.mod)
}
