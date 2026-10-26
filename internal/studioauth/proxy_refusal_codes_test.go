package studioauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// A refusal from the Studio proxy, or from a route RequireAdmin gates, has the
// shape every other Studio API uses: `error` is the code that follows the
// status, `message` the sentence for a person. It used to put the sentence in
// `error`, so a client branching on the code saw "Authentication required"
// where it expected unauthorized.
func TestProxyRefusalsCarryTheCodeAndTheSentence(t *testing.T) {
	for name, tc := range map[string]struct {
		call    func(t *testing.T) *httptest.ResponseRecorder
		status  int
		code    string
		message string
	}{
		"proxy, not signed in": {func(t *testing.T) *httptest.ResponseRecorder {
			return proxyCall(membershipConfig("admin"), studioRequest(http.MethodGet, "/rest/v1/posts", ""))
		}, http.StatusUnauthorized, "unauthorized", "Authentication required"},
		"gated route, not signed in": {func(t *testing.T) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			RequireAdmin(membershipConfig("admin"), http.NotFoundHandler()).
				ServeHTTP(rec, studioRequest(http.MethodGet, "/studio-config", ""))
			return rec
		}, http.StatusUnauthorized, "unauthorized", "Authentication required"},
		"proxy, a role that may not write": {func(t *testing.T) *httptest.ResponseRecorder {
			return proxyCall(membershipConfig("viewer"), studioRequest(http.MethodPost, "/rest/v1/posts", proxyCaller(t)))
		}, http.StatusForbidden, "forbidden", `Studio role "viewer" cannot perform this request`},
		"proxy, a role that may not elevate": {func(t *testing.T) *httptest.ResponseRecorder {
			req := studioRequest(http.MethodGet, "/rest/v1/posts", proxyCaller(t))
			req.Header.Set(ActingModeHeader, ModeElevated)
			return proxyCall(membershipConfig("viewer"), req)
		}, http.StatusForbidden, "forbidden", `Studio role "viewer" cannot act in the mode it asked for`},
	} {
		t.Run(name, func(t *testing.T) {
			rec := tc.call(t)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}
			assertErrorBody(t, rec, tc.code, tc.message)
		})
	}
}

func proxyCall(c Config, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	ProxyHandler(http.NotFoundHandler(), c).ServeHTTP(rec, req)
	return rec
}

func proxyCaller(t *testing.T) string {
	return bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
}

func assertErrorBody(t *testing.T, rec *httptest.ResponseRecorder, code, message string) {
	t.Helper()
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %q", rec.Body.String())
	}
	if body.Error != code || body.Message != message {
		t.Errorf("body = %+v, want error %q and message %q", body, code, message)
	}
}

// A status with no code of its own takes its class's, so a client branching on
// the code still tells its own mistake from a server fault.
func TestAStatusWithNoCodeOfItsOwnTakesItsClassCode(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusRequestEntityTooLarge: "validation_error",
		http.StatusTeapot:                "validation_error",
		http.StatusBadGateway:            "internal_error",
		http.StatusGatewayTimeout:        "internal_error",
	} {
		rec := httptest.NewRecorder()
		WriteErrorBody(rec, status, "the sentence")
		var body struct{ Error, Message string }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%d: %v", status, err)
		}
		if rec.Code != status || body.Error != want || body.Message != "the sentence" {
			t.Errorf("%d: got %d %+v, want code %q", status, rec.Code, body, want)
		}
	}
}
