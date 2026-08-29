package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/auth/hooks/v0hooks"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/reloader"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

func TestVerifySendEmailHookSignature_acceptsSignedPayload(t *testing.T) {
	rawSecret := "MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	secrets := conf.HTTPHookSecrets{"v1," + rawSecret}

	payload := []byte(`{"email_data":{"site_url":"http://localhost:9999"}}`)
	msgID := "msg_test_hook_signature"
	ts := time.Now()

	wh, err := standardwebhooks.NewWebhook(rawSecret)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := wh.Sign(msgID, ts, payload)
	if err != nil {
		t.Fatal(err)
	}

	h := http.Header{}
	h.Set(standardwebhooks.HeaderWebhookID, msgID)
	h.Set(standardwebhooks.HeaderWebhookSignature, sig)
	h.Set(standardwebhooks.HeaderWebhookTimestamp, fmt.Sprint(ts.Unix()))

	if err := verifySendEmailHookSignature(payload, h, secrets); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifySendEmailHookSignature_rejectsTamperedBody(t *testing.T) {
	rawSecret := "MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	secrets := conf.HTTPHookSecrets{"v1," + rawSecret}

	payload := []byte(`{"original":true}`)
	msgID := "msg_tamper"
	ts := time.Now()

	wh, err := standardwebhooks.NewWebhook(rawSecret)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := wh.Sign(msgID, ts, payload)
	if err != nil {
		t.Fatal(err)
	}

	h := http.Header{}
	h.Set(standardwebhooks.HeaderWebhookID, msgID)
	h.Set(standardwebhooks.HeaderWebhookSignature, sig)
	h.Set(standardwebhooks.HeaderWebhookTimestamp, fmt.Sprint(ts.Unix()))

	tampered := []byte(`{"original":false}`)
	if err := verifySendEmailHookSignature(tampered, h, secrets); err == nil {
		t.Fatal("expected verification error for tampered body")
	}
}

// The receiver is the inbound half of the send-email hook: the auth service
// POSTs a signed payload here instead of calling the mailer directly. Nothing
// exercised the handler itself, only the signature check inside it.

// signedRequest builds the request the auth service would send.
func signedRequest(t *testing.T, secret string, payload []byte) *http.Request {
	t.Helper()

	wh, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		t.Fatal(err)
	}
	id, ts := "msg_receiver_test", time.Now()
	sig, err := wh.Sign(id, ts, payload)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/v0hooks/send-email", bytes.NewReader(payload))
	req.Header.Set(standardwebhooks.HeaderWebhookID, id)
	req.Header.Set(standardwebhooks.HeaderWebhookSignature, sig)
	req.Header.Set(standardwebhooks.HeaderWebhookTimestamp, fmt.Sprint(ts.Unix()))
	return req
}

// Everything the receiver refuses, and why. A payload that reached delivery
// unsigned would let anyone who can reach the port send mail as the project.
func TestTheSendEmailHookReceiverRefuses(t *testing.T) {
	const secret = "MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	secrets := conf.HTTPHookSecrets{"v1," + secret}

	// The atomic handler wraps something that is not the auth service, which is
	// the misconfiguration the last branch reports.
	receiver := newSendEmailHookReceiver(reloader.NewAtomicHandler(http.NotFoundHandler()), secrets)

	valid := []byte(`{"user":{"id":"00000000-0000-0000-0000-000000000000"},"email_data":{"site_url":"http://localhost:9999"}}`)

	for name, tc := range map[string]struct {
		req  *http.Request
		want int
	}{
		"a GET": {
			httptest.NewRequest(http.MethodGet, "/internal/v0hooks/send-email", nil),
			http.StatusMethodNotAllowed,
		},
		"a body larger than the cap": {
			signedRequest(t, secret, bytes.Repeat([]byte("x"), sendEmailHookMaxBody+1)),
			http.StatusRequestEntityTooLarge,
		},
		"an unsigned payload": {
			httptest.NewRequest(http.MethodPost, "/internal/v0hooks/send-email", bytes.NewReader(valid)),
			http.StatusUnauthorized,
		},
		"a signature from another secret": {
			signedRequest(t, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", valid),
			http.StatusUnauthorized,
		},
		"a signed payload that is not JSON": {
			signedRequest(t, secret, []byte("{not json")),
			http.StatusBadRequest,
		},
		"a signed payload with nothing to deliver it": {
			signedRequest(t, secret, valid),
			http.StatusInternalServerError,
		},
	} {
		rec := httptest.NewRecorder()
		receiver.ServeHTTP(rec, tc.req)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// A body that cannot be read at all is a bad request, not a panic. The auth
// service is on the other end of a network, so a truncated POST is reachable.
func TestTheSendEmailHookReceiverOnATruncatedBody(t *testing.T) {
	secrets := conf.HTTPHookSecrets{"v1,MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"}
	receiver := newSendEmailHookReceiver(reloader.NewAtomicHandler(http.NotFoundHandler()), secrets)

	req := httptest.NewRequest(http.MethodPost, "/internal/v0hooks/send-email", errReader{})
	rec := httptest.NewRecorder()
	receiver.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// The secrets themselves. A deployment that configured none, or configured them
// in the wrong format, must not verify everything by accident.
func TestTheSendEmailHookSecrets(t *testing.T) {
	payload := []byte(`{}`)
	headers := http.Header{}

	for name, secrets := range map[string]conf.HTTPHookSecrets{
		"none configured":                 {},
		"not a v1 secret":                 {"whsec_something"},
		"a v1 secret that is not one":     {"v1,!!!not base64!!!"},
		"a v1 secret that does not match": {"v1,MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"},
	} {
		if err := verifySendEmailHookSignature(payload, headers, secrets); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
}

// stubDeliverer stands in for the auth service.
type stubDeliverer struct{ err error }

func (s stubDeliverer) DeliverInboundSendEmailHook(*http.Request, *v0hooks.SendEmailInput) error {
	return s.err
}

func (s stubDeliverer) ServeHTTP(http.ResponseWriter, *http.Request) {}

// What the caller is told once the payload is through the door. The auth
// service's own refusals carry a status and reach the caller as they are;
// anything else is this server's problem, not the caller's.
func TestWhatTheSendEmailHookAnswersOnceItIsThrough(t *testing.T) {
	const secret = "MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	secrets := conf.HTTPHookSecrets{"v1," + secret}
	payload := []byte(`{"email_data":{"site_url":"http://localhost:9999"}}`)

	for name, tc := range map[string]struct {
		err  error
		want int
		body string
	}{
		"delivered": {nil, http.StatusNoContent, ""},
		"the auth service refused it": {
			apierrors.NewHTTPError(http.StatusUnprocessableEntity, "no_such_user", "no such user"),
			http.StatusUnprocessableEntity, "no such user",
		},
		"delivery failed": {
			errors.New("the SMTP server hung up"),
			http.StatusInternalServerError, "internal error",
		},
	} {
		receiver := newSendEmailHookReceiver(
			reloader.NewAtomicHandler(stubDeliverer{err: tc.err}), secrets)

		rec := httptest.NewRecorder()
		receiver.ServeHTTP(rec, signedRequest(t, secret, payload))

		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
		if tc.body != "" && !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%s: body = %q, want it to carry %q", name, rec.Body.String(), tc.body)
		}
	}
}
