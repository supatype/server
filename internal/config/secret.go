package config

// Secret is a configuration value that must never reach a log line.
//
// It is a named string so envconfig decodes it like any other variable, and it
// masks itself in every way a value usually gets printed: fmt's %v, %s and %q
// through String, %#v through GoString, and logrus's JSON formatter and any
// json.Marshal of the Config through MarshalText. Reading the value takes an
// explicit Reveal, so the one place that needs it says so.
type Secret string

// redacted is what a set Secret prints as. An unset one prints as nothing, so a
// log line still tells an operator whether the value was configured.
const redacted = "[redacted]"

// Reveal returns the value itself, for the client that has to send it.
func (s Secret) Reveal() string { return string(s) }

// String implements fmt.Stringer.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

// GoString implements fmt.GoStringer, so %#v masks it too.
func (s Secret) GoString() string { return s.String() }

// MarshalText implements encoding.TextMarshaler, so JSON output masks it too.
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }
