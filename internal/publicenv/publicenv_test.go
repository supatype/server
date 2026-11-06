package publicenv

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testURL  = "https://api.example.com"
	testAnon = "anon.jwt.value"
)

func serve(t *testing.T, v Values, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler(v).ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func on() Values { return Values{Enabled: true, URL: testURL, AnonKey: testAnon} }

func TestJSONBody(t *testing.T) {
	rec := serve(t, on(), http.MethodGet, JSONPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v: %q", err, rec.Body.String())
	}
	if len(body) != 2 || body["url"] != testURL || body["anonKey"] != testAnon {
		t.Errorf("body = %v", body)
	}
}

func TestScriptBody(t *testing.T) {
	rec := serve(t, on(), http.MethodGet, ScriptPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	want := `window.__SUPATYPE_ENV__ = {"url":"https://api.example.com","anonKey":"anon.jwt.value"};` + "\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// A value that could close the script element or break out of the literal is
// escaped by the JSON encoder, never spliced in raw.
func TestScriptEscapesHostileValues(t *testing.T) {
	v := Values{Enabled: true, URL: `https://x/</script><script>alert(1)</script>`, AnonKey: "a\"b\u2028c"}
	body := serve(t, v, http.MethodGet, ScriptPath).Body.String()
	for _, raw := range []string{"</script>", "<script>", "\u2028", `a"b`} {
		if strings.Contains(body, raw) {
			t.Errorf("body carries %q unescaped: %q", raw, body)
		}
	}
	literal := strings.TrimSuffix(strings.TrimPrefix(body, "window.__SUPATYPE_ENV__ = "), ";\n")
	var got Values
	if err := json.Unmarshal([]byte(literal), &got); err != nil || got.URL != v.URL || got.AnonKey != v.AnonKey {
		t.Errorf("escaped literal does not decode back: %v %+v", err, got)
	}
}

func TestHeadHasHeadersAndNoBody(t *testing.T) {
	for _, path := range []string{JSONPath, ScriptPath} {
		get := serve(t, on(), http.MethodGet, path)
		head := serve(t, on(), http.MethodHead, path)
		if head.Code != http.StatusOK {
			t.Errorf("%s HEAD status %d", path, head.Code)
		}
		if head.Body.Len() != 0 {
			t.Errorf("%s HEAD wrote a body: %q", path, head.Body.String())
		}
		for _, h := range []string{"Content-Type", "Cache-Control", "Content-Length"} {
			if head.Header().Get(h) == "" || head.Header().Get(h) != get.Header().Get(h) {
				t.Errorf("%s HEAD %s = %q, GET %q", path, h, head.Header().Get(h), get.Header().Get(h))
			}
		}
	}
}

func TestOtherMethodsAre405(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions} {
		rec := serve(t, on(), method, ScriptPath)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", method, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
			t.Errorf("%s: Allow = %q", method, got)
		}
		if strings.Contains(rec.Body.String(), testAnon) {
			t.Errorf("%s: 405 body carries the anon key", method)
		}
	}
}

func TestSwitchedOffIs404NoStore(t *testing.T) {
	v := on()
	v.Enabled = false
	for _, path := range []string{JSONPath, ScriptPath} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := serve(t, v, method, path)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: status %d, want 404", method, path, rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("%s %s: Cache-Control = %q, want no-store", method, path, got)
			}
			if strings.Contains(rec.Body.String(), testAnon) {
				t.Errorf("%s %s: switched off but body carries the anon key", method, path)
			}
		}
	}
}

// jwtWithRole is an unsigned token carrying role. Only the claim is read, so
// the signature segment is filler.
func jwtWithRole(role string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc([]byte(`{"role":"`+role+`"}`)) + ".sig"
}

// misconfigurations is every configuration that cannot be served, with the
// settings its answer must name, in order: key before URL.
func misconfigurations() map[string]struct {
	mutate func(*Values)
	names  []string
} {
	serviceKey := jwtWithRole("service_role")
	return map[string]struct {
		mutate func(*Values)
		names  []string
	}{
		"no anon key":                   {func(v *Values) { v.AnonKey = "  " }, []string{"SUPATYPE_ANON_KEY"}},
		"no public URL":                 {func(v *Values) { v.URL = "" }, []string{"SUPATYPE_URL", "API_EXTERNAL_URL"}},
		"a service role anon key":       {func(v *Values) { v.AnonKey = serviceKey }, []string{"SUPATYPE_ANON_KEY", "service role key"}},
		"no anon key and no URL":        {func(v *Values) { v.AnonKey = ""; v.URL = "" }, []string{"SUPATYPE_ANON_KEY is not set", "SUPATYPE_URL", "SUPATYPE_API_EXTERNAL_URL"}},
		"a service role key and no URL": {func(v *Values) { v.AnonKey = serviceKey; v.URL = "" }, []string{"service role key", "SUPATYPE_URL", "SUPATYPE_API_EXTERNAL_URL"}},
	}
}

