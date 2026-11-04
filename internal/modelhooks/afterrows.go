package modelhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// maxReadBackKeys caps how many rows the request path reads back by key. A bigger bulk insert uses
// the caller's representation, as before, rather than building a filter longer than a URL allows.
const maxReadBackKeys = 100

// AfterRows decides where an after hook's rows come from. Optional: without it the request path
// behaves as released, showing rows only when the caller asked for return=representation.
type AfterRows struct {
	// ChangeLogActive reports whether the change-log listener delivers after hooks, in which case
	// the request path leaves every table with a known shape to it.
	ChangeLogActive func() bool
	// Shapes returns the manifest's table shapes for a request.
	Shapes func(*http.Request) ShapeFunc
	// Reader reads written rows back as the service role, for the request-path fallback.
	Reader *RowReader
	// Marks signs the RequestPathHeader the request path sends when it fires an after hook the
	// listener would otherwise also deliver.
	Marks *RequestPathMarks
}

// mark sets the request-path header, only when this path fires for a table the listener also
// handles. Middleware has already removed any the client sent.
func (a *AfterRows) mark(req *http.Request, table string, fires bool) {
	if a == nil || !fires {
		return
	}
	if token := a.Marks.Mint(table); token != "" {
		req.Header.Set(RequestPathHeader, token)
	}
}

// route reports the table's shape, and whether the request path should fire its after hook at all.
func (a *AfterRows) route(req *http.Request, table string) (TableShape, bool, bool) {
	if a == nil || a.Shapes == nil {
		return TableShape{}, false, true
	}
	shape, known := a.Shapes(req)(table)
	if known && a.ChangeLogActive != nil && a.ChangeLogActive() {
		return shape, known, false
	}
	return shape, known, true
}

// rows is what the request-path after hook is shown: the written rows read back by key when the
// write pins them, else the caller's representation, either way without restricted columns.
func (a *AfterRows) rows(ctx context.Context, req *http.Request, target Target, body []byte, rec *recorder, shape TableShape) json.RawMessage {
	representation := json.RawMessage(nil)
	if rec.captureBody && rec.body.Len() > 0 {
		representation = json.RawMessage(rec.body.Bytes())
	}
	if a == nil || a.Reader == nil {
		return representation
	}
	if keys := writtenKeys(target.Operation, body, req.URL.RawQuery, shape.PrimaryKey); keys != nil {
		read, err := a.Reader.ByKeys(ctx, req, target.Table, shape.PrimaryKey, keys)
		if err == nil {
			return stripRows(read, shape, target.AfterEvent)
		}
	}
	var parsed []map[string]json.RawMessage
	if err := json.Unmarshal(representation, &parsed); err != nil {
		return nil
	}
	return stripRows(parsed, shape, target.AfterEvent)
}

func stripRows(rows []map[string]json.RawMessage, shape TableShape, event string) json.RawMessage {
	out := shape.StripAll(event, rows)
	if out == nil {
		out = []map[string]json.RawMessage{}
	}
	// Rows decoded from JSON re-encode without error.
	encoded, _ := json.Marshal(out)
	return encoded
}

// writtenKeys returns the key of every row a write touched, as text, or nil when the write does not
// pin them: an insert whose rows lack the key, an update filtered by anything but the key, any
// delete (the rows are gone), or more rows than are worth a read-back.
func writtenKeys(op Operation, body []byte, query string, pk []string) []map[string]string {
	if len(pk) == 0 {
		return nil
	}
	switch op {
	case OpInsert:
		return insertKeys(body, pk)
	case OpUpdate:
		return updateKeys(body, query, pk)
	}
	return nil
}

func insertKeys(body []byte, pk []string) []map[string]string {
	var rows []map[string]json.RawMessage
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] != '[' {
		trimmed = append(append([]byte("["), trimmed...), ']')
	}
	if err := json.Unmarshal(trimmed, &rows); err != nil || len(rows) == 0 || len(rows) > maxReadBackKeys {
		return nil
	}
	keys := make([]map[string]string, len(rows))
	for i, row := range rows {
		key, ok := keyOf(row, pk)
		if !ok {
			return nil
		}
		keys[i] = key
	}
	return keys
}

// readOnlyParams are query parameters that shape a response without choosing rows.
var readOnlyParams = map[string]bool{"select": true, "columns": true, "on_conflict": true}

