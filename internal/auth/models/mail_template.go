package models

import (
	"database/sql"
	"time"

	"github.com/gobuffalo/pop/v6"
	"github.com/pkg/errors"
	"github.com/supatype/server/internal/auth/storage"
)

// MailTemplate is an email template saved through the admin API. A row
// overrides the configured template of its type until it is deleted.
type MailTemplate struct {
	Type      string    `db:"type" json:"type"`
	Subject   string    `db:"subject" json:"subject"`
	Body      string    `db:"body" json:"body"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// TableName is the table saved templates live in.
func (MailTemplate) TableName() string {
	return "mail_templates"
}

func mailTemplateTable() string {
	return (&pop.Model{Value: MailTemplate{}}).TableName()
}

// FindMailTemplate returns the saved template of typ, or nil when none is saved.
func FindMailTemplate(tx *storage.Connection, typ string) (*MailTemplate, error) {
	var tpl MailTemplate
	if err := tx.Q().Where("type = ?", typ).First(&tpl); err != nil {
		if errors.Cause(err) == sql.ErrNoRows {
			return nil, nil
		}
		return nil, errors.Wrap(err, "error finding saved mail template")
	}
	return &tpl, nil
}

// SaveMailTemplate inserts or replaces the saved template of tpl.Type and
// stamps tpl.UpdatedAt.
func SaveMailTemplate(tx *storage.Connection, tpl *MailTemplate) error {
	// Microseconds, as Postgres stores it, so the answer to a save matches a later read.
	tpl.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	err := tx.RawQuery(
		`insert into `+mailTemplateTable()+` (type, subject, body, updated_at)
		 values (?, ?, ?, ?)
		 on conflict (type) do update
		 set subject = excluded.subject, body = excluded.body, updated_at = excluded.updated_at`,
		tpl.Type, tpl.Subject, tpl.Body, tpl.UpdatedAt,
	).Exec()
	return errors.Wrap(err, "error saving mail template")
}

// DeleteMailTemplate removes the saved template of typ, if there is one.
func DeleteMailTemplate(tx *storage.Connection, typ string) error {
	err := tx.RawQuery(`delete from `+mailTemplateTable()+` where type = ?`, typ).Exec()
	return errors.Wrap(err, "error deleting saved mail template")
}
