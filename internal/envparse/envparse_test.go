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

// Process reads the environment and never writes it: a goroutine reading a
// blank typed setting while a load runs must always find it present. Hiding
// the variable for the length of the parse made it vanish for every other
// reader of the process environment.
func TestProcessNeverChangesTheProcessEnvironment(t *testing.T) {
	t.Setenv("T_PORT", "")
	stop := make(chan struct{})
	vanished := make(chan bool, 1)
	go func() {
		seen := false
		for {
			select {
			case <-stop:
				vanished <- seen
				return
			default:
				_, ok := os.LookupEnv("T_PORT")
				seen = seen || !ok
			}
		}
	}()
	for range 2000 {
		var s spec
		if err := Process("t", &s); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if <-vanished {
		t.Fatal("T_PORT went missing from the environment while Process ran")
	}
}
