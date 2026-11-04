package modelhooks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWrittenKeysRefusesWhatItCannotPin(t *testing.T) {
	cases := []struct {
		name  string
		op    Operation
		body  string
		query string
		pk    []string
	}{
		{"an empty insert", OpInsert, `[]`, "", []string{"id"}},
		{"an insert that is not JSON", OpInsert, `{oops`, "", []string{"id"}},
		{"a query that does not parse", OpUpdate, `{}`, "id=%zz", []string{"id"}},
		{"a key given twice", OpUpdate, `{}`, "id=eq.1&id=eq.2", []string{"id"}},
		{"half a composite key", OpUpdate, `{}`, "a=eq.1", []string{"a", "b"}},
	}
	for _, tc := range cases {
		if got := writtenKeys(tc.op, []byte(tc.body), tc.query, tc.pk); got != nil {
			t.Errorf("%s: got %v, want no read-back", tc.name, got)
		}
	}
}

func TestAPatchThatIsNotAnObjectKeepsTheFilterKey(t *testing.T) {
	got := writtenKeys(OpUpdate, []byte(`[1]`), "id=eq.5", []string{"id"})
	if len(got) != 1 || got[0]["id"] != "5" {
		t.Fatalf("got %v", got)
	}
}

func TestKeyTextRefusesABrokenString(t *testing.T) {
	if _, ok := keyText(json.RawMessage(`"\q"`)); ok {
		t.Fatal("a string that does not decode is not a key")
	}
}

func TestStripRowsSendsAnEmptyArrayRatherThanNull(t *testing.T) {
	if got := string(stripRows(nil, TableShape{}, EventAfterChange)); got != "[]" {
		t.Fatalf("a read-back that found nothing is [] to the hook, got %s", got)
	}
}

func TestRowsWithoutAReaderUseTheRepresentation(t *testing.T) {
	rec := &recorder{captureBody: true}
	rec.body.WriteString(`[{"id":1}]`)
	req := httptest.NewRequest(http.MethodPost, "/posts", nil)
	got := (&AfterRows{}).rows(context.Background(), req, Target{Table: "posts", Operation: OpInsert}, []byte(`{"id":1}`), rec, TableShape{PrimaryKey: []string{"id"}})
	if string(got) != `[{"id":1}]` {
		t.Fatalf("got %s", got)
	}
}

// doerFunc is a Doer from a function.
type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

// failingBody errors on read.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (failingBody) Close() error             { return nil }

func readerWith(base string, client Doer) *RowReader {
	return NewRowReader(func(*http.Request) string { return base }, nil, "", client)
}

func TestByKeysReportsEveryWayTheReadCanFail(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/posts", nil)
	keys := []map[string]string{{"id": "1"}}
	read := func(r *RowReader) error {
		_, err := r.ByKeys(context.Background(), req, "posts", []string{"id"}, keys)
		return err
	}
	if read(readerWith("", nil)) == nil {
		t.Error("no PostgREST URL")
	}
	if read(readerWith("http://bad host", nil)) == nil {
		t.Error("an unparseable URL")
	}
	if read(readerWith("http://x", doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("refused")
	}))) == nil {
		t.Error("a refused connection")
	}
	if read(readerWith("http://x", doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: failingBody{}}, nil
	}))) == nil {
		t.Error("a body cut off mid-read")
	}
	if read(readerWith("http://x", doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("not json"))}, nil
	}))) == nil {
		t.Error("an answer that is not JSON")
	}
}

func TestByKeysAsksForTheRequestSchema(t *testing.T) {
	var profile string
	client := doerFunc(func(r *http.Request) (*http.Response, error) {
		profile = r.Header.Get("Accept-Profile")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("[]"))}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/posts", nil)
	for _, schema := range []string{"app", ""} {
		r := NewRowReader(func(*http.Request) string { return "http://x" }, func(*http.Request) string { return schema }, "", client)
		if _, err := r.ByKeys(context.Background(), req, "posts", []string{"id"}, []map[string]string{{"id": "1"}}); err != nil {
			t.Fatal(err)
		}
		if profile != schema {
			t.Fatalf("Accept-Profile = %q, want %q", profile, schema)
		}
	}
}

func TestAMarkNamingNoValidTableDoesNotVerify(t *testing.T) {
	m := NewRequestPathMarks("s")
	for _, table := range []string{"!!", ""} {
		token := table + ".n." + m.sign(table, "n")
		if _, ok := m.Verify(token); ok {
			t.Fatalf("a signed mark whose table is %q must not verify", table)
		}
	}
}
