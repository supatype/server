package storage

import "testing"

func TestEnsurePostgresSearchPathInURL(t *testing.T) {
	const in = "postgres://u:p@h:5432/db?sslmode=disable"
	out, err := EnsurePostgresSearchPathInURL(in, "auth")
	if err != nil {
		t.Fatal(err)
	}
	if out == in || out == "" {
		t.Fatalf("expected search_path added, got %q", out)
	}
	if _, err := EnsurePostgresSearchPathInURL("mysql://x", "auth"); err != nil {
		t.Fatal(err)
	}
}
