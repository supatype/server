package corsorigins

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeStore is a saved list two policies can share, the way two replicas share one database.
type fakeStore struct {
	saved *Saved
	err   error
	reads int
}

func (f *fakeStore) Load(context.Context) (*Saved, error) {
	f.reads++
	return f.saved, f.err
}

func (f *fakeStore) Save(_ context.Context, origins []string) (*Saved, error) {
	f.saved = &Saved{Origins: origins, UpdatedAt: time.Unix(1, 0)}
	return f.saved, nil
}

func (f *fakeStore) Delete(context.Context) error { f.saved = nil; return nil }

// clock is a time a test moves by hand.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func policyOn(store Store, c *clock, d Defaults) *Policy {
	p := NewPolicy(store, d)
	p.now = c.now
	return p
}

func TestNormaliseKeepsOriginsAndRefusesEverythingElse(t *testing.T) {
	got, err := Normalise([]string{" https://App.Example.com/ ", "http://localhost:5173", "*", "https://app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "https://app.example.com,http://localhost:5173,*" {
		t.Errorf("got %v", got)
	}
	for _, bad := range []string{"app.example.com", "https://app.example.com/path", "ftp://x.example", "https://x.example?q=1", "https://u:p@x.example", ""} {
		if _, err := Normalise([]string{bad}); !errors.Is(err, ErrInvalidOrigin) {
			t.Errorf("%q: err = %v, want ErrInvalidOrigin", bad, err)
		}
	}
}

func TestUnionKeepsOrderAndDropsRepeatsAndBlanks(t *testing.T) {
	got := Union([]string{"a", " a ", ""}, nil, []string{"b", "a"})
	if strings.Join(got, ",") != "a,b" {
		t.Errorf("got %v", got)
	}
}

func TestTheDefaultIsTheSiteURLThenConfigurationThenTheHost(t *testing.T) {
	p := NewPolicy(nil, Defaults{SiteURL: "https://app.example.com/login", Configured: []string{"https://admin.example.com/", "*"}})
	got := p.InForce(context.Background(), []string{"https://tenant.example.com"})
	if strings.Join(got.Origins, ",") != "https://app.example.com,https://admin.example.com,*,https://tenant.example.com" {
		t.Errorf("origins = %v", got.Origins)
	}
	if got.Source != "default" {
		t.Errorf("source = %q", got.Source)
	}
}

func TestASaveOnOneReplicaReachesAnotherWithinTheTTL(t *testing.T) {
	store := &fakeStore{}
	c := &clock{t: time.Unix(100, 0)}
	one := policyOn(store, c, Defaults{SiteURL: "https://app.example.com"})
	two := policyOn(store, c, Defaults{SiteURL: "https://app.example.com"})

	if got := two.InForce(context.Background(), nil); got.Source != "default" {
		t.Fatalf("before any save: %v", got)
	}
	if _, err := one.Save(context.Background(), []string{"https://partner.example"}); err != nil {
		t.Fatal(err)
	}
	if got := one.InForce(context.Background(), nil); got.Source != "saved" {
		t.Error("the replica that saved should answer with the save at once")
	}
	if got := two.InForce(context.Background(), nil); got.Source != "default" {
		t.Error("another replica keeps its cached answer until the TTL passes")
	}
	c.t = c.t.Add(DefaultTTL)
	got := two.InForce(context.Background(), nil)
	if got.Source != "saved" || strings.Join(got.Origins, ",") != "https://app.example.com,https://partner.example" {
		t.Errorf("after the TTL: %v", got)
	}
}

func TestADatabaseErrorKeepsTheLastKnownList(t *testing.T) {
	store := &fakeStore{saved: &Saved{Origins: []string{"https://partner.example"}}}
	c := &clock{t: time.Unix(100, 0)}
	p := policyOn(store, c, Defaults{SiteURL: "https://app.example.com"})
	_ = p.InForce(context.Background(), nil)

	store.err = errors.New("connection refused")
	c.t = c.t.Add(DefaultTTL)
	got := p.InForce(context.Background(), nil)
	if got.Source != "saved" || strings.Join(got.Origins, ",") != "https://app.example.com,https://partner.example" {
		t.Errorf("a failed read should keep the saved list in force, got %v", got)
	}
}

func TestPlatformOriginsStayWhateverIsSaved(t *testing.T) {
	store := &fakeStore{}
	p := NewPolicy(store, Defaults{SiteURL: "https://app.example.com", Platform: []string{"https://studio.supatype.com"}})
	if _, err := p.Save(context.Background(), []string{}); err != nil {
		t.Fatal(err)
	}
	got := p.InForce(context.Background(), nil)
	if strings.Join(got.Origins, ",") != "https://studio.supatype.com,https://app.example.com" {
		t.Errorf("an empty saved list leaves only the platform's origins and the site URL, got %v", got.Origins)
	}
}

func TestNothingToSaveInWithoutAStore(t *testing.T) {
	p := NewPolicy(nil, Defaults{})
	if _, err := p.Save(context.Background(), []string{"https://x.example"}); !errors.Is(err, ErrNoStore) {
		t.Errorf("save: %v", err)
	}
	if err := p.Reset(context.Background()); !errors.Is(err, ErrNoStore) {
		t.Errorf("reset: %v", err)
	}
}

// brokenStore fails every write, as a database that went away mid-request would.
type brokenStore struct{ fakeStore }

func (b *brokenStore) Save(context.Context, []string) (*Saved, error) { return nil, errors.New("down") }
func (b *brokenStore) Delete(context.Context) error                   { return errors.New("down") }

func TestAFailedWriteChangesNothing(t *testing.T) {
	store := &brokenStore{fakeStore{saved: &Saved{Origins: []string{"https://partner.example"}}}}
	p := NewPolicy(store, Defaults{SiteURL: "https://app.example.com"})
	if _, err := p.Save(context.Background(), []string{"https://other.example"}); err == nil {
		t.Error("save: want the store's error")
	}
	if err := p.Reset(context.Background()); err == nil {
		t.Error("reset: want the store's error")
	}
	if got := p.InForce(context.Background(), nil); strings.Join(got.Origins, ",") != "https://app.example.com,https://partner.example" {
		t.Errorf("a failed write must leave the saved list in force, got %v", got.Origins)
	}
}

// SHc: the site URL is always allowed, like a platform origin. A saved list cannot take it away.
func TestTheSiteURLCannotBeSavedAway(t *testing.T) {
	p := NewPolicy(&fakeStore{}, Defaults{SiteURL: "https://app.example.com/login"})
	if _, err := p.Save(context.Background(), []string{"https://partner.example"}); err != nil {
		t.Fatal(err)
	}
	got := p.InForce(context.Background(), nil)
	if strings.Join(got.Origins, ",") != "https://app.example.com,https://partner.example" {
		t.Errorf("origins = %v, want the site URL kept beside the saved list", got.Origins)
	}
	if _, err := p.Save(context.Background(), []string{}); err != nil {
		t.Fatal(err)
	}
	if got := p.InForce(context.Background(), nil); strings.Join(got.Origins, ",") != "https://app.example.com" {
		t.Errorf("an empty saved list still leaves the site URL, got %v", got.Origins)
	}
}
