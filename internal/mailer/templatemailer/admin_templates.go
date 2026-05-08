package templatemailer

import (
	"context"
	"fmt"
	"strings"

	"github.com/supatype/auth/internal/conf"
)

// CanonicalPathTemplateType maps URL path segments (Supabase-compatible, e.g. magiclink) to internal template keys.
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

func (o *Cache) purge(typ string) {
	o.rw.Lock()
	defer o.rw.Unlock()
	delete(o.m, typ)
}

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

// AdminTemplateSnapshot returns merged subject/body HTML for Studio (Settings → Authentication → Email templates).
func (m *Mailer) AdminTemplateSnapshot(ctx context.Context, typ string) (subject string, html string, err error) {
	subject = getEmailContentConfig(
		&m.cfg.Mailer.Subjects,
		typ,
		getEmailContentConfig(defaultTemplateSubjects, typ, ""),
	)
	html, err = m.tc.resolveMergedBodyHTML(ctx, m.cfg, typ)
	return subject, html, err
}

// AdminTemplateUpdate applies subject/HTML at runtime for the mailer and drops the cached entry for typ.
func (m *Mailer) AdminTemplateUpdate(typ, subjectLine, html string) error {
	if err := patchMailerSubjectField(&m.cfg.Mailer.Subjects, typ, subjectLine); err != nil {
		return err
	}
	if err := patchMailerTemplateField(&m.cfg.Mailer.Templates, typ, html); err != nil {
		return err
	}
	m.tc.purge(typ)
	return nil
}

func patchMailerSubjectField(cfg *conf.EmailContentConfiguration, typ, v string) error {
	switch typ {
	case InviteTemplate:
		cfg.Invite = v
	case ConfirmationTemplate:
		cfg.Confirmation = v
	case RecoveryTemplate:
		cfg.Recovery = v
	case EmailChangeTemplate:
		cfg.EmailChange = v
	case MagicLinkTemplate:
		cfg.MagicLink = v
	default:
		return fmt.Errorf("unknown mail template key: %q", typ)
	}
	return nil
}

func patchMailerTemplateField(cfg *conf.EmailContentConfiguration, typ, v string) error {
	switch typ {
	case InviteTemplate:
		cfg.Invite = v
	case ConfirmationTemplate:
		cfg.Confirmation = v
	case RecoveryTemplate:
		cfg.Recovery = v
	case EmailChangeTemplate:
		cfg.EmailChange = v
	case MagicLinkTemplate:
		cfg.MagicLink = v
	default:
		return fmt.Errorf("unknown mail template key: %q", typ)
	}
	return nil
}
