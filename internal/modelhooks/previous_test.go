package modelhooks

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// callbackFor wires a Callback against a fake PostgREST that echoes what it was asked for.
func callbackFor(t *testing.T, rows int) (*Callback, func() string) {
	t.Helper()

	var lastURL string
	var lastAuth string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastURL = r.URL.String()
		lastAuth = r.Header.Get("Authorization")
		out := make([]map[string]any, 0, rows)
		for i := 0; i < rows; i++ {
			out = append(out, map[string]any{"id": fmt.Sprint(i), "title": "stored"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(rest.Close)

	cb, err := NewCallback(
		func(*http.Request) string { return rest.URL },
		func(*http.Request) string { return "public" },
		"service-role-key",
		rest.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return cb, func() string { return lastURL + " auth=" + lastAuth }
}

func post(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, path, nil))
	return rr
}

func TestPreviousReturnsTheAffectedRows(t *testing.T) {
	cb, seen := callbackFor(t, 2)
	path := strings.TrimPrefix(cb.Path(OpUpdate, "posts", "status=eq.draft"), PreviousPathPrefix)

	rr := post(t, cb.Handler(), "/"+path)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body)
	}

	var body struct {
		Rows      []map[string]any `json:"rows"`
		Truncated bool             `json:"truncated"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Rows) != 2 || body.Truncated {
		t.Fatalf("rows = %d truncated = %v, want 2 and false", len(body.Rows), body.Truncated)
	}

	// The request's own filter, verbatim — the server never takes one from the hook, which is what
	// stops a hook widening `id=eq.1` into `id=gt.0`.
	if got := seen(); !strings.Contains(got, "status=eq.draft") {
		t.Fatalf("PostgREST call = %s, want the request's filter", got)
	}
	// Read as the service role, so the hook sees rows as stored rather than as the caller's
	// projection: a masked column read as the caller would come back NULL and a validation against
	// it would pass on nothing.
	if got := seen(); !strings.Contains(got, "auth=Bearer service-role-key") {
		t.Fatalf("PostgREST call = %s, want the service-role key", got)
	}
}

func TestPreviousTruncatesAtTheCap(t *testing.T) {
	cb, _ := callbackFor(t, DefaultPreviousLimit+5)
	path := strings.TrimPrefix(cb.Path(OpUpdate, "posts", ""), PreviousPathPrefix)

	rr := post(t, cb.Handler(), "/"+path)

	var body struct {
		Rows      []map[string]any `json:"rows"`
		Truncated bool             `json:"truncated"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)

	// Capped rather than streamed: an unfiltered PATCH would otherwise hand a hook the table.
	if len(body.Rows) != DefaultPreviousLimit {
		t.Fatalf("rows = %d, want the cap of %d", len(body.Rows), DefaultPreviousLimit)
	}
	// And it says so, so a hook can refuse rather than act on a prefix it did not know was a prefix.
	if !body.Truncated {
		t.Fatal("truncated = false when rows were dropped")
	}
}

func TestPreviousRefusesTokensItDidNotMint(t *testing.T) {
	cb, _ := callbackFor(t, 1)
	valid := strings.TrimPrefix(cb.Path(OpUpdate, "posts", "id=eq.1"), PreviousPathPrefix)
	payload, signature, _ := strings.Cut(valid, ".")

	// A token whose claims were edited to widen the filter or change the table — the attack the
	// signature exists to stop.
	forged := strings.TrimPrefix(cb.Path(OpUpdate, "salaries", ""), PreviousPathPrefix)
	forgedPayload, _, _ := strings.Cut(forged, ".")

	cases := []struct{ name, token string }{
		{"no signature", payload},
		{"wrong signature", payload + ".AAAA"},
		{"claims swapped under a valid signature", forgedPayload + "." + signature},
		{"empty", ""},
		{"garbage", "not-a-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := post(t, cb.Handler(), "/"+tc.token)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rr.Code)
			}
			// No detail: poking at this endpoint should teach nothing about why a token failed.
			if strings.Contains(rr.Body.String(), "signature") || strings.Contains(rr.Body.String(), "expired") {
				t.Fatalf("body leaks why the token failed: %s", rr.Body)
			}
		})
	}
}

func TestPreviousRejectsAnExpiredToken(t *testing.T) {
	cb, _ := callbackFor(t, 1)
	// Mint by hand with an expiry in the past, since Path always mints a live one.
	claims, _ := json.Marshal(previousClaims{Table: "posts", Filter: "id=eq.1", Expiry: 1})
	payload := encodeClaims(claims)
	rr := post(t, cb.Handler(), "/"+payload+"."+cb.sign(payload))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an expired token", rr.Code)
	}
}

func TestPreviousPathIsAbsentForAnInsert(t *testing.T) {
	// There are no prior rows, and the generated context type omits `previous` for an insert — so
	// handing one over would offer a call that can only return nothing useful.
	cb, _ := callbackFor(t, 1)
	if got := cb.Path(OpInsert, "posts", ""); got != "" {
		t.Fatalf("path = %q, want empty for an insert", got)
	}
}

func TestPreviousTokensDoNotSurviveARestart(t *testing.T) {
	// The key is per-process, so a token that leaked into a log is useless later — and there is no
	// state to strand mid-request either way.
	first, _ := callbackFor(t, 1)
	second, _ := callbackFor(t, 1)

	token := strings.TrimPrefix(first.Path(OpUpdate, "posts", "id=eq.1"), PreviousPathPrefix)
	if rr := post(t, second.Handler(), "/"+token); rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want another process to refuse the token", rr.Code)
	}
}

// encodeClaims is the token payload encoding, here rather than in previous.go
// because only this file mints a token by hand.
func encodeClaims(claims []byte) string {
	return base64.RawURLEncoding.EncodeToString(claims)
}
