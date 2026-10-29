// Package envparse reads settings from the environment into a struct the way
// envconfig does, with one difference documented on Process.
package envparse

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"text/template"

	"github.com/kelseyhightower/envconfig"
)

// Process fills spec from the environment like envconfig.Process, except
// that a blank value for a non-string setting counts as unset, so the field's
// default applies. That is how compose reads ${SUPATYPE_SMTP_PORT:-587}, and it
// is what a project's .env means by a line such as SUPATYPE_SMTP_PORT= whether
// the server loads the file itself or the variable arrives exported.
//
// envconfig reads the process environment itself and offers no way to read
// anything else, so it cannot be told to skip a blank value. Process therefore
// walks envconfig's own list of settings, looks each one up in a snapshot of
// the environment, and leaves only the conversion of a value to envconfig. The
// process environment is read once and never written, so a goroutine reading
// it while a load runs sees it exactly as it is.
func Process(prefix string, spec any) error {
	settings, err := settingsOf(prefix, spec)
	if err != nil {
		return err
	}
	env := environment()
	for _, s := range settings {
		if err := s.load(env); err != nil {
			return err
		}
	}
	return nil
}

// setting is one variable envconfig reads into spec: the field's name, the
// variable's name and alternate name, the field's tags, and the field itself.
type setting struct {
	name, key, alt string
	tags           reflect.StructTag
	field          reflect.Value
}

// settingsTemplate hands each of envconfig's settings to the add function.
const settingsTemplate = `{{range .}}{{add .Name .Key .Alt .Tags .Field}}{{end}}`

// settingsOf is every setting envconfig reads into spec, in the order it reads
// them. They come from the library's own field list through Usaget, so the
// names are derived exactly as Process derives them, prefix, split_words and
// alternate names included.
func settingsOf(prefix string, spec any) ([]setting, error) {
	var settings []setting
	add := func(name, key, alt string, tags reflect.StructTag, field reflect.Value) string {
		settings = append(settings, setting{name: name, key: key, alt: alt, tags: tags, field: field})
		return ""
	}
	list := template.Must(template.New("settings").Funcs(template.FuncMap{"add": add}).Parse(settingsTemplate))
	if err := envconfig.Usaget(prefix, spec, io.Discard, list); err != nil {
		return nil, err
	}
	return settings, nil
}

// environment is a snapshot of the process environment, keyed by the name
// envconfig would match. It walks os.Environ rather than looking each name up,
// so the only names it reads are the struct-derived ones envconfig already
// reads, which the environment surface in hack/env-surface.txt lists.
func environment() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[envName(key)] = value
		}
	}
	return env
}

// envName is the name envconfig would match key against. Windows names are
// case-insensitive, so there a lower-case spelling still reaches the field.
func envName(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

// load reads the setting into its field as envconfig.Process does: the
// variable, else its alternate name, else the default tag, else nothing, which
// is an error only for a required setting.
func (s setting) load(env map[string]string) error {
	value, ok := s.lookup(env, s.key)
	if !ok && s.alt != "" {
		value, ok = s.lookup(env, s.alt)
	}
	if ok {
		return s.set(value)
	}
	if def := s.tags.Get("default"); def != "" {
		return s.set(def)
	}
	if isTrue(s.tags.Get("required")) {
		return fmt.Errorf("required key %s missing value", cmp.Or(s.alt, s.key))
	}
	return nil
}

// lookup is the value of the variable name, treating a blank value as unset
// when the field has no use for one.
func (s setting) lookup(env map[string]string, name string) (string, bool) {
	value, ok := env[name]
	if ok && strings.TrimSpace(value) == "" && blankIsUnset(s.field) {
		return "", false
	}
	return value, ok
}

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

// unsetName is a variable name no environment can hold: every OS splits an
// entry at its first '=', so no name contains one past its first character.
const unsetName = "ENVPARSE=UNSET"

// set converts value into the field with envconfig's own conversion. That
// conversion is only reachable through envconfig.Process, which reads the
// process environment, so the value goes in as the default of a one-field
// struct whose variable is named unsetName and is therefore never found.
//
// A default tag cannot carry an empty value, so an empty value, which only a
// string field accepts, is stored directly, as envconfig stores it.
func (s setting) set(value string) error {
	if value == "" {
		setEmpty(s.field)
		return nil
	}
	tag := fmt.Sprintf(`envconfig:%q default:%s`, unsetName, strconv.Quote(value))
	field := reflect.StructField{Name: "V", Type: s.field.Type(), Tag: reflect.StructTag(tag)}
	holder := reflect.New(reflect.StructOf([]reflect.StructField{field})).Elem()
	holder.Field(0).Set(s.field)
	if err := envconfig.Process("", holder.Addr().Interface()); err != nil {
		return s.renamed(err)
	}
	s.field.Set(holder.Field(0))
	return nil
}

// renamed is err from converting the setting's value, naming the setting
// rather than the one-field struct the conversion ran on.
func (s setting) renamed(err error) error {
	var parse *envconfig.ParseError
	if errors.As(err, &parse) {
		parse.KeyName, parse.FieldName = s.key, s.name
	}
	return err
}

// setEmpty stores an empty value in a string field, or through a pointer to
// one, allocating the pointer when it is nil.
func setEmpty(field reflect.Value) {
	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		field = field.Elem()
	}
	field.SetZero()
}

// isTrue reads a boolean tag as envconfig does: anything unparseable is false.
func isTrue(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}
