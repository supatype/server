// Package resendclient implements mailer.Client using the Resend API.
package resendclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	resendAPIURL = "https://api.resend.com/emails"
	httpTimeout  = 10 * time.Second
)

// Client sends transactional email via the Resend API.
// https://resend.com/docs/api-reference/emails/send-email
type Client struct {
	apiKey string
	from   string
	http   *http.Client
}

// New returns a Client that sends from fromAddress using apiKey.
// fromAddress must be a verified sender address or domain in Resend.
func New(apiKey, fromAddress string) *Client {
	return &Client{
		apiKey: apiKey,
		from:   fromAddress,
		http:   &http.Client{Timeout: httpTimeout},
	}
}

type sendRequest struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html,omitempty"`
	Text    string `json:"text,omitempty"`
}

// Mail implements mailer.Client. The body is sent as the HTML part.
// headers and typ are accepted for interface compatibility but not forwarded
// to Resend (the API does not support arbitrary headers in the basic Send call).
func (c *Client) Mail(
	ctx context.Context,
	to string,
	subject string,
	body string,
	headers map[string][]string,
	typ string,
) error {
	payload := sendRequest{
		From:    c.from,
		To:      to,
		Subject: subject,
		HTML:    body,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("resend: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendAPIURL, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("resend: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("resend: send: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
			return fmt.Errorf("resend: API error %d: decode response: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("resend: API error %d: %s — %s", resp.StatusCode, apiErr.Name, apiErr.Message)
	}

	return nil
}
