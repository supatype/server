package templatemailer

import (
	"context"
	"strings"
	"time"

	"github.com/supatype/server/internal/conf"
)

// CanonicalPathTemplateType maps URL path segments (e.g. magiclink) to internal template keys.
func CanonicalPathTemplateType(p string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "invite":
		return InviteTemplate, true
	case "confirmation":
		return ConfirmationTemplate, true
	case "recovery":
		return RecoveryTemplate, true
	case "email_change":
		return EmailChangeTemplate, true
	case "magiclink", "magic_link":
		return MagicLinkTemplate, true
	default:
		return "", false
	}
}

// UseSavedTemplates makes templates saved in store win over the configured
// ones. It is called once, while the API is built and before it serves.
func (m *Mailer) UseSavedTemplates(store SavedTemplateStore) {
	m.saved = newSavedTemplates(store)
}

// entry is the template mail of type typ is sent with: the saved edit when
// there is one, otherwise the configured or built-in template.
func (m *Mailer) entry(ctx context.Context, cfg *conf.GlobalConfiguration, typ string) (*tplCacheEntry, error) {
	if m.saved != nil {
		if ent, ok := m.saved.entry(ctx, typ); ok {
			return ent, nil
		}
	}
	return m.tc.get(ctx, cfg, typ)
}

// AdminTemplate is what Studio's Email templates view shows for one type.
type AdminTemplate struct {
	Subject string
	Body    string
	// SavedAt is when the edit in force was saved, and nil when the configured
	// or built-in template is in force.
	SavedAt *time.Time
}

// AdminTemplateSnapshot returns the template in force for typ. It reads the
// store directly rather than through the cache, so the view never shows an
// answer older than the database's.
func (m *Mailer) AdminTemplateSnapshot(ctx context.Context, typ string) (AdminTemplate, error) {
	if m.saved != nil {
		saved, err := m.saved.store.Find(ctx, typ)
		if err != nil {
			return AdminTemplate{}, err
		}
		if saved != nil {
			at := saved.UpdatedAt
			return AdminTemplate{Subject: saved.Subject, Body: saved.Body, SavedAt: &at}, nil
		}
	}
	subject := getEmailContentConfig(
		&m.cfg.Mailer.Subjects,
		typ,
		getEmailContentConfig(defaultTemplateSubjects, typ, ""),
	)
	html, err := m.tc.resolveMergedBodyHTML(ctx, m.cfg, typ)
	return AdminTemplate{Subject: subject, Body: html}, err
}

// AdminTemplateSave saves subject and html as the template of typ, after
// checking both parse, and returns when it was saved.
func (m *Mailer) AdminTemplateSave(ctx context.Context, typ, subject, html string) (time.Time, error) {
	if m.saved == nil {
		return time.Time{}, ErrNoSavedTemplateStore
	}
	if _, _, err := parseSavedTemplate(subject, html); err != nil {
		return time.Time{}, &InvalidTemplateError{err: err}
	}
	tpl := &SavedTemplate{Subject: subject, Body: html}
	if err := m.saved.store.Save(ctx, typ, tpl); err != nil {
		return time.Time{}, err
	}
	m.saved.forget(typ)
	return tpl.UpdatedAt, nil
}

// AdminTemplateReset removes the saved template of typ, so the configured or
// built-in template is in force again.
func (m *Mailer) AdminTemplateReset(ctx context.Context, typ string) error {
	if m.saved == nil {
		return ErrNoSavedTemplateStore
	}
	if err := m.saved.store.Delete(ctx, typ); err != nil {
		return err
	}
	m.saved.forget(typ)
	return nil
}

// InvalidTemplateError is a subject or body that does not parse as a template.
type InvalidTemplateError struct{ err error }

func (e *InvalidTemplateError) Error() string { return "invalid template: " + e.err.Error() }

func (e *InvalidTemplateError) Unwrap() error { return e.err }

// resolveMergedBodyHTML returns inline HTML loaded the same way as loadEntryBody, without parsing templates.
func (o *Cache) resolveMergedBodyHTML(ctx context.Context, cfg *conf.GlobalConfiguration, typ string) (string, error) {
	raw := getEmailContentConfig(&cfg.Mailer.Templates, typ, "")
	if raw == "" {
		return getEmailContentConfig(defaultTemplateBodies, typ, ""), nil
	}
	if looksLikeInlineHTML(raw) {
		return raw, nil
	}
	if !strings.HasPrefix(raw, "http") {
		raw = cfg.SiteURL + raw
	}
	return o.fetch(ctx, cfg, raw)
}
