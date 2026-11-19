package publicenv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveWith(t *testing.T, env func(*http.Request) map[string]string, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	HandlerWith(on(), env).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func staticEnv(m map[string]string) func(*http.Request) map[string]string {
	return func(*http.Request) map[string]string { return m }
}

func TestJSONCarriesThePublicValuesBesideTheAddressAndKey(t *testing.T) {
	rec := serveWith(t, staticEnv(map[string]string{"STRIPE_PK": "pk_live_1", "MAPS": "m"}), JSONPath)
	var body struct {
		URL     string            `json:"url"`
		AnonKey string            `json:"anonKey"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %v: %q", err, rec.Body.String())
	}
	if body.URL != testURL || body.AnonKey != testAnon || body.Env["STRIPE_PK"] != "pk_live_1" || body.Env["MAPS"] != "m" {
		t.Errorf("body = %+v", body)
	}
}

func TestScriptCarriesThePublicValues(t *testing.T) {
	rec := serveWith(t, staticEnv(map[string]string{"STRIPE_PK": "pk_live_1"}), ScriptPath)
	want := `window.__SUPATYPE_ENV__ = {"url":"` + testURL + `","anonKey":"` + testAnon + `","env":{"STRIPE_PK":"pk_live_1"}};` + "\n"
	if rec.Body.String() != want {
		t.Errorf("script = %q, want %q", rec.Body.String(), want)
	}
}

// No values is the document the server has always sent, byte for byte.
func TestNoPublicValuesLeavesTheDocumentAsItWas(t *testing.T) {
	for _, env := range []func(*http.Request) map[string]string{nil, staticEnv(nil), staticEnv(map[string]string{})} {
		rec := serveWith(t, env, JSONPath)
		if strings.Contains(rec.Body.String(), "env") {
			t.Errorf("body = %q, want no env member", rec.Body.String())
		}
	}
}

// The values are read per request, so a change to the manifest reaches the next page load.
func TestPublicValuesAreReadOnEveryRequest(t *testing.T) {
	current := map[string]string{"A": "1"}
	h := HandlerWith(on(), func(*http.Request) map[string]string { return current })
	get := func() string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, JSONPath, nil))
		return rec.Body.String()
	}
	if !strings.Contains(get(), `"A":"1"`) {
		t.Fatal("first read missing A")
	}
	current = map[string]string{"A": "2"}
	if !strings.Contains(get(), `"A":"2"`) {
		t.Fatal("second read still the old value")
	}
}

// A value is data: it cannot close the script element or end the statement early.
func TestPublicValuesCannotBreakOutOfTheScript(t *testing.T) {
	rec := serveWith(t, staticEnv(map[string]string{"X": "</script><script>alert(1)</script> "}), ScriptPath)
	body := rec.Body.String()
	if strings.Contains(body, "</script>") || strings.Contains(body, " ") {
		t.Errorf("script = %q", body)
	}
}

// A misconfigured deployment serves no values with its fault.
func TestAFaultServesNoPublicValues(t *testing.T) {
	rec := httptest.NewRecorder()
	HandlerWith(Values{Enabled: true}, staticEnv(map[string]string{"A": "1"})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ScriptPath, nil))
	if strings.Contains(rec.Body.String(), `"A"`) {
		t.Errorf("script = %q", rec.Body.String())
	}
}
