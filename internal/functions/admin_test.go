package functions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/deno"
)

// The env API writes files a worker then reads as its secrets, so what it
// refuses matters as much as what it stores: a name it will not accept, a key
// it will not return the value of, a file it cannot read.

const serviceKey = "service-role-key"

// api returns the handler over a fresh functions directory, and that directory.
func api(t *testing.T, manager LogSource) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{Mode: "standalone", ServiceRoleKey: serviceKey}
	return Handler(cfg, dir, manager), dir
}

// call runs one authorised request.
func call(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// dataStrings reads the {"data": [...]} shape the list endpoints return.
func dataStrings(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not the list shape: %s", rec.Body.String())
	}
	return body.Data
}

// ─── Listing functions ────────────────────────────────────────────────────────

// A function is a .ts file or a directory with an index.ts in it. Anything else
// in the directory is not a function and must not be listed as one.
func TestListFunctions(t *testing.T) {
	handler, dir := api(t, nil)

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("hello.ts", "export default {}")
	write("nested/index.ts", "export default {}")
	write("notes.md", "not a function")
	write(".hidden.ts", "not a function")
	write("no-entry/other.ts", "not a function")
	write(".env.local", "SECRET=x")

	rec := call(t, handler, http.MethodGet, "/list", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var body struct {
		Data []functionMeta `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	found := map[string]bool{}
	for _, fn := range body.Data {
		found[fn.Name] = true
		if fn.DeployedAt == "" {
			t.Errorf("%s has no deployed-at", fn.Name)
		}
		if _, err := time.Parse(time.RFC3339, fn.DeployedAt); err != nil {
			t.Errorf("%s: deployed-at is not a timestamp: %q", fn.Name, fn.DeployedAt)
		}
	}
	for _, name := range []string{"hello", "nested"} {
		if !found[name] {
			t.Errorf("%s is missing from %v", name, body.Data)
		}
	}
	for _, name := range []string{"notes.md", "notes", ".hidden", "no-entry", ".env.local"} {
		if found[name] {
			t.Errorf("%s was listed as a function", name)
		}
	}
}

// A project with no functions directory has no functions, which is not an
// error: it is a project that has not written one yet.
func TestListFunctionsWithNoDirectory(t *testing.T) {
	cfg := &config.Config{Mode: "standalone", ServiceRoleKey: serviceKey}
	handler := Handler(cfg, filepath.Join(t.TempDir(), "absent"), nil)

	rec := call(t, handler, http.MethodGet, "/list", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("body = %q, want an empty list", rec.Body.String())
	}
}

// A directory that cannot be scanned is a misconfiguration worth reporting,
// distinct from one that simply is not there yet.
//
// A null byte rather than a file at the path: Windows reports an empty listing
// for a file where Linux reports ENOTDIR, and a test that only fails on one of
// them is worse than none.
func TestListFunctionsWhenTheDirectoryCannotBeRead(t *testing.T) {
	handler := Handler(&config.Config{Mode: "standalone", ServiceRoleKey: serviceKey}, "bad\x00path", nil)

	if rec := call(t, handler, http.MethodGet, "/list", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// ─── Logs ─────────────────────────────────────────────────────────────────────

// With edge functions disabled there is no worker and so no logs, which is an
// empty answer rather than a failure.
func TestLogsWithNoWorker(t *testing.T) {
	handler, _ := api(t, nil)

	rec := call(t, handler, http.MethodGet, "/hello/logs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(dataStrings(t, rec)) != 0 {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestLogsComeFromTheWorker(t *testing.T) {
	manager := deno.New(filepath.Join(t.TempDir(), "deno"), filepath.Join(t.TempDir(), "r.ts"), 8001, nil, false)
	handler, _ := api(t, manager)

	rec := call(t, handler, http.MethodGet, "/hello/logs?since=1h", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Data []logEntry `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data == nil {
		t.Error("data should be an empty list, not null")
	}
}

// An unreadable window is an hour, because a request that says "since 2 weeks"
// badly should still show something rather than nothing.
func TestParseSince(t *testing.T) {
	now := time.Now().UTC()

	for name, tc := range map[string]struct {
		raw  string
		want time.Duration
	}{
		"an hour":         {"1h", time.Hour},
		"fifteen minutes": {"15m", 15 * time.Minute},
		"a day":           {"24h", 24 * time.Hour},
		"nothing":         {"", time.Hour},
		"nonsense":        {"a fortnight", time.Hour},
	} {
		got := now.Sub(parseSince(tc.raw))
		if got < tc.want-time.Second || got > tc.want+time.Second {
			t.Errorf("%s: window = %v, want about %v", name, got, tc.want)
		}
	}
}

// ─── Env: the shared file and one function's ──────────────────────────────────

func TestEnvRoundTrip(t *testing.T) {
	for name, prefix := range map[string]string{
		"the shared file": "",
		"one function's":  "/hello",
	} {
		handler, dir := api(t, nil)

		if got := dataStrings(t, call(t, handler, http.MethodGet, prefix+"/env", "")); len(got) != 0 {
			t.Errorf("%s: a fresh project already has %v", name, got)
		}

		if rec := call(t, handler, http.MethodPost, prefix+"/env", `{"key":"API_KEY","value":"secret"}`); rec.Code != http.StatusOK {
			t.Fatalf("%s: set returned %d (%s)", name, rec.Code, rec.Body.String())
		}
		if rec := call(t, handler, http.MethodPost, prefix+"/env", `{"key":"OTHER","value":"2"}`); rec.Code != http.StatusOK {
			t.Fatalf("%s: set returned %d", name, rec.Code)
		}

		got := dataStrings(t, call(t, handler, http.MethodGet, prefix+"/env", ""))
		if len(got) != 2 || got[0] != "API_KEY" || got[1] != "OTHER" {
			t.Errorf("%s: keys = %v, want them sorted", name, got)
		}
		if strings.Contains(listResponse(t, handler, prefix).Body.String(), "secret") {
			t.Errorf("%s: the value was returned alongside the key", name)
		}

		if rec := call(t, handler, http.MethodDelete, prefix+"/env/API_KEY", ""); rec.Code != http.StatusOK {
			t.Errorf("%s: delete returned %d", name, rec.Code)
		}
		if got := dataStrings(t, call(t, handler, http.MethodGet, prefix+"/env", "")); len(got) != 1 || got[0] != "OTHER" {
			t.Errorf("%s: after delete, keys = %v", name, got)
		}

		// The file on disk is what the worker reads, so it has to be right.
		expected := ".env.local"
		if prefix != "" {
			expected = ".env.hello.local"
		}
		raw, err := os.ReadFile(filepath.Join(dir, expected)) // #nosec G304 -- this test's own temp dir
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.TrimSpace(string(raw)) != "OTHER=2" {
			t.Errorf("%s: file = %q", name, raw)
		}
	}
}

// listResponse is the raw list response, for the one assertion that needs the
// body rather than the parsed keys.
func listResponse(t *testing.T, handler http.Handler, prefix string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, handler, http.MethodGet, prefix+"/env", "")
}

// The shared file and a function's are separate, or a secret meant for one
// function reaches every function.
func TestTheSharedFileAndAFunctionsAreSeparate(t *testing.T) {
	handler, _ := api(t, nil)

	call(t, handler, http.MethodPost, "/env", `{"key":"SHARED","value":"1"}`)
	call(t, handler, http.MethodPost, "/hello/env", `{"key":"MINE","value":"2"}`)

	if got := dataStrings(t, call(t, handler, http.MethodGet, "/env", "")); len(got) != 1 || got[0] != "SHARED" {
		t.Errorf("shared keys = %v", got)
	}
	if got := dataStrings(t, call(t, handler, http.MethodGet, "/hello/env", "")); len(got) != 1 || got[0] != "MINE" {
		t.Errorf("function keys = %v", got)
	}
	if got := dataStrings(t, call(t, handler, http.MethodGet, "/other/env", "")); len(got) != 0 {
		t.Errorf("another function sees %v", got)
	}
}

// Setting a key that is already there replaces its value rather than adding a
// second line the worker would read ambiguously.
func TestSettingAnExistingKeyReplacesIt(t *testing.T) {
	handler, dir := api(t, nil)

	call(t, handler, http.MethodPost, "/env", `{"key":"API_KEY","value":"first"}`)
	call(t, handler, http.MethodPost, "/env", `{"key":"API_KEY","value":"second"}`)

	raw, err := os.ReadFile(filepath.Join(dir, ".env.local")) // #nosec G304 -- this test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "API_KEY=second" {
		t.Errorf("file = %q, want one line", raw)
	}
}

