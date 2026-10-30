package objstore

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The client escapes every segment of an object name with encodeURIComponent,
// which escapes characters Go leaves alone in a path (':' '@' '+' '=' ',' ';'
// '$' '&'). Go then keeps the escaped form as RawPath, chi routes on it, and
// the route parameters arrive still escaped. Each handler must see the name
// the caller meant, not its escaped spelling.

// encodedNames pairs a name with its spelling from encodeURIComponent.
var encodedNames = []struct{ name, escaped string }{
	{"photos/shot 10:30.png", "photos/shot%2010%3A30.png"},
	{"photos/a@b+c=d,e;f$g&h.png", "photos/a%40b%2Bc%3Dd%2Ce%3Bf%24g%26h.png"},
}

func TestAnEncodedObjectNameIsStoredDecoded(t *testing.T) {
	for _, tc := range encodedNames {
		t.Run(tc.name, func(t *testing.T) {
			handler, _ := newStore(t)
			makeBucket(t, handler, "files", true)

			rec := do(t, handler, http.MethodPost, "/object/files/"+tc.escaped, userToken(t), strings.NewReader("pixels"))
			if rec.Code != http.StatusOK {
				t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
			}
			var uploaded map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
				t.Fatal(err)
			}
			if uploaded["path"] != tc.name {
				t.Errorf("upload path = %q, want %q", uploaded["path"], tc.name)
			}

			wantListed(t, handler, "photos", strings.TrimPrefix(tc.name, "photos/"))
			wantContent(t, handler, "/object/authenticated/files/"+tc.escaped, userToken(t), "pixels")
			wantContent(t, handler, "/object/public/files/"+tc.escaped, "", "pixels")
			wantContent(t, handler, signedPath(t, handler, tc.escaped), "", "pixels")

			body := `{"prefixes":[` + jsonString(t, tc.name) + `]}`
			rec = do(t, handler, http.MethodDelete, "/object/files", userToken(t), strings.NewReader(body))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), jsonString(t, tc.name)) {
				t.Errorf("delete: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// A name that already arrives decoded is not decoded a second time: "%41"
// spelled "%2541" is the literal three characters, not "A".
func TestADecodedObjectNameIsNotDecodedTwice(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "b", false)

	rec := do(t, handler, http.MethodPost, "/object/b/literal%2541.txt", userToken(t), strings.NewReader("x"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"literal%41.txt"`) {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	wantContent(t, handler, "/object/authenticated/b/literal%2541.txt", userToken(t), "x")
}

// A bucket id escaped more than Go would escape it still names the bucket.
func TestAnEncodedBucketIDNamesTheBucket(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "my-bucket", false)

	if rec := do(t, handler, http.MethodGet, "/bucket/my%2Dbucket", serviceToken(t), nil); rec.Code != http.StatusOK {
		t.Errorf("get: %d %s", rec.Code, rec.Body.String())
	}
	rec := do(t, handler, http.MethodPost, "/object/my%2Dbucket/a%40b.txt", userToken(t), strings.NewReader("x"))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, handler, http.MethodPost, "/object/list/my%2Dbucket", userToken(t), strings.NewReader(`{}`)); !strings.Contains(rec.Body.String(), `"a@b.txt"`) {
		t.Errorf("list: %d %s", rec.Code, rec.Body.String())
	}
}

// wantListed fails unless listing the folder shows the name.
func wantListed(t *testing.T, handler http.Handler, folder, name string) {
	t.Helper()
	rec := do(t, handler, http.MethodPost, "/object/list/files", userToken(t), strings.NewReader(`{"prefix":`+jsonString(t, folder)+`}`))
	var items []listItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("list: %v (%s)", err, rec.Body.String())
	}
	for _, it := range items {
		if it.Name == name {
			return
		}
	}
	t.Errorf("list of %q = %s, want %q in it", folder, rec.Body.String(), name)
}

// wantContent fails unless the download answers the bytes that were uploaded.
func wantContent(t *testing.T, handler http.Handler, target, bearer, want string) {
	t.Helper()
	rec := do(t, handler, http.MethodGet, target, bearer, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Errorf("GET %s: %d %s", target, rec.Code, rec.Body.String())
	}
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
