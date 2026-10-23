package studioauth

import (
	"net/http"

	"github.com/supatype/server/internal/utilities"
)

// A refusal from the Studio APIs carries two fields: `error`, a stable machine
// code naming what happened, and `message`, the sentence for a person. The code
// follows the status, so a client can branch on either and they never disagree.
//
// It used to be "forbidden" whatever happened, so a project with no schema
// pushed, a database that could not be read and a malformed body all looked
// like an access refusal to anything reading the code.
//
// The names are the Cloud control plane's, so a Studio client reads one
// vocabulary whichever host answers it.
var errorCodes = map[int]string{
	http.StatusBadRequest:          "validation_error",
	http.StatusUnauthorized:        "unauthorized",
	http.StatusForbidden:           "forbidden",
	http.StatusNotFound:            "not_found",
	http.StatusMethodNotAllowed:    "method_not_allowed",
	http.StatusConflict:            "conflict",
	http.StatusInternalServerError: "internal_error",
	http.StatusNotImplemented:      "not_implemented",
	http.StatusServiceUnavailable:  "unavailable",
}

// errorCode is the machine code for a status. A status with no code of its own
// takes its class's: the caller's fault or the server's.
func errorCode(status int) string {
	if code, ok := errorCodes[status]; ok {
		return code
	}
	if status >= http.StatusInternalServerError {
		return errorCodes[http.StatusInternalServerError]
	}
	return errorCodes[http.StatusBadRequest]
}

func errorBody(status int, message string) map[string]interface{} {
	return map[string]interface{}{"error": errorCode(status), "message": message}
}

// WriteErrorBody answers a failed request with its status, code and message.
func WriteErrorBody(w http.ResponseWriter, status int, message string) {
	utilities.WriteJSON(w, status, errorBody(status, message))
}

// refusalStatus is 401 for a caller who has not signed in and 403 for one who
// has and is still refused.
func refusalStatus(result Result) int {
	if result.Message == "Authentication required" {
		return http.StatusUnauthorized
	}
	return http.StatusForbidden
}

// writeRefusal answers a caller ResolveAccess did not admit.
func writeRefusal(w http.ResponseWriter, result Result) {
	WriteErrorBody(w, refusalStatus(result), result.Message)
}
