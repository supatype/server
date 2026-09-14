package functions

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// What the functions directory holds besides functions.
//
// The CLI writes `deno.d.ts` and `tsconfig.json` beside them so an editor understands the Deno
// runtime, and `_shared/` for code several functions import. None of it is a route.
//
// Listing one anyway is not a cosmetic fault: it puts a name in Studio's sidebar that can be
// selected and invoked, and the worker — which discovers routes by its own rules — answers 404. The
// person is told the function they just clicked does not exist. `deno.d.ts` was listed as a function
// called "deno.d" and did exactly that.
//
// So these pin that this list matches what the worker will actually serve.

func listedNames(t *testing.T, dir string) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	listFunctions(dir)(rec, httptest.NewRequest(http.MethodGet, "/list", nil))

	var body struct {
		Data []functionMeta `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("could not read the response: %v (%s)", err, rec.Body.String())
	}
	names := make([]string, 0, len(body.Data))
	for _, f := range body.Data {
		names = append(names, f.Name)
	}
	return names
}

func writeFunctionsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Two real functions: one flat file, one directory with an entrypoint.
	if err := os.WriteFile(filepath.Join(dir, "ping.ts"), []byte("export default () => new Response()"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "greet"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "greet", "index.ts"), []byte("export default () => new Response()"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Everything else the CLI puts there.
	for name, content := range map[string]string{
		"deno.d.ts":     "declare namespace Deno {}",
		"tsconfig.json": "{}",
		".env.local":    "SECRET=1",
		"_helpers.ts":   "export const help = 1",
		"types.d.ts":    "export type A = 1",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Shared code, and a directory that is not a function because it has no entrypoint.
	for _, sub := range []string{"_shared", "notes"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "api.ts"), []byte("export const x = 1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestOnlyRealFunctionsAreListed(t *testing.T) {
	names := listedNames(t, writeFunctionsDir(t))

	want := map[string]bool{"ping": true, "greet": true}
	for _, name := range names {
		if !want[name] {
			t.Errorf("listed %q, which the worker will not serve", name)
		}
		delete(want, name)
	}
	for missing := range want {
		t.Errorf("did not list %q, which is a real function", missing)
	}
}

func TestTheAmbientTypesAreNotAFunction(t *testing.T) {
	// The case that shipped: `deno.d.ts` became a function named "deno.d", selectable in Studio and
	// 404 on invoke.
	for _, name := range listedNames(t, writeFunctionsDir(t)) {
		if name == "deno.d" || name == "types.d" {
			t.Fatalf("listed %q: a .d.ts declares types and defines no handler", name)
		}
	}
}

func TestAMissingDirectoryIsAnEmptyListRatherThanAnError(t *testing.T) {
	// A project with no functions at all is an ordinary state, not a fault to report.
	if names := listedNames(t, filepath.Join(t.TempDir(), "nothing-here")); len(names) != 0 {
		t.Fatalf("names = %v, want none", names)
	}
}
