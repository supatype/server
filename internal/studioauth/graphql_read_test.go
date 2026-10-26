package studioauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
)

// graphQLBody is a POST body asking for one document.
func graphQLBody(query, operationName string) string {
	fields := map[string]string{"query": query}
	if operationName != "" {
		fields["operationName"] = operationName
	}
	b, _ := json.Marshal(fields)
	return string(b)
}

// sendGraphQL sends one GraphQL request through the proxy as role.
func sendGraphQL(t *testing.T, role, method, target, body string) (*proxyUpstream, *httptest.ResponseRecorder) {
	t.Helper()
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	next := &proxyUpstream{}
	rec := httptest.NewRecorder()
	ProxyHandler(next, membershipConfig(role)).ServeHTTP(rec, req)
	return next, rec
}

// Fresh-user test C18: a viewer's GraphQL Run, a query sent as a POST, was
// refused as a write. Reads in every shape GraphQL sends them now pass.
func TestAViewerRunsGraphQLQueries(t *testing.T) {
	for name, body := range map[string]string{
		"shorthand":     graphQLBody(`{ postsCollection { edges { node { id } } } }`, ""),
		"named":         graphQLBody(`query Posts($n: Int = 1) { postsCollection(first: $n) { edges { node { id } } } }`, ""),
		"introspection": graphQLBody(`query IntrospectionQuery { __schema { types { name } } }`, ""),
		"with fragment": graphQLBody(`query A { ...F } fragment F on Query { __typename }`, ""),
		"comments":      graphQLBody("# mutation { deleteFromPostsCollection { affectedCount } }\nquery A { __typename }", ""),
		"selected":      graphQLBody(`query A { __typename } mutation B { deleteFromPostsCollection { affectedCount } }`, "A"),
		"string arg":    graphQLBody(`query A { posts(filter: "} mutation { x }") { id } }`, ""),
		"batch":         `[` + graphQLBody(`{ a }`, "") + `,` + graphQLBody(`query B { b }`, "") + `]`,
	} {
		if next, rec := sendGraphQL(t, "viewer", http.MethodPost, "/graphql/v1", body); !next.reached {
			t.Errorf("%s: viewer refused with %d %s", name, rec.Code, rec.Body.String())
		}
	}
	get := "/graphql/v1?query=" + url.QueryEscape(`{ __typename }`)
	if next, rec := sendGraphQL(t, "viewer", http.MethodGet, get, ""); !next.reached {
		t.Errorf("GET query: viewer refused with %d", rec.Code)
	}
}

// Anything that writes, or that the classifier cannot read, is a write.
func TestAViewerCannotRunGraphQLWrites(t *testing.T) {
	for name, body := range map[string]string{
		"mutation":            graphQLBody(`mutation { deleteFromPostsCollection { affectedCount } }`, ""),
		"subscription":        graphQLBody(`subscription { posts { id } }`, ""),
		"selected mutation":   graphQLBody(`query A { __typename } mutation B { deleteFromPostsCollection { affectedCount } }`, "B"),
		"mixed, no name":      graphQLBody(`query A { __typename } mutation B { x }`, ""),
		"unknown name":        graphQLBody(`query A { __typename }`, "Z"),
		"mutation in a batch": `[` + graphQLBody(`{ a }`, "") + `,` + graphQLBody(`mutation { x }`, "") + `]`,
		"empty batch":         `[]`,
		"duplicate key":       `{"query":"{ a }","query":"mutation { x }"}`,
		"case variant key":    `{"Query":"{ a }","query":"mutation { x }"}`,
		"unbalanced":          graphQLBody(`{ a `, ""),
		"unterminated string": graphQLBody(`{ a(s: "x) }`, ""),
		"keyword case":        graphQLBody(`Mutation { x }`, ""),
		"not json":            `query={a}`,
		"no query":            `{}`,
	} {
		if next, rec := sendGraphQL(t, "viewer", http.MethodPost, "/graphql/v1", body); next.reached || rec.Code != http.StatusForbidden {
			t.Errorf("%s: viewer got %d, reached %v", name, rec.Code, next.reached)
		}
	}
	get := "/graphql/v1?query=" + url.QueryEscape(`mutation { x }`)
	if next, _ := sendGraphQL(t, "viewer", http.MethodGet, get, ""); next.reached {
		t.Error("GET mutation: viewer reached upstream")
	}
}

