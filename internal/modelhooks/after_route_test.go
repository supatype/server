package modelhooks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// afterStack is a hooked table behind the middleware, with a stand-in PostgREST that answers both
// the write and the service-role read-back, and a hook function that records what it was sent.
type afterStack struct {
	server  *httptest.Server
	mu      sync.Mutex
	hooks   []map[string]json.RawMessage
	reads   []*http.Request
	readErr bool
	// writeMark is the request-path header the write reached PostgREST with.
	writeMark string
}

const serviceKey = "service-role-key"

func newAfterStack(t *testing.T, changeLog bool, shape *TableShape) *afterStack {
	t.Helper()
	s := &afterStack{}

	fn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]json.RawMessage
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &p)
		s.mu.Lock()
		s.hooks = append(s.hooks, p)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(fn.Close)

	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			s.mu.Lock()
			s.writeMark = r.Header.Get(RequestPathHeader)
			s.mu.Unlock()
			// The write. Returns rows only under return=representation, as PostgREST does, and
			// masks the restricted column, as field masking does for the caller.
			if strings.Contains(r.Header.Get("Prefer"), "return=representation") {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`[{"id":7,"title":"from-representation","secret":null}]`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			return
		}
		s.mu.Lock()
		s.reads = append(s.reads, r.Clone(r.Context()))
		failing := s.readErr
		s.mu.Unlock()
		if failing {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`[{"id":7,"title":"stored","secret":"hidden"}]`))
	}))
	t.Cleanup(rest.Close)

	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequest(r.Method, rest.URL+"/posts?"+r.URL.RawQuery, r.Body)
		req.Header = r.Header.Clone()
		res, err := rest.Client().Do(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		w.WriteHeader(res.StatusCode)
		_, _ = w.Write(body)
	})

	hooks := map[string]TableHooksView{"posts": {
		EventAfterChange: {Function: "after-change"},
		EventAfterDelete: {Function: "after-delete"},
	}}
	mw := Middleware(Options{
		Dispatcher: NewDispatcher(nil, ""),
		Hooks:      func(*http.Request) map[string]TableHooksView { return hooks },
		ResolveURL: func(*http.Request, string) (string, error) { return fn.URL, nil },
		RequestID:  func(*http.Request) string { return "req-1" },
		AfterRows: &AfterRows{
			ChangeLogActive: func() bool { return changeLog },
			Shapes: func(*http.Request) ShapeFunc {
				return func(table string) (TableShape, bool) {
					if shape == nil || table != "posts" {
						return TableShape{}, false
					}
					return *shape, true
				}
			},
			Reader: NewRowReader(func(*http.Request) string { return rest.URL }, nil, serviceKey, nil),
			Marks:  NewRequestPathMarks(markSecret),
		},
	})
	s.server = httptest.NewServer(mw(proxy))
	t.Cleanup(s.server.Close)
	return s
}

func (s *afterStack) send(t *testing.T, method, path, body string, headers ...string) {
	t.Helper()
	req, _ := http.NewRequest(method, s.server.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
}

// settle waits for the detached after phase.
func (s *afterStack) settle() ([]map[string]json.RawMessage, []*http.Request) {
	time.Sleep(300 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]json.RawMessage(nil), s.hooks...), append([]*http.Request(nil), s.reads...)
}

func rowsOf(t *testing.T, p map[string]json.RawMessage) []map[string]json.RawMessage {
	t.Helper()
	var rows []map[string]json.RawMessage
	if raw, ok := p["rows"]; ok {
		if err := json.Unmarshal(raw, &rows); err != nil {
			t.Fatalf("rows: %v", err)
		}
	}
	return rows
}

var postsShape = &TableShape{RestrictedColumns: []string{"secret"}, PrimaryKey: []string{"id"}}

func TestAfterHookIsLeftToTheChangeLogWhenItIsActive(t *testing.T) {
	s := newAfterStack(t, true, postsShape)
	s.send(t, http.MethodPost, "/posts", `{"id":7,"title":"x"}`, "Prefer", "return=representation")
	if hooks, _ := s.settle(); len(hooks) != 0 {
		t.Fatalf("the listener delivers this write; the request path must not fire it too: %v", hooks)
	}
}

