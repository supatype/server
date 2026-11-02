package consoleclient

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

const magicLink = "http://localhost:8000/auth/v1/verify?token=abc123&type=magiclink"

// sendOne sends one email through a console client and returns the entry it
// logged.
func sendOne(t *testing.T, logBody bool) *logrus.Entry {
	t.Helper()
	hook := logtest.NewGlobal()
	t.Cleanup(hook.Reset)

	err := New(logBody).Mail(context.Background(), "a@example.com", "Your link", "<a href=\""+magicLink+"\">Sign in</a>", nil, "magiclink")
	require.NoError(t, err)
	require.Len(t, hook.AllEntries(), 1)
	return hook.LastEntry()
}

// In dev the console is the only place a magic link can be read, so the body
// is logged.
func TestMailLogsBodyInDev(t *testing.T) {
	entry := sendOne(t, true)
	require.Contains(t, entry.Data["body"], magicLink)
}

// Self-host also defaults to the console mailer, and a live sign-in link in a
// log is a credential, so outside dev the body is never logged.
func TestMailOmitsBodyOutsideDev(t *testing.T) {
	entry := sendOne(t, false)
	require.NotContains(t, entry.Data, "body")
	require.NotContains(t, entry.Message, "token=")
	require.Equal(t, len("<a href=\""+magicLink+"\">Sign in</a>"), entry.Data["body_len"])
}
