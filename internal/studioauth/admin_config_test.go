package studioauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/supatype/server/internal/studiobootstrap"
)

func TestResolveAdminConfigPrefersTheStoredCopy(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "admin-config.json"), []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		stored  StoredAdminConfig
		want    string
		wantErr error
	}{
		"stored copy wins over a stale file": {
			func(context.Context) ([]byte, error) { return []byte(`{"from":"db"}`), nil }, `{"from":"db"}`, nil},
		"empty stored copy falls back to the file": {
			func(context.Context) ([]byte, error) { return nil, nil }, `{"from":"file"}`, nil},
		"never pushed falls back to the file": {
			func(context.Context) ([]byte, error) { return nil, studiobootstrap.ErrNoSchemaState }, `{"from":"file"}`, nil},
		"database down still serves the file": {
			func(context.Context) ([]byte, error) { return nil, errors.New("down") }, `{"from":"file"}`, nil},
		"no loader serves the file": {nil, `{"from":"file"}`, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ResolveAdminConfig(context.Background(), tc.stored, "admin-config.json")
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestResolveAdminConfigWithNothingStoredAnywhere(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := ResolveAdminConfig(context.Background(), func(context.Context) ([]byte, error) { return nil, nil }, "admin-config.json")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not exist", err)
	}
	_, err = ResolveAdminConfig(context.Background(), func(context.Context) ([]byte, error) { return nil, errors.New("down") }, "admin-config.json")
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want the database error", err)
	}
}

func TestStoredAdminConfigFromReturnsTheSnapshotsConfigOrItsError(t *testing.T) {
	ctx := context.Background()
	body, err := StoredAdminConfigFrom(func(context.Context) (*studiobootstrap.Snapshot, error) {
		return &studiobootstrap.Snapshot{AdminConfig: []byte(`{"from":"db"}`)}, nil
	})(ctx)
	if err != nil || string(body) != `{"from":"db"}` {
		t.Fatalf("got %q, %v", body, err)
	}

	boom := errors.New("down")
	body, err = StoredAdminConfigFrom(func(context.Context) (*studiobootstrap.Snapshot, error) {
		return nil, boom
	})(ctx)
	if !errors.Is(err, boom) || body != nil {
		t.Fatalf("got %q, %v; want the loader's error and no body", body, err)
	}
}
