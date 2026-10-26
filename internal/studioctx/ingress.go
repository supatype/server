package studioctx

import (
	"encoding/json"
	"net/http"
	"strings"
)

// RefuseForgedClaim answers 401 to a request that carries a reserved claim and
// did not come through the Studio proxy. Mount it on every public data-plane
// route a Studio token could unlock.
//
// Every place a token can travel is checked: the Authorization header, the
// `apikey` header, and each query value, since Realtime and signed links carry
// their token in the query string.
func RefuseForgedClaim(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !IsProxied(req.Context()) && requestCarriesReservedClaim(req) {
			refuse(w)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func requestCarriesReservedClaim(req *http.Request) bool {
	for _, token := range presentedTokens(req) {
		if TokenCarriesReservedClaim(token) {
			return true
		}
	}
	return false
}

// presentedTokens is every value in the request that could be a bearer token.
func presentedTokens(req *http.Request) []string {
	tokens := []string{strings.TrimSpace(req.Header.Get("apikey"))}
	if auth := strings.TrimSpace(req.Header.Get("Authorization")); len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
		tokens = append(tokens, strings.TrimSpace(auth[7:]))
	}
	for _, values := range req.URL.Query() {
		tokens = append(tokens, values...)
	}
	return tokens
}

func refuse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    "reserved_claim",
		"message": "This token carries a claim only Studio may set.",
	})
}
