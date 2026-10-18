package draftinsert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// refused is PostgREST v14.18's answer to `POST /posts` with `Prefer: return=representation`
// from an editor whose read rule hides unpublished posts, byte for byte.
const refused = `{"code":"42501","details":null,"hint":null,"message":"new row violates row-level security policy for table \"posts\""}`

// drafts says `posts` keeps drafts, and counts how often it was asked.
func drafts(asked *int) Lookup {
	return func(context.Context) (map[string]bool, bool) {
		*asked++
		return map[string]bool{"posts": true}, true
	}
}

// answering is an upstream that answers every request with `status` and `body`.
func answering(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(method, path, strings.NewReader(`{"title":"x"}`)))
	return rr
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body is not JSON: %v (%q)", err, rr.Body.String())
	}
	return body
}

func TestARefusedInsertIntoADraftsTableNamesCreateRecord(t *testing.T) {
	asked := 0
	rr := serve(Middleware(drafts(&asked), answering(http.StatusForbidden, refused)), http.MethodPost, "/posts")

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want PostgREST's 403 kept", rr.Code)
	}
	body := decode(t, rr)
	if body["hint"] != Hint {
		t.Fatalf("hint = %v, want the create_record hint", body["hint"])
	}
	message, _ := body["message"].(string)
	if body["code"] != "42501" || !strings.HasPrefix(message, rlsRefusal) {
		t.Fatalf("the refusal itself changed: %v", body)
	}
	if got, want := rr.Header().Get("Content-Length"), strconv.Itoa(rr.Body.Len()); got != want {
		t.Fatalf("Content-Length = %s, want %s for the rewritten body", got, want)
	}
}

func TestTheHintNamesCreateRecordAndTheClient(t *testing.T) {
	for _, phrase := range []string{"keeps drafts", "supatype.create_record", "client"} {
		if !strings.Contains(Hint, phrase) {
			t.Errorf("the hint does not say %q", phrase)
		}
	}
}

func TestATableWithoutDraftsKeepsItsRefusalAsItWas(t *testing.T) {
	asked := 0
	rr := serve(Middleware(drafts(&asked), answering(http.StatusForbidden, refused)), http.MethodPost, "/comments")
	if rr.Body.String() != refused {
		t.Fatalf("body = %q, want PostgREST's untouched", rr.Body.String())
	}
}

func TestAHintPostgRESTAlreadySentIsKept(t *testing.T) {
	own := `{"code":"42501","details":null,"hint":"their own","message":"new row violates row-level security policy for table \"posts\""}`
	asked := 0
	rr := serve(Middleware(drafts(&asked), answering(http.StatusForbidden, own)), http.MethodPost, "/posts")
	if decode(t, rr)["hint"] != "their own" {
		t.Fatalf("body = %q, want the existing hint kept", rr.Body.String())
	}
}

func TestAPermissionRefusalThatIsNotRowSecurityIsLeftAlone(t *testing.T) {
	grant := `{"code":"42501","details":null,"hint":null,"message":"permission denied for table posts"}`
	asked := 0
	rr := serve(Middleware(drafts(&asked), answering(http.StatusForbidden, grant)), http.MethodPost, "/posts")
	if rr.Body.String() != grant {
		t.Fatalf("body = %q, want a missing grant left as PostgREST said it", rr.Body.String())
	}
}

func TestOnlyARefusedInsertAsksWhichTablesKeepDrafts(t *testing.T) {
	asked := 0
	created := `[{"id":"1","title":"x"}]`
	h := Middleware(drafts(&asked), answering(http.StatusCreated, created))

	rr := serve(h, http.MethodPost, "/posts")
	if rr.Code != http.StatusCreated || rr.Body.String() != created {
		t.Fatalf("got %d %q, want the insert's answer streamed through", rr.Code, rr.Body.String())
	}
	if asked != 0 {
		t.Fatalf("the drafts tables were asked %d times for an insert that succeeded", asked)
	}
}

func TestReadsAndRPCCallsAreNotLookedAt(t *testing.T) {
	asked := 0
	h := Middleware(drafts(&asked), answering(http.StatusForbidden, refused))
	for _, call := range []struct{ method, path string }{
		{http.MethodGet, "/posts"},
		{http.MethodPatch, "/posts"},
		{http.MethodPost, "/rpc/create_record"},
	} {
		if rr := serve(h, call.method, call.path); rr.Body.String() != refused {
			t.Errorf("%s %s: body = %q, want it untouched", call.method, call.path, rr.Body.String())
		}
	}
	if asked != 0 {
		t.Fatalf("the drafts tables were asked %d times for requests that are not inserts", asked)
	}
}

func TestAnUnknownClassificationAddsNothing(t *testing.T) {
	unknown := func(context.Context) (map[string]bool, bool) { return nil, false }
	rr := serve(Middleware(unknown, answering(http.StatusForbidden, refused)), http.MethodPost, "/posts")
	if rr.Body.String() != refused {
		t.Fatalf("body = %q, want nothing claimed when the schema could not be read", rr.Body.String())
	}
}
