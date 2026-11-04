package modelhooks

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// sealerFor derives the key that seals a caller's token from the per-process signing key, so the
// signature and the seal never share a key, and builds the AEAD over it.
func sealerFor(key []byte) cipher.AEAD {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("previous-caller-seal"))
	// A 32-byte key is always a valid AES-256 key, and GCM over AES cannot fail to build, so neither
	// error is a condition to handle.
	block, _ := aes.NewCipher(mac.Sum(nil))
	aead, _ := cipher.NewGCM(block)
	return aead
}

func (c *Callback) sealCaller(token string) string {
	if token == "" {
		return ""
	}
	nonce := make([]byte, c.seal.NonceSize())
	_, _ = rand.Read(nonce)
	return base64.RawURLEncoding.EncodeToString(c.seal.Seal(nonce, nonce, []byte(token), nil))
}

func (c *Callback) openCaller(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	size := c.seal.NonceSize()
	if err != nil || len(raw) < size {
		return "", errors.New("unreadable caller token")
	}
	plain, err := c.seal.Open(nil, raw[:size], raw[size:], nil)
	if err != nil {
		return "", errors.New("unreadable caller token")
	}
	return string(plain), nil
}

// callerBearer opens the caller's token and refuses one that has expired. PostgREST verifies the
// signature on the read itself; the expiry is checked here as well so a lapsed caller is refused
// before anything is read on their behalf. An empty result is an anonymous caller.
func (c *Callback) callerBearer(claims previousClaims) (string, error) {
	token, err := c.openCaller(claims.Caller)
	if err != nil || token == "" {
		return token, err
	}
	parsed := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, parsed); err != nil {
		return "", errors.New("caller token is not a JWT")
	}
	exp, err := parsed.GetExpirationTime()
	if err != nil {
		return "", errors.New("caller token has an unreadable expiry")
	}
	if exp != nil && time.Now().After(exp.Time) {
		return "", errors.New("caller token has expired")
	}
	return token, nil
}

// fetch answers previous(): the caller's rule picks the keys, the service role reads the rows.
func (c *Callback) fetch(req *http.Request, claims previousClaims) (json.RawMessage, bool, error) {
	base := strings.TrimRight(c.restBase(req), "/")
	if base == "" {
		return nil, false, errors.New("no PostgREST URL")
	}
	key := c.keyFor(req, claims.Table)
	if len(key) == 0 {
		return nil, false, fmt.Errorf("no primaryKey for table %q in the manifest's tables map, so its rows cannot be read back by key", claims.Table)
	}
	bearer, err := c.callerBearer(claims)
	if err != nil {
		return nil, false, err
	}
	keys, truncated, err := c.readCallerKeys(req, base, claims, key, bearer)
	if err != nil {
		return nil, false, err
	}
	if len(keys) == 0 {
		return json.RawMessage("[]"), truncated, nil
	}
	rows, err := c.readByKeys(req, base, claims.Table, key, keys)
	return rows, truncated, err
}

func (c *Callback) keyFor(req *http.Request, table string) []string {
	if c.primaryKey == nil {
		return nil
	}
	return c.primaryKey(req, table)
}

// readCallerKeys is the first read: as the caller, the request's own filter plus the generated
// check for the operation, primary key columns only, one over the cap so a full page is
// distinguishable from a truncated one. A table with no generated check makes PostgREST refuse the
// unknown column, and that refusal is returned rather than worked around.
func (c *Callback) readCallerKeys(req *http.Request, base string, claims previousClaims, key []string, bearer string) ([]map[string]json.RawMessage, bool, error) {
	check, err := checkFor(claims.Op, claims.Table)
	if err != nil {
		return nil, false, err
	}
	query := joinQuery(
		withoutParams(claims.Filter, "select", "limit"),
		"select="+url.QueryEscape(strings.Join(key, ",")),
		url.QueryEscape(check)+"=is.true",
		"limit="+strconv.Itoa(c.limit+1),
	)
	body, err := c.get(req, base+"/"+claims.Table+"?"+query, bearer)
	if err != nil {
		return nil, false, fmt.Errorf("reading the keys as the caller: %w", err)
	}
	var keys []map[string]json.RawMessage
	if err := json.Unmarshal(body, &keys); err != nil {
		return nil, false, err
	}
	truncated := len(keys) > c.limit
	if truncated {
		keys = keys[:c.limit]
	}
	return keys, truncated, nil
}

