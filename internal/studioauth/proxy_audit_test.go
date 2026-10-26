package studioauth

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
)

// bodyUpstream records the body a forwarded request carried, and answers with
// status.
type bodyUpstream struct {
	reached bool
	body    string
	status  int
}

func (u *bodyUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.reached = true
	raw, _ := io.ReadAll(r.Body)
	u.body = string(raw)
	w.WriteHeader(u.status)
}

// proxied is one request sent through the proxy in a test.
type proxied struct {
	role, mode, method, target, body string
}

// sendThroughProxy sends p through the proxy with audit, and returns what
// reached upstream and the response.
func sendThroughProxy(t *testing.T, p proxied, audit *fakeAuditor) (*bodyUpstream, *httptest.ResponseRecorder) {
	t.Helper()
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
	req := httptest.NewRequest(p.method, p.target, strings.NewReader(p.body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if p.mode != "" {
		req.Header.Set(ActingModeHeader, p.mode)
	}
	return forwardThroughProxy(req, p.role, audit)
}

// forwardThroughProxy sends a request built by the test through the proxy as
// role, with audit.
func forwardThroughProxy(req *http.Request, role string, audit *fakeAuditor) (*bodyUpstream, *httptest.ResponseRecorder) {
	cfg := membershipConfig(role)
	cfg.Audit = audit
	next := &bodyUpstream{status: http.StatusCreated}
	rec := httptest.NewRecorder()
	ProxyHandler(next, cfg).ServeHTTP(rec, req)
	return next, rec
}

// A write through Studio is recorded before it is sent with its method, path,
// query and a digest of its body, and the upstream status after.
func TestAStudioWriteIsAuditedInFull(t *testing.T) {
	audit := &fakeAuditor{}
	body := `{"query":"mutation { deleteFromPostsCollection { affectedCount } }"}`
	p := proxied{role: "editor", method: http.MethodPost, target: "/rest/v1/rpc/archive?select=id&limit=5", body: body}
	next, rec := sendThroughProxy(t, p, audit)
	if !next.reached || rec.Code != http.StatusCreated || len(audit.records) != 1 {
		t.Fatalf("reached %v, status %d, records %v", next.reached, rec.Code, audit.records)
	}
	got := audit.records[0]
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))
	if got.Method != http.MethodPost || got.Path != "/rest/v1/rpc/archive" || got.Query != "select=id&limit=5" {
		t.Errorf("recorded %+v, want the method, path and raw query", got)
	}
	if !strings.HasPrefix(got.Body, wantDigest) || got.Mode != ModeStudio || got.Actor != "user-1" {
		t.Errorf("recorded %+v, want body %s by user-1 as studio", got, wantDigest)
	}
	if next.body != body {
		t.Errorf("upstream body %q, want it untouched by the digest", next.body)
	}
	if len(audit.statuses) != 1 || audit.statuses[0] != http.StatusCreated {
		t.Errorf("statuses %v, want the upstream's 201", audit.statuses)
	}
}

// A Studio-mode or elevated write that cannot be recorded is not made.
func TestAnUnauditableWriteIsRefused(t *testing.T) {
	for _, p := range []proxied{
		{role: "editor", method: http.MethodPost, target: "/rest/v1/posts", body: `{}`},
		{role: "admin", mode: ModeElevated, method: http.MethodPatch, target: "/rest/v1/posts?id=eq.1", body: `{}`},
		{role: "editor", method: http.MethodPost, target: "/storage/v1/object/move", body: `{}`},
	} {
		next, rec := sendThroughProxy(t, p, &fakeAuditor{fail: true})
		if next.reached || rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status %d, reached %v, want 503 and nothing sent", p.method, p.target, rec.Code, next.reached)
		}
		if !strings.Contains(rec.Body.String(), "could not record it in the audit log") {
			t.Errorf("%s %s: body %q, want a plain reason", p.method, p.target, rec.Body.String())
		}
	}
	// A read is logged, not stored, so a broken audit table does not stop it.
	next, rec := sendThroughProxy(t, proxied{role: "editor", method: http.MethodGet, target: "/rest/v1/posts"}, &fakeAuditor{fail: true})
	if !next.reached || rec.Code != http.StatusCreated {
		t.Errorf("read: status %d, reached %v", rec.Code, next.reached)
	}
}