func updateKeys(body []byte, query string, pk []string) []map[string]string {
	params, err := url.ParseQuery(query)
	if err != nil {
		return nil
	}
	key := map[string]string{}
	for name, values := range params {
		if readOnlyParams[name] {
			continue
		}
		if len(values) != 1 {
			return nil
		}
		value, isEq := strings.CutPrefix(values[0], "eq.")
		if !isEq || !slices.Contains(pk, name) {
			return nil
		}
		key[name] = value
	}
	if len(key) != len(pk) {
		return nil
	}
	return []map[string]string{movedKey(body, key, pk)}
}

// movedKey applies a patch that changes the key itself, so the read-back finds the row where it is.
func movedKey(patch []byte, key map[string]string, pk []string) map[string]string {
	var row map[string]json.RawMessage
	if json.Unmarshal(patch, &row) != nil {
		return key
	}
	for _, name := range pk {
		if v, ok := row[name]; ok {
			if text, ok := keyText(v); ok {
				key[name] = text
			}
		}
	}
	return key
}

func keyOf(row map[string]json.RawMessage, pk []string) (map[string]string, bool) {
	key := make(map[string]string, len(pk))
	for _, name := range pk {
		text, ok := keyText(row[name])
		if !ok {
			return nil, false
		}
		key[name] = text
	}
	return key, true
}

// keyText is a JSON key value as PostgREST filter text: a string unquoted, a number or boolean as
// written. Null, objects and arrays cannot be a key value.
func keyText(v json.RawMessage) (string, bool) {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || bytes.Equal(v, []byte("null")) || v[0] == '{' || v[0] == '[' {
		return "", false
	}
	if v[0] == '"' {
		var s string
		if json.Unmarshal(v, &s) != nil {
			return "", false
		}
		return s, true
	}
	return string(v), true
}

// writtenRowsFilter selects the rows with the given keys. Every value is double-quoted, PostgREST's escape
// for values holding commas, dots or parentheses.
func writtenRowsFilter(pk []string, keys []map[string]string) url.Values {
	q := url.Values{}
	if len(pk) == 1 {
		quoted := make([]string, len(keys))
		for i, k := range keys {
			quoted[i] = quote(k[pk[0]])
		}
		q.Set(pk[0], "in.("+strings.Join(quoted, ",")+")")
		return q
	}
	ands := make([]string, len(keys))
	for i, k := range keys {
		terms := make([]string, len(pk))
		for j, name := range pk {
			terms[j] = name + ".eq." + quote(k[name])
		}
		ands[i] = "and(" + strings.Join(terms, ",") + ")"
	}
	q.Set("or", "("+strings.Join(ands, ",")+")")
	return q
}

// keyEscaper escapes a value for the inside of a double-quoted PostgREST literal.
var keyEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func quote(s string) string {
	return `"` + keyEscaper.Replace(s) + `"`
}

// RowReader reads rows through PostgREST as the service role, so a hook sees the row as stored
// whatever the caller may read, before column stripping is applied.
type RowReader struct {
	restBase       func(*http.Request) string
	schemaFor      func(*http.Request) string
	serviceRoleKey string
	client         Doer
}

// NewRowReader builds a reader. A nil client means a default with a timeout.
func NewRowReader(restBase, schemaFor func(*http.Request) string, serviceRoleKey string, client Doer) *RowReader {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &RowReader{restBase: restBase, schemaFor: schemaFor, serviceRoleKey: serviceRoleKey, client: client}
}

// ByKeys reads the rows with the given keys.
func (r *RowReader) ByKeys(ctx context.Context, req *http.Request, table string, pk []string, keys []map[string]string) ([]map[string]json.RawMessage, error) {
	base := strings.TrimRight(r.restBase(req), "/")
	if base == "" {
		return nil, fmt.Errorf("no PostgREST URL")
	}
	q := writtenRowsFilter(pk, keys)
	q.Set("limit", strconv.Itoa(len(keys)))
	fetch, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+url.PathEscape(table)+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	r.authorise(fetch, req)
	res, err := r.client.Do(fetch)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("PostgREST answered %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, MaxBodyBytes))
	if err != nil {
		return nil, err
	}
	var rows []map[string]json.RawMessage
	return rows, json.Unmarshal(raw, &rows)
}

func (r *RowReader) authorise(fetch, req *http.Request) {
	fetch.Header.Set("Accept", "application/json")
	if r.serviceRoleKey != "" {
		fetch.Header.Set("apikey", r.serviceRoleKey)
		fetch.Header.Set("Authorization", "Bearer "+r.serviceRoleKey)
	}
	if r.schemaFor != nil {
		if schema := r.schemaFor(req); schema != "" {
			fetch.Header.Set("Accept-Profile", schema)
		}
	}
}
