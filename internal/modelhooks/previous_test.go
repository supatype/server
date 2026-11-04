package modelhooks

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testServiceKey = "service-role-key"

// fakeRest stands in for PostgREST with the two behaviours previous() depends on: a caller's read
// sees only the rows its update or delete rule allows, and only the service role sees the restricted
// `secret` column. Every request is recorded so a test can say which credential asked for what.
type fakeRest struct {
	rows  []map[string]any
	calls []string
}

func (f *fakeRest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	f.calls = append(f.calls, r.URL.String()+" auth="+auth)
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/json")

	if r.URL.Path != "/posts" {
		// The table exists but has no generated check: the column is unknown to PostgREST.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"42703","message":"column can_update_legacy does not exist"}`))
		return
	}
	if auth == "Bearer "+testServiceKey {
		_ = json.NewEncoder(w).Encode(f.byKeys(q.Get("id")))
		return
	}
	_ = json.NewEncoder(w).Encode(f.callerKeys(q, auth))
}

// callerKeys is what the caller's own read returns: the ids of the rows its rule allows.
func (f *fakeRest) callerKeys(q map[string][]string, auth string) []map[string]any {
	if first(q, "can_update_posts") != "is.true" && first(q, "can_delete_posts") != "is.true" {
		return nil
	}
	sub := subjectOf(strings.TrimPrefix(auth, "Bearer "))
	limit, _ := strconv.Atoi(first(q, "limit"))
	out := []map[string]any{}
	for _, row := range f.rows {
		if row["owner"] == sub && len(out) < limit {
			out = append(out, map[string]any{"id": row["id"]})
		}
	}
	return out
}

// byKeys answers `id=in.("1","3")` with the full stored rows.
func (f *fakeRest) byKeys(filter string) []map[string]any {
	list := strings.TrimSuffix(strings.TrimPrefix(filter, "in.("), ")")
	want := map[string]bool{}
	for _, v := range strings.Split(list, ",") {
		want[strings.Trim(v, `"`)] = true
	}
	out := []map[string]any{}
	for _, row := range f.rows {
		if want[fmt.Sprint(row["id"])] {
			out = append(out, row)
		}
	}
	return out
}

func first(q map[string][]string, key string) string {
	if v := q[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func subjectOf(token string) string {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return ""
	}
	sub, _ := claims["sub"].(string)
	return sub
}

// userToken mints a caller token. The callback never verifies it (PostgREST does), so any key will do.
func userToken(t *testing.T, sub string, expiry time.Time) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": sub, "role": "authenticated", "exp": expiry.Unix(),
	}).SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// ownedRows seeds n rows for each owner, alternating, with ids from 1.
func ownedRows(n int, owners ...string) []map[string]any {
	rows := []map[string]any{}
	for i := 0; i < n*len(owners); i++ {
		rows = append(rows, map[string]any{
			"id": i + 1, "owner": owners[i%len(owners)], "title": "stored", "secret": fmt.Sprintf("s%d", i+1),
		})
	}
	return rows
}

// callbackFor wires a Callback against fake, with posts keyed by id.
func callbackFor(t *testing.T, fake *fakeRest) *Callback {
	t.Helper()
	rest := httptest.NewServer(fake)
	t.Cleanup(rest.Close)
	return testCallback(t, rest.URL, rest.Client())
}

func postsKeyedByID(_ *http.Request, table string) []string {
	if table == "posts" || table == "legacy" {
		return []string{"id"}
	}
	return nil
}

// writeAs is the request a hook is minted for: the caller's write, carrying their bearer.
func writeAs(token string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/posts", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func post(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, strings.TrimPrefix(path, strings.TrimSuffix(PreviousPathPrefix, "/")), nil))
	return rr
}

type previousBody struct {
	Rows      []map[string]any `json:"rows"`
	Truncated bool             `json:"truncated"`
}

func decodePrevious(t *testing.T, rr *httptest.ResponseRecorder) previousBody {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body)
	}
	var body previousBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// Two users' rows match the same filter. The hook for alice's write is handed alice's rows only,
