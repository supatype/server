package serverconf

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// DefaultManifestRelPath is the default route manifest path when SUPATYPE_MANIFEST_PATH is unset.
const DefaultManifestRelPath = ".supatype/manifest.json"

// ProjectRootFromManifestPath returns the project directory that should hold `.env` files for a
// standard layout `…/PROJECT/.supatype/manifest.json`. For other layouts it returns the directory
// containing the manifest file.
func ProjectRootFromManifestPath(manifestRelOrAbs string) (string, error) {
	p := strings.TrimSpace(manifestRelOrAbs)
	if p == "" {
		p = DefaultManifestRelPath
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(abs)
	if filepath.Base(parent) == ".supatype" {
		return filepath.Dir(parent), nil
	}
	return parent, nil
}

// LoadDotEnvForServe loads `.env.local` then `.env` from several locations without overwriting keys
// already set in the process environment (shell / container env wins over files; godotenv never
// overwrites). Order: directory of --config (when -c points to a file), cwd, then the manifest-derived
// project root (after cwd, SUPATYPE_MANIFEST_PATH may be set from cwd’s `.env`). Each directory is
// loaded at most once. This never logs file contents.
func LoadDotEnvForServe(cwd, configFilePath string) error {
	loaded := make(map[string]struct{})
	try := func(dir string) error {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return nil
		}
		abs, err := filepath.Abs(filepath.Clean(dir))
		if err != nil {
			return err
		}
		if _, dup := loaded[abs]; dup {
			return nil
		}
		if err := LoadDotEnv(abs); err != nil {
			return err
		}
		loaded[abs] = struct{}{}
		return nil
	}

	if cf := strings.TrimSpace(configFilePath); cf != "" {
		if fi, err := os.Stat(cf); err == nil && !fi.IsDir() {
			if err := try(filepath.Dir(cf)); err != nil {
				return err
			}
		}
	}
	if err := try(cwd); err != nil {
		return err
	}
	proj, err := ProjectRootFromManifestPath(os.Getenv("SUPATYPE_MANIFEST_PATH"))
	if err != nil {
		return err
	}
	return try(proj)
}

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
