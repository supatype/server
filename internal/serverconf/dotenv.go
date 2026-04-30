package serverconf

import (
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// LoadDotEnv loads environment variables from the .env file in dir (if it exists).
// Existing environment variables are NOT overwritten — .env only fills gaps.
// Call this before Load() so that SUPATYPE_* vars from .env are visible.
func LoadDotEnv(dir string) error {
	path := filepath.Join(dir, ".env")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // no .env file is fine
	}
	return godotenv.Load(path)
}
