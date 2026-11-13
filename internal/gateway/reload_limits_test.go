package gateway

import (
	"testing"
	"time"

	"github.com/supatype/server/internal/auth"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/corsorigins"
)

func cfgWithEmailCap(events float64, siteURL string) *conf.GlobalConfiguration {
	cfg := &conf.GlobalConfiguration{SiteURL: siteURL}
	cfg.RateLimitEmailSent = conf.Rate{Events: events, OverTime: time.Hour}
	return cfg
}

func newTestReloader(cfg *conf.GlobalConfiguration) *apiReloader {
	return &apiReloader{
		limiters:  auth.NewLimiterOptions(cfg),
		emailRate: cfg.RateLimitEmailSent,
		cors:      corsorigins.NewPolicy(nil, corsorigins.Defaults{SiteURL: cfg.SiteURL}),
	}
}

func spend(l interface{ Allow() bool }, n int) {
	for i := 0; i < n; i++ {
		l.Allow()
	}
}

func TestReloadWithANewEmailCapStartsAFreshCount(t *testing.T) {
	r := newTestReloader(cfgWithEmailCap(2, "https://a.example"))
	spend(r.limiters.Email, 2)
	if r.limiters.Email.Allow() {
		t.Fatal("setup: cap of 2 should be spent")
	}

	got := r.refresh(cfgWithEmailCap(5, "https://a.example"))
	for i := 0; i < 5; i++ {
		if !got.Email.Allow() {
			t.Fatalf("send %d refused after raising the cap to 5", i+1)
		}
	}
	if got.Email.Allow() {
		t.Error("a sixth send was allowed under a cap of 5")
	}
}

func TestReloadWithTheSameEmailCapKeepsTheCount(t *testing.T) {
	r := newTestReloader(cfgWithEmailCap(2, "https://a.example"))
	spend(r.limiters.Email, 2)

	got := r.refresh(cfgWithEmailCap(2, "https://a.example"))
	if got.Email.Allow() {
		t.Error("an unchanged cap reset the count")
	}
}

func TestReloadWithACapOfZeroDoesNotPanic(t *testing.T) {
	r := newTestReloader(cfgWithEmailCap(2, "https://a.example"))
	got := r.refresh(cfgWithEmailCap(0, "https://a.example"))
	if got.Email == nil {
		t.Fatal("no email limiter after a cap of 0")
	}
	got.Email.Allow()
}

func TestReloadMovesCORSToTheNewSiteURL(t *testing.T) {
	r := newTestReloader(cfgWithEmailCap(2, "https://old.example"))
	r.refresh(cfgWithEmailCap(2, "https://new.example"))

	if got := r.cors.SiteOrigin(); got != "https://new.example" {
		t.Errorf("site origin = %q, want https://new.example", got)
	}
	origins := r.cors.DefaultOrigins(nil)
	if len(origins) != 1 || origins[0] != "https://new.example" {
		t.Errorf("default origins = %v, want only the new site URL", origins)
	}
}
