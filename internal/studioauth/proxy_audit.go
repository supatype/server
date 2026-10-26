package studioauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/studiomembers"
)

// ElevatedAuditor records requests that skipped the app's row rules.
// studiomembers.Store is the production one; tests supply their own.
type ElevatedAuditor interface {
	AuditElevated(ctx context.Context, r studiomembers.ElevatedRequest) (studiomembers.AuditRef, error)
	AuditElevatedStatus(ctx context.Context, ref studiomembers.AuditRef, status int) error
}

// auditor is where this config records elevated requests: Audit when set, else
// the membership store.
func (c Config) auditor() ElevatedAuditor {
	if c.Audit != nil {
		return c.Audit
	}
	return c.Members
}

// auditBodyHashLimit bounds the body the proxy reads to digest. A larger body
// is recorded by its size and type only, so auditing never buffers a file in
// memory. A body of unknown length (chunked) is read up to the limit to find
// out which it is.
const auditBodyHashLimit = 1 << 20

// auditStatementLimit bounds the SQL editor statement an audit row keeps.
const auditStatementLimit = 16 << 10

// truncatedStatement marks a statement the audit row kept only the start of.
const truncatedStatement = "\n-- [statement truncated in the audit log]"

// refusedUnrecordableSQL answers a SQL editor request too large to record.
const refusedUnrecordableSQL = "This statement was not run: it is too large for Studio to record in the audit log."

// refusedUnaudited is the answer to a write that could not be recorded.
const refusedUnaudited = "This change was not made: Studio could not record it in the audit log. Try again, " +
	"or ask an admin to check the audit table."

// proxyAudit is one recorded write, waiting for its upstream status.
type proxyAudit struct {
	auditor ElevatedAuditor
	ref     studiomembers.AuditRef
	ctx     context.Context
}

// auditForward records a request that skipped the app's row rules, with who
// made it, their Studio role, the method, path, query, a digest of the body and,
// for the SQL editor, the statement, so a change made through Studio is
// attributable.
//
// Reads are logged only: Studio polls, and an audit row per read would bury the
// membership trail in noise. A write gets a durable row before it is sent, and
// fails closed: a write that cannot be recorded is refused, having written the
// response, because an elevation nobody can account for is the thing the trail
// exists to prevent. A request acting as the caller, or reading the metadata
// catalogue, skips nothing and is not recorded.
func auditForward(w http.ResponseWriter, req2 *http.Request, c Config, adm admission, mode, path string) (*proxyAudit, bool) {
	if mode == ModeSelf || under(path, "/metadata") {
		return nil, true
	}
	logElevated(req2, adm, mode, path)
	if isReadOnly(req2.Method) {
		return nil, true
	}
	record, recordable := elevatedRecord(req2, adm, mode, path)
	if !recordable {
		writeError(w, http.StatusRequestEntityTooLarge, refusedUnrecordableSQL)
		return nil, false
	}
	ref, err := c.auditor().AuditElevated(req2.Context(), record)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, refusedUnaudited)
		return nil, false
	}
	return &proxyAudit{auditor: c.auditor(), ref: ref, ctx: req2.Context()}, true
}

// elevatedRecord is the audit row for one write. path is the path as routed,
// not the decoded URL.Path, so `a%2Fb` is not recorded as `a/b`. A SQL editor
// request also records its statement; one whose body was too large to read
// reports false, since what it ran could not be attributed.
func elevatedRecord(req2 *http.Request, adm admission, mode, path string) (studiomembers.ElevatedRequest, bool) {
	summary, body := bodySummary(req2)
	record := studiomembers.ElevatedRequest{
		Actor:  adm.actor,
		Role:   adm.role,
		Mode:   mode,
		Method: req2.Method,
		Path:   path,
		Query:  req2.URL.RawQuery,
		Body:   summary,
	}
	if !under(path, "/sql") {
		return record, true
	}
	if body == nil && summary != "" {
		return record, false
	}
	record.Statement = sqlStatement(body)
	return record, true
}

// sqlStatement is the statement a SQL editor body asks the runner to execute,
// decoded the way the runner decodes it, cut to auditStatementLimit bytes.
func sqlStatement(body []byte) string {
	var request struct {
		Query string `json:"query"`
	}
	if json.NewDecoder(bytes.NewReader(body)).Decode(&request) != nil {
		return ""
	}
	if len(request.Query) <= auditStatementLimit {
		return request.Query
	}
	return strings.ToValidUTF8(request.Query[:auditStatementLimit], "") + truncatedStatement
}

func logElevated(req2 *http.Request, adm admission, mode, path string) {
	logrus.WithFields(logrus.Fields{
		"actor":  adm.actor,
		"role":   adm.role,
		"method": req2.Method,
		"path":   path,
		"query":  req2.URL.RawQuery,
		"mode":   mode,
	}).Info("studio: elevated request")
}

// finish records the upstream status. The write has happened by now, so a
// failure here is logged rather than turned into an error the caller would
// read as the write failing.
func (a *proxyAudit) finish(status int) {
	if a == nil {
		return
	}
	if err := a.auditor.AuditElevatedStatus(a.ctx, a.ref, status); err != nil {
		logrus.WithError(err).WithField("status", status).Error("studio: upstream status not audited")
	}
}

// bodySummary is a digest of the body and its size: enough to tell which
// GraphQL mutation, RPC call or storage move a row records, without storing
// content that may be personal data. It also returns the body when it read all
// of it, nil otherwise. A body it reads is put back for upstream.
func bodySummary(req *http.Request) (string, []byte) {
	if req.Body == nil || req.Body == http.NoBody {
		return "", nil
	}
	contentType := req.Header.Get("Content-Type")
	if req.ContentLength > auditBodyHashLimit {
		return fmt.Sprintf("unhashed; %d bytes; %s", req.ContentLength, contentType), nil
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, auditBodyHashLimit+1))
	req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), req.Body))
	if err != nil {
		return fmt.Sprintf("unreadable; %d bytes; %s", req.ContentLength, contentType), nil
	}
	if len(body) > auditBodyHashLimit {
		return fmt.Sprintf("unhashed; over %d bytes; %s", auditBodyHashLimit, contentType), nil
	}
	return fmt.Sprintf("sha256:%x; %d bytes; %s", sha256.Sum256(body), len(body), contentType), body
}

// statusRecorder notes the status a forwarded write was answered with. It is
// only put in front of writes: reads include streams and WebSocket upgrades,
// which need the writer's own Flusher and Hijacker.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// serveAudited forwards a request and records its upstream status when it is a
// recorded write.
func serveAudited(inner http.Handler, w http.ResponseWriter, req2 *http.Request, audit *proxyAudit) {
	if audit == nil {
		inner.ServeHTTP(w, req2)
		return
	}
	recorder := &statusRecorder{ResponseWriter: w}
	inner.ServeHTTP(recorder, req2)
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	audit.finish(recorder.status)
}