// readByKeys is the second read: as the service role, by key only, so every column comes back as
// stored and nothing outside the caller's rows can.
func (c *Callback) readByKeys(req *http.Request, base, table string, key []string, keys []map[string]json.RawMessage) (json.RawMessage, error) {
	filter, err := keyFilter(key, keys)
	if err != nil {
		return nil, err
	}
	body, err := c.get(req, base+"/"+table+"?"+filter, c.serviceRoleKey)
	if err != nil {
		return nil, fmt.Errorf("reading the rows as the service role: %w", err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []json.RawMessage{}
	}
	// A slice of raw messages that each decoded from JSON cannot fail to encode.
	out, _ := json.Marshal(rows)
	return out, nil
}

func checkFor(op Operation, table string) (string, error) {
	switch op {
	case OpUpdate:
		return "can_update_" + table, nil
	case OpDelete:
		return "can_delete_" + table, nil
	}
	return "", fmt.Errorf("no previous rows for operation %q", op)
}

// get runs one PostgREST read and returns the body for the caller to decode into the shape it needs.
// bearer is sent as the credential; an empty one reads as anon.
func (c *Callback) get(req *http.Request, target, bearer string) ([]byte, error) {
	fetch, err := c.newRead(req, target, bearer)
	if err != nil {
		return nil, err
	}
	res, err := c.client.Do(fetch)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxVerdictBytes))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, postgrestError(res.StatusCode, body)
	}
	return body, nil
}

func (c *Callback) newRead(req *http.Request, target, bearer string) (*http.Request, error) {
	fetch, err := http.NewRequestWithContext(req.Context(), http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	fetch.Header.Set("Accept", "application/json")
	if bearer != "" {
		fetch.Header.Set("Authorization", "Bearer "+bearer)
	}
	if bearer != "" && bearer == c.serviceRoleKey {
		fetch.Header.Set("apikey", bearer)
	}
	if c.schemaFor != nil {
		if schema := c.schemaFor(req); schema != "" {
			fetch.Header.Set("Accept-Profile", schema)
		}
	}
	return fetch, nil
}

// postgrestError names PostgREST's own error code, which is what tells "no generated check"
// (42703, an unknown column) apart from a refused credential or a bad filter in the log.
func postgrestError(status int, body []byte) error {
	var detail struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &detail)
	return fmt.Errorf("PostgREST answered %d (code %q: %s)", status, detail.Code, detail.Message)
}

// keyFilter selects exactly the given rows by key: `col=in.(...)` for a single column, and an `or`
// of `and`s for a composite key. Every value is double-quoted, so a comma, a parenthesis or a quote
// inside a key cannot change the filter's shape.
func keyFilter(key []string, rows []map[string]json.RawMessage) (string, error) {
	terms := make([]string, 0, len(rows))
	for _, row := range rows {
		parts := make([]string, 0, len(key))
		for _, column := range key {
			literal, err := keyLiteral(row, column)
			if err != nil {
				return "", err
			}
			parts = append(parts, literal)
		}
		terms = append(terms, rowTerm(key, parts))
	}
	if len(key) == 1 {
		return url.QueryEscape(key[0]) + "=" + url.QueryEscape("in.("+strings.Join(terms, ",")+")"), nil
	}
	return "or=" + url.QueryEscape("("+strings.Join(terms, ",")+")"), nil
}

// rowTerm is one row's part of keyFilter: the bare literal for a single-column key, or
// `and(col.eq.literal,...)` for a composite one.
func rowTerm(key, literals []string) string {
	if len(key) == 1 {
		return literals[0]
	}
	parts := make([]string, len(key))
	for i, column := range key {
		parts[i] = column + ".eq." + literals[i]
	}
	return "and(" + strings.Join(parts, ",") + ")"
}

// keyLiteral renders one key value as a double-quoted PostgREST literal.
func keyLiteral(row map[string]json.RawMessage, column string) (string, error) {
	raw, ok := row[column]
	trimmed := strings.TrimSpace(string(raw))
	if !ok || trimmed == "" || trimmed == "null" {
		return "", fmt.Errorf("a row came back without a value for key column %q", column)
	}
	value := trimmed
	switch trimmed[0] {
	case '"':
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
	case '{', '[':
		return "", fmt.Errorf("key column %q is not a scalar", column)
	}
	escaped := strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
	return `"` + escaped + `"`, nil
}

// withoutParams drops the named parameters from a raw query, keeping every other part verbatim so
// the request's filter reaches PostgREST exactly as the caller sent it.
func withoutParams(raw string, names ...string) string {
	if raw == "" {
		return ""
	}
	kept := make([]string, 0)
	for _, part := range strings.Split(raw, "&") {
		name, _, _ := strings.Cut(part, "=")
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = decoded
		}
		if !slices.Contains(names, name) {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "&")
}

func joinQuery(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "&")
}
