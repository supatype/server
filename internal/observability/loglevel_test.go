package observability

import "testing"

// The structured log level used to be read from LOG_LEVEL at package
// initialisation, which made it a hidden dependency on the process environment
// and left it unsettable from configuration.
func TestParseLevel(t *testing.T) {
	for name, want := range map[string]int{
		"debug": levelDebug,
		"info":  levelInfo,
		"warn":  levelWarn,
		"error": levelError,

		// Anything unrecognised is info, which is what reading an unset or
		// misspelled LOG_LEVEL always produced.
		"":         levelInfo,
		"INFO":     levelInfo,
		"trace":    levelInfo,
		"warning":  levelInfo,
		"critical": levelInfo,
	} {
		if got := ParseLevel(name); got != want {
			t.Errorf("ParseLevel(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestSetStructuredLevel(t *testing.T) {
	original := logLevel
	t.Cleanup(func() { logLevel = original })

	SetStructuredLevel("error")
	if logLevel != levelError {
		t.Errorf("after SetStructuredLevel(error), logLevel = %d, want %d", logLevel, levelError)
	}

	SetStructuredLevel("debug")
	if logLevel != levelDebug {
		t.Errorf("after SetStructuredLevel(debug), logLevel = %d, want %d", logLevel, levelDebug)
	}

	SetStructuredLevel("nonsense")
	if logLevel != levelInfo {
		t.Errorf("an unrecognised level should fall back to info, got %d", logLevel)
	}
}

// The default must be info without anyone calling Configure, because a binary
// that never sets a level should still log.
func TestDefaultLevelIsInfo(t *testing.T) {
	if levelInfo != 1 {
		t.Fatalf("levelInfo = %d; the ordering constants have shifted", levelInfo)
	}
	if ParseLevel("") != levelInfo {
		t.Error("an unset level must mean info")
	}
}
