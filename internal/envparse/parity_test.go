package envparse

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// upper is a setting type that converts itself, as envconfig.Decoder allows.
type upper struct{ V string }

func (u *upper) Decode(value string) error {
	if value == "bad" {
		return errors.New("refused")
	}
	u.V = strings.ToUpper(value)
	return nil
}

type Inner struct {
	Depth int `default:"3"`
}

type nested struct {
	Level   int
	Enabled bool
}

// wide covers every way envconfig names and converts a setting.
type wide struct {
	Inner
	Nested    nested
	SplitMe   int            `split_words:"true"`
	Aliased   string         `envconfig:"ALIASED_NAME"`
	Must      string         `required:"true"`
	Count     *int           `default:"4"`
	Unsigned  uint8          `default:"7"`
	Ratio     float64        `default:"0.5"`
	Wait      time.Duration  `default:"1m"`
	List      []int          `default:"1,2"`
	Raw       []byte         `default:"raw"`
	Pairs     map[string]int `default:"a:1,b:2"`
	Custom    upper          `default:"loud"`
	Blank     string         `default:"kept"`
	BlankPtr  *string
	Preset    *string
	Skipped   int `ignored:"true"`
	unexposed int
}

// newWide is a wide with values set before loading, as a caller may have.
func newWide() *wide {
	preset := "before"
	return &wide{Preset: &preset, Skipped: 9, unexposed: 1}
}

// setAll sets each variable for the length of the test.
func setAll(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// Wherever no blank typed value is involved, Process fills a spec exactly as
// envconfig.Process does: names, alternate names, defaults and conversions.
func TestProcessMatchesEnvconfig(t *testing.T) {
	setAll(t, map[string]string{
		"W_DEPTH":          "5",
		"W_NESTED_LEVEL":   "2",
		"W_NESTED_ENABLED": "true",
		"W_SPLIT_ME":       "8",
		"ALIASED_NAME":     "bare alternate",
		"W_MUST":           "here",
		"W_RATIO":          "1.5",
		"W_PAIRS":          "z:26",
		"W_CUSTOM":         "quiet",
		"W_BLANK":          "",
		"W_BLANKPTR":       "",
		"W_PRESET":         "",
	})
	got, want := newWide(), newWide()
	if err := Process("w", got); err != nil {
		t.Fatal(err)
	}
	if err := envconfig.Process("w", want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Process filled\n%+v\nenvconfig filled\n%+v", got, want)
	}
}

// A setting that is missing or cannot be converted fails as envconfig fails,
// naming the same variable and field.
func TestProcessFailsAsEnvconfigFails(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"required missing": {},
		"bad int":          {"W_MUST": "x", "W_SPLIT_ME": "eight"},
		"bad decoder":      {"W_MUST": "x", "W_CUSTOM": "bad"},
	} {
		t.Run(name, func(t *testing.T) {
			setAll(t, env)
			got, want := Process("w", newWide()), envconfig.Process("w", newWide())
			if got == nil || want == nil || got.Error() != want.Error() {
				t.Fatalf("Process: %v\nenvconfig: %v", got, want)
			}
		})
	}
}

// A spec that is not a struct pointer is refused before anything is read.
func TestProcessRefusesANonPointerSpec(t *testing.T) {
	if err := Process("w", wide{}); !errors.Is(err, envconfig.ErrInvalidSpecification) {
		t.Fatalf("want ErrInvalidSpecification, got %v", err)
	}
}
