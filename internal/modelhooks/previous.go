package modelhooks

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PreviousPathPrefix is where the callback is mounted. Sent to a hook as a **path**, not a URL: the
// server has no portable way to know its own in-network address, while the worker is already told how
// to reach the stack (`SUPATYPE_INTERNAL_URL`). The generated adapter joins the two.
const PreviousPathPrefix = "/hooks/v1/previous/"

// previousTTL bounds a callback token's life. Generous next to a hook's default 2s timeout, and short
// enough that a token copied out of a log is useless by the time anyone reads it.
const previousTTL = 30 * time.Second

// DefaultPreviousLimit caps how many rows a hook can pull back.
//
// A hook on an unfiltered `PATCH` would otherwise be handed the table. Over the cap the answer carries
// `truncated: true` so the hook can refuse rather than act on a prefix it did not know was a prefix.
const DefaultPreviousLimit = 100

// previousClaims is what a callback token carries. Self-contained, so the endpoint keeps no state and
// a restart cannot strand an in-flight request.
type previousClaims struct {
	Table  string `json:"t"`
	Filter string `json:"f"`
	Expiry int64  `json:"x"`
}

// Callback mints and serves the `previous()` endpoint.
//
// Reads run as the **service role**, so a hook sees the rows as stored — RLS bypassed, field masking
// not applied. That is deliberate: a hook validating against a masked column would otherwise compare
// with `NULL` and pass. It is also not a privilege the hook lacks, since the worker already holds the
// service-role key; the endpoint is a convenience, not an escalation.
//
// The token is what keeps it from being a general "read any rows matching any filter" surface: it
// pins the table and the filter to one in-flight request and expires with it.
type Callback struct {
	key            []byte
	restBase       func(*http.Request) string
	schemaFor      func(*http.Request) string
	serviceRoleKey string
	limit          int
	client         Doer
}

// NewCallback builds the endpoint. The signing key is generated here and never leaves the process, so
// tokens are useless to anything but this server, and useless at all after a restart.
func NewCallback(
	restBase func(*http.Request) string,
	schemaFor func(*http.Request) string,
	serviceRoleKey string,
	client Doer,
) (*Callback, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating a hook callback key: %w", err)
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Callback{
		key:            key,
		restBase:       restBase,
		schemaFor:      schemaFor,
		serviceRoleKey: serviceRoleKey,
		limit:          DefaultPreviousLimit,
		client:         client,
	}, nil
}

// Path returns the callback path for one request's table and filter, or "" when there is nothing to
// fetch — an insert has no prior rows, so the context type omits `previous` entirely.
func (c *Callback) Path(op Operation, table, filter string) string {
	if c == nil || op == OpInsert || table == "" {
		return ""
	}
	claims := previousClaims{Table: table, Filter: filter, Expiry: time.Now().Add(previousTTL).Unix()}
	encoded, err := json.Marshal(claims)
	if err != nil {
		return ""
	}
	payload := base64.RawURLEncoding.EncodeToString(encoded)
	return PreviousPathPrefix + payload + "." + c.sign(payload)
}

func (c *Callback) sign(payload string) string {
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (c *Callback) verify(token string) (previousClaims, error) {
	payload, signature, found := strings.Cut(token, ".")
	if !found {
		return previousClaims{}, fmt.Errorf("malformed token")
	}
	if !hmac.Equal([]byte(signature), []byte(c.sign(payload))) {
		return previousClaims{}, fmt.Errorf("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return previousClaims{}, fmt.Errorf("undecodable token")
	}
	var claims previousClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return previousClaims{}, fmt.Errorf("unreadable token")
	}
	if time.Now().Unix() > claims.Expiry {
		return previousClaims{}, fmt.Errorf("expired token")
	}
	return claims, nil
}

// previousResponse is what a hook receives from the callback: the affected rows as stored, and
// whether the cap trimmed them.
type previousResponse struct {
	Rows      json.RawMessage `json:"rows"`
	Truncated bool            `json:"truncated"`
}

// Handler serves the callback. Mounted at PreviousPathPrefix on the outer mux.
func (c *Callback) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"message": "POST only"})
			return
		}
		token := strings.TrimPrefix(req.URL.Path, "/")
		claims, err := c.verify(token)
		if err != nil {
			// No detail: a caller poking at this endpoint learns only that the token was not good.
			writeJSON(w, http.StatusForbidden, map[string]string{"message": "Invalid hook callback token"})
			return
		}

		rows, truncated, err := c.fetch(req, claims)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"message": "Could not read the affected rows"})
			return
		}

		// Encoded from a struct rather than assembled by hand. The rows are row *content* — values a
		// caller wrote — so streaming them into the response with string concatenation puts
		// attacker-influenced bytes past the encoder, which gosec flags as tainted output (G705) and
		// which would also emit invalid JSON for a nil `rows`. `nosniff` because a JSON content type is
		// only a promise until the browser is told not to guess.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if len(rows) == 0 {
			rows = json.RawMessage("[]")
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(previousResponse{Rows: rows, Truncated: truncated})
	})
}

// fetch reads the affected rows through PostgREST, reusing the request's own filter verbatim.
//
// Through PostgREST rather than SQL so the filter needs no parsing: the string the caller sent is the
// string that selects the rows, which is also why a hook cannot widen it — the server never takes one
// from the hook.
func (c *Callback) fetch(req *http.Request, claims previousClaims) (json.RawMessage, bool, error) {
	base := strings.TrimRight(c.restBase(req), "/")
	if base == "" {
		return nil, false, fmt.Errorf("no PostgREST URL")
	}

	target := base + "/" + claims.Table
	query := claims.Filter
	// One over the cap, so a full page is distinguishable from a truncated one.
	limit := "limit=" + strconv.Itoa(c.limit+1)
	if query == "" {
		query = limit
	} else {
		query += "&" + limit
	}
	target += "?" + query
	if _, err := url.Parse(target); err != nil {
		return nil, false, err
	}

	fetch, err := http.NewRequestWithContext(req.Context(), http.MethodGet, target, nil)
	if err != nil {
		return nil, false, err
	}
	fetch.Header.Set("Accept", "application/json")
	if c.serviceRoleKey != "" {
		fetch.Header.Set("apikey", c.serviceRoleKey)
		fetch.Header.Set("Authorization", "Bearer "+c.serviceRoleKey)
	}
	if c.schemaFor != nil {
		if schema := c.schemaFor(req); schema != "" {
			fetch.Header.Set("Accept-Profile", schema)
		}
	}

	res, err := c.client.Do(fetch)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, false, fmt.Errorf("PostgREST answered %d", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, maxVerdictBytes))
	if err != nil {
		return nil, false, err
	}

	var rows []json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, false, err
	}
	truncated := len(rows) > c.limit
	if truncated {
		rows = rows[:c.limit]
	}
	trimmed, err := json.Marshal(rows)
	if err != nil {
		return nil, false, err
	}
	return trimmed, truncated, nil
}

// encodeClaims is the token payload encoding, exposed for tests that need to mint by hand.
func encodeClaims(claims []byte) string {
	return base64.RawURLEncoding.EncodeToString(claims)
}
