package utilities

import "strings"

// StringValue safely extracts a string from a *string, returning empty string if nil
func StringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// StringPtr returns a pointer to a string if non-empty, nil otherwise
func StringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// FirstNonEmpty returns the first value that is not blank.
//
// This is the resolution rule the service uses everywhere a value can come from
// more than one place: a tenant manifest overrides configuration, configuration
// overrides a built-in default. Three packages had their own copy.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