// because those are the rows her update can change; bob's would let a validating hook reject her
// write over a row she never touched, and echoing them in the rejection would leak them.
func TestPreviousReturnsOnlyTheRowsTheCallerCanChange(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(2, "alice", "bob")}
	cb := callbackFor(t, fake)
	alice := userToken(t, "alice", time.Now().Add(time.Hour))

	body := decodePrevious(t, post(t, cb.Handler(), cb.Path(writeAs(alice), OpUpdate, "posts", "status=eq.draft")))

	if len(body.Rows) != 2 || body.Truncated {
		t.Fatalf("rows = %v truncated = %v, want alice's two rows", body.Rows, body.Truncated)
	}
	for _, row := range body.Rows {
		if row["owner"] != "alice" {
			t.Errorf("handed a row alice cannot change: %v", row)
		}
		// Restricted columns come back as stored: a hook validating against a masked column would
		// compare with NULL and pass on nothing.
		if s, _ := row["secret"].(string); s == "" {
			t.Errorf("restricted column came back masked: %v", row)
		}
	}

	if len(fake.calls) != 2 {
		t.Fatalf("calls = %v, want one read as the caller and one as the service role", fake.calls)
	}
	callerRead, serviceRead := fake.calls[0], fake.calls[1]
	for _, want := range []string{"status=eq.draft", "can_update_posts=is.true", "select=id", "limit=101", "auth=Bearer " + alice} {
		if !strings.Contains(callerRead, want) {
			t.Errorf("caller read = %s, want it to contain %q", callerRead, want)
		}
	}
	if !strings.Contains(serviceRead, "auth=Bearer "+testServiceKey) || strings.Contains(serviceRead, "status=eq.draft") {
		t.Errorf("service read = %s, want the service role reading by key only", serviceRead)
	}
}

// A delete is checked against the delete rule, not the update rule.
func TestPreviousForADeleteUsesTheDeleteRule(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(1, "alice", "bob")}
	cb := callbackFor(t, fake)
	alice := userToken(t, "alice", time.Now().Add(time.Hour))

	body := decodePrevious(t, post(t, cb.Handler(), cb.Path(writeAs(alice), OpDelete, "posts", "id=gt.0")))

	if len(body.Rows) != 1 || !strings.Contains(fake.calls[0], "can_delete_posts=is.true") {
		t.Fatalf("rows = %v, calls = %v, want alice's row through can_delete_posts", body.Rows, fake.calls)
	}
}

// Nothing the caller can change is an empty list, read without a second request.
func TestPreviousWithNothingTheCallerCanChange(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(2, "bob")}
	cb := callbackFor(t, fake)
	alice := userToken(t, "alice", time.Now().Add(time.Hour))

	body := decodePrevious(t, post(t, cb.Handler(), cb.Path(writeAs(alice), OpUpdate, "posts", "")))

	if body.Rows == nil || len(body.Rows) != 0 || len(fake.calls) != 1 {
		t.Fatalf("rows = %v, calls = %v, want [] from one read", body.Rows, fake.calls)
	}
}

func TestPreviousTruncatesAtTheCap(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(DefaultPreviousLimit+5, "alice")}
	cb := callbackFor(t, fake)
	alice := userToken(t, "alice", time.Now().Add(time.Hour))

	body := decodePrevious(t, post(t, cb.Handler(), cb.Path(writeAs(alice), OpUpdate, "posts", "")))

	// Capped rather than streamed: an unfiltered PATCH would otherwise hand a hook the table.
	if len(body.Rows) != DefaultPreviousLimit {
		t.Fatalf("rows = %d, want the cap of %d", len(body.Rows), DefaultPreviousLimit)
	}
	// And it says so, so a hook can refuse rather than act on a prefix it did not know was a prefix.
	if !body.Truncated {
		t.Fatal("truncated = false when rows were dropped")
	}
}

// Without the table's primary key there is no way to read the rows back by key, and falling back
// to the request's filter as the service role is the leak this design closes. So it fails, and no
// read is made at all.
func TestPreviousFailsClosedWithoutAPrimaryKey(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(1, "alice")}
	cb := callbackFor(t, fake)
	alice := userToken(t, "alice", time.Now().Add(time.Hour))

	rr := post(t, cb.Handler(), cb.Path(writeAs(alice), OpUpdate, "unkeyed", "id=eq.1"))

	if rr.Code != http.StatusBadGateway || len(fake.calls) != 0 {
		t.Fatalf("status = %d, calls = %v, want 502 with no read", rr.Code, fake.calls)
	}
}

