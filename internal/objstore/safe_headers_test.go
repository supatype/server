package objstore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sandboxCSP = "sandbox; default-src 'none'"

// uploadTyped puts an object with a Content-Type and fails the test if it could not.
func uploadTyped(t *testing.T, handler http.Handler, objPath, contentType string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/object/files/"+objPath, strings.NewReader("<script>alert(1)</script>"))
	req.Header.Set("Authorization", "Bearer "+userToken(t))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload %s: %d %s", objPath, rec.Code, rec.Body.String())
	}
}

// signedPath returns the store-relative signed download path for an object.
func signedPath(t *testing.T, handler http.Handler, objPath string) string {
	t.Helper()
	rec := do(t, handler, http.MethodPost, "/object/sign/files/"+objPath, userToken(t), strings.NewReader(`{"expiresIn":60}`))
	var signed struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &signed); err != nil {
		t.Fatalf("sign %s: %v %s", objPath, err, rec.Body.String())
	}
	return signed.SignedURL[strings.Index(signed.SignedURL, "/storage/v1")+len("/storage/v1"):]
}

// downloads fetches one object over every serving path: public, authenticated, signed.
func downloads(t *testing.T, handler http.Handler, objPath string) map[string]*httptest.ResponseRecorder {
	t.Helper()
	return map[string]*httptest.ResponseRecorder{
		"public":        do(t, handler, http.MethodGet, "/object/public/files/"+objPath, "", nil),
		"authenticated": do(t, handler, http.MethodGet, "/object/authenticated/files/"+objPath, userToken(t), nil),
		"signed":        do(t, handler, http.MethodGet, signedPath(t, handler, objPath), "", nil),
	}
}

type headerCase struct {
	objPath, contentType, disposition, csp string
}

func checkServedHeaders(t *testing.T, tc headerCase) {
	t.Helper()
	handler, _ := newStore(t)
	makeBucket(t, handler, "files", true)
	uploadTyped(t, handler, tc.objPath, tc.contentType)
	for route, rec := range downloads(t, handler, tc.objPath) {
		h := rec.Header()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: status %d", route, tc.objPath, rec.Code)
		}
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s %s: nosniff = %q", route, tc.objPath, got)
		}
		if got := h.Get("Content-Disposition"); got != tc.disposition {
			t.Errorf("%s %s: disposition = %q, want %q", route, tc.objPath, got, tc.disposition)
		}
		if got := h.Get("Content-Security-Policy"); got != tc.csp {
			t.Errorf("%s %s: csp = %q, want %q", route, tc.objPath, got, tc.csp)
		}
	}
}

// An uploaded page or SVG is handed over as a download in a sandbox, so it
// cannot run script on the storage origin.
func TestActiveObjectsAreServedAsSandboxedAttachments(t *testing.T) {
	for _, tc := range []headerCase{
		{"pages/evil.html", "text/html; charset=utf-8", `attachment; filename=evil.html`, sandboxCSP},
		{"img/evil.svg", "image/svg+xml", `attachment; filename=evil.svg`, sandboxCSP},
		{"x/feed.atom", "Application/Atom+XML", `attachment; filename=feed.atom`, sandboxCSP},
	} {
		checkServedHeaders(t, tc)
	}
}

// Passive media stays inline so images still render, but is never sniffed and
// still runs in a sandbox if a browser renders it as a document.
func TestPassiveObjectsStayInline(t *testing.T) {
	for _, tc := range []headerCase{
		{"img/a.png", "image/png", "", sandboxCSP},
		{"img/a.jpg", "image/jpeg", "", sandboxCSP},
	} {
		checkServedHeaders(t, tc)
	}
}

