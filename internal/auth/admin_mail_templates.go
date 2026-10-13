package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/auth/mailer/templatemailer"
)

// mailTemplateResponse is one template as Studio's Email templates view reads it.
// Saved says whether an edit saved through this API is in force; when it is
// false the configured or built-in template is, and DELETE has nothing to undo.
type mailTemplateResponse struct {
	Subject string     `json:"subject"`
	Body    string     `json:"body"`
	Content string     `json:"content"`
	Saved   bool       `json:"saved"`
	SavedAt *time.Time `json:"saved_at,omitempty"`
}

func newMailTemplateResponse(tpl templatemailer.AdminTemplate) mailTemplateResponse {
	html := strings.TrimSpace(tpl.Body)
	return mailTemplateResponse{
		Subject: tpl.Subject,
		Body:    html,
		Content: html,
		Saved:   tpl.SavedAt != nil,
		SavedAt: tpl.SavedAt,
	}
}

// templateMailerFor resolves the path's template type and the template mailer.
func (a *API) templateMailerFor(r *http.Request) (*templatemailer.Mailer, string, error) {
	typ, ok := templatemailer.CanonicalPathTemplateType(chi.URLParam(r, "template_type"))
	if !ok {
		return nil, "", apierrors.NewNotFoundError(apierrors.ErrorCodeValidationFailed, "Unknown email template type")
	}
	tm, ok := a.mailer.(*templatemailer.Mailer)
	if !ok {
		return nil, "", apierrors.NewInternalServerError("Email template admin requires the template mailer")
	}
	return tm, typ, nil
}

func (a *API) adminMailTemplateGet(w http.ResponseWriter, r *http.Request) error {
	tm, typ, err := a.templateMailerFor(r)
	if err != nil {
		return err
	}
	tpl, err := tm.AdminTemplateSnapshot(r.Context(), typ)
	if err != nil {
		return apierrors.NewInternalServerError("Failed to load mail template").WithInternalError(err)
	}
	return sendJSON(w, http.StatusOK, newMailTemplateResponse(tpl))
}

// adminMailTemplatePut saves an edit. It is kept in the database, so it is in
// force on every replica and survives a restart, until DELETE removes it.
func (a *API) adminMailTemplatePut(w http.ResponseWriter, r *http.Request) error {
	tm, typ, err := a.templateMailerFor(r)
	if err != nil {
		return err
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
	if strings.TrimSpace(payload.Subject) == "" || strings.TrimSpace(html) == "" {
		return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "A saved template needs a subject and a body")
	}

	savedAt, err := tm.AdminTemplateSave(r.Context(), typ, payload.Subject, html)
	if err != nil {
		return mailTemplateWriteError(err)
	}
	return sendJSON(w, http.StatusOK, newMailTemplateResponse(templatemailer.AdminTemplate{
		Subject: payload.Subject, Body: html, SavedAt: &savedAt,
	}))
}

// adminMailTemplateDelete removes a saved edit and answers with the configured
// or built-in template that is in force again.
func (a *API) adminMailTemplateDelete(w http.ResponseWriter, r *http.Request) error {
	tm, typ, err := a.templateMailerFor(r)
	if err != nil {
		return err
	}
	if err := tm.AdminTemplateReset(r.Context(), typ); err != nil {
		return mailTemplateWriteError(err)
	}
	return a.adminMailTemplateGet(w, r)
}

func mailTemplateWriteError(err error) error {
	var invalid *templatemailer.InvalidTemplateError
	if errors.As(err, &invalid) {
		return apierrors.NewBadRequestError(apierrors.ErrorCodeValidationFailed, "%v", err).WithInternalError(err)
	}
	return apierrors.NewInternalServerError("Failed to save mail template").WithInternalError(err)
}
