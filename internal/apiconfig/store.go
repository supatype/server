package apiconfig

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// Store is the interface for reading and writing ApiConfig.
type Store interface {
	Get(ctx context.Context) (ApiConfig, error)
	Set(ctx context.Context, cfg ApiConfig) error
}

// ─── FileStore ────────────────────────────────────────────────────────────────

// FileStore persists ApiConfig as JSON in a local file.
// If the file does not exist, DefaultApiConfig() is returned.
type FileStore struct {
	path string
	mu   sync.RWMutex
}

// NewFileStore creates a FileStore that reads/writes path.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) Get(_ context.Context) (ApiConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultApiConfig(), nil
		}
		return DefaultApiConfig(), err
	}
	var cfg ApiConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultApiConfig(), err
	}
	return cfg, nil
}

// marshalConfig is a seam. ApiConfig is plain data, so marshalling it cannot
// fail and the error branch in Set is unreachable in production. The test
// replaces this to prove the branch reports rather than writing a truncated
// file.
var marshalConfig = func(cfg ApiConfig) ([]byte, error) {
	return json.MarshalIndent(cfg, "", "  ")
}

func (s *FileStore) Set(_ context.Context, cfg ApiConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := marshalConfig(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
