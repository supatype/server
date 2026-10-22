package studioauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// GraphQL carries reads and writes alike on POST, so its method says nothing
// about which a request is. A Studio viewer's query was refused as a write
// (fresh-user test C18). A request to /graphql/v1 is judged by the operations
// it runs instead: queries, introspection among them, read; mutations write.
// Anything this cannot parse, and any subscription, is judged a write, so a
// document that confuses the classifier is never let through as a read.

// permissionMethod is the method one request is judged as. A GraphQL request
// that only reads is judged as a GET, any other GraphQL request as a POST,
// whatever method carried it.
func permissionMethod(req *http.Request, path string) string {
	if !under(path, "/graphql/v1") || req.Method == http.MethodOptions {
		return req.Method
	}
	if graphQLRequestReads(req) {
		return http.MethodGet
	}
	return http.MethodPost
}

// graphQLRequestReads reports whether every operation one GraphQL request
// would run is a query. A GET or HEAD names its document in the `query`
// parameter, and one with none runs nothing; a POST carries a JSON object, or
// a batch of them, with `query` and `operationName`.
func graphQLRequestReads(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead:
		return graphQLParamsRead(req)
	case http.MethodPost:
		body, err := readBodyRestoring(req)
		return err == nil && graphQLBodyReads(body)
	default:
		return false
	}
}

// graphQLParamsRead judges a GET's query parameters. A repeated parameter is
// ambiguous, so it is not a read.
func graphQLParamsRead(req *http.Request) bool {
	params := req.URL.Query()
	queries, names := params["query"], params["operationName"]
	if len(queries) == 0 {
		return true
	}
	if len(queries) != 1 || len(names) > 1 {
		return false
	}
	name := ""
	if len(names) == 1 {
		name = names[0]
	}
	return graphQLDocumentReads(queries[0], name)
}

// graphQLBodyReads judges a POST body: one request object, or a non-empty
// batch whose every object reads.
func graphQLBodyReads(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return graphQLObjectReads(trimmed)
	}
	var batch []json.RawMessage
	if json.Unmarshal(trimmed, &batch) != nil || len(batch) == 0 {
		return false
	}
	for _, item := range batch {
		if !graphQLObjectReads(item) {
			return false
		}
	}
	return true
}

// graphQLObjectReads judges one request object. Keys are matched exactly, as
// Postgres matches them to the function's arguments, and an object that
// repeats a key is not a read, since parsers disagree on which copy counts.
func graphQLObjectReads(raw []byte) bool {
	fields, ok := strictObject(raw)
	if !ok {
		return false
	}
	var query string
	if json.Unmarshal(fields["query"], &query) != nil {
		return false
	}
	var name *string
	if op, present := fields["operationName"]; present && json.Unmarshal(op, &name) != nil {
		return false
	}
	if name == nil {
		return graphQLDocumentReads(query, "")
	}
	return graphQLDocumentReads(query, *name)
}

// strictObject decodes a JSON object into its fields, reporting false for
// anything else, for a repeated key, or for trailing data.
func strictObject(raw []byte) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		key, isKey := tok.(string)
		var value json.RawMessage
		if err != nil || !isKey || dec.Decode(&value) != nil {
			return nil, false
		}
		if _, seen := fields[key]; seen {
			return nil, false
		}
		fields[key] = value
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, false
	}
	_, err := dec.Token()
	return fields, errors.Is(err, io.EOF)
}

// graphQLDocumentReads reports whether the operation a document runs is a
// query: the one operationName selects, or, with no name, every operation.
func graphQLDocumentReads(document, operationName string) bool {
	ops, ok := graphQLOperations(document)
	if !ok || len(ops) == 0 {
		return false
	}
	if operationName != "" {
		for _, op := range ops {
			if op.name == operationName {
				return op.kind == "query"
			}
		}
		return false
	}
	for _, op := range ops {
		if op.kind != "query" {
			return false
		}
	}
	return true
}

// graphQLOperation is one operation definition: its type and its name, empty
// when anonymous.
type graphQLOperation struct{ kind, name string }

// graphQLOperations lists a document's operations, reporting false when the
// document is not one this can read. Fragments are skipped.
func graphQLOperations(document string) ([]graphQLOperation, bool) {
	tokens, ok := graphQLTokens(document)
	if !ok {
		return nil, false
	}
	var ops []graphQLOperation
	for i := 0; i < len(tokens); {
		op, isOp, ok := graphQLDefinitionHead(tokens, i)
		if !ok {
			return nil, false
		}
		if isOp {
			ops = append(ops, op)
		}
		if i, ok = skipGraphQLDefinition(tokens, i); !ok {
			return nil, false
		}
	}
	return ops, true
}