// ─── Env: refusals ────────────────────────────────────────────────────────────

func TestSetEnvRefusesABodyWithoutAKey(t *testing.T) {
	handler, _ := api(t, nil)

	for name, body := range map[string]string{
		"not JSON":       "{",
		"no key":         `{"value":"x"}`,
		"an empty key":   `{"key":"","value":"x"}`,
		"nothing at all": "",
	} {
		for _, path := range []string{"/env", "/hello/env"} {
			if rec := call(t, handler, http.MethodPost, path, body); rec.Code != http.StatusBadRequest {
				t.Errorf("%s at %s: status = %d, want 400", name, path, rec.Code)
			}
		}
	}
}

// Removing something that is not there is a 404, not a silent success: the
// caller asked for a state change that did not happen.
func TestDeletingAKeyThatIsNotThere(t *testing.T) {
	handler, _ := api(t, nil)

	for _, path := range []string{"/env/ABSENT", "/hello/env/ABSENT"} {
		if rec := call(t, handler, http.MethodDelete, path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
	}
}

// withName builds a request carrying a function name as chi would.
//
// Tested at the resolver rather than through the router, because most of what
// this refuses is not something a URL path can even express — which is exactly
// why the check must not rely on the router having refused it first.
func withName(name string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/env", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("name", name)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

// The name becomes part of a filename, so it is checked rather than cleaned.
func TestAFunctionNameThatCouldNotBeAFilename(t *testing.T) {
	resolve := functionEnvFile(t.TempDir())

	for _, name := range []string{
		"", "  ", "..", ".", "with space", "with.dot",
		"with/slash", `with\backslash`, "with\x00null", `..\..\etc\passwd`,
	} {
		if _, err := resolve(withName(name)); err == nil {
			t.Errorf("%q was accepted as a function name", name)
		}
	}
}

// And the names a function may actually have are accepted.
func TestAFunctionNameThatIsFine(t *testing.T) {
	dir := t.TempDir()
	resolve := functionEnvFile(dir)

	for _, name := range []string{"hello", "send-email", "send_email", "fn2"} {
		path, err := resolve(withName(name))
		if err != nil {
			t.Errorf("%q was refused: %v", name, err)
			continue
		}
		if want := filepath.Join(dir, ".env."+name+".local"); path != want {
			t.Errorf("%q resolved to %q, want %q", name, path, want)
		}
	}
}

// The shared file needs no name and takes none.
func TestTheSharedEnvFile(t *testing.T) {
	dir := t.TempDir()
	path, err := sharedEnvFile(dir)(httptest.NewRequest(http.MethodGet, "/env", nil))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".env.local"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

// A file that exists but cannot be parsed as an env file is reported, not
// treated as empty: treating it as empty would let a set wipe it.
func TestAnUnreadableEnvFileIsReported(t *testing.T) {
	handler, dir := api(t, nil)

	// A directory where the env file should be: it exists, and it cannot be read.
	if err := os.Mkdir(filepath.Join(dir, ".env.local"), 0o750); err != nil {
		t.Fatal(err)
	}

	for method, path := range map[string]string{
		http.MethodGet:    "/env",
		http.MethodDelete: "/env/ANY",
	} {
		body := ""
		if rec := call(t, handler, method, path, body); rec.Code != http.StatusInternalServerError {
			t.Errorf("%s %s: status = %d, want 500", method, path, rec.Code)
		}
	}
	if rec := call(t, handler, http.MethodPost, "/env", `{"key":"K","value":"v"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("POST: status = %d, want 500", rec.Code)
	}
}

// A file that cannot be written is reported rather than answered with "set".
func TestAnUnwritableEnvFileIsReported(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Mode: "standalone", ServiceRoleKey: serviceKey}
	handler := Handler(cfg, filepath.Join(dir, "absent"), nil)

	if rec := call(t, handler, http.MethodPost, "/env", `{"key":"K","value":"v"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// Comments and blank lines are for humans editing the file by hand, and must
// not come back as keys.
func TestReadEnvFileIgnoresCommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.local")
	content := strings.Join([]string{
		"# a comment",
		"",
		"   ",
		"API_KEY=secret",
		"  PADDED  =  value  ",
		"NO_VALUE=",
		"# another",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	vars, err := readEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 3 {
		t.Fatalf("vars = %v, want three", vars)
	}
	if vars["API_KEY"] != "secret" || vars["PADDED"] != "value" || vars["NO_VALUE"] != "" {
		t.Errorf("vars = %v", vars)
	}
}

// A bare filename resolves against the working directory rather than failing.
func TestReadEnvFileOnAMissingFile(t *testing.T) {
	vars, err := readEnvFile(filepath.Join(t.TempDir(), ".env.local"))
	if err != nil {
		t.Fatalf("a missing env file is not an error: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("vars = %v", vars)
	}
}

// A directory that is not there is the same as no variables set.
func TestReadEnvFileUnderAMissingDirectory(t *testing.T) {
	vars, err := readEnvFile(filepath.Join(t.TempDir(), "absent", ".env.local"))
	if err != nil {
		t.Fatalf("a missing directory is not an error: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("vars = %v", vars)
	}
}

// stubLogs is a worker that has these lines and nothing else.
type stubLogs []deno.LogLine

func (s stubLogs) RecentLogs(time.Time, int) []deno.LogLine { return s }

// The worker's lines are relayed with their level and a parseable timestamp,
// which is what `supatype dev` renders.
func TestLogsAreRelayedFromTheWorker(t *testing.T) {
	at := time.Date(2026, 8, 28, 12, 0, 0, 500, time.UTC)
	source := stubLogs{
		{Timestamp: at, Level: "info", Message: "listening"},
		{Timestamp: at.Add(time.Second), Level: "error", Message: "boom"},
	}
	handler := Handler(&config.Config{Mode: "standalone", ServiceRoleKey: serviceKey}, t.TempDir(), source)

	rec := call(t, handler, http.MethodGet, "/hello/logs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var body struct {
		Data []logEntry `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("entries = %v", body.Data)
	}
	if body.Data[0].Level != "info" || body.Data[0].Message != "listening" {
		t.Errorf("first entry = %+v", body.Data[0])
	}
	if body.Data[1].Level != "error" || body.Data[1].Message != "boom" {
		t.Errorf("second entry = %+v", body.Data[1])
	}
	// Nanosecond precision, because two lines within the same millisecond are
	// ordinary and a follower keys off the timestamp.
	if _, err := time.Parse(time.RFC3339Nano, body.Data[0].Timestamp); err != nil {
		t.Errorf("timestamp = %q: %v", body.Data[0].Timestamp, err)
	}
}

// A directory that cannot be opened at all is different from one that is not
// there, and only the second means "no variables set".
func TestReadEnvFileReportsWhatItCannotOpen(t *testing.T) {
	if _, err := readEnvFile("bad\x00dir/.env.local"); err == nil {
		t.Error("a directory that cannot be opened was read as empty")
	}
	if _, err := readEnvFile(filepath.Join(t.TempDir(), "na\x00me")); err == nil {
		t.Error("a filename that cannot be opened was read as empty")
	}
}

// A file that cannot be written is reported rather than answered with "set",
// for both endpoints that write.
func TestAFailedWriteIsReported(t *testing.T) {
	handler := Handler(&config.Config{Mode: "standalone", ServiceRoleKey: serviceKey},
		filepath.Join(t.TempDir(), "absent"), nil)

	if rec := call(t, handler, http.MethodPost, "/env", `{"key":"K","value":"v"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("set: status = %d, want 500", rec.Code)
	}
}

// Every endpoint that reads a file reports a file it cannot read, rather than
// treating it as empty — which for a set would silently wipe it.
func TestEveryEndpointReportsAnUnreadableFile(t *testing.T) {
	handler := Handler(&config.Config{Mode: "standalone", ServiceRoleKey: serviceKey}, "bad\x00dir", nil)

	for name, tc := range map[string]struct{ method, path, body string }{
		"list":   {http.MethodGet, "/env", ""},
		"set":    {http.MethodPost, "/env", `{"key":"K","value":"v"}`},
		"delete": {http.MethodDelete, "/env/K", ""},
	} {
		if rec := call(t, handler, tc.method, tc.path, tc.body); rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500", name, rec.Code)
		}
	}
}

// A bare filename resolves against the working directory, which is what a
// relative functions directory in configuration produces.
func TestReadEnvFileWithNoDirectoryInThePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("K=v"), 0o600); err != nil {
		t.Fatal(err)
	}

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	vars, err := readEnvFile(".env.local")
	if err != nil {
		t.Fatal(err)
	}
	if vars["K"] != "v" {
		t.Errorf("vars = %v", vars)
	}
}

// A name the router cannot produce is still refused, and refused as the
// caller's mistake rather than the service's.
func TestEveryEnvEndpointRefusesABadFunctionName(t *testing.T) {
	file := functionEnvFile(t.TempDir())

	for name, handler := range map[string]http.HandlerFunc{
		"list":   listEnv(file),
		"set":    setEnv(file),
		"delete": deleteEnv(file),
	} {
		req := withName("not a name")
		if name == "set" {
			req = requestWithBody(req, `{"key":"K","value":"v"}`)
		}
		if name == "delete" {
			req = withKey(req, "K")
		}

		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
}

// The route always supplies a key, so this is the guard for a caller that
// reaches the handler another way.
func TestDeleteWithoutAKey(t *testing.T) {
	rec := httptest.NewRecorder()
	deleteEnv(sharedEnvFile(t.TempDir()))(rec, httptest.NewRequest(http.MethodDelete, "/env/", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// requestWithBody attaches a body while keeping the route context.
func requestWithBody(req *http.Request, body string) *http.Request {
	withBody := httptest.NewRequest(http.MethodPost, "/env", strings.NewReader(body))
	return withBody.WithContext(req.Context())
}

// withKey adds the key path parameter to an existing route context.
func withKey(req *http.Request, key string) *http.Request {
	routeCtx := chi.RouteContext(req.Context())
	routeCtx.URLParams.Add("key", key)
	return req
}

// A directory the server cannot write into answers, rather than losing the
// variable silently. The env file is written through a root, so both the root
// and the file within it can refuse.
func TestSettingAVariableThatCannotBeWritten(t *testing.T) {
	cfg := &config.Config{Mode: "standalone", ServiceRoleKey: serviceKey}

	missing := Handler(cfg, filepath.Join(t.TempDir(), "no-such-functions-dir"), nil)
	if rec := call(t, missing, http.MethodPost, "/env", `{"key":"K","value":"v"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("a functions directory that is not there: status = %d (%s)", rec.Code, rec.Body.String())
	}

	// An env file that can be read and not written: the read succeeds and the
	// write is what fails, which is the branch a directory in its place cannot
	// reach because the read fails first.
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.local")
	if err := os.WriteFile(path, []byte("EXISTING=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	readOnly := Handler(cfg, dir, nil)
	if rec := call(t, readOnly, http.MethodPost, "/env", `{"key":"K","value":"v"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("an env file that cannot be written: status = %d (%s)", rec.Code, rec.Body.String())
	}
}
