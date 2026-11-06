// Package publicenv serves a deployment's public frontend values: the address
// its API answers on and its anon key.
//
// The anon key is not a secret. It is the key every browser that loads the app
// already holds, and row-level security is what protects the data behind it.
// Serving it lets one frontend build run against any deployment that serves it,
// and lets `supatype build` fetch it for a self-hosted deployment. The service
// role key is never a field here, and an anon key that is really a service role
// token is refused, so no configuration can put one in a response.
package publicenv

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

const (
	// JSONPath is the document the CLI fetches.
	JSONPath = "/supatype-env.json"
	// ScriptPath is the same values as a script, for a static app's script tag.
	ScriptPath = "/supatype-env.js"

	// scriptGlobal is the name the generated client reads the values from.
	scriptGlobal = "window.__SUPATYPE_ENV__"

	missingAnonKey = "public env unavailable: SUPATYPE_ANON_KEY is not set on this server"
	serviceRoleKey = "public env refused: the configured SUPATYPE_ANON_KEY is a service role key, not an anon key"
	missingURL     = "public env unavailable: set SUPATYPE_URL or SUPATYPE_API_EXTERNAL_URL to this deployment's public API address"
)

// Values is what the two paths serve, and whether they serve it at all.
type Values struct {
	// Enabled is SUPATYPE_PUBLIC_ENV. Off, both paths are 404.
	Enabled bool `json:"-"`
	// URL is the public API address. Empty is a 503 naming the settings, never
	// a guess from the request: the client has no other source for it.
	URL string `json:"url"`
	// AnonKey is SUPATYPE_ANON_KEY. Empty, or a service role token, is a 503.
	AnonKey string `json:"anonKey"`
}

// PublicURL is the deployment's public API address: SUPATYPE_URL when it is
// set, else the auth service's API_EXTERNAL_URL, the same precedence the edge
// functions are given. The auth service accepts its external URL with or
// without its own /auth/v1 mount, so that suffix is removed to leave the root.
func PublicURL(supatypeURL, apiExternalURL string) string {
	for _, u := range []string{supatypeURL, apiExternalURL} {
		if u = strings.TrimRight(strings.TrimSpace(u), "/"); u != "" {
			return strings.TrimSuffix(u, "/auth/v1")
		}
	}
	return ""
}

// Handler answers JSONPath and ScriptPath from v. The bodies and the verdict
// on the configuration are settled once: the values come from configuration
// and do not change while it runs.
//
// Switched off is 404: the deployment chose not to publish. Misconfigured is
// 503 naming the setting to fix: the deployment means to publish and cannot.
func Handler(v Values) http.Handler {
	v.URL = strings.TrimSpace(v.URL)
	v.AnonKey = strings.TrimSpace(v.AnonKey)
	problem := misconfiguration(v)
	// json.Marshal escapes <, >, & and U+2028/U+2029, so the literal cannot
	// close the script element or end the statement early. A struct of two
	// strings cannot fail to marshal.
	doc, _ := json.Marshal(v)
	script := []byte(scriptGlobal + " = " + string(doc) + ";\n")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodGet && r.Method != http.MethodHead:
			w.Header().Set("Allow", "GET, HEAD")
			refuse(w, http.StatusMethodNotAllowed, "method not allowed")
		case !v.Enabled:
			refuse(w, http.StatusNotFound, "404 page not found")
		case problem != "":
			refuse(w, http.StatusServiceUnavailable, problem)
		case r.URL.Path == ScriptPath:
			write(w, r, "application/javascript; charset=utf-8", script)
		default:
			write(w, r, "application/json; charset=utf-8", doc)
		}
	})
}

// misconfiguration is everything that stops v being served, one problem per
// line with the key before the URL, or "" when nothing does. Every problem is
// named at once so a deployer fixes them in one pass rather than one per load.
// No answer echoes a value.
func misconfiguration(v Values) string {
	var problems []string
	switch {
	case v.AnonKey == "":
		problems = append(problems, missingAnonKey)
	case jwtRole(v.AnonKey) == "service_role":
		problems = append(problems, serviceRoleKey)
	}
	if v.URL == "" {
		problems = append(problems, missingURL)
	}
	return strings.Join(problems, "\n")
}

// jwtRole is the role claim of a JWT, or "" for a key that is not one. The
// signature is not checked: this only stops a key pasted into the wrong
// variable from being published, and a token that claims service_role is
// refused whoever signed it.
func jwtRole(key string) string {
	parts := strings.Split(key, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Role
}

// write sends body with the headers both paths share. no-cache rather than a
// max-age: the values change when the deployment's configuration does, and a
// browser must revalidate rather than keep an old key.
func write(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// refuse answers with no-store, so neither a browser nor a CDN keeps a refusal
// that the next configuration change may lift.
func refuse(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, msg, status)
}
