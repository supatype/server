// Send-email hook receiver (POST /internal/v0hooks/send-email).
//
// GoTrue is configured with GOTRUE_HOOK_SEND_EMAIL_ENABLED=true, GOTRUE_HOOK_SEND_EMAIL_URI
// pointing at this path (or any HTTPS URL for an Edge Function), and GOTRUE_HOOK_SEND_EMAIL_SECRETS
// matching Standard Webhooks v1 symmetric secrets (same format as outbound hooks). The API then
// POSTs the payload here instead of calling the mailer directly; this handler verifies the
// signature and runs DeliverInboundSendEmailHook (same templated path as direct sends).
//
// In local dev, supatype.config.ts `email.send_email_hook` wires these env vars; optional
// `send_email_hook_uri` / `send_email_hook_secrets` override defaults.
package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/supatype/auth/internal/api"
	"github.com/supatype/auth/internal/api/apierrors"
	"github.com/supatype/auth/internal/conf"
	"github.com/supatype/auth/internal/hooks/v0hooks"
	"github.com/supatype/auth/internal/reloader"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

const sendEmailHookMaxBody = 200 * 1024

func newSendEmailHookReceiver(ah *reloader.AtomicHandler, secrets conf.HTTPHookSecrets) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		raw, err := io.ReadAll(io.LimitReader(r.Body, sendEmailHookMaxBody+1))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(raw) > sendEmailHookMaxBody {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}

		if err := verifySendEmailHookSignature(raw, r.Header, secrets); err != nil {
			logrus.WithError(err).Warn("send-email hook: signature verification failed")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var in v0hooks.SendEmailInput
		if err := json.Unmarshal(raw, &in); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		h := ah.LoadHandler()
		a, ok := h.(*api.API)
		if !ok || a == nil {
			logrus.Error("send-email hook: atomic handler does not wrap *api.API")
			http.Error(w, "misconfigured server", http.StatusInternalServerError)
			return
		}

		if err := a.DeliverInboundSendEmailHook(r, &in); err != nil {
			var herr *apierrors.HTTPError
			if errors.As(err, &herr) {
				http.Error(w, herr.Message, herr.HTTPStatus)
				return
			}
			logrus.WithError(err).Error("send-email hook: delivery failed")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

func verifySendEmailHookSignature(payload []byte, headers http.Header, secrets conf.HTTPHookSecrets) error {
	if len(secrets) == 0 {
		return errors.New("no webhook secrets configured")
	}
	var lastErr error
	for _, s := range secrets {
		if !strings.HasPrefix(s, "v1,") {
			lastErr = errors.New("invalid secret format")
			continue
		}
		trimmed := strings.TrimPrefix(s, "v1,")
		wh, werr := standardwebhooks.NewWebhook(trimmed)
		if werr != nil {
			lastErr = werr
			continue
		}
		verr := wh.Verify(payload, headers)
		if verr == nil {
			return nil
		}
		lastErr = verr
	}
	if lastErr == nil {
		return errors.New("no valid webhook secret")
	}
	return lastErr
}
