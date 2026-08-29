package objstore

import (
	"encoding/json"
	"errors"
	"io"
	"os"
)

// Seams for the filesystem operations whose failures this package has to
// handle and cannot provoke the same way on every platform.
//
// Reading a directory as if it were a file, writing into a path that turns out
// to be a file, and removing a directory something else still holds open all
// behave differently on Windows and on Linux. A test that only fails on one of
// them is worse than no test, so the failures are stated here instead.
//
// Everything below is the real thing unless a test says otherwise.
var (
	openRoot  = os.OpenRoot
	mkdirAll  = os.MkdirAll
	readDir   = os.ReadDir
	removeAll = os.RemoveAll
	readFile  = os.ReadFile
	writeFile = os.WriteFile
	readAll   = io.ReadAll

	// statFile describes an already-open file. It does not fail on a handle
	// this package just opened, which is exactly why the branch that handles it
	// needs stating rather than provoking.
	statFile = func(f *os.File) (os.FileInfo, error) { return f.Stat() }
)

// marshalIndent is the seam for encoding what this package stores.
//
// A []Bucket and an ObjectMeta are both plain data, so neither can fail to
// encode and the error branches below are unreachable in production. They are
// kept because writing a truncated buckets.json would lose every bucket, and
// the test replaces this to prove they report instead.
var marshalIndent = func(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// writeTo streams src into rel under root, creating or truncating it.
//
// A copy that fails and a close that fails are the same thing to the caller —
// the object is not stored — so they are reported together rather than as two
// branches saying the same thing in different words. The close still has to
// happen on the failing path, or a failed upload leaks the handle.
func writeTo(root *os.Root, rel string, src io.Reader) (int64, error) {
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	size, copyErr := io.Copy(f, src)
	return size, errors.Join(copyErr, f.Close())
}
