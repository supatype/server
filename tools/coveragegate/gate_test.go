package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mod = "github.com/supatype/server"

func TestParseProfile_skipsModeLineAndBlanks(t *testing.T) {
	in := "mode: set\n\n" + mod + "/internal/proxy/proxy.go:1.1,2.2 3 1\n"
	blocks, err := ParseProfile(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(blocks))
	}
	for block, hit := range blocks {
		if block.Stmts != 3 || !hit {
			t.Fatalf("got %+v hit=%v", block, hit)
		}
	}
}

// A block reached by one test binary and missed by another is covered.
//
// Both orderings are asserted on purpose: with the covered entry last,
// last-write-wins produces the right answer by luck, so a hit-last-only test
// cannot tell an OR from an assignment.
func TestParseProfile_duplicateBlocksAreORed(t *testing.T) {
	orderings := map[string]string{
		"hit last":  mod + "/a/x.go:1.1,2.2 1 0\n" + mod + "/a/x.go:1.1,2.2 1 1\n",
		"hit first": mod + "/a/x.go:1.1,2.2 1 1\n" + mod + "/a/x.go:1.1,2.2 1 0\n",
	}
	for name, dupes := range orderings {
		blocks, err := ParseProfile(strings.NewReader("mode: set\n" + dupes + mod + "/a/x.go:3.1,4.2 1 0\n"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(blocks) != 2 {
			t.Fatalf("%s: want 2 distinct blocks, got %d", name, len(blocks))
		}
		if !blocks[Block{File: mod + "/a/x.go", StartPos: "1.1", EndPos: "2.2", Stmts: 1}] {
			t.Errorf("%s: duplicated block should be covered", name)
		}
		if blocks[Block{File: mod + "/a/x.go", StartPos: "3.1", EndPos: "4.2", Stmts: 1}] {
			t.Errorf("%s: unreached block should not be covered", name)
		}
	}
}

func TestParseProfile_malformed(t *testing.T) {
	for name, in := range map[string]string{
		"too few fields": "a/x.go:1.1,2.2 1\n",
		"too many":       "a/x.go:1.1,2.2 1 1 1\n",
		"no colon":       "ax.go1.1,2.2 1 1\n",
		"comma early":    "a/x.go,1.1:2.2 1 1\n",
		"bad stmts":      "a/x.go:1.1,2.2 x 1\n",
		"bad count":      "a/x.go:1.1,2.2 1 x\n",
	} {
		if _, err := ParseProfile(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestParseProfile_readError(t *testing.T) {
	if _, err := ParseProfile(failingReader{}); err == nil {
		t.Fatal("want read error")
	}
}

func TestTallyPercent(t *testing.T) {
	cases := []struct {
		tally Tally
		want  float64
	}{
		{Tally{}, 100},
		{Tally{Covered: 1, Total: 4}, 25},
		{Tally{Covered: 3, Total: 3}, 100},
	}
	for _, c := range cases {
		if got := c.tally.Percent(); got != c.want {
			t.Errorf("%+v: want %v, got %v", c.tally, c.want, got)
		}
	}
}

func TestTallies_groupsByPackageAndSkipsForeignFiles(t *testing.T) {
	blocks := map[Block]bool{
		{File: mod + "/internal/proxy/proxy.go", StartPos: "1.1", EndPos: "2.2", Stmts: 3}:     true,
		{File: mod + "/internal/proxy/websocket.go", StartPos: "1.1", EndPos: "2.2", Stmts: 1}: false,
		{File: mod + "/internal/modes/dev.go", StartPos: "1.1", EndPos: "2.2", Stmts: 2}:       true,
		{File: "other.example/pkg/x.go", StartPos: "1.1", EndPos: "2.2", Stmts: 9}:             true,
	}
	got := Tallies(blocks, mod)
	if len(got) != 2 {
		t.Fatalf("want 2 packages, got %d: %v", len(got), got)
	}
	if got["internal/proxy"] != (Tally{Covered: 3, Total: 4}) {
		t.Errorf("internal/proxy: got %+v", got["internal/proxy"])
	}
	if got["internal/modes"] != (Tally{Covered: 2, Total: 2}) {
		t.Errorf("internal/modes: got %+v", got["internal/modes"])
	}
}

func TestTallies_trailingSlashOnModulePath(t *testing.T) {
	blocks := map[Block]bool{{File: mod + "/a/x.go", Stmts: 1}: true}
	if got := Tallies(blocks, mod+"/"); len(got) != 1 || got["a"].Total != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestLoadFloors(t *testing.T) {
	f, err := LoadFloors(strings.NewReader(`{"default":100,"pinned":["internal/proxy"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Default != 100 || len(f.Pinned) != 1 {
		t.Fatalf("got %+v", f)
	}
	if f.Packages == nil {
		t.Fatal("Packages must be non-nil so callers can index it")
	}
}

// A misspelled key would otherwise be ignored, turning the gate off in exactly
// the case where someone thought they were configuring it.
func TestLoadFloors_rejectsUnknownFields(t *testing.T) {
	if _, err := LoadFloors(strings.NewReader(`{"defualt":100}`)); err == nil {
		t.Fatal("want error for unknown field")
	}
}

func TestLoadFloors_badJSON(t *testing.T) {
	if _, err := LoadFloors(strings.NewReader("{")); err == nil {
		t.Fatal("want error")
	}
}

func TestExcluded(t *testing.T) {
	f := Floors{Exclude: []string{"cmd", "tools"}}
	for pkg, want := range map[string]bool{
		"cmd":                 true,
		"cmd/thing":           true,
		"tools/coveragegate":  true,
		"internal/proxy":      false,
		"cmdlike":             false,
		"internal/cmd/nested": false,
	} {
		if got := f.Excluded(pkg); got != want {
			t.Errorf("%s: want %v, got %v", pkg, want, got)
		}
	}
}

func TestFloorFor(t *testing.T) {
	f := Floors{
		Default:  100,
		Pinned:   []string{"internal/gateway"},
		Packages: map[string]float64{"internal/auth": 62.5, "internal/gateway": 10},
	}
	if got, pinned := f.FloorFor("internal/gateway"); got != 100 || !pinned {
		t.Errorf("pinned must win over Packages: got %v pinned=%v", got, pinned)
	}
	if got, pinned := f.FloorFor("internal/auth"); got != 62.5 || pinned {
		t.Errorf("recorded floor: got %v pinned=%v", got, pinned)
	}
	if got, pinned := f.FloorFor("internal/brand-new"); got != 100 || pinned {
		t.Errorf("unknown package should get Default: got %v pinned=%v", got, pinned)
	}
}

func TestCheck(t *testing.T) {
	f := Floors{
		Default:  100,
		Exclude:  []string{"cmd"},
		Pinned:   []string{"internal/gateway"},
		Packages: map[string]float64{"internal/auth": 60},
	}
	tallies := map[string]Tally{
		"internal/gateway": {Covered: 9, Total: 10}, // 90, under the 100 pinned floor
		"internal/auth":    {Covered: 7, Total: 10}, // 70, over its 60 floor
		"internal/new":     {Covered: 1, Total: 2},  // 50, under the 100 default
		"cmd":              {Covered: 0, Total: 10}, // excluded
	}
	got := Check(tallies, f)
	if len(got) != 2 {
		t.Fatalf("want 2 violations, got %d: %v", len(got), got)
	}
	if got[0].Pkg != "internal/gateway" || !got[0].Pinned {
		t.Errorf("first violation should be the pinned package: %+v", got[0])
	}
	if got[1].Pkg != "internal/new" || got[1].Pinned {
		t.Errorf("second violation: %+v", got[1])
	}
}

func TestCheck_exactlyAtFloorPasses(t *testing.T) {
	f := Floors{Default: 100, Pinned: []string{"a"}}
	got := Check(map[string]Tally{"a": {Covered: 3, Total: 3}}, f)
	if len(got) != 0 {
		t.Fatalf("full coverage must satisfy a 100 percent floor, got %v", got)
	}
}

// A floor for a package that no longer exists is a floor nobody is enforcing.
func TestCheck_reportsStaleFloors(t *testing.T) {
	f := Floors{Default: 100, Pinned: []string{"gone/pinned"}, Packages: map[string]float64{"gone/pkg": 50}}
	got := Check(map[string]Tally{}, f)
	if len(got) != 2 {
		t.Fatalf("want 2 stale reports, got %d: %v", len(got), got)
	}
	for _, v := range got {
		if !v.Stale {
			t.Errorf("%+v should be marked stale", v)
		}
	}
}

func TestViolationString(t *testing.T) {
	if s := (Violation{Pkg: "a", Stale: true}).String(); !strings.Contains(s, "no coverage data") {
		t.Errorf("stale: %q", s)
	}
	if s := (Violation{Pkg: "a", Got: 90, Floor: 100, Pinned: true}).String(); !strings.Contains(s, "(pinned)") {
		t.Errorf("pinned: %q", s)
	}
	if s := (Violation{Pkg: "a", Got: 10, Floor: 20}).String(); !strings.Contains(s, "(floor)") {
		t.Errorf("floor: %q", s)
	}
}

func TestSeed(t *testing.T) {
	f := Floors{Default: 100, Exclude: []string{"cmd"}, Pinned: []string{"internal/gateway"}}
	tallies := map[string]Tally{
		"internal/auth":    {Covered: 7, Total: 10}, // 70, seeded at 69.5
		"internal/tiny":    {Covered: 0, Total: 10}, // 0, clamped at 0
		"internal/gateway": {Covered: 1, Total: 10}, // on the pinned list, omitted
		"cmd":              {Covered: 1, Total: 10}, // excluded, omitted
	}
	got := Seed(tallies, f, 0.5)
	if len(got.Packages) != 2 {
		t.Fatalf("want 2 seeded floors, got %v", got.Packages)
	}
	if got.Packages["internal/auth"] != 69.5 {
		t.Errorf("margin not applied: %v", got.Packages["internal/auth"])
	}
	if got.Packages["internal/tiny"] != 0 {
		t.Errorf("floor must clamp at 0: %v", got.Packages["internal/tiny"])
	}
	if len(got.Pinned) != 1 || got.Pinned[0] != "internal/gateway" || got.Default != 100 {
		t.Errorf("seeding must preserve the rest of the file: %+v", got)
	}
}

func TestSeed_roundsToTwoDecimals(t *testing.T) {
	got := Seed(map[string]Tally{"a": {Covered: 1, Total: 3}}, Floors{}, 0)
	if got.Packages["a"] != 33.33 {
		t.Fatalf("want 33.33, got %v", got.Packages["a"])
	}
}

func TestRun_enforcesAndReseeds(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "coverage.out")
	floors := filepath.Join(dir, "floors.json")
	write(t, profile, "mode: set\n"+mod+"/internal/proxy/proxy.go:1.1,2.2 4 0\n")
	write(t, floors, `{"default":100,"exclude":["cmd"]}`)

	if err := run(profile, floors, mod, false, 0.5); err == nil {
		t.Fatal("zero coverage against a 100 percent default must fail")
	}
	if err := run(profile, floors, mod, true, 0.5); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if err := run(profile, floors, mod, false, 0.5); err != nil {
		t.Fatalf("a reseeded floors file must then pass: %v", err)
	}
	if body := read(t, floors); !strings.Contains(body, `"internal/proxy": 0`) {
		t.Errorf("reseeded file: %s", body)
	}
}

func TestRun_missingFiles(t *testing.T) {
	dir := t.TempDir()
	floors := filepath.Join(dir, "floors.json")
	write(t, floors, `{"default":100}`)
	if err := run(filepath.Join(dir, "nope.out"), floors, mod, false, 0.5); err == nil {
		t.Error("missing profile must error")
	}
	profile := filepath.Join(dir, "coverage.out")
	write(t, profile, "mode: set\n")
	if err := run(profile, filepath.Join(dir, "nope.json"), mod, false, 0.5); err == nil {
		t.Error("missing floors must error")
	}
	write(t, profile, "garbage line\n")
	if err := run(profile, floors, mod, false, 0.5); err == nil {
		t.Error("unparseable profile must error")
	}
	write(t, profile, "mode: set\n")
	bad := filepath.Join(dir, "bad.json")
	write(t, bad, "{")
	if err := run(profile, bad, mod, false, 0.5); err == nil {
		t.Error("unparseable floors must error")
	}
}

func TestWriteFloors_unwritablePath(t *testing.T) {
	if err := writeFloors(filepath.Join(t.TempDir(), "no", "such", "dir.json"), Floors{}); err == nil {
		t.Fatal("want error writing into a missing directory")
	}
}

func TestReport_passAndFail(t *testing.T) {
	if err := report(nil, 3); err != nil {
		t.Errorf("no violations must pass: %v", err)
	}
	if err := report([]Violation{{Pkg: "a", Got: 1, Floor: 2}}, 3); err == nil {
		t.Error("violations must fail")
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path) // #nosec G304 -- test-local temp path
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
