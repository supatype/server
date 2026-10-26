package studioauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supatype/server/internal/data"
	"github.com/supatype/server/internal/studiomembers"
)

// A membership change the store could not make because the database is missing
// or failed is the server's fault, and says so the way listing members does. Only
// what the store refused on the caller's account is the caller's to fix.
func TestMemberChangeErrorsSayWhoseFaultTheyAre(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want int
		code string
	}{
		"no such user":           {studiomembers.ErrUnknownUser, http.StatusNotFound, "not_found"},
		"the last admin":         {studiomembers.ErrLastAdmin, http.StatusConflict, "conflict"},
		"their own role":         {studiomembers.ErrOwnRole, http.StatusForbidden, "forbidden"},
		"their own access":       {studiomembers.ErrOwnAccess, http.StatusForbidden, "forbidden"},
		"no database":            {data.ErrNoDatabase, http.StatusServiceUnavailable, "unavailable"},
		"a failed database call": {fmt.Errorf("check user: %w", errors.New("connection refused")), http.StatusServiceUnavailable, "unavailable"},
	} {
		rec := httptest.NewRecorder()
		writeMemberError(rec, tc.err)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
			continue
		}
		var body struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: body is not JSON: %v", name, err)
		}
		if body.Error != tc.code {
			t.Errorf("%s: error = %q, want %q", name, body.Error, tc.code)
		}
	}
}

// A database failure's own text stays in the server: the caller gets a sentence,
// not the driver's error.
func TestAFailedMemberChangeKeepsTheDatabaseErrorToItself(t *testing.T) {
	rec := httptest.NewRecorder()
	writeMemberError(rec, fmt.Errorf("check user: %w", errors.New("password authentication failed for user x")))
	if got := rec.Body.String(); got == "" || strings.Contains(got, "password") || strings.Contains(got, "check user") {
		t.Errorf("body = %s, want a plain sentence without the database error", got)
	}
}
