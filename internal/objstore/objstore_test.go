package objstore

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// This package had no test file at all, which for a thing that writes files to
// disk under paths a caller chooses is the wrong place to have none. What
// follows is mostly about the refusals: a path that tries to leave the storage
// root, a token that is not ours, a bucket someone else's token asked for.

const secret = "local-dev-jwt-secret"

// ─── Harness ──────────────────────────────────────────────────────────────────

// newStore returns a handler over a fresh storage root, and that root.
func newStore(t *testing.T) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	return Handler(root, secret), root
}

// token mints an HS256 JWT this store will accept.
func token(t *testing.T, role string, exp time.Time) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims := jwtClaims{Sub: "user-1", Role: role}
	if !exp.IsZero() {
		claims.Exp = exp.Unix()
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	body := header + "." + base64.RawURLEncoding.EncodeToString(payload)

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func serviceToken(t *testing.T) string { return token(t, "service_role", time.Time{}) }
func userToken(t *testing.T) string    { return token(t, "authenticated", time.Time{}) }

// do runs one request with the given bearer token.
func do(t *testing.T, handler http.Handler, method, path, bearer string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// makeBucket creates a bucket and fails the test if it could not.
func makeBucket(t *testing.T, handler http.Handler, id string, public bool) {
	t.Helper()
	body := fmt.Sprintf(`{"id":%q,"name":%q,"public":%t}`, id, id, public)
	rec := do(t, handler, http.MethodPost, "/bucket", serviceToken(t), strings.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("creating bucket %q: %d %s", id, rec.Code, rec.Body.String())
	}
}

// upload puts an object and fails the test if it could not.
func upload(t *testing.T, handler http.Handler, bucket, objPath, content string) {
	t.Helper()
	rec := do(t, handler, http.MethodPost, "/object/"+bucket+"/"+objPath, userToken(t), strings.NewReader(content))
	if rec.Code != http.StatusOK {
		t.Fatalf("uploading %s/%s: %d %s", bucket, objPath, rec.Code, rec.Body.String())
	}
}

// ─── Paths ────────────────────────────────────────────────────────────────────

// Every path a caller supplies ends up as a filename, so what is refused is the
// whole of this package's safety.
func TestCleanBucketID(t *testing.T) {
	for name, tc := range map[string]struct {
		id      string
		wantErr bool
	}{
		"a plain name":       {"avatars", false},
		"padded":             {"  avatars  ", false},
		"with a hyphen":      {"user-avatars", false},
		"with a dot":         {"my.bucket", false},
		"empty":              {"", true},
		"only whitespace":    {"   ", true},
		"a forward slash":    {"a/b", true},
		"a backslash":        {`a\b`, true},
		"a parent reference": {"..", true},
		"a leading parent":   {"../etc", true},
	} {
		got, err := cleanBucketID(tc.id)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: cleanBucketID(%q) = (%q, %v)", name, tc.id, got, err)
		}
		if err == nil && got != strings.TrimSpace(tc.id) {
			t.Errorf("%s: got %q", name, got)
		}
	}
}

func TestCleanObjectPath(t *testing.T) {
	for name, tc := range map[string]struct {
		path       string
		allowEmpty bool
		wantErr    bool
	}{
		"a file":                {"avatar.png", false, false},
		"a nested file":         {"users/1/avatar.png", false, false},
		"a leading slash":       {"/avatar.png", false, false},
		"empty, not allowed":    {"", false, true},
		"empty, allowed":        {"", true, false},
		"a slash only, allowed": {"/", true, false},
		"a parent reference":    {"../secrets", false, true},
		"a deeper escape":       {"a/../../secrets", false, true},
		"an absolute path":      {"//etc/passwd", false, true},
	} {
		_, err := cleanObjectPath(tc.path, tc.allowEmpty)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: cleanObjectPath(%q, %v) = %v", name, tc.path, tc.allowEmpty, err)
		}
	}
}

// The three path builders all refuse what cleanBucketID and cleanObjectPath
// refuse, so nothing reaches the filesystem on a bad path.
func TestPathBuildersRefuseWhatTheirPartsRefuse(t *testing.T) {
	s := &store{root: t.TempDir(), mu: &sync.RWMutex{}}

	if _, err := s.bucketDir(".."); err == nil {
		t.Error("bucketDir accepted a parent reference")
	}
	for name, tc := range map[string]struct{ bucket, path string }{
		"a bad bucket": {"..", "a.png"},
		"a bad path":   {"avatars", "../a.png"},
	} {
		if _, err := s.objectFileRel(tc.bucket, tc.path); err == nil {
			t.Errorf("objectFileRel accepted %s", name)
		}
		if _, err := s.objectMetaRel(tc.bucket, tc.path); err == nil {
			t.Errorf("objectMetaRel accepted %s", name)
		}
	}

	// The sidecar sits beside the object under .meta, keyed by the same path.
	got, err := s.objectMetaRel("avatars", "users/1/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("avatars", ".meta", "users", "1", "a.png.json"); got != want {
		t.Errorf("meta path = %q, want %q", got, want)
	}
}

// ─── Tokens ───────────────────────────────────────────────────────────────────

// A token is only ours if we signed it and it has not expired.
func TestParseJWT(t *testing.T) {
	s := &store{root: t.TempDir(), jwtSecret: []byte(secret), mu: &sync.RWMutex{}}

	elsewhere := &store{jwtSecret: []byte("another-secret")}
	foreign := token(t, "service_role", time.Time{})

	for name, tc := range map[string]struct {
		token string
		want  bool
	}{
		"one we signed":          {serviceToken(t), true},
		"not yet expired":        {token(t, "authenticated", time.Now().Add(time.Hour)), true},
		"expired":                {token(t, "authenticated", time.Now().Add(-time.Hour)), false},
		"not three segments":     {"a.b", false},
		"a signature not base64": {"a.b.!!!", false},
		"a payload not base64":   {"a.!!!.c", false},
		"a payload that is not JSON": {
			"a." + base64.RawURLEncoding.EncodeToString([]byte("{")) + ".c", false,
		},
		"empty": {"", false},
	} {
		got := s.parseJWT(tc.token) != nil
		if got != tc.want {
			t.Errorf("%s: accepted = %v, want %v", name, got, tc.want)
		}
	}

	// A token we did not sign is not ours, whatever it claims.
	if elsewhere.parseJWT(foreign) != nil {
		t.Error("a token signed with another secret was accepted")
	}
}

