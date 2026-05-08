package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/supatype/auth/internal/api/apierrors"
	"github.com/supatype/auth/internal/mailer/templatemailer"
)

func (a *API) adminMailTemplateGet(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	segment := chi.URLParam(r, "template_type")

	typ, ok := templatemailer.CanonicalPathTemplateType(segment)
	if !ok {
		return apierrors.NewNotFoundError(apierrors.ErrorCodeValidationFailed, "Unknown email template type")
	}

	tm, ok := a.mailer.(*templatemailer.Mailer)
	if !ok {
		return apierrors.NewInternalServerError("Email template admin requires the template mailer")
	}

	subj, html, err := tm.AdminTemplateSnapshot(ctx, typ)
	if err != nil {
		return apierrors.NewInternalServerError("Failed to load mail template").WithInternalError(err)
	}

	html = strings.TrimSpace(html)
	return sendJSON(w, http.StatusOK, map[string]string{
		"subject": subj,
		"body":    html,
		"content": html,
	})
}

func (a *API) adminMailTemplatePut(w http.ResponseWriter, r *http.Request) error {
	segment := chi.URLParam(r, "template_type")

	typ, ok := templatemailer.CanonicalPathTemplateType(segment)
	if !ok {
		return apierrors.NewNotFoundError(apierrors.ErrorCodeValidationFailed, "Unknown email template type")
	}

	tm, tmplOk := a.mailer.(*templatemailer.Mailer)
	if !tmplOk {
		return apierrors.NewInternalServerError("Email template admin requires the template mailer")
	}

	var payload struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return apierrors.NewBadRequestError(apierrors.ErrorCodeBadJSON, "Invalid JSON body: %v", err).WithInternalError(err)
	}

	html := payload.Content
	if html == "" {
		html = payload.Body
	}
	if err := tm.AdminTemplateUpdate(typ, payload.Subject, html); err != nil {
		return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "%v", err).WithInternalError(err)
	}

	html = strings.TrimSpace(html)
	return sendJSON(w, http.StatusOK, map[string]string{
		"subject": payload.Subject,
		"body":    html,
		"content": html,
	})
}