// A table with no generated can_update_<t> check makes PostgREST refuse the caller's read, and that
// refusal is the answer: there is no rule to apply, so there are no rows to vouch for.
func TestPreviousFailsClosedWithoutAGeneratedCheck(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(1, "alice")}
	cb := callbackFor(t, fake)
	alice := userToken(t, "alice", time.Now().Add(time.Hour))

	rr := post(t, cb.Handler(), cb.Path(writeAs(alice), OpUpdate, "legacy", "id=eq.1"))

	if rr.Code != http.StatusBadGateway || len(fake.calls) != 1 {
		t.Fatalf("status = %d, calls = %v, want 502 after the caller's read was refused", rr.Code, fake.calls)
	}
}

// The caller's token is the credential for the first read. Once it has expired the caller could not
// make the write either, so nothing is read on their behalf.
func TestPreviousRefusesAnExpiredCallerToken(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(1, "alice")}
	cb := callbackFor(t, fake)
	expired := userToken(t, "alice", time.Now().Add(-time.Minute))

	rr := post(t, cb.Handler(), cb.Path(writeAs(expired), OpUpdate, "posts", "id=eq.1"))

	if rr.Code != http.StatusBadGateway || len(fake.calls) != 0 {
		t.Fatalf("status = %d, calls = %v, want 502 with no read", rr.Code, fake.calls)
	}
}

// An anonymous caller reads as the anon role: no credential is invented for them.
func TestPreviousForAnAnonymousCaller(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(1, "alice")}
	cb := callbackFor(t, fake)

	body := decodePrevious(t, post(t, cb.Handler(), cb.Path(writeAs(""), OpUpdate, "posts", "id=eq.1")))

	if len(body.Rows) != 0 || len(fake.calls) != 1 || !strings.HasSuffix(fake.calls[0], " auth=") {
		t.Fatalf("rows = %v, calls = %v, want one read with no credential", body.Rows, fake.calls)
	}
}

// The callback path travels to the worker and may be logged on the way. The caller's token inside
// it is sealed, so the path never shows it.
func TestThePreviousPathDoesNotShowTheCallersToken(t *testing.T) {
	cb := callbackFor(t, &fakeRest{})
	alice := userToken(t, "alice", time.Now().Add(time.Hour))

	path := cb.Path(writeAs(alice), OpUpdate, "posts", "id=eq.1")
	payload, _, _ := strings.Cut(strings.TrimPrefix(path, PreviousPathPrefix), ".")
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	_, claims, _ := strings.Cut(alice, ".")
	if strings.Contains(path, alice) || strings.Contains(string(raw), alice) || strings.Contains(string(raw), claims) {
		t.Fatalf("the callback token carries the caller's bearer in the clear: %s", raw)
	}
}

