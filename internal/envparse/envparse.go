// Package envparse reads settings from the environment into a struct the way
// envconfig does, with one difference documented on Process.
package envparse

import (
	"os"
	"reflect"
	"runtime"
	"strings"
	"text/template"

	"github.com/kelseyhightower/envconfig"
)

// blankKeysTemplate lists every variable name envconfig would read for a field
// whose blank value cannot mean anything, one per line. It runs over the
// library's own field list so the names are derived exactly as Process derives
// them, prefix, split_words and alternate names included.
const blankKeysTemplate = `{{range .}}{{if blankIsUnset .Field}}{{.Key}}
{{if .Alt}}{{.Alt}}
{{end}}{{end}}{{end}}`

var blankKeys = template.Must(template.New("blank").
	Funcs(template.FuncMap{"blankIsUnset": blankIsUnset}).
	Parse(blankKeysTemplate))

// blankIsUnset reports whether a blank value for the field should read as
// "not set". A string field keeps a blank value, because some settings use it
// on purpose: a blank SUPATYPE_DENO_PATH turns the Deno supervisor off. Every
// other kind (numbers, bools, durations, lists, maps) has no blank value to
// parse, so a blank line can only mean the setting was left empty.
func blankIsUnset(field reflect.Value) bool {
	t := field.Type()
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Kind() != reflect.String
}

// Process fills spec from the environment like envconfig.Process, except
// that a blank value for a non-string setting counts as unset, so the field's
// default applies. That is how compose reads ${SUPATYPE_SMTP_PORT:-587}, and it
// is what a project's .env means by a line such as SUPATYPE_SMTP_PORT= whether
// the server loads the file itself or the variable arrives exported.
//
// The blank variables are hidden from envconfig for the duration of the call
// and put back afterwards, so the process environment is left as it was found.
func Process(prefix string, spec any) error {
	hidden, err := blankTypedVars(prefix, spec)
	if err != nil {
		return err
	}
	defer restoreEnv(hidden)
	for key := range hidden {
		if err := os.Unsetenv(key); err != nil {
			return err
		}
	}
	return envconfig.Process(prefix, spec)
}

// blankTypedVars returns the non-string settings in spec that are present in
// the environment with a blank value, mapped to that value.
//
// It walks os.Environ rather than looking each name up, so the only names it
// reads are the struct-derived ones envconfig already reads, which the
// environment surface in hack/env-surface.txt lists.
func blankTypedVars(prefix string, spec any) (map[string]string, error) {
	var names strings.Builder
	if err := envconfig.Usaget(prefix, spec, &names, blankKeys); err != nil {
		return nil, err
	}
	typed := make(map[string]bool)
	for _, key := range strings.Fields(names.String()) {
		typed[key] = true
	}
	blank := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && typed[envName(key)] && strings.TrimSpace(value) == "" {
			blank[key] = value
		}
	}
	return blank, nil
}

// envName is the name envconfig would match key against. Windows names are
// case-insensitive, so there a lower-case spelling still reaches the field.
func envName(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

// restoreEnv puts hidden variables back with their original values. A failure
// here leaves a blank typed variable unset, which reads the same to every
// consumer of it, so it is not worth failing a load that has already succeeded.
func restoreEnv(vars map[string]string) {
	for key, value := range vars {
		_ = os.Setenv(key, value)
	}
}