// The SDK sends the key in either header, so both have to work, and the bearer
// wins when both are present.
func TestExtractClaimsReadsEitherHeader(t *testing.T) {
	s := &store{root: t.TempDir(), jwtSecret: []byte(secret), mu: &sync.RWMutex{}}
	valid := serviceToken(t)

	for name, tc := range map[string]struct {
		auth, apikey string
		want         bool
	}{
		"a bearer token":   {"Bearer " + valid, "", true},
		"an apikey header": {"", valid, true},
		"both":             {"Bearer " + valid, "nonsense", true},
		"not a bearer":     {"Basic " + valid, "", false},
		"neither":          {"", "", false},
		"an empty bearer":  {"Bearer ", "", false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/bucket", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		if tc.apikey != "" {
			req.Header.Set("apikey", tc.apikey)
		}
		if got := s.extractClaims(req) != nil; got != tc.want {
			t.Errorf("%s: accepted = %v, want %v", name, got, tc.want)
		}
	}
}

func TestIsServiceRole(t *testing.T) {
	if isServiceRole(nil) {
		t.Error("no claims is not the service role")
	}
	if isServiceRole(&jwtClaims{Role: "authenticated"}) {
		t.Error("an end user is not the service role")
	}
	if !isServiceRole(&jwtClaims{Role: "service_role"}) {
		t.Error("the service role was not recognised")
	}
}

// ─── Who may reach what ───────────────────────────────────────────────────────

// Bucket administration is the service role's alone. An end user's token
// creating or deleting buckets would be a tenant reconfiguring its own storage.
func TestBucketAdministrationNeedsTheServiceRole(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", false)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/bucket"},
		{http.MethodPost, "/bucket"},
		{http.MethodGet, "/bucket/avatars"},
		{http.MethodPut, "/bucket/avatars"},
		{http.MethodDelete, "/bucket/avatars"},
		{http.MethodPost, "/bucket/avatars/empty"},
	}

	for _, route := range routes {
		for name, bearer := range map[string]string{
			"an end user":    userToken(t),
			"no token":       "",
			"a bad token":    "not-a-token",
			"an expired one": token(t, "service_role", time.Now().Add(-time.Hour)),
		} {
			rec := do(t, handler, route.method, route.path, bearer, strings.NewReader("{}"))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with %s: status = %d, want 401", route.method, route.path, name, rec.Code)
			}
		}
	}
}

// The object routes need a token, but not the service role: they are what an
// application's users call.
func TestObjectRoutesNeedATokenButNotTheServiceRole(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", false)

	routes := []struct{ method, path string }{
		{http.MethodPost, "/object/list/avatars"},
		{http.MethodPost, "/object/sign/avatars/a.png"},
		{http.MethodGet, "/object/authenticated/avatars/a.png"},
		{http.MethodPost, "/object/avatars/a.png"},
		{http.MethodDelete, "/object/avatars"},
	}

	for _, route := range routes {
		rec := do(t, handler, route.method, route.path, "", strings.NewReader("{}"))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s unauthenticated: status = %d, want 401", route.method, route.path, rec.Code)
		}

		rec = do(t, handler, route.method, route.path, userToken(t), strings.NewReader("{}"))
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s with an end user's token was refused", route.method, route.path)
		}
	}
}

// ─── Buckets ──────────────────────────────────────────────────────────────────

