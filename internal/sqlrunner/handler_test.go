package sqlrunner

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
)

const serviceKey = "service-role-key"

func authorised(cfg *config.Config) *config.Config {
	cfg.ServiceRoleKey = serviceKey
	return cfg
}

// post runs one request through the handler.
func post(t *testing.T, cfg *config.Config, pools Pools, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	rec := httptest.NewRecorder()
	Handler(cfg, pools).ServeHTTP(rec, req)
	return rec
}

// serving returns a Pools that hands out this pool.
func serving(pool Pool) Pools {
	return func() (Pool, error) { return pool, nil }
}

// errorBody reads the "error" field the failure paths write.
func errorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not the error shape: %q", rec.Body.String())
	}
	return body.Error
}

// ─── Gate ─────────────────────────────────────────────────────────────────────

// Arbitrary SQL execution is not something dev mode opens. The only bypass is
// the explicit switch, and a deployment that configured no key must fail closed
// rather than accept anything.
func TestOnlyAServiceRoleBearerGetsIn(t *testing.T) {
	pool := &fakePool{}

	for name, tc := range map[string]struct {
		cfg    config.Config
		header string
		want   int
	}{
		"the service role key":           {config.Config{ServiceRoleKey: serviceKey}, "Bearer " + serviceKey, http.StatusOK},
		"the wrong key":                  {config.Config{ServiceRoleKey: serviceKey}, "Bearer nope", http.StatusUnauthorized},
		"no authorization at all":        {config.Config{ServiceRoleKey: serviceKey}, "", http.StatusUnauthorized},
		"a bare token, not a bearer one": {config.Config{ServiceRoleKey: serviceKey}, serviceKey, http.StatusUnauthorized},
		"no key configured":              {config.Config{}, "Bearer " + serviceKey, http.StatusUnauthorized},
		"no key configured, none sent":   {config.Config{}, "", http.StatusUnauthorized},
		"dev mode is not a bypass":       {config.Config{Mode: "dev"}, "Bearer anything", http.StatusUnauthorized},
		"the explicit insecure switch":   {config.Config{SQLRunnerInsecure: true}, "", http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(`{"query":"select 1"}`))
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		rec := httptest.NewRecorder()
		cfg := tc.cfg
		Handler(&cfg, serving(pool)).ServeHTTP(rec, req)

		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, errorBody(t, rec))
		}
	}
}

// The endpoint runs a query. Nothing else it might be asked to do is a query.
func TestOnlyPOST(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodHead} {
		req := httptest.NewRequest(method, "/sql", nil)
		rec := httptest.NewRecorder()
		Handler(authorised(&config.Config{}), serving(&fakePool{})).ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}
}

