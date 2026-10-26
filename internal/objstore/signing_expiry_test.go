package objstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/supatype/server/internal/studioctx"
)

// signFor signs files/x/a.png with body in ctx and returns the response.
func signFor(t *testing.T, handler http.Handler, ctx context.Context, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/object/sign/files/x/a.png", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+userToken(t))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// signedTTL is how long the signed URL in a signing response lives, in seconds.
func signedTTL(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()
	var signed struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &signed); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("sign: %d %s (%v)", rec.Code, rec.Body.String(), err)
	}
	token := signed.SignedURL[strings.Index(signed.SignedURL, "token=")+len("token="):]
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
	if err != nil {
		t.Fatalf("decode %q: %v", token, err)
	}
	var p signedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("payload %s: %v", raw, err)
	}
	return p.Exp - time.Now().Unix()
}

func signingStore(t *testing.T) http.Handler {
	t.Helper()
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	uploadTyped(t, handler, "x/a.png", "image/png")
	return handler
}

// A key that only case-folds to expiresIn is refused, rather than read as the
// expiry by a decoder that ignores case: the Studio clamp forwarded
// `{"expiresIn":3600,"expiresin":31536000}` and the store signed for a year.
func TestSigningRefusesAKeyThatOnlyFoldsToExpiresIn(t *testing.T) {
	handler := signingStore(t)
	for _, body := range []string{
		`{"expiresIn":3600,"expiresin":31536000}`,
		`{"expiresin":31536000}`,
		`{"EXPIRESIN":31536000}`,
		`{"expireſIn":31536000}`,
	} {
		if rec := signFor(t, handler, context.Background(), body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d %s, want 400", body, rec.Code, rec.Body.String())
		}
	}
}

// A request the Studio proxy forwards signs for at most an hour even if the
// body asks for longer; an app's own request keeps the expiry it asked for.
func TestSigningThroughStudioIsCappedInTheStore(t *testing.T) {
	handler := signingStore(t)
	const year = 31536000
	proxied := studioctx.WithProxied(context.Background())
	if ttl := signedTTL(t, signFor(t, handler, proxied, `{"expiresIn":31536000}`)); ttl > studioctx.MaxSignedURLSeconds {
		t.Errorf("through Studio: signed for %ds, want at most %d", ttl, studioctx.MaxSignedURLSeconds)
	}
	if ttl := signedTTL(t, signFor(t, handler, context.Background(), `{"expiresIn":31536000}`)); ttl < year-5 {
		t.Errorf("app request: signed for %ds, want the year it asked for", ttl)
	}
	if ttl := signedTTL(t, signFor(t, handler, context.Background(), `{"expiresIn":null}`)); ttl != defaultSignedURLSeconds {
		t.Errorf("null expiry: signed for %ds, want the default", ttl)
	}
}

// An expiry that is not a whole number of seconds is refused rather than read
// as no expiry and given the default hour.
func TestSigningRefusesAnExpiryThatIsNotWholeSeconds(t *testing.T) {
	handler := signingStore(t)
	for _, body := range []string{`{"expiresIn":"3600"}`, `{"expiresIn":1.5}`, `{"expiresIn":true}`} {
		if rec := signFor(t, handler, context.Background(), body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d %s, want 400", body, rec.Code, rec.Body.String())
		}
	}
}
