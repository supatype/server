package config

import "strings"

// The service has always had two spellings of "on", and collapsing them here
// would change what existing deployments mean by their own configuration. Both
// are named types rather than plain bools so envconfig decodes each the way its
// variable has always been read, and so the inconsistency is visible in one
// place instead of being rediscovered at every call site.
//
// Unifying them is a deliberate behaviour change and belongs with the rename in
// Phase 6, where the variables are changing name anyway and a release note can
// say so.

// StrictBool is true only for the exact word "true", in any case.
//
// This is how the cloud gateway has always read its switches. Note that it is
// narrower than Go's own strconv.ParseBool, which would also accept "1" and
// "t"; using a plain bool field here would quietly widen them.
type StrictBool bool

// Decode implements envconfig.Decoder.
func (b *StrictBool) Decode(value string) error {
	*b = StrictBool(strings.EqualFold(strings.TrimSpace(value), "true"))
	return nil
}

// Bool reports the value as a plain bool.
func (b StrictBool) Bool() bool { return bool(b) }

// SwitchBool is true for 1, true, yes or on, in any case.
//
// This is how the Studio dev bypass and the SQL runner's insecure switch have
// always been read.
type SwitchBool bool

// Decode implements envconfig.Decoder.
func (b *SwitchBool) Decode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		*b = true
	default:
		*b = false
	}
	return nil
}

// Bool reports the value as a plain bool.
func (b SwitchBool) Bool() bool { return bool(b) }
