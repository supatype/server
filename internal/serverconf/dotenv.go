package serverconf

import (
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// LoadDotEnv loads `.env.local` then `.env` in dir when present (each via godotenv.Load: never overwrites
// already-set process environment). Loading `.env.local` first lets keys there win over `.env` for
// values not already exported in the shell.
// Call this before Load() so that SUPATYPE_* vars from files are visible.
func LoadDotEnv(dir string) error {
	localPath := filepath.Join(dir, ".env.local")
	basePath := filepath.Join(dir, ".env")
	var err error
	if _, statErr := os.Stat(localPath); statErr == nil {
		if err = godotenv.Load(localPath); err != nil {
			return err
		}
	}
	if _, statErr := os.Stat(basePath); statErr == nil {
		if err = godotenv.Load(basePath); err != nil {
			return err
		}
	}
	return nil
}
