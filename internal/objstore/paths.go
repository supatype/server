package objstore

import (
	"fmt"
	"path/filepath"
	"strings"
)

func cleanBucketID(bucket string) (string, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" || strings.ContainsAny(bucket, `/\`) || !filepath.IsLocal(bucket) {
		return "", fmt.Errorf("invalid bucket id")
	}
	return bucket, nil
}

func cleanObjectPath(objPath string, allowEmpty bool) (string, error) {
	objPath = strings.TrimPrefix(objPath, "/")
	if objPath == "" {
		if allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("invalid object path")
	}

	rel := filepath.FromSlash(objPath)
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("invalid object path")
	}
	return rel, nil
}

func (s *store) bucketDir(bucket string) (string, error) {
	bucket, err := cleanBucketID(bucket)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, bucket), nil
}

// objectPaths returns where an object and its sidecar live, relative to the
// storage root, validating the bucket and path once.
//
// The two used to be built by separate functions that each validated the same
// two strings. Any caller wanting both therefore validated twice and had to
// handle a second failure that could not happen — if the first call refused the
// input, the second never ran.
func (s *store) objectPaths(bucket, objPath string) (fileRel, metaRel string, err error) {
	bucket, err = cleanBucketID(bucket)
	if err != nil {
		return "", "", err
	}
	rel, err := cleanObjectPath(objPath, false)
	if err != nil {
		return "", "", err
	}
	dir, file := filepath.Split(rel)
	return filepath.Join(bucket, rel), filepath.Join(bucket, ".meta", dir, file+".json"), nil
}

func (s *store) objectFileRel(bucket, objPath string) (string, error) {
	fileRel, _, err := s.objectPaths(bucket, objPath)
	return fileRel, err
}

func (s *store) objectMetaRel(bucket, objPath string) (string, error) {
	_, metaRel, err := s.objectPaths(bucket, objPath)
	return metaRel, err
}