// namesInOrder reports the first of names that text does not carry after the
// one before it, or "" when it carries them all in order.
func namesInOrder(text string, names []string) string {
	for _, want := range names {
		i := strings.Index(text, want)
		if i < 0 {
			return want
		}
		text = text[i+len(want):]
	}
	return ""
}

// The document a deployment cannot serve correctly names every setting to fix,
// in the order key then URL, with 503 and no-store, and never echoes the key it
// refused. Naming only the first problem sends the deployer back for each.
func TestMisconfiguredJSONIs503NamingTheSetting(t *testing.T) {
	for name, tc := range misconfigurations() {
		v := on()
		tc.mutate(&v)
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := serve(t, v, method, JSONPath)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("%s: %s status %d, want 503", name, method, rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("%s: %s Cache-Control = %q, want no-store", name, method, got)
			}
		}
		body := serve(t, v, http.MethodGet, JSONPath).Body.String()
		if missing := namesInOrder(body, tc.names); missing != "" {
			t.Errorf("%s: body %q does not name %s, or not in order", name, body, missing)
		}
		if strings.Contains(body, jwtWithRole("service_role")) || strings.Contains(body, "__SUPATYPE_ENV__") {
			t.Errorf("%s: body carries values: %q", name, body)
		}
	}
}

// A browser neither runs a script whose answer is an error status nor shows
// the page why it failed. So the script answers 200 with the reason in place
// of the values, the same text the document's 503 carries, and no-store so the
// fixed configuration is read on the next load. It never carries a key.
func TestMisconfiguredScriptCarriesTheReason(t *testing.T) {
	for name, tc := range misconfigurations() {
		v := on()
		tc.mutate(&v)
		rec := serve(t, v, http.MethodGet, ScriptPath)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", name, rec.Code)
		}
		checkErrorScriptHeaders(t, name, rec)
		body := rec.Body.String()
		got := decodeScript(t, name, body)
		want := strings.TrimSuffix(serve(t, v, http.MethodGet, JSONPath).Body.String(), "\n")
		if len(got) != 1 || got["error"] != want {
			t.Errorf("%s: script sets %v, want only error %q", name, got, want)
		}
		if missing := namesInOrder(got["error"], tc.names); missing != "" {
			t.Errorf("%s: error %q does not name %s, or not in order", name, got["error"], missing)
		}
		if strings.Contains(body, jwtWithRole("service_role")) {
			t.Errorf("%s: script carries the refused key: %q", name, body)
		}
		head := serve(t, v, http.MethodHead, ScriptPath)
		if head.Code != http.StatusOK || head.Body.Len() != 0 {
			t.Errorf("%s: HEAD = %d with %d body bytes", name, head.Code, head.Body.Len())
		}
		checkErrorScriptHeaders(t, name+" HEAD", head)
	}
}

func checkErrorScriptHeaders(t *testing.T, name string, rec *httptest.ResponseRecorder) {
	t.Helper()
	for h, want := range map[string]string{
		"Content-Type":           "application/javascript; charset=utf-8",
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := rec.Header().Get(h); got != want {
			t.Errorf("%s: %s = %q, want %q", name, h, got, want)
		}
	}
}

// decodeScript is the object literal a script body assigns, which must be the
// whole statement and valid JSON.
func decodeScript(t *testing.T, name, body string) map[string]string {
	t.Helper()
	const prefix, suffix = "window.__SUPATYPE_ENV__ = ", ";\n"
	if !strings.HasPrefix(body, prefix) || !strings.HasSuffix(body, suffix) {
		t.Fatalf("%s: body is not one assignment: %q", name, body)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(body, prefix), suffix)), &got); err != nil {
		t.Fatalf("%s: literal is not JSON: %v: %q", name, err, body)
	}
	return got
}

// Only a token that decodes and claims service_role is refused. An anon token
// and a key that is not a JWT at all are served as configured.
func TestAnAnonRoleOrOpaqueKeyIsServed(t *testing.T) {
	for _, key := range []string{jwtWithRole("anon"), "opaque-key", "a.!!!.c", testAnon} {
		v := on()
		v.AnonKey = key
		if rec := serve(t, v, http.MethodGet, JSONPath); rec.Code != http.StatusOK {
			t.Errorf("anon key %q: status %d, want 200", key, rec.Code)
		}
	}
}

func TestPublicURL(t *testing.T) {
	for _, tc := range []struct{ supatypeURL, external, want string }{
		{"https://api.example.com/", "https://other.example.com", "https://api.example.com"},
		{"", "https://api.example.com", "https://api.example.com"},
		{"  ", "https://api.example.com/auth/v1/", "https://api.example.com"},
		{"", "https://api.example.com/auth/v1", "https://api.example.com"},
		{"", "", ""},
	} {
		if got := PublicURL(tc.supatypeURL, tc.external); got != tc.want {
			t.Errorf("PublicURL(%q, %q) = %q, want %q", tc.supatypeURL, tc.external, got, tc.want)
		}
	}
}
