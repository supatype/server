package robots

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsBot(t *testing.T) {
	for ua, want := range map[string]bool{
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)": true,
		"GPTBot/1.0": true,
		"Mozilla/5.0 AppleWebKit (KHTML, like Gecko) Chrome Safari": false,
		"": false,
		// Matching is case-insensitive and by substring, because crawlers carry
		// version and URL detail around their own name.
		"CCBOT":               true,
		"some-ahrefs-thing/9": true,
	} {
		if got := IsBot(ua); got != want {
			t.Errorf("IsBot(%q) = %v, want %v", ua, got, want)
		}
	}
}

func TestIsPrefetch(t *testing.T) {
	for name, header := range map[string]map[string]string{
		"Sec-Purpose": {"Sec-Purpose": "prefetch"},
		"Purpose":     {"Purpose": "prefetch"},
		"mixed case":  {"Sec-Purpose": "PreFetch"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		for k, v := range header {
			r.Header.Set(k, v)
		}
		if !IsPrefetch(r) {
			t.Errorf("%s: want prefetch", name)
		}
	}

	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	if IsPrefetch(plain) {
		t.Error("a plain request is not a prefetch")
	}
	navigate := httptest.NewRequest(http.MethodGet, "/", nil)
	navigate.Header.Set("Sec-Purpose", "navigate")
	if IsPrefetch(navigate) {
		t.Error("navigate is not a prefetch")
	}
}

// Production must be left alone entirely: no header, no robots.txt, no refusal.
// Blocking crawlers there would hide the customer's own site from search.
func TestProductionIsUntouched(t *testing.T) {
	p := Policy{NonProd: false, BlockBots: true}
	r := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	r.Header.Set("User-Agent", "Googlebot")
	w := httptest.NewRecorder()

	if p.Handle(w, r) {
		t.Fatal("production must not answer the request itself")
	}
	if w.Header().Get("X-Robots-Tag") != "" {
		t.Error("production must not set X-Robots-Tag")
	}
}

func TestNonProdMarksEveryResponse(t *testing.T) {
	p := Policy{NonProd: true}
	w := httptest.NewRecorder()

	if p.Handle(w, httptest.NewRequest(http.MethodGet, "/anything", nil)) {
		t.Fatal("a normal path should still reach the application")
	}
	if got := w.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Errorf("X-Robots-Tag = %q", got)
	}
}

func TestNonProdServesADisallowingRobotsTxt(t *testing.T) {
	w := httptest.NewRecorder()
	if !(Policy{NonProd: true}).Handle(w, httptest.NewRequest(http.MethodGet, "/robots.txt", nil)) {
		t.Fatal("robots.txt must be answered here")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Disallow: /") {
		t.Errorf("body = %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestBlockBotsOnlyAppliesToNonProd(t *testing.T) {
	bot := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("User-Agent", "Googlebot/2.1")
		return r
	}

	w := httptest.NewRecorder()
	if !(Policy{NonProd: true, BlockBots: true}).Handle(w, bot()) {
		t.Fatal("a non-prod deployment with blocking on should refuse a crawler")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}

	// Non-prod without blocking still lets the crawler through; the noindex
	// header is the whole policy.
	w = httptest.NewRecorder()
	if (Policy{NonProd: true}).Handle(w, bot()) {
		t.Error("without BlockBots a crawler should not be refused")
	}
}
