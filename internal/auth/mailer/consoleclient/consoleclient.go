// Package consoleclient implements mailer.Client by logging structured email metadata.
// It is intended for local development (SUPATYPE / defineConfig "console" provider).
package consoleclient

import (
	"context"
	"errors"

	"github.com/sirupsen/logrus"
)

type Client struct {
	logBody bool
}

// New returns a console client. logBody logs each email's body, which is how a
// developer reads a magic link or OTP without a mail server. It is for dev mode
// only: self-host also defaults to this client, and a live sign-in link in a
// log is a credential.
func New(logBody bool) *Client {
	return &Client{logBody: logBody}
}

func (c *Client) Mail(
	ctx context.Context,
	to string,
	subject string,
	body string,
	headers map[string][]string,
	typ string,
) error {
	if to == "" {
		return errors.New("to field cannot be empty")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	fields := logrus.Fields{
		"component":    "mailer_console",
		"to":           to,
		"subject":      subject,
		"template_typ": typ,
		"body_len":     len(body),
		"header_keys":  headerKeys(headers),
	}
	if c.logBody {
		fields["body"] = body
		logrus.WithFields(fields).Info("console mailer: would send email (dev mode, body included)")
		return nil
	}
	logrus.WithFields(fields).Info("console mailer: would send email (body omitted; use smtp/resend/ses for delivery)")
	return nil
}

func headerKeys(h map[string][]string) []string {
	if len(h) == 0 {
		return nil
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	return keys
}