func TestBucketLifecycle(t *testing.T) {
	handler, root := newStore(t)

	// Nothing to begin with.
	rec := do(t, handler, http.MethodGet, "/bucket", serviceToken(t), nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("a fresh store listed %q", rec.Body.String())
	}

	makeBucket(t, handler, "avatars", true)

	// The directory is real.
	if info, err := os.Stat(filepath.Join(root, "avatars")); err != nil || !info.IsDir() {
		t.Errorf("the bucket directory was not created: %v", err)
	}

	// And it comes back.
	rec = do(t, handler, http.MethodGet, "/bucket/avatars", serviceToken(t), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	var got Bucket
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "avatars" || !got.Public || got.CreatedAt == "" {
		t.Errorf("bucket = %+v", got)
	}

	// Updated in place.
	rec = do(t, handler, http.MethodPut, "/bucket/avatars", serviceToken(t),
		strings.NewReader(`{"public":false,"file_size_limit":1024,"allowed_mime_types":["image/png"]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, handler, http.MethodGet, "/bucket/avatars", serviceToken(t), nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Public || got.FileSizeLimit == nil || *got.FileSizeLimit != 1024 ||
		len(got.AllowedMimeTypes) != 1 || got.AllowedMimeTypes[0] != "image/png" {
		t.Errorf("after update: %+v", got)
	}

	// And gone.
	rec = do(t, handler, http.MethodDelete, "/bucket/avatars", serviceToken(t), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "avatars")); !os.IsNotExist(err) {
		t.Error("the bucket directory outlived the bucket")
	}
}

// A PUT that names nothing changes nothing but the timestamp, rather than
// resetting the fields it omitted.
func TestUpdateOnlyTouchesWhatTheBodyNames(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)

	rec := do(t, handler, http.MethodPut, "/bucket/avatars", serviceToken(t), strings.NewReader(`{}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d", rec.Code)
	}

	rec = do(t, handler, http.MethodGet, "/bucket/avatars", serviceToken(t), nil)
	var got Bucket
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Public {
		t.Error("an empty update turned a public bucket private")
	}
}

func TestCreateBucketRefusals(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", false)

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"not JSON":            {"{", http.StatusBadRequest},
		"no name":             {`{"id":"x"}`, http.StatusBadRequest},
		"a name that escapes": {`{"name":"../etc"}`, http.StatusBadRequest},
		"an id that escapes":  {`{"id":"../etc","name":"ok"}`, http.StatusBadRequest},
		"one that exists":     {`{"name":"avatars"}`, http.StatusConflict},
	} {
		rec := do(t, handler, http.MethodPost, "/bucket", serviceToken(t), strings.NewReader(tc.body))
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// The id defaults to the name, which is what the SDK relies on when it sends
// only a name.
func TestCreateBucketDefaultsTheIDToTheName(t *testing.T) {
	handler, _ := newStore(t)

	rec := do(t, handler, http.MethodPost, "/bucket", serviceToken(t), strings.NewReader(`{"name":"avatars"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, handler, http.MethodGet, "/bucket/avatars", serviceToken(t), nil); rec.Code != http.StatusOK {
		t.Errorf("the bucket is not addressable by its name: %d", rec.Code)
	}
}

// Deleting a bucket with objects in it would orphan them on disk.
func TestABucketWithObjectsCannotBeDeleted(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", false)
	upload(t, handler, "avatars", "a.png", "png bytes")

	rec := do(t, handler, http.MethodDelete, "/bucket/avatars", serviceToken(t), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	// Emptying it first makes the delete work, and the sidecar directory does
	// not count as content.
	if rec := do(t, handler, http.MethodPost, "/bucket/avatars/empty", serviceToken(t), nil); rec.Code != http.StatusOK {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, handler, http.MethodDelete, "/bucket/avatars", serviceToken(t), nil); rec.Code != http.StatusOK {
		t.Errorf("delete after empty: %d %s", rec.Code, rec.Body.String())
	}
}

// A bucket holding only its own sidecar directory is empty as far as the caller
// is concerned.
func TestABucketHoldingOnlySidecarsIsEmpty(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", false)

	if err := os.MkdirAll(filepath.Join(root, "avatars", ".meta"), 0o700); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, handler, http.MethodDelete, "/bucket/avatars", serviceToken(t), nil); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the sidecar directory not to count", rec.Code)
	}
}

func TestOperationsOnABucketThatIsNotThere(t *testing.T) {
	handler, _ := newStore(t)

	for name, tc := range map[string]struct{ method, path, body string }{
		"get":    {http.MethodGet, "/bucket/ghost", ""},
		"update": {http.MethodPut, "/bucket/ghost", "{}"},
		"delete": {http.MethodDelete, "/bucket/ghost", ""},
		"empty":  {http.MethodPost, "/bucket/ghost/empty", ""},
	} {
		rec := do(t, handler, tc.method, tc.path, serviceToken(t), strings.NewReader(tc.body))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// A buckets.json that cannot be read is a broken store, not a missing bucket.
// Answering 404 tells the caller to recreate things that are still there.
func TestACorruptBucketFileIsFiveHundredNotFourOhFour(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)

	corrupt := filepath.Join(root, ".supatype", "buckets.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct{ method, path, bearer, body string }{
		"list buckets":    {http.MethodGet, "/bucket", serviceToken(t), ""},
		"get a bucket":    {http.MethodGet, "/bucket/avatars", serviceToken(t), ""},
		"update":          {http.MethodPut, "/bucket/avatars", serviceToken(t), "{}"},
		"delete":          {http.MethodDelete, "/bucket/avatars", serviceToken(t), ""},
		"create":          {http.MethodPost, "/bucket", serviceToken(t), `{"name":"other"}`},
		"empty":           {http.MethodPost, "/bucket/avatars/empty", serviceToken(t), ""},
		"upload":          {http.MethodPost, "/object/avatars/a.png", userToken(t), "bytes"},
		"public download": {http.MethodGet, "/object/public/avatars/a.png", "", ""},
	} {
		rec := do(t, handler, tc.method, tc.path, tc.bearer, strings.NewReader(tc.body))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// The branch is unreachable with a real []Bucket, so the seam proves it reports
// rather than truncating the file that records every bucket.
func TestSaveBucketsReportsAMarshalFailure(t *testing.T) {
	original := marshalIndent
	t.Cleanup(func() { marshalIndent = original })
	marshalIndent = func(any) ([]byte, error) { return nil, errors.New("nope") }

	handler, _ := newStore(t)
	rec := do(t, handler, http.MethodPost, "/bucket", serviceToken(t), strings.NewReader(`{"name":"avatars"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// ─── Objects ──────────────────────────────────────────────────────────────────

func TestObjectRoundTrip(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)

	req := httptest.NewRequest(http.MethodPost, "/object/avatars/users/1/a.png", strings.NewReader("png bytes"))
	req.Header.Set("Authorization", "Bearer "+userToken(t))
	req.Header.Set("Content-Type", "image/png; charset=binary")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}

	// The public download needs no token, and carries the type the upload named
	// with its parameters stripped.
	rec = do(t, handler, http.MethodGet, "/object/public/avatars/users/1/a.png", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "png bytes" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("content-type = %q, want the parameters stripped", got)
	}

	// And the authenticated one needs a token.
	rec = do(t, handler, http.MethodGet, "/object/authenticated/avatars/users/1/a.png", userToken(t), nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "png bytes" {
		t.Errorf("authenticated download: %d %q", rec.Code, rec.Body.String())
	}
}

// A content type nobody sent is bytes, which is better than letting a browser
// sniff one.
func TestAnUploadWithNoContentType(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.bin", "bytes")

	rec := do(t, handler, http.MethodGet, "/object/public/avatars/a.bin", "", nil)
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("content-type = %q", got)
	}
}

// A content type that cannot be parsed is kept as sent rather than replaced.
func TestAnUnparseableContentTypeIsKept(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)

	req := httptest.NewRequest(http.MethodPost, "/object/avatars/a.bin", strings.NewReader("bytes"))
	req.Header.Set("Authorization", "Bearer "+userToken(t))
	req.Header.Set("Content-Type", "not/a/media/type;;;")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
}

// Overwriting is opt-in, so a client that did not ask cannot lose someone
// else's file to a name collision.
func TestOverwritingNeedsUpsert(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "first")

	rec := do(t, handler, http.MethodPost, "/object/avatars/a.png", userToken(t), strings.NewReader("second"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if body := do(t, handler, http.MethodGet, "/object/public/avatars/a.png", "", nil).Body.String(); body != "first" {
		t.Errorf("the object was overwritten anyway: %q", body)
	}

	req := httptest.NewRequest(http.MethodPost, "/object/avatars/a.png", strings.NewReader("second"))
	req.Header.Set("Authorization", "Bearer "+userToken(t))
	req.Header.Set("x-upsert", "true")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
	}
	if body := do(t, handler, http.MethodGet, "/object/public/avatars/a.png", "", nil).Body.String(); body != "second" {
		t.Errorf("the upsert did not replace it: %q", body)
	}
}

func TestUploadRefusals(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", false)

	if rec := do(t, handler, http.MethodPost, "/object/ghost/a.png", userToken(t), strings.NewReader("x")); rec.Code != http.StatusNotFound {
		t.Errorf("a bucket that is not there: status = %d, want 404", rec.Code)
	}
}

// A private bucket is not served without a token, whatever path is asked for.
func TestAPrivateBucketIsNotPublic(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "private", false)
	upload(t, handler, "private", "a.png", "secret")

	rec := do(t, handler, http.MethodGet, "/object/public/private/a.png", "", nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Error("the object was served anyway")
	}
}

func TestDownloadingSomethingThatIsNotThere(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)

	if rec := do(t, handler, http.MethodGet, "/object/public/avatars/ghost.png", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if rec := do(t, handler, http.MethodGet, "/object/public/ghost/a.png", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("a missing bucket: status = %d, want 404", rec.Code)
	}
}

// Range requests are what a video player sends, and ServeContent handles them
// only if the file is served through it.
func TestARangeRequestIsHonoured(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.bin", "0123456789")

	req := httptest.NewRequest(http.MethodGet, "/object/public/avatars/a.bin", nil)
	req.Header.Set("Range", "bytes=2-4")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if rec.Body.String() != "234" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestRemoveObjects(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "one")
	upload(t, handler, "avatars", "b.png", "two")

	rec := do(t, handler, http.MethodDelete, "/object/avatars", userToken(t),
		strings.NewReader(`{"prefixes":["a.png","ghost.png"]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}

	var deleted []listItem
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	// Only what was actually there is reported deleted.
	if len(deleted) != 1 || deleted[0].Name != "a.png" {
		t.Errorf("deleted = %+v", deleted)
	}

	if rec := do(t, handler, http.MethodGet, "/object/public/avatars/a.png", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("the object survived: %d", rec.Code)
	}
	if rec := do(t, handler, http.MethodGet, "/object/public/avatars/b.png", "", nil); rec.Code != http.StatusOK {
		t.Errorf("the wrong object was removed: %d", rec.Code)
	}
}

// Deleting nothing is an empty list, not null: a client iterating the result
// must not have to test for it.
func TestRemovingNothingReturnsAnEmptyList(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)

	rec := do(t, handler, http.MethodDelete, "/object/avatars", userToken(t), strings.NewReader(`{"prefixes":[]}`))
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestRemoveObjectsRefusals(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"not JSON":              {"{", http.StatusBadRequest},
		"a prefix that escapes": {`{"prefixes":["../../etc/passwd"]}`, http.StatusBadRequest},
	} {
		rec := do(t, handler, http.MethodDelete, "/object/avatars", userToken(t), strings.NewReader(tc.body))
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, tc.want)
		}
	}
}

// ─── Listing ──────────────────────────────────────────────────────────────────

func TestListObjects(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "one")
	upload(t, handler, "avatars", "users/1/b.png", "two")

	rec := do(t, handler, http.MethodPost, "/object/list/avatars", userToken(t), strings.NewReader(`{}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}

	var items []listItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v, want both objects and no sidecars", items)
	}
	for _, item := range items {
		if item.Metadata["mimetype"] == nil || item.Metadata["size"] == nil {
			t.Errorf("%s: metadata = %v, want size and mimetype nested", item.Name, item.Metadata)
		}
	}
}

// A prefix narrows the listing to one directory, which is how the storage
// browser walks a tree.
func TestListObjectsUnderAPrefix(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "one")
	upload(t, handler, "avatars", "users/1/b.png", "two")

	rec := do(t, handler, http.MethodPost, "/object/list/avatars", userToken(t), strings.NewReader(`{"prefix":"users"}`))
	var items []listItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "users/1/b.png" {
		t.Errorf("items = %+v", items)
	}
}

func TestListObjectsPagination(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	for i := 0; i < 5; i++ {
		upload(t, handler, "avatars", fmt.Sprintf("%d.png", i), "x")
	}

	count := func(body string) int {
		rec := do(t, handler, http.MethodPost, "/object/list/avatars", userToken(t), strings.NewReader(body))
		var items []listItem
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return len(items)
	}

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"everything":          {`{}`, 5},
		"a limit":             {`{"limit":2}`, 2},
		"an offset":           {`{"offset":3}`, 2},
		"past the end":        {`{"offset":99}`, 0},
		"both":                {`{"offset":1,"limit":2}`, 2},
		"a limit of zero":     {`{"limit":0}`, 5},
		"a negative offset":   {`{"offset":-1}`, 5},
		"an unparseable body": {`{`, 5},
	} {
		if got := count(tc.body); got != tc.want {
			t.Errorf("%s: %d items, want %d", name, got, tc.want)
		}
	}
}

// An object whose sidecar is missing still lists, with an id that is the same
// every time. It used to be random, so anything keying on id saw every file
// replaced on each refresh.
func TestAnObjectWithNoSidecarGetsAStableID(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)

	// Written directly, so there is no sidecar.
	if err := os.WriteFile(filepath.Join(root, "avatars", "orphan.png"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	ids := func() string {
		rec := do(t, handler, http.MethodPost, "/object/list/avatars", userToken(t), strings.NewReader(`{}`))
		var items []listItem
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("items = %+v", items)
		}
		return items[0].ID
	}

	first, second := ids(), ids()
	if first == "" {
		t.Fatal("no id at all")
	}
	if first != second {
		t.Errorf("listing twice gave %q then %q", first, second)
	}
	if syntheticID("avatars", "orphan.png") != first {
		t.Errorf("the id is not derived from the bucket and path: %q", first)
	}
	if syntheticID("other", "orphan.png") == first {
		t.Error("two buckets share an id for the same path")
	}
}

func TestListObjectsRefusals(t *testing.T) {
	handler, _ := newStore(t)

	for name, tc := range map[string]struct {
		path, body string
		want       int
	}{
		// A bucket id that could not name a directory is the caller's mistake,
		// which is a different answer from a bucket that simply is not there.
		"a bucket id that escapes": {"/object/list/..", `{}`, http.StatusBadRequest},
		"a prefix that escapes":    {"/object/list/avatars", `{"prefix":"../.."}`, http.StatusBadRequest},
	} {
		rec := do(t, handler, http.MethodPost, tc.path, userToken(t), strings.NewReader(tc.body))
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// A bucket directory that does not exist lists as empty rather than failing:
// the bucket record can exist before anything has been put in it.
func TestListingAnEmptyBucket(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)

	rec := do(t, handler, http.MethodPost, "/object/list/avatars", userToken(t), strings.NewReader(`{}`))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}

// ─── Signed URLs ──────────────────────────────────────────────────────────────

func TestSignedURLRoundTrip(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "private", false)
	upload(t, handler, "private", "a.png", "secret bytes")

	rec := do(t, handler, http.MethodPost, "/object/sign/private/a.png", userToken(t), strings.NewReader(`{"expiresIn":60}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("sign: %d %s", rec.Code, rec.Body.String())
	}
	var signed struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &signed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(signed.SignedURL, "/storage/v1/object/sign/private/a.png?token=") {
		t.Fatalf("signed URL = %q", signed.SignedURL)
	}

	// The token is the credential: no bearer, and the private bucket opens.
	at := signed.SignedURL[strings.Index(signed.SignedURL, "/storage/v1")+len("/storage/v1"):]
	rec = do(t, handler, http.MethodGet, at, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("serve: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "secret bytes" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// The signed path is what gets served, not the path in the URL, so a token for
// one object cannot be pointed at another.
func TestASignedTokenCannotBeRepointed(t *testing.T) {
	s := &store{root: t.TempDir(), jwtSecret: []byte(secret), mu: &sync.RWMutex{}}
	handler, root := newStore(t)
	makeBucket(t, handler, "private", false)
	upload(t, handler, "private", "mine.png", "mine")
	upload(t, handler, "private", "yours.png", "yours")
	_ = root

	forMine := s.signToken(signedPayload{B: "private", P: "mine.png", Exp: time.Now().Add(time.Minute).Unix()})

	rec := do(t, handler, http.MethodGet, "/object/sign/private/yours.png?token="+forMine, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != "mine" {
		t.Errorf("body = %q, want the signed object rather than the one in the URL", rec.Body.String())
	}
}

func TestSignedURLRefusals(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "private", false)
	upload(t, handler, "private", "a.png", "secret")

	elsewhere := &store{jwtSecret: []byte("another-secret"), mu: &sync.RWMutex{}}
	forged := elsewhere.signToken(signedPayload{B: "private", P: "a.png", Exp: time.Now().Add(time.Minute).Unix()})

	ours := &store{jwtSecret: []byte(secret), mu: &sync.RWMutex{}}
	expired := ours.signToken(signedPayload{B: "private", P: "a.png", Exp: time.Now().Add(-time.Minute).Unix()})

	for name, tc := range map[string]struct {
		query string
		want  int
	}{
		"no token":          {"", http.StatusBadRequest},
		"nonsense":          {"?token=nonsense", http.StatusForbidden},
		"signed by another": {"?token=" + forged, http.StatusForbidden},
		"expired":           {"?token=" + expired, http.StatusForbidden},
		"no separator":      {"?token=abc", http.StatusForbidden},
	} {
		rec := do(t, handler, http.MethodGet, "/object/sign/private/a.png"+tc.query, "", nil)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, tc.want)
		}
		if strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s: the object was served", name)
		}
	}
}

func TestVerifyTokenRefusals(t *testing.T) {
	s := &store{jwtSecret: []byte(secret), mu: &sync.RWMutex{}}
	valid := s.signToken(signedPayload{B: "b", P: "p", Exp: time.Now().Add(time.Minute).Unix()})

	for name, tc := range map[string]string{
		"no separator":           "abcdef",
		"a signature not base64": strings.Split(valid, ".")[0] + ".!!!",
		// Correctly signed, so the HMAC check passes and the decode is reached.
		"a payload not base64": "!!!." + signatureFor(t, "!!!"),
		"a payload not JSON": base64.RawURLEncoding.EncodeToString([]byte("{")) + "." +
			signatureFor(t, base64.RawURLEncoding.EncodeToString([]byte("{"))),
	} {
		if _, ok := s.verifyToken(tc); ok {
			t.Errorf("%s was accepted", name)
		}
	}

	// A token with no expiry never expires, which is what expiresIn <= 0 means
	// after it has been defaulted.
	forever := s.signToken(signedPayload{B: "b", P: "p"})
	if _, ok := s.verifyToken(forever); !ok {
		t.Error("a token with no expiry was refused")
	}
}

// signatureFor produces the signature this store would put on an encoded body.
func signatureFor(t *testing.T, encoded string) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// An expiry the caller did not give becomes an hour, rather than a token that
// is already expired.
func TestSigningDefaultsTheExpiry(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "private", false)
	upload(t, handler, "private", "a.png", "secret")

	for _, body := range []string{`{}`, `{"expiresIn":0}`, `{"expiresIn":-5}`} {
		rec := do(t, handler, http.MethodPost, "/object/sign/private/a.png", userToken(t), strings.NewReader(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", body, rec.Code)
		}
		var signed struct {
			SignedURL string `json:"signedURL"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &signed); err != nil {
			t.Fatal(err)
		}
		at := signed.SignedURL[strings.Index(signed.SignedURL, "/storage/v1")+len("/storage/v1"):]
		if rec := do(t, handler, http.MethodGet, at, "", nil); rec.Code != http.StatusOK {
			t.Errorf("%s: the defaulted token did not work: %d", body, rec.Code)
		}
	}
}

func TestSigningRefusesABodyItCannotRead(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "private", false)

	rec := do(t, handler, http.MethodPost, "/object/sign/private/a.png", userToken(t), strings.NewReader("{"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// A signed URL over TLS has to say https, or the browser refuses it as mixed
// content.
func TestASignedURLUsesTheRequestScheme(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "private", false)

	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/object/sign/private/a.png",
		strings.NewReader(`{"expiresIn":60}`))
	req.Header.Set("Authorization", "Bearer "+userToken(t))
	req.TLS = &tlsState
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var signed struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &signed); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(signed.SignedURL, "https://") {
		t.Errorf("signed URL = %q, want https", signed.SignedURL)
	}
}

