package studioauth

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
)

// auditedRequest builds an elevated write from user-1; the caller adjusts it
// before forwardThroughProxy.
func auditedRequest(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer "+bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"}))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ActingModeHeader, ModeElevated)
	return req
}

// onlyRecord is the single audit row a forwarded request left.
func onlyRecord(t *testing.T, audit *fakeAuditor, next *bodyUpstream, rec *httptest.ResponseRecorder) {
	t.Helper()
	if !next.reached || len(audit.records) != 1 {
		t.Fatalf("status %d, reached %v, records %v", rec.Code, next.reached, audit.records)
	}
}

// A chunked body, whose length is not known up front, is still digested when
// it fits under the limit, and reaches upstream whole.
func TestAChunkedWriteIsDigested(t *testing.T) {
	body := `{"title":"chunked"}`
	req := auditedRequest(t, http.MethodPost, "/rest/v1/posts", strings.NewReader(body))
	req.ContentLength = -1
	audit := &fakeAuditor{}
	next, rec := forwardThroughProxy(req, "admin", audit)
	onlyRecord(t, audit, next, rec)
	want := fmt.Sprintf("sha256:%x; %d bytes", sha256.Sum256([]byte(body)), len(body))
	if got := audit.records[0].Body; !strings.HasPrefix(got, want) {
		t.Errorf("body summary %q, want %q", got, want)
	}
	if next.body != body {
		t.Errorf("upstream body %q, want %q", next.body, body)
	}
}

// A chunked body over the limit is recorded by size, and still reaches
// upstream whole.
func TestAChunkedWriteOverTheLimitIsSentWhole(t *testing.T) {
	body := `{"x":"` + strings.Repeat("a", auditBodyHashLimit) + `"}`
	req := auditedRequest(t, http.MethodPost, "/rest/v1/posts", strings.NewReader(body))
	req.ContentLength = -1
	audit := &fakeAuditor{}
	next, rec := forwardThroughProxy(req, "admin", audit)
	onlyRecord(t, audit, next, rec)
	if got := audit.records[0].Body; !strings.HasPrefix(got, "unhashed; over") {
		t.Errorf("body summary %q, want it unhashed by size", got)
	}
	if len(next.body) != len(body) {
		t.Errorf("upstream got %d bytes, want %d", len(next.body), len(body))
	}
}

// The path recorded is the one routed, so an encoded slash is not recorded as
// a different path than the one the upstream was asked for.
func TestTheRecordedPathIsTheRoutedPath(t *testing.T) {
	req := auditedRequest(t, http.MethodPost, "/rest/v1/rpc/a%2Fb", strings.NewReader(`{}`))
	audit := &fakeAuditor{}
	next, rec := forwardThroughProxy(req, "admin", audit)
	onlyRecord(t, audit, next, rec)
	if got := audit.records[0].Path; got != "/rest/v1/rpc/a%2Fb" {
		t.Errorf("recorded path %q, want the routed /rest/v1/rpc/a%%2Fb", got)
	}
}

// A statement run through the SQL editor is recorded, so a membership change
// made there is attributable; a long one is cut with a marker.
func TestASQLEditorStatementIsRecorded(t *testing.T) {
	short := "update _supatype.studio_members set role = 'admin'"
	long := "select 1; -- " + strings.Repeat("x", auditStatementLimit)
	for statement, want := range map[string]string{
		short: short,
		long:  long[:auditStatementLimit] + truncatedStatement,
	} {
		body := fmt.Sprintf(`{"query":%q}`, statement)
		audit := &fakeAuditor{}
		next, rec := forwardThroughProxy(auditedRequest(t, http.MethodPost, "/sql", strings.NewReader(body)), "admin", audit)
		onlyRecord(t, audit, next, rec)
		if got := audit.records[0].Statement; got != want {
			t.Errorf("recorded statement of %d bytes, want %d: %.60q", len(got), len(want), got)
		}
	}
}

// A SQL editor body too large to read cannot be attributed, so it is not run.
func TestAnUnrecordableSQLEditorRequestIsRefused(t *testing.T) {
	body := fmt.Sprintf(`{"query":"select 1 -- %s"}`, strings.Repeat("x", auditBodyHashLimit))
	for _, length := range []int64{int64(len(body)), -1} {
		req := auditedRequest(t, http.MethodPost, "/sql", strings.NewReader(body))
		req.ContentLength = length
		next, rec := forwardThroughProxy(req, "admin", &fakeAuditor{})
		if next.reached || rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("length %d: status %d, reached %v, want 413 and nothing run", length, rec.Code, next.reached)
		}
	}
}
