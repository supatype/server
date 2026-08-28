package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is where this tool sits relative to the repository.
const repoRoot = "../.."

// TestGoldenSurfaceIsCurrent is the behaviour lock. It fails when the set of
// environment variables the service reads stops matching the checked-in record,
// which during the coherence refactor is the signal that a variable was renamed,
// dropped or added.
func TestGoldenSurfaceIsCurrent(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(repoRoot, goldenPath))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Collect(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got == string(want) {
		return
	}
	t.Errorf("the environment surface no longer matches %s.\n"+
		"If the change is intended, regenerate it and review the diff as part of the commit:\n"+
		"    go run ./tools/envsurface -update\n\n%s", goldenPath, firstDiff(string(want), got))
}

// TestGoldenSurfaceHasNoUnresolvedReads keeps the lock honest. A read whose name
// this tool cannot work out is a variable the lock is not watching.
func TestGoldenSurfaceHasNoUnresolvedReads(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot, goldenPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "<unresolved:") {
		t.Errorf("%s records an unresolved env read; teach tools/envsurface to resolve it "+
			"or read the variable through the config package instead", goldenPath)
	}
}

// TestSurfaceStillCarriesKnownNames guards against the generator silently
// producing an empty or truncated surface, which would make the lock vacuous.
func TestSurfaceStillCarriesKnownNames(t *testing.T) {
	got, err := Collect(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"GOTRUE_JWT_SECRET",      // auth config, the most load-bearing variable
		"GOTRUE_DB_DATABASE_URL", // prefixed key
		"DATABASE_URL",           // the bare fallback for the same field
		"SUPATYPE_MODE",          // gateway config
		"SUPATYPE_SQL_DATABASE_URL",
		"STORAGE_PATH",
	} {
		if !strings.Contains(got, "\n"+name+"\n") {
			t.Errorf("surface is missing %s", name)
		}
	}
}

func TestStructNames_recordsKeyAndBareAlt(t *testing.T) {
	type inner struct {
		Secret string `envconfig:"JWT_SECRET"`
	}
	type spec struct {
		Nested inner
		Plain  string
	}
	got, err := StructNames("gotrue", &spec{}, "test")
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, v := range got {
		names[v.Name] = true
		if v.Source != FromStruct || v.Where != "test" {
			t.Errorf("unexpected provenance: %+v", v)
		}
	}
	for _, want := range []string{"GOTRUE_NESTED_JWT_SECRET", "JWT_SECRET", "GOTRUE_PLAIN"} {
		if !names[want] {
			t.Errorf("missing %s from %v", want, names)
		}
	}
}

func TestStructNames_rejectsNonPointer(t *testing.T) {
	if _, err := StructNames("gotrue", struct{}{}, "test"); err == nil {
		t.Fatal("want error for a non-pointer spec")
	}
}

func TestSkipPath(t *testing.T) {
	for path, want := range map[string]bool{
		"internal/proxy/proxy.go":     false,
		"vendor/x/y.go":               true,
		"internal/x/testdata/z.go":    true,
		"tools/envsurface/surface.go": true,
		"internal/mytools/x.go":       false, // contains "tools/" as a substring only
		"a/node_modules/b.go":         true,
	} {
		if got := skipPath(path); got != want {
			t.Errorf("%s: want %v, got %v", path, want, got)
		}
	}
}

func TestCallNames_resolvesLiteralsConstsAndRanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "names.go"), `package p

const fromConst = "FROM_CONST"
var fromVar = "FROM_VAR"
`)
	writeFile(t, filepath.Join(dir, "reads.go"), `package p

import "os"

func read() {
	os.Getenv("PLAIN_LITERAL")
	_, _ = os.LookupEnv("VIA_LOOKUP")
	os.Getenv(fromConst)
	os.Getenv(fromVar)
	for _, k := range []string{"RANGE_ONE", "RANGE_TWO"} {
		os.Getenv(k)
	}
}
`)
	got, problems := CallNames(dir)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	names := make(map[string]Source)
	for _, v := range got {
		names[v.Name] = v.Source
	}
	for _, want := range []string{
		"PLAIN_LITERAL", "VIA_LOOKUP", "FROM_CONST", "FROM_VAR", "RANGE_ONE", "RANGE_TWO",
	} {
		if names[want] != FromCall {
			t.Errorf("%s: want source %q, got %q", want, FromCall, names[want])
		}
	}
}

