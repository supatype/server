package auth

import (
	"context"

	"github.com/supatype/server/internal/auth/mailer/templatemailer"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/storage"
)

// mailTemplateStore keeps templates saved through the admin API in the auth
// schema, so they outlive the process and every replica reads the same ones.
type mailTemplateStore struct {
	db *storage.Connection
}

func (s mailTemplateStore) Find(ctx context.Context, typ string) (*templatemailer.SavedTemplate, error) {
	row, err := models.FindMailTemplate(s.db.WithContext(ctx), typ)
	if err != nil || row == nil {
		return nil, err
	}
	return &templatemailer.SavedTemplate{Subject: row.Subject, Body: row.Body, UpdatedAt: row.UpdatedAt}, nil
}

func (s mailTemplateStore) Save(ctx context.Context, typ string, tpl *templatemailer.SavedTemplate) error {
	row := &models.MailTemplate{Type: typ, Subject: tpl.Subject, Body: tpl.Body}
	if err := models.SaveMailTemplate(s.db.WithContext(ctx), row); err != nil {
		return err
	}
	tpl.UpdatedAt = row.UpdatedAt
	return nil
}

func (s mailTemplateStore) Delete(ctx context.Context, typ string) error {
	return models.DeleteMailTemplate(s.db.WithContext(ctx), typ)
}