func TestApplySafeObjectHeadersPolicy(t *testing.T) {
	for _, tc := range []struct {
		contentType, existing, name, wantDisposition string
	}{
		{"text/html", "", "a b\".html", `attachment; filename="a b\".html"`},
		{"text/html", `inline; filename="kept.html"`, "other.html", `attachment; filename=kept.html`},
		{"text/html", "", "caf\u00e9.html", `attachment; filename*=utf-8''caf%C3%A9.html`},
		{"application/javascript", "", "", "attachment"},
		{"multipart/x-mixed-replace; boundary=x", "", "m", "attachment; filename=m"},
		{"", "", "nothing", "attachment; filename=nothing"},
		{"application/json", "", "a.json", ""},
		{"text/plain", "inline", "a.txt", "inline"},
	} {
		h := http.Header{}
		h.Set("Content-Type", tc.contentType)
		if tc.existing != "" {
			h.Set("Content-Disposition", tc.existing)
		}
		ApplySafeObjectHeaders(h, tc.name)
		if got := h.Get("Content-Disposition"); got != tc.wantDisposition {
			t.Errorf("%q: disposition = %q, want %q", tc.contentType, got, tc.wantDisposition)
		}
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%q: nosniff = %q", tc.contentType, got)
		}
	}
}

type servedTypeCase struct {
	in, wantType string
	active       bool
}

// servedTypeCases covers what a Content-Type can look like on the way out: a
// list, parameters, case, whitespace, nothing at all, and every active type.
func servedTypeCases() []servedTypeCase {
	cases := []servedTypeCase{
		{"text/plain, text/html", octetStream, true},
		{"image/png,image/svg+xml", octetStream, true},
		{`text/plain; x="a,b"`, octetStream, true},
		{"TEXT/HTML;;;", octetStream, true},
		{"text", octetStream, true},
		{"", "", true},
		{"   ", "", true},
		{"text/html; charset=utf-8", "text/html; charset=utf-8", true},
		{"Text/HTML", "text/html", true},
		{" Image/PNG ", "image/png", false},
		{"image/png; Q=1", "image/png; q=1", false},
		{"application/json", "application/json", false},
		{"text/plain", "text/plain", false},
		{"Application/Atom+XML", "application/atom+xml", true},
	}
	for active := range activeContentTypes {
		cases = append(cases, servedTypeCase{active, active, true})
	}
	return cases
}

// Every object is sandboxed and served with its type as parsed; one whose type
// cannot be read as exactly one type is an octet-stream download.
func TestServedContentTypeIsCanonicalAndSandboxed(t *testing.T) {
	for _, tc := range servedTypeCases() {
		h := http.Header{}
		h.Set("Content-Type", tc.in)
		ApplySafeObjectHeaders(h, "obj")
		if got := h.Get("Content-Type"); got != tc.wantType {
			t.Errorf("%q: served type = %q, want %q", tc.in, got, tc.wantType)
		}
		if got := h.Get("Content-Security-Policy"); got != sandboxCSP {
			t.Errorf("%q: csp = %q", tc.in, got)
		}
		if got := h.Get("Content-Disposition") != ""; got != tc.active {
			t.Errorf("%q: attachment = %v, want %v", tc.in, got, tc.active)
		}
	}
}

// An upload keeps only a type it could parse as exactly one type, so a comma
// list is stored and served as an octet-stream download.
func TestUploadStoresOnlyAParsedType(t *testing.T) {
	for in, want := range map[string]string{
		"text/plain, text/html":   octetStream,
		"text/plain,text/html":    octetStream,
		"TEXT/HTML;;;":            octetStream,
		"":                        octetStream,
		" Image/PNG; foo=bar ":    "image/png",
		"text/html; charset=utf8": "text/html",
	} {
		handler, _ := newStore(t)
		makeBucket(t, handler, "files", true)
		uploadTyped(t, handler, "x/obj", in)
		rec := do(t, handler, http.MethodGet, "/object/public/files/x/obj", "", nil)
		if got := rec.Header().Get("Content-Type"); got != want {
			t.Errorf("%q: served %q, want %q", in, got, want)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != sandboxCSP {
			t.Errorf("%q: csp = %q", in, got)
		}
	}
}
