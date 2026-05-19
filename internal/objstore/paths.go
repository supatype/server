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

func (s *store) objectFileRel(bucket, objPath string) (string, error) {
	bucket, err := cleanBucketID(bucket)
	if err != nil {
		return "", err
	}
	objPath, err = cleanObjectPath(objPath, false)
	if err != nil {
		return "", err
	}
	return filepath.Join(bucket, objPath), nil
}

func (s *store) objectFilePath(bucket, objPath string) (string, error) {
	rel, err := s.objectFileRel(bucket, objPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, rel), nil
}

func (s *store) objectMetaRel(bucket, objPath string) (string, error) {
	bucket, err := cleanBucketID(bucket)
	if err != nil {
		return "", err
	}
	objPath, err = cleanObjectPath(objPath, false)
	if err != nil {
		return "", err
	}
	dir, file := filepath.Split(objPath)
	return filepath.Join(bucket, ".meta", dir, file+".json"), nil
}

func (s *store) objectMetaPath(bucket, objPath string) (string, error) {
	rel, err := s.objectMetaRel(bucket, objPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, rel), nil
}