// ─── Metadata ─────────────────────────────────────────────────────────────────

// A sidecar that will not parse is not fatal: the object still serves, just
// without the type it recorded.
func TestAnUnreadableSidecarDoesNotBlockTheDownload(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "png bytes")

	sidecar := filepath.Join(root, "avatars", ".meta", "a.png.json")
	if err := os.WriteFile(sidecar, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := do(t, handler, http.MethodGet, "/object/public/avatars/a.png", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "png bytes" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}

// Downloading touches the access time, which is what a lifecycle policy would
// read.
func TestDownloadingUpdatesTheAccessTime(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "png bytes")

	s := &store{root: root, jwtSecret: []byte(secret), mu: &sync.RWMutex{}}
	before, err := s.loadObjectMeta("avatars", "a.png")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(1100 * time.Millisecond) // RFC3339 has second resolution.
	do(t, handler, http.MethodGet, "/object/public/avatars/a.png", "", nil)

	after, err := s.loadObjectMeta("avatars", "a.png")
	if err != nil {
		t.Fatal(err)
	}
	if after.LastAccessedAt == before.LastAccessedAt {
		t.Errorf("last accessed did not move: %q", after.LastAccessedAt)
	}
	if after.CreatedAt != before.CreatedAt {
		t.Errorf("created was rewritten: %q then %q", before.CreatedAt, after.CreatedAt)
	}
}