// The method is checked before the credential, so an unauthenticated GET is not
// told which of the two was wrong.
func TestTheMethodIsCheckedBeforeTheCredential(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/sql", nil)
	rec := httptest.NewRecorder()
	Handler(authorised(&config.Config{}), serving(&fakePool{})).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

// ─── Request body ─────────────────────────────────────────────────────────────

func TestABodyWithoutAQueryIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":            "{",
		"empty":               "",
		"no query field":      `{"schema":"public"}`,
		"an empty query":      `{"query":""}`,
		"a query of spaces":   `{"query":"   \n\t "}`,
		"a query of the null": `{"query":null}`,
	} {
		rec := post(t, authorised(&config.Config{}), serving(&fakePool{}), body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
		if got := errorBody(t, rec); got != "query is required" {
			t.Errorf("%s: error = %q", name, got)
		}
	}
}

// A query that is only whitespace never reaches the database.
func TestNothingIsExecutedForARefusedBody(t *testing.T) {
	pool := &fakePool{}
	post(t, authorised(&config.Config{}), serving(pool), `{"query":"  "}`, nil)
	if pool.begins != 0 {
		t.Errorf("a transaction was begun for a body that was refused")
	}
}

// ─── Schema resolution ────────────────────────────────────────────────────────

// bearerWithRole builds an unsigned JWT carrying just the role claim, which is
// all the schema routing reads. The signature is checkServiceRole's business.
func bearerWithRole(role string) string {
	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"role":%q}`, role)))
	return "Bearer header." + claims + ".signature"
}

func TestResolveSchema(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg       config.Config
		auth      string
		requested string
		want      string
	}{
		"nothing configured, nothing asked for": {config.Config{}, "", "", "public"},
		"the configured default":                {config.Config{SQLSchema: "app"}, "", "", "app"},
		"a configured default with padding":     {config.Config{SQLSchema: "  app  "}, "", "", "app"},
		"a blank configured default":            {config.Config{SQLSchema: "   "}, "", "", "public"},

		// The override is the service role's alone.
		"service_role may override":  {config.Config{SQLSchema: "app"}, bearerWithRole("service_role"), "other", "other"},
		"authenticated may not":      {config.Config{SQLSchema: "app"}, bearerWithRole("authenticated"), "other", "app"},
		"anon may not":               {config.Config{SQLSchema: "app"}, bearerWithRole("anon"), "other", "app"},
		"no token may not":           {config.Config{SQLSchema: "app"}, "", "other", "app"},
		"the insecure switch may":    {config.Config{SQLSchema: "app", SQLRunnerInsecure: true}, "", "other", "other"},
		"dev mode is not an opening": {config.Config{SQLSchema: "app", Mode: "dev"}, "", "other", "app"},

		// The value is bound as a parameter, so this is not about SQL injection.
		// search_path takes a comma-separated list, so anything but a bare
		// identifier could still put a schema on the path that was not asked for.
		"an injection attempt from service_role": {config.Config{SQLSchema: "app"}, bearerWithRole("service_role"), "public; drop table users", "app"},
		"a quoted name":                          {config.Config{SQLSchema: "app"}, bearerWithRole("service_role"), `"Weird"`, "app"},
		"a name starting with a digit":           {config.Config{SQLSchema: "app"}, bearerWithRole("service_role"), "1st", "app"},
		"a dotted name":                          {config.Config{SQLSchema: "app"}, bearerWithRole("service_role"), "a.b", "app"},
		"an empty request":                       {config.Config{SQLSchema: "app"}, bearerWithRole("service_role"), "", "app"},
		"an injection attempt while insecure":    {config.Config{SQLSchema: "app", SQLRunnerInsecure: true}, "", "drop table users", "app"},
		"an underscore-led name":                 {config.Config{SQLSchema: "app"}, bearerWithRole("service_role"), "_private", "_private"},
	} {
		cfg := tc.cfg
		if got := resolveSchema(&cfg, tc.auth, tc.requested); got != tc.want {
			t.Errorf("%s: schema = %q, want %q", name, got, tc.want)
		}
	}
}

// Anything that is not a readable role claim is treated as anon, which is the
// role with no override.
func TestJWTRole(t *testing.T) {
	for name, tc := range map[string]struct {
		header string
		want   string
	}{
		"a role claim":                {bearerWithRole("service_role"), "service_role"},
		"no header":                   {"", "anon"},
		"a bearer with nothing after": {"Bearer ", "anon"},
		"not a bearer":                {"Basic abc", "anon"},
		"not three segments":          {"Bearer a.b", "anon"},
		"four segments":               {"Bearer a.b.c.d", "anon"},
		"payload is not base64":       {"Bearer a.!!!.c", "anon"},
		"payload is not JSON":         {"Bearer a." + base64.RawURLEncoding.EncodeToString([]byte("{")) + ".c", "anon"},
		"no role in the claims":       {"Bearer a." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`)) + ".c", "anon"},
		"an empty role":               {bearerWithRole(""), "anon"},
	} {
		if got := jwtRole(tc.header); got != tc.want {
			t.Errorf("%s: role = %q, want %q", name, got, tc.want)
		}
	}
}