// A composite key is read back by every column of it, not the first.
func TestTheKeyFilterForCompositeKeys(t *testing.T) {
	keys := []map[string]json.RawMessage{
		{"org": json.RawMessage(`"a,b"`), "id": json.RawMessage(`1`)},
		{"org": json.RawMessage(`"q\"x"`), "id": json.RawMessage(`2`)},
	}
	got, err := keyFilter([]string{"org", "id"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	want := `or=` + url.QueryEscape(`(and(org.eq."a,b",id.eq."1"),and(org.eq."q\"x",id.eq."2"))`)
	if got != want {
		t.Errorf("filter = %s, want %s", got, want)
	}

	single, err := keyFilter([]string{"id"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	if want := `id=` + url.QueryEscape(`in.("1","2")`); single != want {
		t.Errorf("filter = %s, want %s", single, want)
	}

	if _, err := keyFilter([]string{"missing"}, keys); err == nil {
		t.Error("a row without its key column was accepted")
	}
	if _, err := keyFilter([]string{"id"}, []map[string]json.RawMessage{{"id": json.RawMessage(`null`)}}); err == nil {
		t.Error("a null key was accepted")
	}
}

func TestPreviousRefusesTokensItDidNotMint(t *testing.T) {
	cb := callbackFor(t, &fakeRest{})
	valid := strings.TrimPrefix(cb.Path(writeAs(""), OpUpdate, "posts", "id=eq.1"), PreviousPathPrefix)
	payload, signature, _ := strings.Cut(valid, ".")

	// A token whose claims were edited to widen the filter or change the table: the attack the
	// signature exists to stop.
	forged := strings.TrimPrefix(cb.Path(writeAs(""), OpUpdate, "salaries", ""), PreviousPathPrefix)
	forgedPayload, _, _ := strings.Cut(forged, ".")

	cases := []struct{ name, token string }{
		{"no signature", payload},
		{"wrong signature", payload + ".AAAA"},
		{"claims swapped under a valid signature", forgedPayload + "." + signature},
		{"empty", ""},
		{"garbage", "not-a-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := post(t, cb.Handler(), "/"+tc.token)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rr.Code)
			}
			// No detail: poking at this endpoint should teach nothing about why a token failed.
			if strings.Contains(rr.Body.String(), "signature") || strings.Contains(rr.Body.String(), "expired") {
				t.Fatalf("body leaks why the token failed: %s", rr.Body)
			}
		})
	}
}

func TestPreviousRejectsAnExpiredToken(t *testing.T) {
	cb := callbackFor(t, &fakeRest{})
	// Mint by hand with an expiry in the past, since Path always mints a live one.
	claims, _ := json.Marshal(previousClaims{Table: "posts", Op: OpUpdate, Filter: "id=eq.1", Expiry: 1})
	payload := encodeClaims(claims)
	rr := post(t, cb.Handler(), "/"+payload+"."+cb.sign(payload))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an expired token", rr.Code)
	}
}

func TestPreviousPathIsAbsentForAnInsert(t *testing.T) {
	// There are no prior rows, and the generated context type omits `previous` for an insert, so
	// handing one over would offer a call that can only return nothing useful.
	cb := callbackFor(t, &fakeRest{})
	if got := cb.Path(writeAs(""), OpInsert, "posts", ""); got != "" {
		t.Fatalf("path = %q, want empty for an insert", got)
	}
}

func TestPreviousTokensDoNotSurviveARestart(t *testing.T) {
	// The key is per-process, so a token that leaked into a log is useless later, and there is no
	// state to strand mid-request either way.
	firstCb := callbackFor(t, &fakeRest{})
	second := callbackFor(t, &fakeRest{})

	token := firstCb.Path(writeAs(""), OpUpdate, "posts", "id=eq.1")
	if rr := post(t, second.Handler(), token); rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want another process to refuse the token", rr.Code)
	}
}

// encodeClaims is the token payload encoding, here rather than in previous.go
// because only this file mints a token by hand.
func encodeClaims(claims []byte) string {
	return base64.RawURLEncoding.EncodeToString(claims)
}

// ─── The ways previous() refuses ──────────────────────────────────────────────

// mintToken signs claims the way Path does, for the token states Path never produces.
func mintToken(cb *Callback, claims previousClaims) string {
	if claims.Expiry == 0 {
		claims.Expiry = time.Now().Add(time.Minute).Unix()
	}
	encoded, _ := json.Marshal(claims)
	payload := encodeClaims(encoded)
	return "/" + payload + "." + cb.sign(payload)
}

// A signed token whose caller part does not open is refused before anything is read: the seal is
// what stands between the path and the caller's credential, and a broken one is not "anonymous".
func TestPreviousRefusesACallerTokenItCannotUse(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(1, "alice")}
	cb := callbackFor(t, fake)
	expiryAsText, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "alice", "exp": "soon"}).
		SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}

	for name, caller := range map[string]string{
		"not base64":                  "!!!",
		"shorter than a nonce":        base64.RawURLEncoding.EncodeToString([]byte("short")),
		"sealed under another key":    callbackFor(t, &fakeRest{}).sealCaller("a.b.c"),
		"a sealed value not a JWT":    cb.sealCaller("not-a-jwt"),
		"a JWT with a textual expiry": cb.sealCaller(expiryAsText),
	} {
		rr := post(t, cb.Handler(), mintToken(cb, previousClaims{Table: "posts", Op: OpUpdate, Caller: caller}))
		if rr.Code != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502", name, rr.Code)
		}
	}
	if len(fake.calls) != 0 {
		t.Errorf("calls = %v, want no read for a caller that could not be used", fake.calls)
	}
}

