package modelhooks

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// RequestPathHeader tells the database, and through the actor marker the change-log listener, that
// the request path fired this write's after hook itself. Set by the middleware only; a value a
// client sent is removed before the request goes on.
const RequestPathHeader = "X-Supatype-After-Path"

// RequestPathMarks mints and checks the header's value: the table, a nonce and an HMAC over both.
//
// Signed because any role can write a marker, so the listener must not skip a delivery on an
// unsigned claim. Keyed from the JWT secret, which every replica shares, so a mark minted on one
// replica verifies on the one holding the reader lock.
type RequestPathMarks struct {
	key []byte
}

// NewRequestPathMarks derives the key from secret, separated from the secret's other uses.
func NewRequestPathMarks(secret string) *RequestPathMarks {
	if secret == "" {
		return &RequestPathMarks{}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("supatype.after-path"))
	return &RequestPathMarks{key: mac.Sum(nil)}
}

// Mint returns a mark for table, or "" when there is no key to sign it with.
func (m *RequestPathMarks) Mint(table string) string {
	if m == nil || len(m.key) == 0 {
		return ""
	}
	nonce := make([]byte, 12)
	// crypto/rand.Read does not fail.
	_, _ = rand.Read(nonce)
	t := base64.RawURLEncoding.EncodeToString([]byte(table))
	n := base64.RawURLEncoding.EncodeToString(nonce)
	return t + "." + n + "." + m.sign(t, n)
}

// Verify returns the table a genuine mark names.
func (m *RequestPathMarks) Verify(token string) (string, bool) {
	if m == nil || len(m.key) == 0 {
		return "", false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || !hmac.Equal([]byte(parts[2]), []byte(m.sign(parts[0], parts[1]))) {
		return "", false
	}
	table, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(table) == 0 {
		return "", false
	}
	return string(table), true
}

func (m *RequestPathMarks) sign(table, nonce string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(table + "." + nonce))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
