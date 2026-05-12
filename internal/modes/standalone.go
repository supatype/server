package modes

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// NewACMEManager returns an autocert.Manager configured for domain.
// Certificates are cached in cacheDir (created if absent).
// Use Manager.TLSConfig() as the http.Server.TLSConfig.
// Use Manager.HTTPHandler(nil) on SUPATYPE_ACME_HTTP_ADDR (default :80) for the
// HTTP-01 ACME challenge.
func NewACMEManager(domain, cacheDir string) (*autocert.Manager, error) {
	// Expand ~ in cache dir.
	if len(cacheDir) > 1 && cacheDir[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		cacheDir = filepath.Join(home, cacheDir[2:])
	}

	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, err
	}

	m := &autocert.Manager{
		Cache:       autocert.DirCache(cacheDir),
		Prompt:      autocert.AcceptTOS,
		HostPolicy:  autocert.HostWhitelist(domain),
		RenewBefore: 30 * 24 * time.Hour, // renew ~30 days before expiry (LE default window)
	}
	return m, nil
}

// StandaloneTLSConfig returns a *tls.Config suitable for use with
// http.Server.TLSConfig in standalone mode.
func StandaloneTLSConfig(m *autocert.Manager) *tls.Config {
	cfg := m.TLSConfig()
	cfg.MinVersion = tls.VersionTLS12
	return cfg
}
