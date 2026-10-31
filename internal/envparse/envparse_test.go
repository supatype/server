package envparse

import (
	"os"
	"runtime"
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

// Where environment names are case-insensitive, as on Windows, a lower-case spelling of a
// setting's name still sets it. Elsewhere it is another variable, and the setting keeps its
// default.
func TestALowerCaseNameReachesTheSettingOnlyWhereNamesIgnoreCase(t *testing.T) {
	original := namesIgnoreCase
	t.Cleanup(func() { namesIgnoreCase = original })
	t.Setenv("t_port", "25")
	for _, tc := range []struct {
		ignoreCase bool
		want       int
	}{{true, 25}, {false, 587}} {
		if !tc.ignoreCase && runtime.GOOS == "windows" {
			continue // the OS folds the name itself there, so t_port is T_PORT
		}
		namesIgnoreCase = tc.ignoreCase
		var s spec
		if err := Process("t", &s); err != nil {
			t.Fatal(err)
		}
		if s.Port != tc.want {
			t.Errorf("names ignore case = %v: port %d, want %d", tc.ignoreCase, s.Port, tc.want)
		}
	}
}
