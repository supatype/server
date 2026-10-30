package envparse

import (
	"os"
	"testing"
	"time"
)

type spec struct {
	Port    int           `default:"587"`
	Wait    time.Duration `default:"2s"`
	Name    string        `default:"named"`
	Enabled *bool         `default:"true"`
}

func TestProcessReadsABlankTypedSettingAsUnset(t *testing.T) {
	t.Setenv("T_PORT", "")
	t.Setenv("T_WAIT", " \t")
	t.Setenv("T_ENABLED", "")
	t.Setenv("T_NAME", "")

	var s spec
	if err := Process("t", &s); err != nil {
		t.Fatal(err)
	}
	if s.Port != 587 || s.Wait != 2*time.Second || s.Enabled == nil || !*s.Enabled {
		t.Fatalf("typed defaults not applied: %+v", s)
	}
	if s.Name != "" {
		t.Fatalf("a blank string setting must stay blank, got %q", s.Name)
	}
	if v, ok := os.LookupEnv("T_WAIT"); !ok || v != " \t" {
		t.Fatalf("T_WAIT not restored as it was: %q, %v", v, ok)
	}
}

func TestProcessStillRejectsAnUnparseableValue(t *testing.T) {
	t.Setenv("T_PORT", "five")

	var s spec
	if err := Process("t", &s); err == nil {
		t.Fatal("want an error for a non-numeric port")
	}
}
