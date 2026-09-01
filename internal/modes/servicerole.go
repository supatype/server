package modes

import (
	"net/http"
	"strings"
)

// ServiceRoleBearer reports whether the request carries exactly key as a bearer
// token in its Authorization header.
//
// Three packages had grown their own copy of this comparison: the functions
// admin API, the admin API and the SQL runner. The copies had drifted, and the
// differences that remain are deliberate and belong to the caller, not here:
// they answer 401 or 403, and they differ over whether dev mode waves a request
// through. Only the comparison is shared.
//
// An empty key never matches, so a deployment that forgot to configure one fails
// closed rather than accepting any request, or worse, an empty bearer token.
func ServiceRoleBearer(r *http.Request, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	token, ok := strings.CutPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ")
	if !ok {
		return false
	}
	return strings.TrimSpace(token) == key
}
