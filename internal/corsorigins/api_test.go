package corsorigins

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func call(t *testing.T, h http.Handler, method, body string) (int, Response) {
	t.Helper()
	req := httptest.NewRequest(method, "/admin/cors-origins", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out Response
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v: %s", err, rec.Body.String())
		}
	}
	return rec.Code, out
}

func admitAll(http.ResponseWriter, *http.Request, bool) bool { return true }

func apiOn(store Store, mode string) http.Handler {
	policy := NewPolicy(store, Defaults{
		SiteURL:    "https://app.example.com",
		Configured: []string{"https://admin.example.com"},
		Platform:   []string{"https://studio.supatype.com"},
	})
	return API(APIConfig{Policy: policy, Mode: mode, Authorize: admitAll,
		Extra: func(*http.Request) []string { return []string{"https://tenant.example.com"} }})
}

func TestTheAPISaysWhatIsInForceAndWhereEachPartComesFrom(t *testing.T) {
	code, got := call(t, apiOn(&fakeStore{}, "standalone"), http.MethodGet, "")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got.Source != "default" || got.SiteOrigin != "https://app.example.com" || !got.Enforced || !got.Editable {
		t.Errorf("got %+v", got)
	}
	if strings.Join(got.Configured, ",") != "https://admin.example.com,https://tenant.example.com" {
		t.Errorf("configured = %v", got.Configured)
	}
	if strings.Join(got.Origins, ",") != "https://studio.supatype.com,https://app.example.com,https://admin.example.com,https://tenant.example.com" {
		t.Errorf("origins = %v", got.Origins)
	}
}

func TestTheAPISavesAndResets(t *testing.T) {
	h := apiOn(&fakeStore{}, "standalone")
	code, got := call(t, h, http.MethodPut, `{"origins":["https://partner.example/"]}`)
	if code != http.StatusOK || got.Source != "saved" || strings.Join(got.Saved, ",") != "https://partner.example" {
		t.Fatalf("save: %d %+v", code, got)
	}
	code, got = call(t, h, http.MethodDelete, "")
	if code != http.StatusOK || got.Source != "default" || got.Saved != nil {
		t.Fatalf("reset: %d %+v", code, got)
	}
}

func TestTheAPIRefusesWhatItCannotSave(t *testing.T) {
	h := apiOn(&fakeStore{}, "standalone")
	for body, want := range map[string]int{
		`{"origins":["https://x.example/path"]}`: http.StatusBadRequest,
		`{"origins":"https://x.example"}`:        http.StatusBadRequest,
		`{}`:                                     http.StatusBadRequest,
		`not json`:                               http.StatusBadRequest,
	} {
		if code, _ := call(t, h, http.MethodPut, body); code != want {
			t.Errorf("%s: status %d, want %d", body, code, want)
		}
	}
	if code, _ := call(t, apiOn(nil, "standalone"), http.MethodPut, `{"origins":[]}`); code != http.StatusConflict {
		t.Errorf("no store: status %d, want 409", code)
	}
	if code, _ := call(t, h, http.MethodPost, `{}`); code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", code)
	}
}

func TestTheAPISaysDevModeDoesNotEnforce(t *testing.T) {
	_, got := call(t, apiOn(&fakeStore{}, "dev"), http.MethodGet, "")
	if got.Enforced {
		t.Error("dev mode reflects every origin, and the API should say so")
	}
}

func TestARefusedCallerChangesNothing(t *testing.T) {
	store := &fakeStore{}
	policy := NewPolicy(store, Defaults{})
	h := API(APIConfig{Policy: policy, Mode: "standalone", Authorize: func(w http.ResponseWriter, _ *http.Request, _ bool) bool {
		w.WriteHeader(http.StatusForbidden)
		return false
	}})
	if code, _ := call(t, h, http.MethodPut, `{"origins":["https://x.example"]}`); code != http.StatusForbidden {
		t.Errorf("status %d", code)
	}
	if store.saved != nil {
		t.Error("a refused save must not reach the store")
	}
}

func TestTheAPIReportsAStoreThatCannotBeWritten(t *testing.T) {
	h := apiOn(&brokenStore{}, "standalone")
	if code, _ := call(t, h, http.MethodPut, `{"origins":["https://x.example"]}`); code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", code)
	}
}
