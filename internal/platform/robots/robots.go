// Package robots keeps crawlers out of deployments that are not production.
//
// A staging or preview stack is a real, reachable copy of somebody's site. If a
// crawler indexes it, the copy competes with the original in search results and
// the customer finds out from their rankings.
package robots

import (
	"net/http"
	"strings"
)

// botUASubstrings are matched case-insensitively anywhere in the user agent.
// Substrings rather than exact names because crawlers carry version and URL
// detail around their own name.
var botUASubstrings = []string{
	"googlebot", "bingbot", "slurp", "duckduckbot", "baiduspider",
	"yandexbot", "gptbot", "bytespider", "claudebot", "anthropic",
	"ccbot", "chatgpt", "petalbot", "semrush", "ahrefs",
}

// IsBot reports whether the user agent looks like a crawler.
func IsBot(userAgent string) bool {
	lowered := strings.ToLower(userAgent)
	for _, needle := range botUASubstrings {
		if strings.Contains(lowered, needle) {
			return true
		}
	}
	return false
}

// IsPrefetch reports whether the browser is speculatively fetching rather than
// acting on an intent. A prefetch is not somebody using the project, so it does
// not count as activity.
func IsPrefetch(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Sec-Purpose"), "prefetch") ||
		strings.EqualFold(r.Header.Get("Purpose"), "prefetch")
}

// Policy is what a deployment does about crawlers.
type Policy struct {
	// NonProd marks a staging or preview deployment. Every response is told not
	// to be indexed and /robots.txt disallows everything.
	NonProd bool
	// BlockBots additionally refuses a request that looks like a crawler. Only
	// consulted when NonProd, because refusing crawlers in production would hide
	// the customer's own site from search.
	BlockBots bool
}

// Disallowed is the body served at /robots.txt on a non-production deployment.
const Disallowed = "User-agent: *\nDisallow: /\n"

// Handle applies the policy. It reports true when it has answered the request
// itself and the caller should stop.
func (p Policy) Handle(w http.ResponseWriter, r *http.Request) bool {
	if !p.NonProd {
		return false
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if r.URL.Path == "/robots.txt" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(Disallowed))
		return true
	}
	if p.BlockBots && IsBot(r.UserAgent()) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return true
	}
	return false
}
