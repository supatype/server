package modes

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func TestNewACMEManagerCreatesCacheAndManager(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cache := filepath.Join(dir, "acme-cache")
	m, err := NewACMEManager("example.com", cache)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("nil manager")
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache dir: %v", err)
	}
	cfg := StandaloneTLSConfig(m)
	if cfg == nil || cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("tls config: %#v", cfg)
	}
}
