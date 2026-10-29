// Package filestamp tells versions of a file apart without reading it, for the
// readers that reload a file the CLI rewrites: the route manifest and the plugin
// widget manifest.
package filestamp

import (
	"os"
	"time"
)

// Stamp identifies a version of a file by its modification time and size. The
// zero value stands for a file that did not exist.
//
// Size is compared as well as the time because a rewrite inside the filesystem's
// timestamp resolution keeps the time.
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

// Same reports whether two stamps name the same version of the file.
func (s Stamp) Same(other Stamp) bool {
	// ModTime is compared with Equal rather than ==, because == on time.Time
	// also compares the monotonic reading and the location pointer.
	return s.size == other.size && s.mod.Equal(other.mod)
}
