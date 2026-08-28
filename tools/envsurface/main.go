// Command envsurface records every environment variable supatype-server reads.
//
// It is a behaviour lock for the coherence refactor. Today the surface comes
// from two independent envconfig structs plus a scatter of direct os.Getenv
// calls; the refactor collapses that into one config package, and this file is
// how we tell "the surface changed on purpose" from "a variable stopped being
// read and nobody noticed".
//
//	go run ./tools/envsurface            # print the surface
//	go run ./tools/envsurface -update    # rewrite hack/env-surface.txt
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/config"
)

// goldenPath is the checked-in surface, relative to the repository root.
const goldenPath = "hack/env-surface.txt"

func main() {
	update := flag.Bool("update", false, "rewrite the golden file instead of printing")
	root := flag.String("root", ".", "repository root to scan for os.Getenv calls")
	golden := flag.String("golden", goldenPath, "path to the golden surface file")
	flag.Parse()

	surface, err := Collect(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "envsurface: %v\n", err)
		os.Exit(1)
	}
	if !*update {
		fmt.Print(surface)
		return
	}
	if err := os.WriteFile(*golden, []byte(surface), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "envsurface: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("envsurface: wrote %s\n", *golden)
}

// Collect gathers the whole surface: both config structs and every direct read.
//
// The GoTrue prefix is passed explicitly rather than read from a constant
// because it is the thing being removed, and this tool has to keep describing
// the surface accurately while that happens.
func Collect(root string) (string, error) {
	var vars []Var

	global, err := StructNames("gotrue", &conf.GlobalConfiguration{}, "internal/conf.GlobalConfiguration")
	if err != nil {
		return "", fmt.Errorf("gather auth config: %w", err)
	}
	vars = append(vars, global...)

	server, err := StructNames("", &config.Config{}, "internal/config.Config")
	if err != nil {
		return "", fmt.Errorf("gather server config: %w", err)
	}
	vars = append(vars, server...)

	calls, problems := CallNames(root)
	if len(problems) > 0 {
		return "", errors.Join(problems...)
	}
	vars = append(vars, calls...)

	return Render(vars), nil
}
