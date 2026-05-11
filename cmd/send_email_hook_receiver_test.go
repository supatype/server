package cmd

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/supatype/auth/internal/conf"

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
