package studioauth

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A key that case-folds to one the signing endpoints accept, without being it,
// is refused: storage's decoder would read `expiresin` or `expireſIn` (U+017F)
// as the expiry and sign past the cap.
func TestSigningBodyWithALookAlikeKeyIsRefused(t *testing.T) {
	for _, body := range []string{
		`{"expiresin":31536000}`,
		`{"expireſIn":31536000}`,
		`{"EXPIRESIN":31536000}`,
		`{"expiresIn":60,"expiresin":31536000}`,
		`{"expiresIn":60,"Paths":["a.png"]}`,
		`{"expiresIn":60,"TRANSFORM":{}}`,
	} {
		p := proxied{role: "editor", method: http.MethodPost, target: "/storage/v1/object/sign/media/a.png", body: body}
		next, rec := sendThroughProxy(t, p, &fakeAuditor{})
		if next.reached || rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, reached %v, want 400 and nothing sent", body, rec.Code, next.reached)
		}
	}
}

// The forwarded body holds only the keys the signing endpoints read, named
// exactly, so nothing the cap did not look at reaches storage.
func TestSigningBodyIsRebuiltFromAcceptedKeys(t *testing.T) {
	body := `{"expiresIn":60,"paths":["a.png"],"transform":{"width":10},"extra":1,"expiresIn ":9}`
	p := proxied{role: "editor", method: http.MethodPost, target: "/storage/v1/object/sign/media", body: body}
	next, rec := sendThroughProxy(t, p, &fakeAuditor{})
	var sent map[string]json.RawMessage
	if err := json.Unmarshal([]byte(next.body), &sent); err != nil || !next.reached {
		t.Fatalf("status %d, upstream body %q (%v)", rec.Code, next.body, err)
	}
	want := map[string]string{"expiresIn": "60", "paths": `["a.png"]`, "transform": `{"width":10}`}
	if len(sent) != len(want) {
		t.Errorf("forwarded %s, want exactly %v", next.body, want)
	}
	for key, value := range want {
		if string(sent[key]) != value {
			t.Errorf("%s = %s, want %s", key, sent[key], value)
		}
	}
	if strings.Contains(next.body, "extra") {
		t.Errorf("forwarded %s, want the unknown key dropped", next.body)
	}
}

// The reviewer's case directly: the clamp must not forward an uncapped expiry
// under any spelling.
func TestClampedSigningBodyNeverCarriesAnUncappedExpiry(t *testing.T) {
	for _, in := range []string{`{"expiresin":31536000}`, `{"expireſIn":31536000}`, `{"EXPIRESIN":31536000}`} {
		out, err := clampedSigningBody(strings.NewReader(in))
		if err == nil || strings.Contains(string(out), "31536000") {
			t.Errorf("%s: forwarded %s (err %v), want a refusal", in, out, err)
		}
	}
}
