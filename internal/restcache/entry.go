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

func encodeEntry(e Entry) ([]byte, error) {
	return json.Marshal(e)
}

func decodeEntry(data []byte) (Entry, error) {
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// DecodeEntryForAdmin decodes a stored cache payload for admin/CLI introspection.
func DecodeEntryForAdmin(data []byte) (Entry, error) {
	return decodeEntry(data)
}
