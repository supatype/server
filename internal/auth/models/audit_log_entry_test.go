package models

import (
	"net/http/httptest"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/supatype/server/internal/conf"
)

// auditedIP is the ip_address logged with the audit event for a request from remoteAddr when the
// caller passes given.
func auditedIP(t *testing.T, remoteAddr, given string) any {
	t.Helper()
	previous := logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))
	t.Cleanup(func() { logrus.StandardLogger().ReplaceHooks(previous) })
	hook := test.NewGlobal()

	r := httptest.NewRequest("POST", "/token", nil)
	r.RemoteAddr = remoteAddr
	actor := &User{ID: uuid.Must(uuid.NewV4())}
	cfg := conf.AuditLogConfiguration{DisablePostgres: true}

	if err := NewAuditLogEntry(cfg, r, nil, actor, LoginAction, given, nil); err != nil {
		t.Fatalf("NewAuditLogEntry: %v", err)
	}
	for _, entry := range hook.AllEntries() {
		if event, ok := entry.Data["auth_audit_event"].(map[string]interface{}); ok {
			return event["ip_address"]
		}
	}
	t.Fatalf("no auth_audit_event was logged")
	return nil
}

// e2e 2026-11-21 FF-9: about forty call sites pass "" (upstream behaviour), so self-host audit rows
// held no address while the auth docs say the audit IP follows the rate-limit rule.
func TestNewAuditLogEntry_defaultsTheIPToTheRequest(t *testing.T) {
	if got := auditedIP(t, "203.0.113.9:4567", ""); got != "203.0.113.9" {
		t.Fatalf("ip_address = %v, want the request's 203.0.113.9", got)
	}
}

func TestNewAuditLogEntry_keepsAnIPTheCallerGave(t *testing.T) {
	if got := auditedIP(t, "203.0.113.9:4567", "198.51.100.7"); got != "198.51.100.7" {
		t.Fatalf("ip_address = %v, want the given 198.51.100.7", got)
	}
}
