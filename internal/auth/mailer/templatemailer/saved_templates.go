package templatemailer

import (
	"context"
	"errors"
	"html/template"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// SavedTemplate is an email template saved through the admin API. While one
// is saved for a type it wins over that type's configured template
// (SUPATYPE_MAILER_TEMPLATES_* and SUPATYPE_MAILER_SUBJECTS_*) and over the
// built-in default.
type SavedTemplate struct {
	Subject   string
	Body      string
	UpdatedAt time.Time
}

// SavedTemplateStore is where saved templates live. It has to outlive the
// process and be shared by every replica, which is why it is the database and
// not this server's configuration. Find answers nil, nil when none is saved.
type SavedTemplateStore interface {
	Find(ctx context.Context, typ string) (*SavedTemplate, error)
	// Save inserts or replaces the template of typ and sets tpl.UpdatedAt.
	Save(ctx context.Context, typ string, tpl *SavedTemplate) error
	Delete(ctx context.Context, typ string) error
}

// savedTemplateTTL is how long a replica trusts its last answer from the store
// before asking again, so a sent email reflects an edit made on another replica
// within this long. Absences are cached too, or every email would cost a query.
const savedTemplateTTL = 10 * time.Second

// ErrNoSavedTemplateStore is returned when saving or resetting a template on a
// mailer that has no store to keep it in.
var ErrNoSavedTemplateStore = errors.New("templatemailer: saved templates need a database")

// savedEntry is one cached answer: ent is nil when no template is saved.
type savedEntry struct {
	ent       *tplCacheEntry
	fetchedAt time.Time
}

// savedTemplates reads saved templates through a short per-type cache.
type savedTemplates struct {
	store SavedTemplateStore
	now   func() time.Time
	ttl   time.Duration

	mu sync.Mutex
	m  map[string]savedEntry
}

func newSavedTemplates(store SavedTemplateStore) *savedTemplates {
	return &savedTemplates{store: store, now: time.Now, ttl: savedTemplateTTL, m: map[string]savedEntry{}}
}

// entry returns the saved template of typ ready to execute, and false when none
// is saved or it cannot be read, in which case the configured template applies.
func (s *savedTemplates) entry(ctx context.Context, typ string) (*tplCacheEntry, bool) {
	if _, editable := editableTemplateTypes[typ]; !editable {
		return nil, false
	}
	now := s.now()
	cached, ok := s.cached(typ)
	if ok && now.Sub(cached.fetchedAt) < s.ttl {
		return cached.ent, cached.ent != nil
	}

	ent, err := s.fetch(ctx, typ, now)
	if err != nil {
		logrus.WithError(err).WithField("mail_type", typ).
			Warn("templatemailer: could not read the saved template; using the last known answer")
		// A database blip keeps the last answer rather than silently
		// reverting a saved edit to the configured template.
		return cached.ent, cached.ent != nil
	}
	s.put(typ, savedEntry{ent: ent, fetchedAt: now})
	return ent, ent != nil
}

func (s *savedTemplates) fetch(ctx context.Context, typ string, now time.Time) (*tplCacheEntry, error) {
	saved, err := s.store.Find(ctx, typ)
	if err != nil || saved == nil {
		return nil, err
	}
	subject, body, err := parseSavedTemplate(saved.Subject, saved.Body)
	if err != nil {
		return nil, err
	}
	return newTplCacheEntry(now, typ, subject, body), nil
}

func (s *savedTemplates) cached(typ string) (savedEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[typ]
	return e, ok
}

func (s *savedTemplates) put(typ string, e savedEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[typ] = e
}

// forget drops typ, so this replica's next email reads the store again.
func (s *savedTemplates) forget(typ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, typ)
}

// parseSavedTemplate parses a saved subject and body the way they will be
// executed, so a template that cannot render is refused when it is saved
// rather than when the first email is due.
func parseSavedTemplate(subject, body string) (*template.Template, *template.Template, error) {
	subj, err := template.New("Subject").Parse(subject)
	if err != nil {
		return nil, nil, err
	}
	b, err := template.New("saved").Parse(body)
	if err != nil {
		return nil, nil, err
	}
	return subj, b, nil
}

// editableTemplateTypes are the types the admin API can save.
var editableTemplateTypes = map[string]struct{}{
	InviteTemplate:       {},
	ConfirmationTemplate: {},
	RecoveryTemplate:     {},
	EmailChangeTemplate:  {},
	MagicLinkTemplate:    {},
}
