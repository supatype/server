package utilities

import (
	"encoding/json"
	"net/http"
)

// WriteJSON sends v as the response body with this status.
//
// Seven packages had grown their own copy of these three lines, three of them
// still spelling the parameter `interface{}`, and one of them logging an
// encode failure the other six discarded.
//
// The encode error is discarded here too, deliberately: it can only happen
// after the status line and headers are already on the wire, so there is
// nothing left to tell the caller and nothing for the server to do about it.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
