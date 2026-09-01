package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kelseyhightower/envconfig"
)

// Source says where a variable name came from, so a diff in the golden file
// points at the code that has to change rather than only at the name.
type Source string

const (
	// FromStruct means envconfig derives the name from a config struct field.
	FromStruct Source = "struct"
	// FromCall means the name reaches os.Getenv or os.LookupEnv from a literal,
	// a package constant, or a literal slice being ranged over.
	FromCall Source = "call"
	// FromDynamic marks a read whose name this tool could not resolve. It is
	// recorded rather than dropped: an unresolved read is a hole in the lock,
	// and the golden file should say so out loud.
	FromDynamic Source = "dynamic"
)

// Var is one environment variable the service reads.
type Var struct {
	Name   string
	Source Source
	Where  string
}

// StructNames returns every name envconfig would look up for spec under prefix.
//
// The names come from envconfig's own gatherInfo, reached through Usagef, and
// not from a reimplementation of its naming rules: getting those rules subtly
// wrong is the mistake this file exists to prevent.
//
// Two names are recorded per field, because Process looks up the prefixed Key
// and then falls back to the bare Alt from an explicit `envconfig` tag. A field
// tagged `envconfig:"DATABASE_URL"` under the service prefix answers to both
// SUPATYPE_DB_DATABASE_URL and DATABASE_URL, and both are therefore live.
func StructNames(prefix string, spec interface{}, where string) ([]Var, error) {
	var sb strings.Builder
	const format = "{{range .}}{{.Key}}\t{{.Alt}}\n{{end}}"
	if err := envconfig.Usagef(prefix, spec, &sb, format); err != nil {
		return nil, err
	}
	var out []Var
	for _, line := range strings.Split(strings.TrimSpace(sb.String()), "\n") {
		key, alt, _ := strings.Cut(strings.TrimSpace(line), "\t")
		for _, name := range []string{key, alt} {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, Var{Name: name, Source: FromStruct, Where: where})
			}
		}
	}
	return out, nil
}

// envReaders are the calls that read a variable outside the config structs.
var envReaders = map[string]bool{"Getenv": true, "LookupEnv": true}

// CallNames returns the variable names read through os.Getenv and os.LookupEnv
// anywhere under root.
//
// Files are parsed a package at a time so a name held in a package constant
// resolves even when the constant lives in another file of that package.
func CallNames(root string) ([]Var, []error) {
	byDir, err := goFilesByDir(root)
	if err != nil {
		return nil, []error{err}
	}
	var found []Var
	var problems []error
	for _, dir := range sortedKeys(byDir) {
		vars, errs := callNamesInPackage(root, byDir[dir])
		found = append(found, vars...)
		problems = append(problems, errs...)
	}
	return found, problems
}

// goFilesByDir groups non-test Go sources by directory, skipping trees that are
// not part of the service.
func goFilesByDir(root string) (map[string][]string, error) {
	byDir := make(map[string][]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") || skipPath(path) {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		byDir[dir] = append(byDir[dir], path)
		return nil
	})
	return byDir, err
}

// skipPath keeps vendored, generated and tool trees out of the surface. The
// tools themselves read flags, not configuration.
//
// Matching is per path segment, not by substring: "internal/mytools/x.go"
// contains "tools/" and is not a tool.
func skipPath(path string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		switch seg {
		case "vendor", "node_modules", "testdata", "tools":
			return true
		}
	}
	return false
}

func callNamesInPackage(root string, paths []string) ([]Var, []error) {
	fset := token.NewFileSet()
	files := make(map[string]*ast.File, len(paths))
	var problems []error
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", path, err))
			continue
		}
		files[path] = file
	}
	consts := stringConsts(files)

	var found []Var
	for _, path := range sortedKeys(files) {
		found = append(found, envReadsInFile(fset, displayPath(root, path), path, files[path], consts)...)
	}
	return found, problems
}

// stringConsts collects package-level constants and variables bound to a single
// string literal, which is how a package names an env var it reads twice.
func stringConsts(files map[string]*ast.File) map[string]string {
	out := make(map[string]string)
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				collectStringSpec(spec, out)
			}
		}
	}
	return out
}

func collectStringSpec(spec ast.Spec, out map[string]string) {
	value, ok := spec.(*ast.ValueSpec)
	if !ok || len(value.Names) != len(value.Values) {
		return
	}
	for i, name := range value.Names {
		if lit, ok := stringLiteral(value.Values[i]); ok {
			out[name.Name] = lit
		}
	}
}

