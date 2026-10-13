package templatemailer

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/conf"
)

// fakeStore is a SavedTemplateStore shared by "replicas" in these tests.
type fakeStore struct {
	mu    sync.Mutex
	rows  map[string]SavedTemplate
	finds int
	err   error
}

func (s *fakeStore) Find(_ context.Context, typ string) (*SavedTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finds++
	if s.err != nil {
		return nil, s.err
	}
	row, ok := s.rows[typ]
	if !ok {
		return nil, nil
	}
	return &row, nil
}

func (s *fakeStore) Save(_ context.Context, typ string, tpl *SavedTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tpl.UpdatedAt = time.Unix(1700000000, 0)
	s.rows[typ] = *tpl
	return nil
}

func (s *fakeStore) Delete(_ context.Context, typ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, typ)
	return nil
}

func (s *fakeStore) set(f func(*fakeStore)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// replica is one server's mailer over store, with a clock the test moves.
type replica struct {
	m   *Mailer
	now time.Time
}

func newReplica(store SavedTemplateStore) *replica {
	r := &replica{now: time.Unix(1700000000, 0)}
	r.m = New(&conf.GlobalConfiguration{}, nil, NewCache())
	r.m.UseSavedTemplates(store)
	r.m.saved.now = func() time.Time { return r.now }
	return r
}

// subject is the subject typ's next email would be sent with.
func (r *replica) subject(t *testing.T, typ string) string {
	t.Helper()
	ent, err := r.m.entry(context.Background(), r.m.cfg, typ)
	require.NoError(t, err)
	subject, _, err := ent.execute(new(bytes.Buffer), map[string]any{})
	require.NoError(t, err)
	return subject
}

const defaultRecoverySubject = "Reset Your Password"

func TestASavedTemplateIsSentAndCachedBriefly(t *testing.T) {
	store := &fakeStore{rows: map[string]SavedTemplate{RecoveryTemplate: {Subject: "Saved", Body: "<p>saved</p>"}}}
	r := newReplica(store)

	require.Equal(t, "Saved", r.subject(t, RecoveryTemplate))
	require.Equal(t, "Saved", r.subject(t, RecoveryTemplate))
	require.Equal(t, 1, store.finds, "a second email inside the TTL must not query again")

	r.now = r.now.Add(savedTemplateTTL)
	require.Equal(t, "Saved", r.subject(t, RecoveryTemplate))
	require.Equal(t, 2, store.finds)
}

// An edit saved on another replica reaches this one's emails within the TTL,
// and this replica's own edits reach them at once.
func TestReplicasConvergeOnTheSavedTemplate(t *testing.T) {
	store := &fakeStore{rows: map[string]SavedTemplate{}}
	a, b := newReplica(store), newReplica(store)
	require.Equal(t, defaultRecoverySubject, b.subject(t, RecoveryTemplate))

	_, err := a.m.AdminTemplateSave(context.Background(), RecoveryTemplate, "From A", "<p>a</p>")
	require.NoError(t, err)
	require.Equal(t, "From A", a.subject(t, RecoveryTemplate), "the saving replica sends the edit at once")
	require.Equal(t, defaultRecoverySubject, b.subject(t, RecoveryTemplate), "inside the TTL")

	b.now = b.now.Add(savedTemplateTTL)
	require.Equal(t, "From A", b.subject(t, RecoveryTemplate))

	require.NoError(t, a.m.AdminTemplateReset(context.Background(), RecoveryTemplate))
	require.Equal(t, defaultRecoverySubject, a.subject(t, RecoveryTemplate))
}

// A database blip keeps the edit in force rather than reverting to the
// configured template for as long as the database is unreachable.
func TestAStoreErrorKeepsTheLastAnswer(t *testing.T) {
	store := &fakeStore{rows: map[string]SavedTemplate{RecoveryTemplate: {Subject: "Saved", Body: "<p>saved</p>"}}}
	r := newReplica(store)
	require.Equal(t, "Saved", r.subject(t, RecoveryTemplate))

	store.set(func(s *fakeStore) { s.err = errors.New("connection refused") })
	r.now = r.now.Add(savedTemplateTTL)
	require.Equal(t, "Saved", r.subject(t, RecoveryTemplate))
}

func TestOnlyEditableTypesAskTheStore(t *testing.T) {
	store := &fakeStore{rows: map[string]SavedTemplate{}}
	r := newReplica(store)
	_, err := r.m.entry(context.Background(), r.m.cfg, ReauthenticationTemplate)
	require.NoError(t, err)
	require.Equal(t, 0, store.finds)
}

func TestATemplateThatDoesNotParseIsNotSaved(t *testing.T) {
	store := &fakeStore{rows: map[string]SavedTemplate{}}
	r := newReplica(store)
	_, err := r.m.AdminTemplateSave(context.Background(), RecoveryTemplate, "Fine", "<p>{{ .Broken </p>")
	var invalid *InvalidTemplateError
	require.ErrorAs(t, err, &invalid)
	require.Empty(t, store.rows)
}

func TestSavingNeedsAStore(t *testing.T) {
	m := New(&conf.GlobalConfiguration{}, nil, NewCache())
	_, err := m.AdminTemplateSave(context.Background(), RecoveryTemplate, "s", "<p>b</p>")
	require.ErrorIs(t, err, ErrNoSavedTemplateStore)
	require.ErrorIs(t, m.AdminTemplateReset(context.Background(), RecoveryTemplate), ErrNoSavedTemplateStore)
}
