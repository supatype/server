// Command coveragegate enforces the per-package coverage ratchet.
//
// It reads a Go coverage profile and a floors file, and fails when any package
// covers less than its recorded floor. Packages on the floors file's seam list
// are held at 100%.
//
// Run it after `go test -coverprofile`:
//
//	go run ./tools/coveragegate -profile coverage.out -floors hack/coverage-floors.json
//
// To reseed the floors from a measured run, which is a deliberate act that
// belongs in its own reviewed commit:
//
//	go run ./tools/coveragegate -profile coverage.out -floors hack/coverage-floors.json -update
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	profilePath := flag.String("profile", "coverage.out", "coverage profile to read")
	floorsPath := flag.String("floors", "hack/coverage-floors.json", "floors file to enforce")
	modulePath := flag.String("module", "github.com/supatype/server", "module path to strip from file paths")
	update := flag.Bool("update", false, "rewrite the floors file from this run instead of enforcing it")
	margin := flag.Float64("margin", 0.5, "percentage points to drop below measured coverage when seeding")
	flag.Parse()

	if err := run(*profilePath, *floorsPath, *modulePath, *update, *margin); err != nil {
		fmt.Fprintf(os.Stderr, "coveragegate: %v\n", err)
		os.Exit(1)
	}
}

func run(profilePath, floorsPath, modulePath string, update bool, margin float64) error {
	tallies, err := talliesFromFile(profilePath, modulePath)
	if err != nil {
		return err
	}
	floors, err := floorsFromFile(floorsPath)
	if err != nil {
		return err
	}
	if update {
		return writeFloors(floorsPath, Seed(tallies, floors, margin))
	}
	return report(Check(tallies, floors), len(tallies))
}

// report prints every violation and returns an error when there is at least
// one, so the exit status is the gate.
func report(violations []Violation, packages int) error {
	if len(violations) == 0 {
		fmt.Printf("coveragegate: %d packages, all at or above their floor\n", packages)
		return nil
	}
	fmt.Fprintf(os.Stderr, "coveragegate: %d package(s) below floor:\n\n", len(violations))
	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "  %s\n", v)
	}
	fmt.Fprintf(os.Stderr, "\nAdd tests, or argue for a new floor in hack/coverage-floors.json.\n")
	return fmt.Errorf("coverage ratchet failed")
}

func talliesFromFile(path, modulePath string) (map[string]Tally, error) {
	f, err := os.Open(path) // #nosec G304 -- path is a CI-supplied flag, not user input
	if err != nil {
		return nil, fmt.Errorf("open profile: %w", err)
	}
	defer f.Close() // #nosec G104 -- read-only file
	blocks, err := ParseProfile(f)
	if err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	return Tallies(blocks, modulePath), nil
}

func floorsFromFile(path string) (Floors, error) {
	f, err := os.Open(path) // #nosec G304 -- path is a CI-supplied flag, not user input
	if err != nil {
		return Floors{}, fmt.Errorf("open floors: %w", err)
	}
	defer f.Close() // #nosec G104 -- read-only file
	floors, err := LoadFloors(f)
	if err != nil {
		return Floors{}, fmt.Errorf("parse floors: %w", err)
	}
	return floors, nil
}

func writeFloors(path string, floors Floors) error {
	body, err := json.MarshalIndent(floors, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Printf("coveragegate: wrote %d floors to %s\n", len(floors.Packages), path)
	return nil
}