// A callback built without a primary key source has no key for any table, so it fails closed.
func TestPreviousWithNoPrimaryKeySource(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(1, "alice")}
	rest := httptest.NewServer(fake)
	t.Cleanup(rest.Close)
	cb := NewCallback(CallbackOptions{RestBase: func(*http.Request) string { return rest.URL }, Client: rest.Client()})

	rr := post(t, cb.Handler(), cb.Path(writeAs(""), OpUpdate, "posts", "id=eq.1"))
	if rr.Code != http.StatusBadGateway || len(fake.calls) != 0 {
		t.Fatalf("status = %d, calls = %v, want 502 with no read", rr.Code, fake.calls)
	}
}

// Only an update or a delete has prior rows to check against a rule. Path never mints anything
// else, so a token for another operation is a forgery that happened to carry a valid signature.
func TestPreviousForAnOperationWithNoRule(t *testing.T) {
	fake := &fakeRest{rows: ownedRows(1, "alice")}
	cb := callbackFor(t, fake)

	rr := post(t, cb.Handler(), mintToken(cb, previousClaims{Table: "posts", Op: OpInsert}))
	if rr.Code != http.StatusBadGateway || len(fake.calls) != 0 {
		t.Fatalf("status = %d, calls = %v, want 502 with no read", rr.Code, fake.calls)
	}
}

// The service role's read by key can go wrong in its own ways, after the caller's read succeeded.
// Each is a 502: handing the hook the keys alone, or nothing, would look like rows that are not.
func TestPreviousWhenTheReadByKeyFails(t *testing.T) {
	for name, tc := range map[string]struct{ callerRows, serviceStatus, serviceBody string }{
		"a caller row without its key": {`[{"title":"x"}]`, "200", `[]`},
		"the service role refused":     {`[{"id":1}]`, "500", `{"code":"XX000","message":"boom"}`},
		"an answer that is not rows":   {`[{"id":1}]`, "200", `{"message":"not a list"}`},
	} {
		rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+testServiceKey {
				_, _ = w.Write([]byte(tc.callerRows))
				return
			}
			status, _ := strconv.Atoi(tc.serviceStatus)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(tc.serviceBody))
		}))
		cb := NewCallback(CallbackOptions{
			RestBase: func(*http.Request) string { return rest.URL }, ServiceRoleKey: testServiceKey,
			PrimaryKey: postsKeyedByID, Client: rest.Client(),
		})
		if rr := post(t, cb.Handler(), cb.Path(writeAs(""), OpUpdate, "posts", "")); rr.Code != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502", name, rr.Code)
		}
		rest.Close()
	}
}

// A JSON null from the read by key is no rows, and a hook iterating them still gets a list.
func TestPreviousWhenTheReadByKeyAnswersNull(t *testing.T) {
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testServiceKey {
			_, _ = w.Write([]byte(`[{"id":1}]`))
			return
		}
		_, _ = w.Write([]byte(`null`))
	}))
	t.Cleanup(rest.Close)
	cb := NewCallback(CallbackOptions{
		RestBase: func(*http.Request) string { return rest.URL }, ServiceRoleKey: testServiceKey,
		PrimaryKey: postsKeyedByID, Client: rest.Client(),
	})

	body := decodePrevious(t, post(t, cb.Handler(), cb.Path(writeAs(""), OpUpdate, "posts", "")))
	if body.Rows == nil || len(body.Rows) != 0 {
		t.Fatalf("rows = %v, want an empty list", body.Rows)
	}
}

// A key value that is not a scalar, or a string that does not decode, cannot be put in a filter.
func TestTheKeyFilterRefusesValuesItCannotQuote(t *testing.T) {
	for name, value := range map[string]json.RawMessage{
		"an object":       json.RawMessage(`{"a":1}`),
		"an array":        json.RawMessage(`[1]`),
		"a broken string": json.RawMessage(`"unterminated`),
	} {
		if _, err := keyFilter([]string{"id"}, []map[string]json.RawMessage{{"id": value}}); err == nil {
			t.Errorf("%s was accepted as a key", name)
		}
	}
}
