package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
	"github.com/supatype/server/internal/draftinsert"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
)

// A plain insert into a drafts model, refused by a real PostgREST, comes out of the real mount
// naming supatype.create_record.
//
// The schema is the engine's `versioned_blog` fixture with `posts` read under
// `published_at <= now()` and created by editors, pushed by the engine to the database
// SUPATYPE_DRAFTS_DATABASE_URL names, and served by the PostgREST at SUPATYPE_DRAFTS_REST_URL
// with SUPATYPE_DRAFTS_JWT_SECRET. The caller is an API editor, for whom the new, unpublished
// row fails the read rule that Postgres checks a returned row against. Skips without all three.
func TestRestMountNamesCreateRecordWhenADraftsInsertIsRefused(t *testing.T) {
	restURL, dsn, secret := os.Getenv("SUPATYPE_DRAFTS_REST_URL"), os.Getenv("SUPATYPE_DRAFTS_DATABASE_URL"), os.Getenv("SUPATYPE_DRAFTS_JWT_SECRET")
	if restURL == "" || dsn == "" || secret == "" {
		t.Skip("set SUPATYPE_DRAFTS_REST_URL, SUPATYPE_DRAFTS_DATABASE_URL and SUPATYPE_DRAFTS_JWT_SECRET")
	}
	resources, err := data.Open(context.Background(), &config.Config{SQLDatabaseURL: dsn})
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(func() { _ = resources.Close() })

	rr := httptest.NewRecorder()
	liveMux(restURL, resources).ServeHTTP(rr, editorInsert(t, secret))

	var refusal struct{ Code, Message, Hint string }
	if err := json.Unmarshal(rr.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("status %d, body %q is not PostgREST's refusal: %v", rr.Code, rr.Body.String(), err)
	}
	if rr.Code != http.StatusForbidden || refusal.Code != "42501" {
		t.Fatalf("status %d, body %q, want the read rule's 42501", rr.Code, rr.Body.String())
	}
	if refusal.Hint != draftinsert.Hint {
		t.Fatalf("hint = %q, want the create_record hint", refusal.Hint)
	}
}

// liveMux is the real outer mux in front of a real PostgREST and database.
func liveMux(restURL string, resources *data.Resources) http.Handler {
	cfg := &config.Config{Mode: "dev", PostgRESTURL: restURL}
	manifest := &proxy.RouteManifest{Schema: "public", PostgRESTURL: restURL}
	mf := func(*http.Request) *proxy.RouteManifest { return manifest }
	hp := func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") }
	return buildOuterMux(cfg, mf, hp, http.NotFoundHandler(), nil, "test", resources, nil)
}

// editorInsert is `POST /rest/v1/posts` asking for the row back, as an API editor.
func editorInsert(t *testing.T, secret string) *http.Request {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":          "11111111-1111-1111-1111-111111111111",
		"role":         "authenticated",
		"app_metadata": map[string]string{"role": "editor"},
		"exp":          time.Now().Add(10 * time.Minute).Unix(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rest/v1/posts", strings.NewReader(`{"title":"Plain insert"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "return=representation")
	return req
}

// A refused insert into a table not known to keep drafts is passed on exactly as it came: here
// there is no schema to read at all, or (when the live test above ran first, since the
// classification is memoised per process) one in which only `posts` keeps drafts.
func TestRestMountLeavesARefusalAloneOnATableNotKnownToKeepDrafts(t *testing.T) {
	const refused = `{"code":"42501","details":null,"hint":null,"message":"new row violates row-level security policy for table \"comments\""}`
	postgrest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(refused))
	}))
	t.Cleanup(postgrest.Close)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rest/v1/comments", strings.NewReader(`{"body":"x"}`))
	liveMux(postgrest.URL, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden || rr.Body.String() != refused {
		t.Fatalf("got %d %q, want PostgREST's refusal unchanged", rr.Code, rr.Body.String())
	}
}
