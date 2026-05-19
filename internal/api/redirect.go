package api

import "net/http"

func redirectFound(w http.ResponseWriter, r *http.Request, target string) {
	// #nosec G710 -- callers pass OAuth provider URLs or redirect targets validated against the configured allow-list.
	http.Redirect(w, r, target, http.StatusFound)
}
