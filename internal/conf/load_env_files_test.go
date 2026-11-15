package conf

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLoadEnvFilesReportsADirectoryFailureApartFromTheFile(t *testing.T) {
	missing := filepath.Join("testdata", "no-such-dir")
	err := LoadEnvFiles("", missing)
	var dirErr *DirectoryError
	if !errors.As(err, &dirErr) {
		t.Fatalf("error = %v, want a *DirectoryError", err)
	}
	if _, err := LoadGlobalWithDir("", missing); err == nil {
		t.Fatal("LoadGlobalWithDir ignored a directory failure")
	}
}

func TestLoadEnvFilesFileFailureIsNotADirectoryError(t *testing.T) {
	err := LoadEnvFiles(filepath.Join("testdata", "absent.env"), "")
	var dirErr *DirectoryError
	if err == nil || errors.As(err, &dirErr) {
		t.Fatalf("error = %v, want a plain file error", err)
	}
}
