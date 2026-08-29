package maskedfields

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/supatype/server/internal/studiobootstrap"
)

// Only a bare table path describes this table's columns. An RPC returns whatever its
// function returns, and a nested path is not a PostgREST table route — annotating either
// would be a claim about the wrong column set.
func TestTableFromPath(t *testing.T) {
	cases := map[string]string{
		"/posts":            "posts",
		"posts":             "posts",
		"/posts/":           "posts",
		"/rpc/do_thing":     "",
		"/posts/1":          "",
		"/":                 "",
		"":                  "",
		"/weird.table_name": "weird.table_name",
	}

	for path, want := range cases {
		if got := tableFromPath(path); got != want {
			t.Errorf("tableFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// stating returns a Lookup that answers with exactly this classification.
func stating(tables map[string][]studiobootstrap.FieldMask) Lookup {
	return func(context.Context) (map[string][]studiobootstrap.FieldMask, bool) {
		return tables, true
	}
}

// unreadable is the classification that could not be loaded.
func unreadable(context.Context) (map[string][]studiobootstrap.FieldMask, bool) {
	return nil, false
}

// serve runs one request through the middleware and returns the recorded response.
func serve(t *testing.T, lookup Lookup, path string) *httptest.ResponseRecorder {
	t.Helper()

	var sawHeaderDownstream string
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sawHeaderDownstream = w.Header().Get(Header)
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	Middleware(lookup, next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	// The header has to be set before the proxy writes, because headers cannot be
	// added once the status line is out. Checking it downstream is what proves the
	// ordering rather than merely the value.
	if got := rec.Header().Get(Header); got != sawHeaderDownstream {
		t.Errorf("header was set after the handler ran: downstream saw %q, response carries %q",
			sawHeaderDownstream, got)
	}
	return rec
}

func TestHeaderNamesTheRestrictedColumnsAndTheirScope(t *testing.T) {
	lookup := stating(map[string][]studiobootstrap.FieldMask{
		"posts": {
			{Column: "ssn", RowDependent: false},
			{Column: "salary", RowDependent: true},
		},
	})

	got := serve(t, lookup, "/posts").Header().Get(Header)

	// The order the classification came in is not something to depend on, so
	// compare the set.
	entries := strings.Split(got, ", ")
	sort.Strings(entries)
	want := []string{"salary=row", "ssn=identity"}
	if len(entries) != len(want) || entries[0] != want[0] || entries[1] != want[1] {
		t.Errorf("header = %q, want the entries %v", got, want)
	}
}

// `identity` and `row` are not interchangeable. A client that read a row-dependent
// column as uniformly masked would hide values the caller was entitled to see.
func TestScopeDistinguishesRowFromIdentity(t *testing.T) {
	for name, tc := range map[string]struct {
		mask studiobootstrap.FieldMask
		want string
	}{
		"the same verdict for every row": {studiobootstrap.FieldMask{Column: "ssn"}, "ssn=identity"},
		"a verdict that varies by row":   {studiobootstrap.FieldMask{Column: "ssn", RowDependent: true}, "ssn=row"},
	} {
		lookup := stating(map[string][]studiobootstrap.FieldMask{"posts": {tc.mask}})
		if got := serve(t, lookup, "/posts").Header().Get(Header); got != tc.want {
			t.Errorf("%s: header = %q, want %q", name, got, tc.want)
		}
	}
}

// An absent header means "not stated". Every case that cannot support a claim has
// to produce absence, not an empty header, which a client would read as "nothing
// is masked".
func TestTheHeaderIsAbsentRatherThanEmpty(t *testing.T) {
	restricted := map[string][]studiobootstrap.FieldMask{"posts": {{Column: "ssn"}}}

	for name, tc := range map[string]struct {
		lookup Lookup
		path   string
	}{
		"the classification could not be read": {unreadable, "/posts"},
		"no lookup was supplied":               {nil, "/posts"},
		"the table restricts nothing":          {stating(restricted), "/comments"},
		"the table has an empty mask list":     {stating(map[string][]studiobootstrap.FieldMask{"posts": {}}), "/posts"},
		"nothing anywhere is restricted":       {stating(map[string][]studiobootstrap.FieldMask{}), "/posts"},
		"the classification is nil but read":   {stating(nil), "/posts"},
		"an RPC call":                          {stating(restricted), "/rpc/do_thing"},
		"a nested path":                        {stating(restricted), "/posts/1"},
		"the collection root":                  {stating(restricted), "/"},
	} {
		rec := serve(t, tc.lookup, tc.path)
		if _, present := rec.Header()[http.CanonicalHeaderKey(Header)]; present {
			t.Errorf("%s: header should be absent, got %q", name, rec.Header().Get(Header))
		}
	}
}

// The middleware annotates; it does not answer. Whatever the request was going to
// get, it still gets.
func TestTheRequestIsAlwaysPassedOn(t *testing.T) {
	for name, lookup := range map[string]Lookup{
		"with a classification":    stating(map[string][]studiobootstrap.FieldMask{"posts": {{Column: "ssn"}}}),
		"without a classification": unreadable,
	} {
		var reached bool
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("downstream"))
		})

		rec := httptest.NewRecorder()
		Middleware(lookup, next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/posts", nil))

		if !reached {
			t.Errorf("%s: the downstream handler was not reached", name)
		}
		if rec.Code != http.StatusTeapot || rec.Body.String() != "downstream" {
			t.Errorf("%s: got %d %q", name, rec.Code, rec.Body.String())
		}
	}
}

// The lookup is given the request's context, so a cancelled request does not leave
// a database call running behind it.
func TestTheLookupIsGivenTheRequestContext(t *testing.T) {
	type key struct{}
	var got bool

	lookup := func(ctx context.Context) (map[string][]studiobootstrap.FieldMask, bool) {
		got = ctx.Value(key{}) == "carried"
		return nil, false
	}

	req := httptest.NewRequest(http.MethodGet, "/posts", nil)
	req = req.WithContext(context.WithValue(req.Context(), key{}, "carried"))
	Middleware(lookup, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), req)

	if !got {
		t.Error("the lookup was called with a context other than the request's")
	}
}

// The classification is not consulted for a path that could not be annotated
// anyway, so an RPC call costs nothing.
func TestNoLookupForAPathThatCannotBeAnnotated(t *testing.T) {
	var calls int
	lookup := func(context.Context) (map[string][]studiobootstrap.FieldMask, bool) {
		calls++
		return nil, false
	}

	for _, path := range []string{"/rpc/do_thing", "/posts/1", "/"} {
		Middleware(lookup, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
			ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if calls != 0 {
		t.Errorf("the classification was looked up %d times for paths that cannot carry the header", calls)
	}
}
