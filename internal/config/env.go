package config

import (
	"os"
	"strings"
)

// Env is a source of environment variables.
//
// It exists so the preflight can be tested against a constructed environment
// rather than the process's own, and so a caller can be explicit about what it
// is checking.
type Env interface {
	// Names lists the variable names present.
	Names() []string
}

// OSEnv is the process environment.
type OSEnv struct{}

// Names lists the variables set on the process.
func (OSEnv) Names() []string {
	entries := os.Environ()
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if name, _, ok := strings.Cut(entry, "="); ok {
			names = append(names, name)
		}
	}
	return names
}

// MapEnv is a fixed set of names, for tests.
type MapEnv map[string]string

// Names lists the variables in the map.
func (m MapEnv) Names() []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}