func TestLoadObjectMetaRefusals(t *testing.T) {
	s := &store{root: t.TempDir(), mu: &sync.RWMutex{}}

	if _, err := s.loadObjectMeta("..", "a.png"); err == nil {
		t.Error("a bucket that escapes was accepted")
	}
	if _, err := s.loadObjectMeta("avatars", "a.png"); err == nil {
		t.Error("a sidecar that is not there was read")
	}

	absent := &store{root: filepath.Join(t.TempDir(), "gone"), mu: &sync.RWMutex{}}
	if _, err := absent.loadObjectMeta("avatars", "a.png"); err == nil {
		t.Error("a storage root that is not there was read")
	}
	// Writing creates what it needs, root included, so a first upload into a
	// freshly configured directory works rather than failing once.
	if err := absent.saveObjectMeta(&ObjectMeta{}, "avatars", "a.png"); err != nil {
		t.Errorf("a sidecar could not be written under a new root: %v", err)
	}
	if err := s.saveObjectMeta(&ObjectMeta{}, "..", "a.png"); err == nil {
		t.Error("a bucket that escapes was written to")
	}
}

// ─── IDs ──────────────────────────────────────────────────────────────────────

func TestIDsAreUUIDShaped(t *testing.T) {
	shape := func(id string) bool {
		parts := strings.Split(id, "-")
		if len(parts) != 5 {
			return false
		}
		for i, want := range []int{8, 4, 4, 4, 12} {
			if len(parts[i]) != want {
				return false
			}
		}
		return true
	}

	random := newID()
	if !shape(random) {
		t.Errorf("newID = %q", random)
	}
	if random == newID() {
		t.Error("newID returned the same value twice")
	}
	if !shape(syntheticID("b", "p")) {
		t.Errorf("syntheticID = %q", syntheticID("b", "p"))
	}
	// Version and variant nibbles, so a client parsing it as a UUID accepts it.
	if random[14] != '4' {
		t.Errorf("newID version nibble = %q", random[14])
	}
	if syntheticID("b", "p")[14] != '8' {
		t.Errorf("syntheticID version nibble = %q", syntheticID("b", "p")[14])
	}
}

