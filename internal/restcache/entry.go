package restcache

import (
	"encoding/json"
	"time"
)

// Entry is a cached upstream REST response stored in Valkey.
type Entry struct {
	StatusCode  int       `json:"status_code"`
	ContentType string    `json:"content_type"`
	Body        []byte    `json:"body"`
	CachedAt    time.Time `json:"cached_at"`
	Table       string    `json:"table,omitempty"`
	Scope       string    `json:"scope,omitempty"`
	Method      string    `json:"method,omitempty"`
	Path        string    `json:"path,omitempty"`
	RawQuery    string    `json:"raw_query,omitempty"`
}

// DecodeEntry reads a stored cache payload.
//
// Exported because the admin API lists and inspects entries, which is the same
// read this package does on a hit: one decode, not two spellings of it.
func DecodeEntry(data []byte) (Entry, error) {
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// marshalEntry is a seam. An Entry is plain data, so encoding one cannot fail
// and the error branch in storeEntry is unreachable in production. The branch
// is kept because writing a truncated entry would be worse than reporting, and
// the test replaces this to prove it reports.
var marshalEntry = json.Marshal
