package studioauth

import (
	"encoding/json"
	"testing"
)

func decodeObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("not a JSON object: %v (%s)", err, body)
	}
	return out
}

func TestAdminConfigWithSiteURLAddsTheSiteURLAndKeepsTheRest(t *testing.T) {
	got := decodeObject(t, AdminConfigWithSiteURL([]byte(`{"adminRoles":["owner"],"projectName":"shop"}`), "https://shop.example.com"))
	if got["siteUrl"] != "https://shop.example.com" {
		t.Errorf("siteUrl = %v", got["siteUrl"])
	}
	if got["projectName"] != "shop" {
		t.Errorf("projectName lost: %v", got)
	}
	if _, ok := got["adminRoles"]; !ok {
		t.Errorf("adminRoles lost: %v", got)
	}
}

func TestAdminConfigWithSiteURLLeavesABodyItCannotReadAlone(t *testing.T) {
	for _, body := range []string{`not json`, `[1,2]`, ``} {
		if got := string(AdminConfigWithSiteURL([]byte(body), "https://x")); got != body {
			t.Errorf("body %q changed to %q", body, got)
		}
	}
}

func TestAdminConfigWithSiteURLOmitsAnEmptySiteURL(t *testing.T) {
	got := decodeObject(t, AdminConfigWithSiteURL([]byte(`{"adminRoles":["owner"]}`), "  "))
	if _, ok := got["siteUrl"]; ok {
		t.Errorf("siteUrl present for an empty value: %v", got)
	}
}
