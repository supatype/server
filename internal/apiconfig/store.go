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

func (s *FileStore) Set(_ context.Context, cfg ApiConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
