// Package consoleclient implements mailer.Client by logging structured email metadata.
// It is intended for local development (SUPATYPE / defineConfig "console" provider).
package consoleclient

import (
	"context"
	"errors"

	"github.com/sirupsen/logrus"
)

type Client struct{}

func New() *Client {
	return &Client{}
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

	logrus.WithFields(logrus.Fields{
		"component":    "mailer_console",
		"to":           to,
		"subject":      subject,
		"template_typ": typ,
		"body_len":     len(body),
		"header_keys":  headerKeys(headers),
	}).Info("console mailer: would send email (body omitted; use smtp/resend/ses for delivery)")
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