// envReadsInFile finds every os.Getenv / os.LookupEnv call in one file and
// resolves the name each one reads.
//
// The enclosing-node stack exists for the range case: a loop over a literal
// slice of names reads all of them, and dropping that would hide the very
// SUPATYPE_ names this refactor is removing.
func envReadsInFile(fset *token.FileSet, display, path string, file *ast.File, consts map[string]string) []Var {
	var found []Var
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		stack = append(stack, n)
		call, ok := n.(*ast.CallExpr)
		if !ok || !isOSEnvRead(call) || len(call.Args) != 1 {
			return true
		}
		where := display + ":" + strconv.Itoa(fset.Position(call.Pos()).Line)
		for _, v := range resolveNames(call.Args[0], stack, consts) {
			found = append(found, Var{Name: v.Name, Source: v.Source, Where: where})
		}
		return true
	})
	return found
}

// resolveNames works out which names an env-read argument can take.
func resolveNames(arg ast.Expr, stack []ast.Node, consts map[string]string) []Var {
	if name, ok := stringLiteral(arg); ok {
		return []Var{{Name: name, Source: FromCall}}
	}
	ident, ok := arg.(*ast.Ident)
	if !ok {
		return []Var{{Name: exprLabel(arg), Source: FromDynamic}}
	}
	if name, ok := consts[ident.Name]; ok {
		return []Var{{Name: name, Source: FromCall}}
	}
	if names := rangeLiterals(ident, stack); len(names) > 0 {
		out := make([]Var, 0, len(names))
		for _, name := range names {
			out = append(out, Var{Name: name, Source: FromCall})
		}
		return out
	}
	return []Var{{Name: exprLabel(arg), Source: FromDynamic}}
}

// rangeLiterals returns the names in `for _, x := range []string{"A", "B"}`
// when ident is that loop's value variable.
func rangeLiterals(ident *ast.Ident, stack []ast.Node) []string {
	for i := len(stack) - 1; i >= 0; i-- {
		rng, ok := stack[i].(*ast.RangeStmt)
		if !ok {
			continue
		}
		value, ok := rng.Value.(*ast.Ident)
		if !ok || value.Name != ident.Name {
			continue
		}
		lit, ok := rng.X.(*ast.CompositeLit)
		if !ok {
			continue
		}
		var out []string
		for _, elt := range lit.Elts {
			if name, ok := stringLiteral(elt); ok {
				out = append(out, name)
			}
		}
		return out
	}
	return nil
}

// displayPath renders a scanned file relative to the scan root, so the golden
// surface reads the same whether it was generated from the repository root or
// from a test running inside tools/envsurface.
func displayPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// isOSEnvRead reports whether the call is os.Getenv or os.LookupEnv.
func isOSEnvRead(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !envReaders[sel.Sel.Name] {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "os"
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// exprLabel names an unresolved argument well enough to find it in the source.
func exprLabel(expr ast.Expr) string {
	if ident, ok := expr.(*ast.Ident); ok {
		return "<unresolved:" + ident.Name + ">"
	}
	return "<unresolved:expression>"
}

// Render formats the surface as the golden file: one name per line with the
// sources that read it, deduplicated and sorted so the file is a stable diff.
func Render(vars []Var) string {
	wheres := make(map[string]map[string]bool)
	for _, v := range vars {
		if wheres[v.Name] == nil {
			wheres[v.Name] = make(map[string]bool)
		}
		wheres[v.Name][string(v.Source)+" "+v.Where] = true
	}
	names := sortedKeys(wheres)

	var sb strings.Builder
	sb.WriteString(surfaceHeader)
	fmt.Fprintf(&sb, "# total: %d names\n\n", len(names))
	for _, name := range names {
		fmt.Fprintf(&sb, "%s\n", name)
		for _, where := range sortedKeys(wheres[name]) {
			fmt.Fprintf(&sb, "    %s\n", where)
		}
	}
	return sb.String()
}

// sortedKeys returns a map's keys in order, for stable output.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const surfaceHeader = `# Environment surface of supatype-server. Generated; do not hand-edit.
#
# Regenerate with:  go run ./tools/envsurface -update
#
# Every name here is one the process actually reads.
#
#   struct   derived by envconfig from a config struct. envconfig looks up the
#            prefixed key and then falls back to the bare name from an explicit
#            tag, so both names appear and both are live.
#   call     a literal, a package constant, or a member of a literal slice being
#            ranged over, passed to os.Getenv or os.LookupEnv.
#   dynamic  a read this tool could not resolve statically. Recorded on purpose:
#            it marks a hole in the lock rather than hiding one.
#
# This file is a behaviour lock for the coherence refactor. A diff here means the
# configuration surface changed, which is either the point of the commit or a bug
# in it. Renaming SUPATYPE_* to SUPATYPE_* should rewrite this file wholesale and
# change nothing else about what the service reads.
#
`