func TestATableWithoutAShapeKeepsTheRequestPathEvenWithTheChangeLog(t *testing.T) {
	s := newAfterStack(t, true, nil)
	s.send(t, http.MethodPost, "/posts", `{"id":7,"title":"x"}`, "Prefer", "return=representation")
	hooks, reads := s.settle()
	if len(hooks) != 1 || len(reads) != 0 {
		t.Fatalf("an unshaped table behaves as released: hooks %d, reads %d", len(hooks), len(reads))
	}
	if string(rowsOf(t, hooks[0])[0]["title"]) != `"from-representation"` {
		t.Fatalf("rows should be the representation as before: %s", hooks[0]["rows"])
	}
}

func TestFallbackReadsAnInsertBackByKeyAsTheServiceRole(t *testing.T) {
	s := newAfterStack(t, false, postsShape)
	// No Prefer: the case that used to give an after hook no rows.
	s.send(t, http.MethodPost, "/posts", `{"id":7,"title":"x"}`)
	hooks, reads := s.settle()
	if len(hooks) != 1 || len(reads) != 1 {
		t.Fatalf("want one read-back and one hook call, got reads %d hooks %d", len(reads), len(hooks))
	}
	if got := reads[0].Header.Get("Authorization"); got != "Bearer "+serviceKey {
		t.Fatalf("read-back must run as the service role, got %q", got)
	}
	if got := reads[0].URL.Query().Get("id"); got != `in.("7")` {
		t.Fatalf("read-back filter = %q", got)
	}
	rows := rowsOf(t, hooks[0])
	if len(rows) != 1 || string(rows[0]["title"]) != `"stored"` {
		t.Fatalf("rows should be the stored row: %s", hooks[0]["rows"])
	}
	if _, leaked := rows[0]["secret"]; leaked {
		t.Fatal("the service-role read-back leaked a restricted column")
	}
}

func TestFallbackReadsAnUpdateByKeyBack(t *testing.T) {
	s := newAfterStack(t, false, postsShape)
	s.send(t, http.MethodPatch, "/posts?id=eq.7", `{"title":"y"}`)
	hooks, reads := s.settle()
	if len(reads) != 1 || reads[0].URL.Query().Get("id") != `in.("7")` {
		t.Fatalf("an update by key should be read back by key: %v", reads)
	}
	if len(rowsOf(t, hooks[0])) != 1 {
		t.Fatalf("update rows: %s", hooks[0]["rows"])
	}
}

func TestFallbackDoesNotReadBackAFilterItCannotPinToKeys(t *testing.T) {
	s := newAfterStack(t, false, postsShape)
	// After a patch the filter may match different rows, or none, so a read-back would lie.
	s.send(t, http.MethodPatch, "/posts?status=eq.draft", `{"status":"live"}`, "Prefer", "return=representation")
	hooks, reads := s.settle()
	if len(reads) != 0 {
		t.Fatalf("a non-key filter must not be read back: %v", reads[0].URL)
	}
	rows := rowsOf(t, hooks[0])
	if string(rows[0]["title"]) != `"from-representation"` {
		t.Fatalf("falls back to the representation, as today: %s", hooks[0]["rows"])
	}
	if _, kept := rows[0]["secret"]; kept {
		t.Fatal("representation rows get the same column stripping")
	}
}

func TestFallbackDeleteKeepsTheFilter(t *testing.T) {
	s := newAfterStack(t, false, postsShape)
	s.send(t, http.MethodDelete, "/posts?id=eq.7", "")
	hooks, reads := s.settle()
	if len(reads) != 0 {
		t.Fatal("a deleted row cannot be read back")
	}
	if string(hooks[0]["filter"]) != `"id=eq.7"` {
		t.Fatalf("delete filter: %s", hooks[0]["filter"])
	}
}

