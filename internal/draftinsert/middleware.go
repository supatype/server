// Package draftinsert names `supatype.create_record` when a plain insert into a model that
// keeps drafts is refused for asking for its row back.
//
// A new record on a drafts model is unpublished. When the model's read rule hides
// unpublished records (`published_at <= now()`), PostgREST's `POST /<table>` with
// `Prefer: return=representation` fails with `42501 new row violates row-level security
// policy`: Postgres checks the returned row against the read rule as it is inserted, before
// anything has made the row visible to its author. Nothing in that message says what to do
// instead, and the database cannot add a hint to a refusal it raises itself, so the gateway
// adds one here: create the record with `supatype.create_record`, or several at once with
// `supatype.create_records`, which insert without reading back and write the first draft.
//
// Advisory only, as the masked-fields header is. The refusal and its status are PostgREST's;
// this adds a `hint` to the body when PostgREST sent none, and changes nothing else.
package draftinsert

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Hint is what a refused insert into a drafts model is told.
const Hint = "This model keeps drafts: a new record starts unpublished, and asking for it back " +
	"is refused when the read rule hides unpublished records. Create a record with " +
	"supatype.create_record, or several at once with supatype.create_records (the Supatype " +
	"client's insert does this for you)."

// rlsRefusal is the start of Postgres's message for a row its policies refuse on write.
const rlsRefusal = "new row violates row-level security policy"

// Lookup answers which tables belong to models that keep drafts.
//
// Taken as a value, so what this middleware writes can be stated in a test rather than
// arranged in a database.
type Lookup func(ctx context.Context) (map[string]bool, bool)

// Middleware adds [Hint] to a refused insert into a drafts table.
//
// Only a `POST` to a bare table path is looked at, and only its 403 is held back to be read;
// every other response streams through untouched. Which tables keep drafts is asked only of a
// refusal, so an insert that succeeds costs nothing.
func Middleware(lookup Lookup, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		table := tableFromPath(req.URL.Path)
		if req.Method != http.MethodPost || table == "" || lookup == nil {
			next.ServeHTTP(w, req)
			return
		}
		hinting := &hintingWriter{ResponseWriter: w}
		next.ServeHTTP(hinting, req)
		hinting.finish(func() bool { return keepsDrafts(req.Context(), lookup, table) })
	})
}

// keepsDrafts reports whether the table belongs to a model that keeps drafts.
func keepsDrafts(ctx context.Context, lookup Lookup, table string) bool {
	tables, ok := lookup(ctx)
	return ok && tables[table]
}

// tableFromPath reads the table from a path the `/rest/v1` prefix has been stripped from,
// e.g. `/posts`. An RPC call or a nested path is not an insert into a table.
func tableFromPath(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return ""
	}
	return trimmed
}

// hintingWriter holds back a 403 so its body can be read, and passes anything else through.
type hintingWriter struct {
	http.ResponseWriter
	held   bool
	status int
	body   bytes.Buffer
}

// WriteHeader holds a plain 403 back. An encoded body (gzip) is passed through as it is,
// since reading it would mean decoding and re-encoding a refusal for one advisory field.
func (w *hintingWriter) WriteHeader(status int) {
	if status == http.StatusForbidden && w.Header().Get("Content-Encoding") == "" {
		w.held, w.status = true, status
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *hintingWriter) Write(p []byte) (int, error) {
	if w.held {
		return w.body.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// Flush passes through for a response that is not held; a held one is written whole at the end.
func (w *hintingWriter) Flush() {
	if !w.held {
		_ = http.NewResponseController(w.ResponseWriter).Flush()
	}
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (w *hintingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// finish writes a held response, with the hint added when the table keeps drafts and the
// body is the refusal the hint is about.
func (w *hintingWriter) finish(drafts func() bool) {
	if !w.held {
		return
	}
	body := w.body.Bytes()
	if drafts() {
		body = withHint(body)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(body)
}

// withHint returns PostgREST's error body with [Hint] set, when it is a row-level security
// refusal carrying no hint of its own. Any other body comes back unchanged.
func withHint(body []byte) []byte {
	var refusal map[string]json.RawMessage
	if json.Unmarshal(body, &refusal) != nil {
		return body
	}
	var code, message string
	_ = json.Unmarshal(refusal["code"], &code)
	_ = json.Unmarshal(refusal["message"], &message)
	if code != "42501" || !strings.HasPrefix(message, rlsRefusal) || hasHint(refusal["hint"]) {
		return body
	}
	// Neither marshal can fail: a string, and a map of values that were valid JSON a moment ago.
	refusal["hint"], _ = json.Marshal(Hint)
	out, _ := json.Marshal(refusal)
	return out
}

// hasHint reports whether PostgREST already sent a hint, which is then left as it is.
func hasHint(raw json.RawMessage) bool {
	var hint string
	return json.Unmarshal(raw, &hint) == nil && hint != ""
}