// A signed URL made through Studio or elevated lives at most an hour, for
// downloads and uploads, single and bulk.
func TestSignedURLsThroughStudioAreCappedAtAnHour(t *testing.T) {
	cases := []struct {
		p    proxied
		want float64
	}{
		{proxied{role: "editor", method: http.MethodPost, target: "/storage/v1/object/sign/media/a.png", body: `{"expiresIn":999999}`}, 3600},
		{proxied{role: "admin", mode: ModeElevated, method: http.MethodPost, target: "/storage/v1/object/sign/media/a.png", body: `{"expiresIn":604800}`}, 3600},
		{proxied{role: "editor", method: http.MethodPost, target: "/storage/v1/object/sign/media", body: `{"expiresIn":"86400","paths":["a.png"]}`}, 3600},
		{proxied{role: "editor", method: http.MethodPost, target: "/storage/v1/object/upload/sign/media/a.png", body: ``}, 3600},
		{proxied{role: "admin", mode: ModeElevated, method: http.MethodPost, target: "/storage/v1/object/upload/sign/media/a.png", body: `{"expiresIn":-1}`}, 3600},
		{proxied{role: "editor", method: http.MethodPost, target: "/storage/v1/object/sign/media/a.png", body: `{"expiresIn":60}`}, 60},
	}
	for _, c := range cases {
		next, rec := sendThroughProxy(t, c.p, &fakeAuditor{})
		var sent map[string]json.RawMessage
		if err := json.Unmarshal([]byte(next.body), &sent); err != nil || !next.reached {
			t.Fatalf("%s %s: status %d, upstream body %q (%v)", c.p.mode, c.p.target, rec.Code, next.body, err)
		}
		if string(sent["expiresIn"]) != fmt.Sprint(c.want) {
			t.Errorf("%s %s %s: expiresIn %s, want %v", c.p.mode, c.p.target, c.p.body, sent["expiresIn"], c.want)
		}
		if strings.Contains(c.p.body, "paths") && string(sent["paths"]) != `["a.png"]` {
			t.Errorf("%s: paths %s, want them kept", c.p.target, sent["paths"])
		}
	}
	next, rec := sendThroughProxy(t, proxied{role: "editor", method: http.MethodPost,
		target: "/storage/v1/object/sign/media/a.png", body: `expiresIn=999999`}, &fakeAuditor{})
	if next.reached || rec.Code != http.StatusBadRequest {
		t.Errorf("unreadable signing body: status %d, reached %v, want 400", rec.Code, next.reached)
	}
}

// sendWrite sends an editor's write through the proxy to upstream, with audit.
func sendWrite(t *testing.T, upstream http.Handler, audit *fakeAuditor, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
	req := httptest.NewRequest(http.MethodPost, "/rest/v1/posts", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	cfg := membershipConfig("editor")
	cfg.Audit = audit
	rec := httptest.NewRecorder()
	ProxyHandler(upstream, cfg).ServeHTTP(rec, req)
	return rec
}

// An upstream that writes its body without a status first answered 200, and
// that is the status recorded. It can still flush through the recorder.
func TestAnImplicitOKIsTheStatusRecorded(t *testing.T) {
	audit := &fakeAuditor{}
	var flushErr error
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
		_, _ = w.Write([]byte("\n"))
		flushErr = http.NewResponseController(w).Flush()
	})
	rec := sendWrite(t, upstream, audit, strings.NewReader(`{"title":"x"}`))
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"id\":1}\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if len(audit.statuses) != 1 || audit.statuses[0] != http.StatusOK {
		t.Errorf("statuses recorded %v, want [200]", audit.statuses)
	}
	if flushErr != nil || !rec.Flushed {
		t.Errorf("flush through the recorder: %v, flushed %v", flushErr, rec.Flushed)
	}
}

// The write has happened by the time its status is recorded, so failing to
// record the status leaves the response the caller gets as it was.
func TestAStatusThatCannotBeRecordedLeavesTheResponseAlone(t *testing.T) {
	audit := &fakeAuditor{failStatus: true}
	next := &bodyUpstream{status: http.StatusCreated}
	rec := sendWrite(t, next, audit, strings.NewReader(`{"title":"x"}`))
	if !next.reached || rec.Code != http.StatusCreated || len(audit.records) != 1 || len(audit.statuses) != 1 {
		t.Fatalf("reached %v, status %d, records %d, statuses %v", next.reached, rec.Code, len(audit.records), audit.statuses)
	}
}

// A body the proxy cannot read to digest is recorded as unreadable, with its
// type, rather than as a digest of whatever part arrived.
func TestABodyThatCannotBeReadIsRecordedAsUnreadable(t *testing.T) {
	audit := &fakeAuditor{}
	sendWrite(t, &bodyUpstream{status: http.StatusBadRequest}, audit, io.MultiReader(strings.NewReader(`{"ti`), failingBody{}))
	if len(audit.records) != 1 || !strings.HasPrefix(audit.records[0].Body, "unreadable; ") || !strings.HasSuffix(audit.records[0].Body, "application/json") {
		t.Fatalf("records %+v, want one marked unreadable with its type", audit.records)
	}
}

// Recording the status of a request that was never recorded does nothing.
func TestFinishingAnUnrecordedRequestIsANoOp(t *testing.T) {
	var audit *proxyAudit
	audit.finish(http.StatusOK)
}
