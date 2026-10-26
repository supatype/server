// Package corsorigins decides which browser origins may call a deployment's API.
//
// Outside dev mode the rule is the same on every host:
//
//   - The default is the project's own site URL, plus the origins its configuration names
//     (SUPATYPE_CORS_ALLOW_ORIGINS, which the CLI fills from `api.corsOrigins` in
//     supatype.config.ts, and on Cloud the tenant manifest's list).
//   - A list saved from Studio replaces that default until it is reset, except for the site URL,
//     which stays allowed whatever is saved: the project's own app is never locked out.
//   - Platform origins (Cloud's own Studio, which signs members in to the project's auth from the
//     browser) are always allowed and cannot be removed from the project.
//   - `*` allows every origin, and only when someone wrote it down.
//
// A standalone server used to answer every origin with `*` when no list was configured, because the
// auth service's own CORS handler defaulted to it and nothing in front of it said otherwise.
package corsorigins

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Wildcard allows every origin. It is honoured only when written explicitly.
const Wildcard = "*"

// OriginOf reduces a URL to its origin (scheme, host and any port), or "" when it has none.
func OriginOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

// ParseList splits a comma-separated list into trimmed, non-empty entries.
func ParseList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Union joins lists in order, dropping blanks and repeats.
func Union(lists ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range lists {
		for _, v := range list {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// ErrInvalidOrigin is returned for an entry that is neither an origin nor the wildcard.
var ErrInvalidOrigin = errors.New("invalid origin")

// ErrNoStore is returned for a save or reset on a deployment with no database to keep the list in.
var ErrNoStore = errors.New("this deployment has no database to keep a saved origin list in")

// Normalise checks a list someone wants saved and returns it in canonical form. Each entry must be
// `*` or an origin: an http or https scheme and a host, with an optional port and nothing else. A
// path, query or fragment is refused rather than trimmed, so what is saved is what was meant.
func Normalise(origins []string) ([]string, error) {
	out := make([]string, 0, len(origins))
	for _, raw := range origins {
		entry := strings.TrimSpace(raw)
		if entry == Wildcard {
			out = append(out, Wildcard)
			continue
		}
		u, err := url.Parse(entry)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, fmt.Errorf("%w: %q (use scheme://host[:port], or *)", ErrInvalidOrigin, raw)
		}
		out = append(out, OriginOf(entry))
	}
	return Union(out), nil
}