func TestCallNames_recordsUnresolvedRead(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dyn.go"), `package p

import "os"

func read(key string) string {
	return os.Getenv(key)
}

func expr(m map[string]string) string {
	return os.Getenv(m["k"])
}
`)
	got, problems := CallNames(dir)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	var dynamic []string
	for _, v := range got {
		if v.Source == FromDynamic {
			dynamic = append(dynamic, v.Name)
		}
	}
	if len(dynamic) != 2 {
		t.Fatalf("want 2 unresolved reads, got %v", dynamic)
	}
	for _, name := range dynamic {
		if !strings.HasPrefix(name, "<unresolved:") {
			t.Errorf("unresolved read should be labelled: %q", name)
		}
	}
}

func TestCallNames_ignoresLookalikes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "look.go"), `package p

type fake struct{}

func (fake) Getenv(string) string { return "" }

var other fake

func read() {
	other.Getenv("NOT_OS_GETENV")
	getenvLocal("ALSO_NOT")
}

func getenvLocal(string) string { return "" }
`)
	got, problems := CallNames(dir)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(got) != 0 {
		t.Fatalf("only os.Getenv and os.LookupEnv count, got %v", got)
	}
}

func TestCallNames_reportsUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken.go"), "package p\nfunc (")
	_, problems := CallNames(dir)
	if len(problems) == 0 {
		t.Fatal("want a parse error")
	}
}

func TestCallNames_skipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "x_test.go"), `package p

import "os"

func read() { os.Getenv("ONLY_IN_TEST") }
`)
	got, _ := CallNames(dir)
	if len(got) != 0 {
		t.Fatalf("test files are not part of the service surface, got %v", got)
	}
}

func TestRender_dedupesAndSorts(t *testing.T) {
	out := Render([]Var{
		{Name: "ZED", Source: FromCall, Where: "b.go:2"},
		{Name: "ALPHA", Source: FromStruct, Where: "conf"},
		{Name: "ALPHA", Source: FromStruct, Where: "conf"},
		{Name: "ALPHA", Source: FromCall, Where: "a.go:1"},
	})
	if strings.Index(out, "\nALPHA\n") > strings.Index(out, "\nZED\n") {
		t.Error("names must be sorted")
	}
	if strings.Count(out, "struct conf") != 1 {
		t.Errorf("duplicate provenance should collapse:\n%s", out)
	}
	if !strings.Contains(out, "# total: 2 names") {
		t.Errorf("header should count distinct names:\n%s", out)
	}
}

func TestStringConstsAndRangeLiterals(t *testing.T) {
	fset := token.NewFileSet()
	src := `package p

const a = "A"
const b, c = "B", "C"
const notString = 3
var d = "D"

func f() {
	for _, k := range []string{"R1", "R2"} {
		_ = k
	}
}
`
	file, err := parser.ParseFile(fset, "x.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	consts := stringConsts(map[string]*ast.File{"x.go": file})
	for name, want := range map[string]string{"a": "A", "b": "B", "c": "C", "d": "D"} {
		if consts[name] != want {
			t.Errorf("const %s: want %q, got %q", name, want, consts[name])
		}
	}
	if _, ok := consts["notString"]; ok {
		t.Error("non-string constants must not be collected")
	}
}

func TestResolveNames_constWinsOverRange(t *testing.T) {
	got := resolveNames(&ast.Ident{Name: "k"}, nil, map[string]string{"k": "FROM_CONST"})
	if len(got) != 1 || got[0].Name != "FROM_CONST" || got[0].Source != FromCall {
		t.Fatalf("got %+v", got)
	}
}

func TestCollect_reportsBadRoot(t *testing.T) {
	if _, err := Collect(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("want error for a missing root")
	}
}

// firstDiff shows the first differing line, which is enough to see what moved
// without printing hundreds of names.
func firstDiff(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		w, g := lineAt(wantLines, i), lineAt(gotLines, i)
		if w != g {
			return "first difference at line " + itoa(i+1) + ":\n  golden: " + w + "\n  actual: " + g
		}
	}
	return "files differ in length only"
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<end of file>"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// directReadBudget is the number of `call`-provenance entries the surface may
// still record: variables read with os.Getenv from inside a package rather than
// handed to it as configuration.
//
// It only ever goes down. Every remaining one is an auth-service variable that
// moves when the GOTRUE_ prefix does, so this reaches zero in the rename phase.
// Until then this is what stops a new direct read appearing unnoticed, which is
// exactly how twelve packages came to read their own configuration.
const directReadBudget = 9

func TestDirectEnvReadsOnlyShrink(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot, goldenPath))
	if err != nil {
		t.Fatal(err)
	}
	var reads []string
	for _, line := range strings.Split(string(body), "\n") {
		read := strings.TrimSpace(line)
		if !strings.HasPrefix(read, "call ") {
			continue
		}
		// internal/config is the one package allowed to read the environment;
		// that is its job. The budget counts everywhere else.
		if strings.Contains(read, "internal/config/") {
			continue
		}
		reads = append(reads, read)
	}
	if len(reads) > directReadBudget {
		t.Errorf("%d direct env reads, budget is %d. Take the variable through "+
			"internal/config instead of reading it where it is used:\n  %s",
			len(reads), directReadBudget, strings.Join(reads, "\n  "))
	}
	if len(reads) < directReadBudget {
		t.Errorf("only %d direct env reads remain but the budget is still %d. "+
			"Lower directReadBudget to %d so the ground gained is held.",
			len(reads), directReadBudget, len(reads))
	}
}
