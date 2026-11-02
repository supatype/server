package objstore

import (
	"os"
	"path/filepath"
)

// The store resolves a name once per request, through locate, and works from
// the location it returns. These name-based helpers resolve it again on each
// call, which is what a test wants and a request does not.

// objectFileRel is where the object objPath in bucket is stored now.
func (s *store) objectFileRel(bucket, objPath string) (string, error) {
	loc, err := s.locate(bucket, objPath)
	return loc.fileRel, err
}

// objectMetaRel is where the sidecar of objPath in bucket is stored now.
func (s *store) objectMetaRel(bucket, objPath string) (string, error) {
	loc, err := s.locate(bucket, objPath)
	return loc.metaRel, err
}

// loadObjectMeta reads the sidecar of objPath in bucket from root.
func (s *store) loadObjectMeta(root *os.Root, bucket, objPath string) (*ObjectMeta, error) {
	metaRel, err := s.objectMetaRel(bucket, objPath)
	if err != nil {
		return nil, err
	}
	return loadMetaAt(root, metaRel)
}

// saveObjectMeta writes the sidecar of objPath in bucket.
func (s *store) saveObjectMeta(meta *ObjectMeta, bucket, objPath string) error {
	metaRel, err := s.objectMetaRel(bucket, objPath)
	if err != nil {
		return err
	}
	return s.saveNewMeta(meta, metaRel)
}

// sameFileAt reports whether a and b, relative to root, are both there and are
// the same file or directory: whether the volume made a short name a for b.
func sameFileAt(root, a, b string) bool {
	aInfo, err := os.Stat(filepath.Join(root, a))
	if err != nil {
		return false
	}
	bInfo, err := os.Stat(filepath.Join(root, b))
	return err == nil && os.SameFile(aInfo, bInfo)
}

// objectPaths is where the object objPath in bucket and its sidecar are
// stored now.
func (s *store) objectPaths(bucket, objPath string) (fileRel, metaRel string, err error) {
	loc, err := s.locate(bucket, objPath)
	return loc.fileRel, loc.metaRel, err
}