func TestFallbackUsesTheRepresentationWhenTheReadBackFails(t *testing.T) {
	s := newAfterStack(t, false, postsShape)
	s.readErr = true
	s.send(t, http.MethodPost, "/posts", `{"id":7,"title":"x"}`, "Prefer", "return=representation")
	hooks, _ := s.settle()
	if len(hooks) != 1 || string(rowsOf(t, hooks[0])[0]["title"]) != `"from-representation"` {
		t.Fatalf("a failed read-back should not cost the hook its call: %v", hooks)
	}
}

func TestWrittenKeys(t *testing.T) {
	pk := []string{"id"}
	cases := []struct {
		name  string
		op    Operation
		body  string
		query string
		want  string
	}{
		{"insert one", OpInsert, `{"id":1,"t":"a"}`, "", `[{"id":"1"}]`},
		{"insert many", OpInsert, `[{"id":1},{"id":"x,\"y"}]`, "", `[{"id":"1"},{"id":"x,\"y"}]`},
		{"insert without key", OpInsert, `[{"id":1},{"t":"a"}]`, "", `null`},
		{"update by key", OpUpdate, `{"t":"a"}`, "id=eq.5", `[{"id":"5"}]`},
		{"update moving the key", OpUpdate, `{"id":6}`, "id=eq.5", `[{"id":"6"}]`},
		{"update with another filter", OpUpdate, `{}`, "id=eq.5&t=eq.a", `null`},
		{"update with select is fine", OpUpdate, `{}`, "id=eq.5&select=id", `[{"id":"5"}]`},
		{"update by in", OpUpdate, `{}`, "id=in.(5,6)", `null`},
		{"delete", OpDelete, ``, "id=eq.5", `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := json.Marshal(writtenKeys(tc.op, []byte(tc.body), tc.query, pk))
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
	composite, _ := json.Marshal(writtenKeys(OpUpdate, []byte(`{}`), "a=eq.1&b=eq.2", []string{"a", "b"}))
	if string(composite) != `[{"a":"1","b":"2"}]` {
		t.Fatalf("composite: %s", composite)
	}
	if writtenKeys(OpInsert, []byte(`{"id":1}`), "", nil) != nil {
		t.Fatal("no primary key means nothing to read back by")
	}
}

func TestKeyFilterQuotesValues(t *testing.T) {
	single := writtenRowsFilter([]string{"id"}, []map[string]string{{"id": "1"}, {"id": `a"b,c`}})
	if got := single.Get("id"); got != `in.("1","a\"b,c")` {
		t.Fatalf("single key filter = %s", got)
	}
	composite := writtenRowsFilter([]string{"a", "b"}, []map[string]string{{"a": "1", "b": "x"}})
	if got := composite.Get("or"); got != `(and(a.eq."1",b.eq."x"))` {
		t.Fatalf("composite filter = %s", got)
	}
}

const markSecret = "mark-secret"

func (s *afterStack) mark() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeMark
}

func TestTheRequestPathMarksTheWriteItFiresFor(t *testing.T) {
	s := newAfterStack(t, false, postsShape)
	s.send(t, http.MethodPost, "/posts", `{"id":7}`, RequestPathHeader, "forged")
	s.settle()
	table, ok := NewRequestPathMarks(markSecret).Verify(s.mark())
	if !ok || table != "posts" {
		t.Fatalf("the write should carry a signed mark for posts, got %q", s.mark())
	}
}

func TestNoMarkReachesPostgRESTWhenTheRequestPathDoesNotFire(t *testing.T) {
	active := newAfterStack(t, true, postsShape)
	active.send(t, http.MethodPost, "/posts", `{"id":7}`, RequestPathHeader, "forged")
	active.settle()
	if active.mark() != "" {
		t.Fatalf("a client-sent mark must be removed, got %q", active.mark())
	}

	unshaped := newAfterStack(t, false, nil)
	unshaped.send(t, http.MethodPost, "/posts", `{"id":7}`)
	unshaped.settle()
	if unshaped.mark() != "" {
		t.Fatal("the listener does not handle an unshaped table, so there is nothing to mark")
	}
}
