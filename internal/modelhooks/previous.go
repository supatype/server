package modelhooks

import (
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/utilities"
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
//
// Caller is the request's bearer token, sealed: the path travels to the worker and may be logged on
// the way, and a caller's credential must not be readable there.
type previousClaims struct {
	Table  string    `json:"t"`
	Op     Operation `json:"o"`
	Filter string    `json:"f"`
	Caller string    `json:"c,omitempty"`
	Expiry int64     `json:"x"`
}

// PrimaryKeyFunc returns a table's primary key columns, in order, or nil when they are not known.
type PrimaryKeyFunc func(req *http.Request, table string) []string

// CallbackOptions configures NewCallback.
type CallbackOptions struct {
	// RestBase and SchemaFor resolve the PostgREST URL and schema for the callback request.
	RestBase  func(*http.Request) string
	SchemaFor func(*http.Request) string
	// ServiceRoleKey reads the affected rows back unmasked.
	ServiceRoleKey string
	// PrimaryKey names the key the rows are read back by. Without one the read fails closed.
	PrimaryKey PrimaryKeyFunc
	// Client defaults to a 10s http.Client.
	Client Doer
}

// Callback mints and serves the `previous()` endpoint.
//
// It answers with the rows the write will actually change, as stored, in two reads. The first runs
// as the **caller**, with the request's filter plus the generated `can_update_<t>` or
// `can_delete_<t>` check, and selects only the primary key: that is the caller's own rule deciding
// which rows are in play. The second runs as the **service role** by those keys, so the hook sees
// every column unmasked: a hook validating against a masked column would otherwise compare with
// `NULL` and pass. Reading as the service role with the request's filter alone would hand a hook
// rows the caller cannot change, which a validating hook can wrongly reject on and a rejection can
// echo back to the caller.
//
// The token is what keeps it from being a general "read any rows matching any filter" surface: it
// pins the table, the operation, the filter and the caller to one in-flight request and expires
// with it.
type Callback struct {
	key            []byte
	seal           cipher.AEAD
	restBase       func(*http.Request) string
	schemaFor      func(*http.Request) string
	serviceRoleKey string
	primaryKey     PrimaryKeyFunc
	limit          int
	client         Doer
}

// NewCallback builds the endpoint. The signing key is generated here and never leaves the process, so
// tokens are useless to anything but this server, and useless at all after a restart.
func NewCallback(opts CallbackOptions) *Callback {
	// crypto/rand.Read does not fail; a short read would be a broken runtime
	// rather than a condition to handle.
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Callback{
		key:            key,
		seal:           sealerFor(key),
		restBase:       opts.RestBase,
		schemaFor:      opts.SchemaFor,
		serviceRoleKey: opts.ServiceRoleKey,
		primaryKey:     opts.PrimaryKey,
		limit:          DefaultPreviousLimit,
		client:         client,
	}
}

// Path returns the callback path for one request's table and filter, or "" when there is nothing to
// fetch: an insert has no prior rows, so the context type omits `previous` entirely.
func (c *Callback) Path(req *http.Request, op Operation, table, filter string) string {
	if c == nil || op == OpInsert || table == "" {
		return ""
	}
	claims := previousClaims{
		Table:  table,
		Op:     op,
		Filter: filter,
		Caller: c.sealCaller(bearerToken(req)),
		Expiry: time.Now().Add(previousTTL).Unix(),
	}
	// previousClaims is strings and an integer, so it cannot fail to encode.
	encoded, _ := json.Marshal(claims)
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
			utilities.WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"message": "POST only"})
			return
		}
		token := strings.TrimPrefix(req.URL.Path, "/")
		claims, err := c.verify(token)
		if err != nil {
			// No detail: a caller poking at this endpoint learns only that the token was not good.
			utilities.WriteJSON(w, http.StatusForbidden, map[string]string{"message": "Invalid hook callback token"})
			return
		}

		rows, truncated, err := c.fetch(req, claims)
		if err != nil {
			// The error never carries the caller's token: it is opened only to be sent to PostgREST.
			logrus.WithError(err).WithField("table", claims.Table).Warn("previous(): could not read the affected rows")
			utilities.WriteJSON(w, http.StatusBadGateway, map[string]string{"message": "Could not read the affected rows"})
			return
		}
		writeRows(w, rows, truncated)
	})
}

// writeRows encodes the answer from a struct rather than assembling it by hand. The rows are row
// *content*, values a caller wrote, so streaming them into the response with string concatenation
// puts attacker-influenced bytes past the encoder, which gosec flags as tainted output (G705) and
// which would also emit invalid JSON for a nil `rows`. `nosniff` because a JSON content type is only
// a promise until the browser is told not to guess.
func writeRows(w http.ResponseWriter, rows json.RawMessage, truncated bool) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(previousResponse{Rows: rows, Truncated: truncated})
}