// tlsState is a minimal non-nil ConnectionState, which is all the handler reads
// to decide the scheme.
var tlsState = tls.ConnectionState{}

// ─── Filesystem failures ──────────────────────────────────────────────────────

// failing swaps one seam for the duration of a test.
//
// Every one of these is a real condition — a full disk, a revoked permission, a
// directory removed under a running server — that cannot be arranged the same
// way on Windows and Linux.
func failing[T any](t *testing.T, seam *T, replacement T) {
	t.Helper()
	original := *seam
	t.Cleanup(func() { *seam = original })
	*seam = replacement
}

var errDisk = errors.New("the disk said no")

// Every place that writes buckets.json reports a failure rather than answering
// as though the change landed.
func TestAFailedBucketWriteIsReported(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", false)

	failing(t, &writeFile, func(string, []byte, os.FileMode) error { return errDisk })

	for name, tc := range map[string]struct{ method, path, body string }{
		"create": {http.MethodPost, "/bucket", `{"name":"other"}`},
		"update": {http.MethodPut, "/bucket/avatars", `{"public":true}`},
		"delete": {http.MethodDelete, "/bucket/avatars", ""},
	} {
		rec := do(t, handler, tc.method, tc.path, serviceToken(t), strings.NewReader(tc.body))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// A bucket whose directory cannot be created is not a bucket, and the caller
// has to be told rather than discovering it on the first upload.
func TestAFailedBucketDirectoryIsReported(t *testing.T) {
	handler, _ := newStore(t)
	failing(t, &mkdirAll, func(string, os.FileMode) error { return errDisk })

	rec := do(t, handler, http.MethodPost, "/bucket", serviceToken(t), strings.NewReader(`{"name":"avatars"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// A bucket directory that cannot be listed is not the same as an empty one:
// deleting on that basis would leave objects behind with no record of them.
func TestABucketDirectoryThatCannotBeRead(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", false)
	failing(t, &readDir, func(string) ([]os.DirEntry, error) { return nil, errDisk })

	for name, tc := range map[string]struct{ method, path string }{
		"delete": {http.MethodDelete, "/bucket/avatars"},
		"empty":  {http.MethodPost, "/bucket/avatars/empty"},
	} {
		rec := do(t, handler, tc.method, tc.path, serviceToken(t), nil)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// A delete that removes the record but not the directory would leave a
// directory no bucket owns, so the failure is reported.
func TestAFailedRemoveIsReported(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", false)
	upload(t, handler, "avatars", "a.png", "x")

	failing(t, &removeAll, func(string) error { return errDisk })

	if rec := do(t, handler, http.MethodPost, "/bucket/avatars/empty", serviceToken(t), nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("empty: status = %d, want 500", rec.Code)
	}

	// And on delete, after the bucket has legitimately been emptied.
	handler2, _ := newStore(t)
	makeBucket(t, handler2, "avatars", false)
	failing(t, &removeAll, func(string) error { return errDisk })
	if rec := do(t, handler2, http.MethodDelete, "/bucket/avatars", serviceToken(t), nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("delete: status = %d, want 500", rec.Code)
	}
}

// A storage root that cannot be opened stops every path that touches a file.
func TestAStorageRootThatCannotBeOpened(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "x")

	failing(t, &openRoot, func(string) (*os.Root, error) { return nil, errDisk })

	for name, tc := range map[string]struct{ method, path, bearer, body string }{
		"upload":   {http.MethodPost, "/object/avatars/b.png", userToken(t), "x"},
		"download": {http.MethodGet, "/object/public/avatars/a.png", "", ""},
		"remove":   {http.MethodDelete, "/object/avatars", userToken(t), `{"prefixes":["a.png"]}`},
	} {
		rec := do(t, handler, tc.method, tc.path, tc.bearer, strings.NewReader(tc.body))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// failingReader is a body that errors on the first read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errDisk }
func (failingReader) Close() error             { return nil }

// An upload whose body cannot be read is not stored, and says so rather than
// recording a truncated object.
func TestAnUploadWhoseBodyFails(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)

	req := httptest.NewRequest(http.MethodPost, "/object/avatars/a.png", failingReader{})
	req.Header.Set("Authorization", "Bearer "+userToken(t))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// A file that cannot be opened for writing is a failed upload.
func TestAnUploadThatCannotOpenItsFile(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)

	// A directory where the object should go: opening it for writing fails on
	// every platform, even though the error differs.
	if err := os.MkdirAll(filepath.Join(root, "avatars", "a.png"), 0o700); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/object/avatars/a.png", strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer "+userToken(t))
	req.Header.Set("x-upsert", "true")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// A sidecar that cannot be read is reported rather than coming back as an empty
// ObjectMeta the caller would act on.
func TestASidecarThatCannotBeRead(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "x")

	s := &store{root: root, jwtSecret: []byte(secret), mu: &sync.RWMutex{}}
	failing(t, &readAll, func(io.Reader) ([]byte, error) { return nil, errDisk })

	if _, err := s.loadObjectMeta("avatars", "a.png"); err == nil {
		t.Error("want an error")
	}
}

// A metadata directory that cannot be made at startup is a warning, not a
// refusal to serve: the buckets are created on demand anyway.
func TestAStoreWhoseMetadataDirectoryCannotBeMade(t *testing.T) {
	failing(t, &mkdirAll, func(string, os.FileMode) error { return errDisk })

	if Handler(t.TempDir(), secret) == nil {
		t.Error("no handler")
	}
}

// A path the router cannot produce still has to be refused where the path is
// built, rather than relied on to have been refused earlier.
func TestHandlersRefuseABucketIDTheyCannotUse(t *testing.T) {
	s := &store{root: t.TempDir(), jwtSecret: []byte(secret), mu: &sync.RWMutex{}}

	if _, err := s.bucketDir("  "); err == nil {
		t.Error("a blank bucket id was accepted")
	}
	if _, err := s.objectFileRel("avatars", ""); err == nil {
		t.Error("an empty object path was accepted")
	}
}

// A token whose signature is right but whose payload is not readable is not a
// token. The signature check runs first, so this needs a correctly signed body.
func TestATokenWithAValidSignatureAndAnUnreadablePayload(t *testing.T) {
	s := &store{jwtSecret: []byte(secret), mu: &sync.RWMutex{}}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))

	sign := func(body string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(body))
		return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}

	for name, payload := range map[string]string{
		"not base64": "!!!not-base64!!!",
		"not JSON":   base64.RawURLEncoding.EncodeToString([]byte("{")),
	} {
		if s.parseJWT(sign(header+"."+payload)) != nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A body that is not JSON is the caller's mistake, on every endpoint that reads
// one.
func TestUpdateBucketRefusesABodyItCannotRead(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", false)

	rec := do(t, handler, http.MethodPut, "/bucket/avatars", serviceToken(t), strings.NewReader("{"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// A buckets.json holding an id that could not name a directory is a store
// someone edited by hand. The handlers still have to refuse it rather than
// joining it onto the storage root.
func TestABucketRecordWithAnUnusableID(t *testing.T) {
	handler, root := newStore(t)

	corrupt := filepath.Join(root, ".supatype", "buckets.json")
	if err := os.WriteFile(corrupt, []byte(`[{"id":"..","name":"escaped"}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct{ method, path string }{
		"get":    {http.MethodGet, "/bucket/.."},
		"delete": {http.MethodDelete, "/bucket/.."},
		"empty":  {http.MethodPost, "/bucket/../empty"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+serviceToken(t))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// get finds the record and returns it; the two that touch the filesystem
		// have to refuse.
		want := http.StatusBadRequest
		if name == "get" {
			want = http.StatusOK
		}
		if rec.Code != want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, want, rec.Body.String())
		}
	}
}

// An object path that tries to leave the bucket is refused where the path is
// built, not left to the filesystem.
func TestAnObjectPathThatEscapes(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)

	for name, tc := range map[string]struct {
		method, path, bearer string
		body                 string
	}{
		"upload":   {http.MethodPost, "/object/avatars/../escaped.png", userToken(t), "x"},
		"download": {http.MethodGet, "/object/public/avatars/../escaped.png", "", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// The object lands but its sidecar does not, so the upload is reported failed
// rather than leaving a file nothing describes.
func TestAnUploadWhoseSidecarCannotBeWritten(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)

	// The sidecar's own path is a directory, so opening it for writing fails
	// after the object itself has been stored.
	sidecar := filepath.Join(root, "avatars", ".meta", "a.png.json")
	if err := os.MkdirAll(sidecar, 0o700); err != nil {
		t.Fatal(err)
	}

	rec := do(t, handler, http.MethodPost, "/object/avatars/a.png", userToken(t), strings.NewReader("x"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// Reaching the store to write a sidecar can fail for the same reason reading
// one can.
func TestASidecarWriteUnderAnUnopenableRoot(t *testing.T) {
	s := &store{root: t.TempDir(), mu: &sync.RWMutex{}}
	failing(t, &openRoot, func(string) (*os.Root, error) { return nil, errDisk })

	if err := s.saveObjectMeta(&ObjectMeta{}, "avatars", "a.png"); err == nil {
		t.Error("want an error")
	}
}

// The branch is unreachable with a real ObjectMeta, so the seam proves it
// reports rather than writing an empty sidecar over a good one.
func TestASidecarThatCannotBeEncoded(t *testing.T) {
	s := &store{root: t.TempDir(), mu: &sync.RWMutex{}}
	failing(t, &marshalIndent, func(any) ([]byte, error) { return nil, errDisk })

	if err := s.saveObjectMeta(&ObjectMeta{}, "avatars", "a.png"); err == nil {
		t.Error("want an error")
	}
}

// A buckets.json that cannot be read at all — as opposed to one that is not
// there — is reported.
func TestABucketsFileThatCannotBeRead(t *testing.T) {
	s := &store{root: t.TempDir(), mu: &sync.RWMutex{}}
	failing(t, &readFile, func(string) ([]byte, error) { return nil, errDisk })

	if _, err := s.loadBuckets(); err == nil {
		t.Error("want an error")
	}
}

// The sidecar's directory has to be made before it is written, and a failure
// there is a failed upload rather than an object with no record.
func TestSaveObjectMetaCannotMakeItsDirectory(t *testing.T) {
	s := &store{root: t.TempDir(), mu: &sync.RWMutex{}}
	failing(t, &mkdirAll, func(string, os.FileMode) error { return errDisk })

	if err := s.saveObjectMeta(&ObjectMeta{}, "avatars", "a.png"); err == nil {
		t.Error("want an error")
	}
}

// And the object's own directory, before the object.
func TestAnUploadThatCannotMakeItsDirectory(t *testing.T) {
	handler, _ := newStore(t)
	makeBucket(t, handler, "avatars", true)
	failing(t, &mkdirAll, func(string, os.FileMode) error { return errDisk })

	rec := do(t, handler, http.MethodPost, "/object/avatars/a.png", userToken(t), strings.NewReader("x"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// A file that exists and still cannot be opened is not a missing object, and
// answering 404 would send someone looking for a file that is right there.
func TestServingAFileThatCannotBeOpened(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "x")

	s := &store{root: root, jwtSecret: []byte(secret), mu: &sync.RWMutex{}}
	rec := httptest.NewRecorder()
	// A name no filesystem will open, and not one that is merely absent.
	s.serveFile(rec, httptest.NewRequest(http.MethodGet, "/", nil), "avatars", "a\x00.png")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// The branch is unreachable on a handle just opened, so the seam proves it
// reports rather than serving a zero-length body under the object's name.
func TestServingAFileThatCannotBeDescribed(t *testing.T) {
	handler, root := newStore(t)
	makeBucket(t, handler, "avatars", true)
	upload(t, handler, "avatars", "a.png", "x")

	failing(t, &statFile, func(*os.File) (os.FileInfo, error) { return nil, errDisk })

	s := &store{root: root, jwtSecret: []byte(secret), mu: &sync.RWMutex{}}
	rec := httptest.NewRecorder()
	s.serveFile(rec, httptest.NewRequest(http.MethodGet, "/", nil), "avatars", "a.png")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
