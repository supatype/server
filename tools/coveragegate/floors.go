package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// epsilon absorbs float division noise. A package whose statements are all
// covered yields exactly 100.0, so this only has to cover representation, not
// run-to-run variance.
const epsilon = 1e-9

// pinnedFloor is what a package on the pinned list must reach. It is not
// configurable: lowering it means removing the package from the pinned list,
// which is a visible line in a diff someone has to argue for.
const pinnedFloor = 100.0

// Floors is the coverage ratchet: a per-package minimum that CI refuses to let
// slip.
//
// Pinned holds the packages held at 100%: the Supatype-owned seam as it reaches
// that mark, plus anything already there that must not slip back. SeamTarget
// records the seam packages still on their way, so the goal survives in the file
// rather than only in a plan; it is documentation and is not enforced. Packages
// holds measured floors for everything else, including the forked auth handlers,
// where the rule is "no worse than today" rather than a number anybody chose.
type Floors struct {
	Comment    string             `json:"_comment,omitempty"`
	Default    float64            `json:"default"`
	Exclude    []string           `json:"exclude"`
	Pinned     []string           `json:"pinned"`
	SeamTarget []string           `json:"seam_target"`
	Packages   map[string]float64 `json:"packages"`
}

// LoadFloors decodes a floors file, refusing unknown fields so a typo in a key
// cannot silently disable the gate it was meant to configure.
func LoadFloors(r io.Reader) (Floors, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var f Floors
	if err := dec.Decode(&f); err != nil {
		return Floors{}, err
	}
	if f.Packages == nil {
		f.Packages = make(map[string]float64)
	}
	return f, nil
}

// Excluded reports whether a package is outside the gate: the entrypoint, the
// generated docs, the vendored client, and this tool itself.
func (f Floors) Excluded(pkg string) bool {
	for _, ex := range f.Exclude {
		if pkg == ex || strings.HasPrefix(pkg, ex+"/") {
			return true
		}
	}
	return false
}

// isPinned reports whether the package is held at 100%.
func (f Floors) isPinned(pkg string) bool {
	for _, s := range f.Pinned {
		if pkg == s {
			return true
		}
	}
	return false
}

// FloorFor returns the minimum coverage for a package and whether that minimum
// comes from the pinned list. A package with no recorded floor gets Default,
// which is 100: a new package arrives fully tested or not at all.
func (f Floors) FloorFor(pkg string) (float64, bool) {
	if f.isPinned(pkg) {
		return pinnedFloor, true
	}
	if got, ok := f.Packages[pkg]; ok {
		return got, false
	}
	return f.Default, false
}

// Violation is one package that failed the gate.
type Violation struct {
	Pkg    string
	Got    float64
	Floor  float64
	Pinned bool
	Stale  bool
}

func (v Violation) String() string {
	if v.Stale {
		return fmt.Sprintf("%-46s has a floor but produced no coverage data (deleted or renamed?)", v.Pkg)
	}
	origin := "floor"
	if v.Pinned {
		origin = "pinned"
	}
	return fmt.Sprintf("%-46s %6.2f%% < %6.2f%% (%s)", v.Pkg, v.Got, v.Floor, origin)
}

// Check compares measured coverage against the floors, and also reports floors
// with no matching package so the file cannot quietly accumulate entries for
// code that no longer exists.
func Check(tallies map[string]Tally, f Floors) []Violation {
	var out []Violation
	for pkg, tally := range tallies {
		if f.Excluded(pkg) {
			continue
		}
		floor, pinned := f.FloorFor(pkg)
		if got := tally.Percent(); got < floor-epsilon {
			out = append(out, Violation{Pkg: pkg, Got: got, Floor: floor, Pinned: pinned})
		}
	}
	out = append(out, staleFloors(tallies, f)...)
	sort.Slice(out, func(i, j int) bool { return out[i].Pkg < out[j].Pkg })
	return out
}

// staleFloors finds recorded packages that no longer appear in the profile.
func staleFloors(tallies map[string]Tally, f Floors) []Violation {
	var out []Violation
	for _, pkg := range append(append([]string{}, f.Pinned...), keys(f.Packages)...) {
		if _, ok := tallies[pkg]; !ok && !f.Excluded(pkg) {
			out = append(out, Violation{Pkg: pkg, Stale: true})
		}
	}
	return out
}

// Seed rebuilds the Packages map from measured coverage, dropping margin
// percentage points so ordinary run-to-run noise does not fail CI. Pinned
// packages are left out: their floor is fixed at 100 and comes from the list.
func Seed(tallies map[string]Tally, f Floors, margin float64) Floors {
	seeded := f
	seeded.Packages = make(map[string]float64, len(tallies))
	for pkg, tally := range tallies {
		if f.Excluded(pkg) || f.isPinned(pkg) {
			continue
		}
		floor := tally.Percent() - margin
		if floor < 0 {
			floor = 0
		}
		seeded.Packages[pkg] = float64(int(floor*100+0.5)) / 100
	}
	return seeded
}

func keys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