// The resolved schema is what is set and what is reported, so a caller can see
// which schema their query actually ran against.
func TestTheResolvedSchemaIsSetLocallyAndReported(t *testing.T) {
	tx := &fakeTx{rows: &fakeRows{columns: []string{"n"}, values: [][]any{{1}}}}
	pool := &fakePool{tx: tx}

	// In production these are the same string: Studio sends the service-role JWT,
	// which the gate compares against the configured key and the schema routing
	// reads the role claim out of.
	bearer := bearerWithRole("service_role")
	cfg := &config.Config{SQLSchema: "app", ServiceRoleKey: strings.TrimPrefix(bearer, "Bearer ")}

	rec := post(t, cfg, serving(pool), `{"query":"select 1 as n","schema":"other"}`, map[string]string{
		"Authorization": bearer,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	// is_local => true is what keeps the search_path from leaking to the next
	// request that borrows this connection.
	if !strings.Contains(tx.execSQL, "set_config") || !strings.Contains(tx.execSQL, "true") {
		t.Errorf("search_path was not set locally: %q", tx.execSQL)
	}
	// Passed as a parameter, never interpolated.
	if len(tx.execArgs) != 1 || tx.execArgs[0] != "other" {
		t.Errorf("set_config args = %v, want the schema as a bound parameter", tx.execArgs)
	}

	var body Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Schema != "other" {
		t.Errorf("reported schema = %q", body.Schema)
	}
}

// ─── Results ──────────────────────────────────────────────────────────────────

func TestRowsComeBackKeyedByColumn(t *testing.T) {
	pool := &fakePool{tx: &fakeTx{rows: &fakeRows{
		columns: []string{"id", "name"},
		values:  [][]any{{1, "ada"}, {2, "grace"}},
	}}}

	rec := post(t, authorised(&config.Config{}), serving(pool), `{"query":"select id, name from people"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	var body Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RowCount != 2 || len(body.Rows) != 2 {
		t.Fatalf("rowCount = %d, rows = %v", body.RowCount, body.Rows)
	}
	if body.Rows[0]["name"] != "ada" || body.Rows[1]["id"] != float64(2) {
		t.Errorf("rows = %v", body.Rows)
	}
}

// An empty result is an empty list, not null: a client iterating the field must
// not have to test for it first.
func TestNoRowsIsAnEmptyListNotNull(t *testing.T) {
	pool := &fakePool{tx: &fakeTx{rows: &fakeRows{columns: []string{"id"}}}}
	rec := post(t, authorised(&config.Config{}), serving(pool), `{"query":"select 1 where false"}`, nil)

	if !strings.Contains(rec.Body.String(), `"rows":[]`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// The cap exists so one query cannot exhaust the process's memory. It is
// reported as the caller's problem to narrow.
func TestAResultOverTheCapIsRefused(t *testing.T) {
	values := make([][]any, maxRows+1)
	for i := range values {
		values[i] = []any{i}
	}
	rows := &fakeRows{columns: []string{"n"}, values: values}
	pool := &fakePool{tx: &fakeTx{rows: rows}}

	rec := post(t, authorised(&config.Config{}), serving(pool), `{"query":"select generate_series(1, 20000)"}`, nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if got := errorBody(t, rec); !strings.Contains(got, "10000 row limit") {
		t.Errorf("error = %q", got)
	}
	if !rows.closed {
		t.Error("the result set was not closed when the cap was hit")
	}
}

// Exactly the cap is allowed. Off by one here means either an arbitrary refusal
// or a cap that does not hold.
func TestExactlyTheCapIsAllowed(t *testing.T) {
	values := make([][]any, maxRows)
	for i := range values {
		values[i] = []any{i}
	}
	pool := &fakePool{tx: &fakeTx{rows: &fakeRows{columns: []string{"n"}, values: values}}}

	rec := post(t, authorised(&config.Config{}), serving(pool), `{"query":"select 1"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// ─── Failures ─────────────────────────────────────────────────────────────────

func TestFailuresAreReportedWithTheStatusTheyMean(t *testing.T) {
	boom := errors.New("boom")

	for name, tc := range map[string]struct {
		pools Pools
		want  int
	}{
		"no database configured": {
			func() (Pool, error) { return nil, boom },
			http.StatusServiceUnavailable,
		},
		"the pool will not begin a transaction": {
			serving(&fakePool{beginErr: boom}),
			http.StatusServiceUnavailable,
		},
		"the search_path cannot be set": {
			serving(&fakePool{tx: &fakeTx{execErr: boom}}),
			http.StatusInternalServerError,
		},
		"the query is rejected": {
			serving(&fakePool{tx: &fakeTx{queryErr: boom}}),
			http.StatusUnprocessableEntity,
		},
		"a row cannot be decoded": {
			serving(&fakePool{tx: &fakeTx{rows: &fakeRows{
				columns: []string{"n"}, values: [][]any{{1}}, valuesErr: boom,
			}}}),
			http.StatusInternalServerError,
		},
		"the result set ends in an error": {
			serving(&fakePool{tx: &fakeTx{rows: &fakeRows{columns: []string{"n"}, err: boom}}}),
			http.StatusInternalServerError,
		},
		"the commit does not land": {
			serving(&fakePool{tx: &fakeTx{commitErr: boom, rows: &fakeRows{columns: []string{"n"}}}}),
			http.StatusInternalServerError,
		},
	} {
		rec := post(t, authorised(&config.Config{}), tc.pools, `{"query":"select 1"}`, nil)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
		if errorBody(t, rec) == "" {
			t.Errorf("%s: the failure was reported with no message", name)
		}
	}
}

// A query the caller got wrong is the caller's to fix, and its message is the
// only thing that says how.
func TestARejectedQueryReportsWhatPostgresSaid(t *testing.T) {
	pool := &fakePool{tx: &fakeTx{queryErr: errors.New(`relation "nope" does not exist`)}}
	rec := post(t, authorised(&config.Config{}), serving(pool), `{"query":"select * from nope"}`, nil)

	if got := errorBody(t, rec); got != `relation "nope" does not exist` {
		t.Errorf("error = %q", got)
	}
}

// The rollback is deferred, so a query that failed halfway does not hold the
// transaction open until the connection is recycled.
func TestTheTransactionIsAlwaysRolledBack(t *testing.T) {
	for name, tx := range map[string]*fakeTx{
		"after a success": {rows: &fakeRows{columns: []string{"n"}}},
		"after a failure": {queryErr: errors.New("boom")},
	} {
		post(t, authorised(&config.Config{}), serving(&fakePool{tx: tx}), `{"query":"select 1"}`, nil)
		if !tx.rolledBack {
			t.Errorf("%s: rollback was not called", name)
		}
	}
}

// Rollback after a successful commit is a no-op in pgx, so committing first and
// rolling back second is the safe order, not a bug.
func TestASuccessCommits(t *testing.T) {
	tx := &fakeTx{rows: &fakeRows{columns: []string{"n"}}}
	post(t, authorised(&config.Config{}), serving(&fakePool{tx: tx}), `{"query":"select 1"}`, nil)

	if !tx.committed {
		t.Error("a successful query was not committed")
	}
}

// A failure before the query has nothing to commit.
func TestAFailureDoesNotCommit(t *testing.T) {
	tx := &fakeTx{execErr: errors.New("boom")}
	post(t, authorised(&config.Config{}), serving(&fakePool{tx: tx}), `{"query":"select 1"}`, nil)

	if tx.committed {
		t.Error("a failed query was committed")
	}
}

// Every response is JSON, including the failures, because the client parses
// before it branches.
func TestEveryResponseIsJSON(t *testing.T) {
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"success":      post(t, authorised(&config.Config{}), serving(&fakePool{tx: &fakeTx{rows: &fakeRows{columns: []string{"n"}}}}), `{"query":"select 1"}`, nil),
		"bad request":  post(t, authorised(&config.Config{}), serving(&fakePool{}), `{`, nil),
		"unauthorised": post(t, &config.Config{}, serving(&fakePool{}), `{"query":"select 1"}`, nil),
	} {
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s: content type = %q", name, got)
		}
		if !json.Valid(rec.Body.Bytes()) {
			t.Errorf("%s: body is not JSON: %s", name, rec.Body.String())
		}
	}
}

// statusFor is what turns an error into a status, so an error that carries no
// classification must not be handed to the caller as their fault.
func TestAnUnclassifiedErrorIsOurs(t *testing.T) {
	if got := statusFor(errors.New("plain")); got != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", got)
	}
	if got := statusFor(fmt.Errorf("wrapped: %w", failure{status: http.StatusTeapot, err: errors.New("x")})); got != http.StatusTeapot {
		t.Errorf("a wrapped failure lost its status: %d", got)
	}
}

// The underlying cause stays reachable, so a caller matching on a pgx error
// still can.
func TestAFailureKeepsItsCause(t *testing.T) {
	cause := errors.New("cause")
	wrapped := failing(http.StatusTeapot, "context: %w", cause)

	if !errors.Is(wrapped, cause) {
		t.Error("the cause was lost")
	}
	if wrapped.Error() != "context: cause" {
		t.Errorf("message = %q", wrapped.Error())
	}
}
