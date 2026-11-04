package changelog

import (
	"encoding/json"
	"strings"
)

// Marker prefixes, written with pg_logical_emit_message(true, prefix, content).
const (
	// PrefixActor carries who made an API request: {"sub","role","email","requestId"}. Emitted by
	// the PostgREST pre-request function, so it is the first message of an API transaction.
	PrefixActor = "supatype.actor"
	// PrefixHooks with content "skip" asks the listener to skip a seed or import transaction.
	PrefixHooks = "supatype.hooks"
	// skipContent is the only PrefixHooks content with a meaning.
	skipContent = "skip"
)

// SlotOptions are the wal2json options the slot is read with, as SQL literals in order.
//
// Format 2 is one row per change, framed by B and C rows, so a large transaction never has to be
// held as one JSON document. Numerics are left as JSON numbers on purpose: the rows go to a hook
// as JSON, and the literal survives decoding here as a json.RawMessage, so a bigint past 2^53 or a
// numeric's scale reaches the hook exactly as Postgres printed it, which is how PostgREST prints it.
var SlotOptions = []string{
	"format-version", "2",
	"include-pk", "1",
	"include-lsn", "1",
	"include-transaction", "1",
	"include-types", "1",
}

// record is one wal2json format 2 row.
type record struct {
	Action        string   `json:"action"`
	LSN           string   `json:"lsn"`
	NextLSN       string   `json:"nextlsn"`
	Schema        string   `json:"schema"`
	Table         string   `json:"table"`
	Columns       []column `json:"columns"`
	Identity      []column `json:"identity"`
	PK            []column `json:"pk"`
	Transactional bool     `json:"transactional"`
	Prefix        string   `json:"prefix"`
	Content       string   `json:"content"`
}

type column struct {
	Name  string          `json:"name"`
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

// rowOf turns wal2json columns into a JSON row, or nil when there are none.
func rowOf(columns []column) map[string]json.RawMessage {
	if len(columns) == 0 {
		return nil
	}
	row := make(map[string]json.RawMessage, len(columns))
	for _, c := range columns {
		row[c.Name] = valueOf(c)
	}
	return row
}

// valueOf is a column's value as the hook should see it, before rendering.
//
// wal2json prints json and jsonb as a JSON string holding the document; this hands back the
// document itself. It prints bytea as bare hex, which Postgres would read back as escape-format
// text; this restores the `\x` prefix so the value means the same bytes. Every other value is
// passed through as wal2json printed it, and rendering turns it into what PostgREST returns.
func valueOf(c column) json.RawMessage {
	if len(c.Value) == 0 {
		return json.RawMessage("null")
	}
	if c.Value[0] != '"' {
		return c.Value
	}
	switch {
	case isJSONType(c.Type):
		return unwrapJSON(c.Value)
	case strings.EqualFold(strings.TrimSpace(c.Type), "bytea"):
		return prefixHex(c.Value)
	}
	return c.Value
}

func unwrapJSON(v json.RawMessage) json.RawMessage {
	var inner string
	if err := json.Unmarshal(v, &inner); err != nil || !json.Valid([]byte(inner)) {
		return v
	}
	return json.RawMessage(inner)
}

func prefixHex(v json.RawMessage) json.RawMessage {
	var hex string
	if err := json.Unmarshal(v, &hex); err != nil || strings.HasPrefix(hex, `\x`) {
		return v
	}
	// A string cannot fail to encode.
	encoded, _ := json.Marshal(`\x` + hex)
	return encoded
}

func isJSONType(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	return t == "json" || t == "jsonb"
}

func keyNames(pk []column) []string {
	if len(pk) == 0 {
		return nil
	}
	names := make([]string, len(pk))
	for i, c := range pk {
		names[i] = c.Name
	}
	return names
}

func opOf(action string) string {
	switch action {
	case "I":
		return "insert"
	case "U":
		return "update"
	case "D":
		return "delete"
	}
	return ""
}