// An editor still runs mutations, recorded as writes; a query is not recorded.
func TestAnEditorRunsGraphQLMutations(t *testing.T) {
	for _, c := range []struct {
		query   string
		records int
	}{{`mutation { x }`, 1}, {`{ x }`, 0}} {
		auditor := &fakeAuditor{}
		cfg := membershipConfig("editor")
		cfg.Audit = auditor
		token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
		req := httptest.NewRequest(http.MethodPost, "/graphql/v1", strings.NewReader(graphQLBody(c.query, "")))
		req.Header.Set("Authorization", "Bearer "+token)
		next := &proxyUpstream{}
		ProxyHandler(next, cfg).ServeHTTP(httptest.NewRecorder(), req)
		if !next.reached || len(auditor.records) != c.records {
			t.Errorf("editor %s: reached %v, audited %d", c.query, next.reached, len(auditor.records))
		}
	}
}

// Strings are skipped as the GraphQL lexer skips them, block strings and
// escaped quotes included, so a brace or keyword inside one changes nothing.
func TestAViewerRunsGraphQLQueriesWithStrings(t *testing.T) {
	for name, body := range map[string]string{
		"block string":         graphQLBody(`query A { posts(filter: """} mutation { x }""") { id } }`, ""),
		"escaped block quotes": graphQLBody(`query A { posts(filter: """a \""" } mutation { x }""") { id } }`, ""),
		"escaped quote":        graphQLBody(`{ posts(filter: "a \" } mutation { x }") { id } }`, ""),
	} {
		if next, rec := sendGraphQL(t, "viewer", http.MethodPost, "/graphql/v1", body); !next.reached {
			t.Errorf("%s: viewer refused with %d %s", name, rec.Code, rec.Body.String())
		}
	}
	for name, target := range map[string]string{
		"GET, selected query": "/graphql/v1?query=" + url.QueryEscape(`query A { a } mutation B { x }`) + "&operationName=A",
		"GET, no document":    "/graphql/v1",
	} {
		if next, rec := sendGraphQL(t, "viewer", http.MethodGet, target, ""); !next.reached {
			t.Errorf("%s: viewer refused with %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

// A document or request the classifier cannot read the way GraphQL would is
// not a read, so a viewer is refused it.
func TestAViewerCannotRunGraphQLItCannotRead(t *testing.T) {
	for name, body := range map[string]string{
		"unterminated block string": graphQLBody(`{ a(s: """x) }`, ""),
		"newline in a string":       graphQLBody("{ a(s: \"x\ny\") }", ""),
		"mismatched brackets":       graphQLBody(`{ a(s: 1 } }`, ""),
		"character the lexer drops": graphQLBody(`{ a ? }`, ""),
		"operationName not a name":  `{"query":"{ a }","operationName":1}`,
		"value missing":             `{"query":}`,
		"object closed as an array": `{"query":"{ a }"]`,
	} {
		if next, rec := sendGraphQL(t, "viewer", http.MethodPost, "/graphql/v1", body); next.reached || rec.Code != http.StatusForbidden {
			t.Errorf("%s: viewer got %d, reached %v", name, rec.Code, next.reached)
		}
	}
	q := url.QueryEscape(`{ a }`)
	for name, target := range map[string]string{
		"GET, repeated query":         "/graphql/v1?query=" + q + "&query=" + q,
		"GET, repeated operationName": "/graphql/v1?query=" + q + "&operationName=A&operationName=B",
	} {
		if next, rec := sendGraphQL(t, "viewer", http.MethodGet, target, ""); next.reached || rec.Code != http.StatusForbidden {
			t.Errorf("%s: viewer got %d, reached %v", name, rec.Code, next.reached)
		}
	}
	if next, rec := sendGraphQL(t, "viewer", http.MethodPut, "/graphql/v1", graphQLBody(`{ a }`, "")); next.reached || rec.Code != http.StatusForbidden {
		t.Errorf("PUT: viewer got %d, reached %v", rec.Code, next.reached)
	}
}