// graphQLDefinitionHead reads the start of the definition at tokens[i].
func graphQLDefinitionHead(tokens []string, i int) (graphQLOperation, bool, bool) {
	switch tokens[i] {
	case "{":
		return graphQLOperation{kind: "query"}, true, true
	case "fragment":
		return graphQLOperation{}, false, true
	case "query", "mutation", "subscription":
		op := graphQLOperation{kind: tokens[i]}
		if i+1 < len(tokens) && isGraphQLName(tokens[i+1]) {
			op.name = tokens[i+1]
		}
		return op, true, true
	default:
		return graphQLOperation{}, false, false
	}
}

// skipGraphQLDefinition returns the index after the definition starting at
// tokens[i]: the end of the first selection set opened at the top level.
// Variable defaults and arguments sit inside brackets, so their braces are
// never at the top level. Unbalanced or mismatched brackets report false.
func skipGraphQLDefinition(tokens []string, i int) (int, bool) {
	var open []string
	for ; i < len(tokens); i++ {
		switch t := tokens[i]; t {
		case "{", "(", "[":
			open = append(open, t)
		case "}", ")", "]":
			if len(open) == 0 || open[len(open)-1] != graphQLOpener[t] {
				return 0, false
			}
			open = open[:len(open)-1]
			if len(open) == 0 && t == "}" {
				return i + 1, true
			}
		}
	}
	return 0, false
}

var graphQLOpener = map[string]string{"}": "{", ")": "(", "]": "["}

func isGraphQLName(token string) bool {
	return token != "" && isNameStart(token[0])
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNameChar(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9')
}

// graphQLTokens splits a document into names and punctuators, as the GraphQL
// lexer does. Strings and numbers become placeholders, and whitespace, commas
// and comments are dropped. Any character the lexer would reject reports false.
func graphQLTokens(document string) ([]string, bool) {
	var tokens []string
	s := strings.TrimPrefix(document, "\uFEFF")
	for i := 0; i < len(s); {
		next, token, ok := graphQLToken(s, i)
		if !ok {
			return nil, false
		}
		if token != "" {
			tokens = append(tokens, token)
		}
		i = next
	}
	return tokens, true
}

// graphQLToken reads the token at s[i], returning where the next starts and
// the token, empty for ignored text.
func graphQLToken(s string, i int) (int, string, bool) {
	c := s[i]
	switch {
	case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
		return i + 1, "", true
	case c == '#':
		return skipComment(s, i), "", true
	case c == '"':
		end, ok := skipString(s, i)
		return end, `"`, ok
	case strings.HasPrefix(s[i:], "..."):
		return i + 3, "...", true
	case strings.IndexByte("!$&():=@[]{|}", c) >= 0:
		return i + 1, s[i : i+1], true
	case isNameStart(c):
		end := scanWhile(s, i+1, isNameChar)
		return end, s[i:end], true
	case c == '-' || (c >= '0' && c <= '9'):
		return scanWhile(s, i+1, isNumberChar), "0", true
	}
	return 0, "", false
}

// scanWhile returns the index of the first byte from i that keep rejects.
func scanWhile(s string, i int, keep func(byte) bool) int {
	for i < len(s) && keep(s[i]) {
		i++
	}
	return i
}

func isNumberChar(c byte) bool {
	return isNameChar(c) || c == '.' || c == '+' || c == '-'
}

func skipComment(s string, i int) int {
	for i < len(s) && s[i] != '\n' && s[i] != '\r' {
		i++
	}
	return i
}

// skipString returns the index after the string or block string at s[i].
func skipString(s string, i int) (int, bool) {
	if strings.HasPrefix(s[i:], `"""`) {
		for j := i + 3; j+3 <= len(s); j++ {
			if s[j] == '\\' && strings.HasPrefix(s[j+1:], `"""`) {
				j += 3
				continue
			}
			if strings.HasPrefix(s[j:], `"""`) {
				return j + 3, true
			}
		}
		return 0, false
	}
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '"':
			return j + 1, true
		case '\n', '\r':
			return 0, false
		}
	}
	return 0, false
}
