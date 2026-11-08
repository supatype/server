package config

import "testing"

func TestStrictBool(t *testing.T) {
	for value, want := range map[string]bool{
		"true": true, "TRUE": true, "True": true, " true ": true,
		// Narrower than strconv.ParseBool on purpose: a plain bool field would
		// accept these, and the cloud gateway never has.
		"1": false, "t": false, "yes": false, "on": false,
		"false": false, "": false, "anything": false,
	} {
		var got StrictBool
		if err := got.Decode(value); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if got.Bool() != want {
			t.Errorf("StrictBool(%q) = %v, want %v", value, got.Bool(), want)
		}
	}
}

func TestSwitchBool(t *testing.T) {
	for value, want := range map[string]bool{
		"1": true, "true": true, "TRUE": true, "yes": true, "YES": true,
		"on": true, "On": true, " on ": true,
		"0": false, "false": false, "off": false, "no": false, "": false, "t": false,
	} {
		var got SwitchBool
		if err := got.Decode(value); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if got.Bool() != want {
			t.Errorf("SwitchBool(%q) = %v, want %v", value, got.Bool(), want)
		}
	}
}

// The two types must stay different. If they ever agree on everything, one of
// them is dead and the split should be removed rather than maintained.
func TestBoolTypesStillDiffer(t *testing.T) {
	var strict StrictBool
	var loose SwitchBool
	for _, value := range []string{"1", "yes", "on"} {
		_ = strict.Decode(value)
		_ = loose.Decode(value)
		if strict.Bool() == loose.Bool() {
			t.Errorf("%q: both types agree; the distinction has been lost", value)
		}
	}
}

// DefaultOnSwitch is on until it is turned off, and its zero value is on, so a
// Config built without Load keeps the behaviour the variable's default
// describes.
func TestDefaultOnSwitch(t *testing.T) {
	var zero DefaultOnSwitch
	if !zero.On() {
		t.Fatal("the zero DefaultOnSwitch must be on")
	}
	for value, want := range map[string]bool{
		"1": true, "true": true, "TRUE": true, "yes": true, "on": true, " On ": true,
		"0": false, "false": false, "False": false, "no": false, "off": false, " OFF ": false,
	} {
		var got DefaultOnSwitch
		if err := got.Decode(value); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if got.On() != want {
			t.Errorf("DefaultOnSwitch(%q).On() = %v, want %v", value, got.On(), want)
		}
	}
	if SwitchedOff().On() {
		t.Error("SwitchedOff must report off")
	}
}

// A typo in an opt-out switch must stop the process rather than leave the
// feature in whichever state the typo happens to decode to.
func TestDefaultOnSwitchRefusesAnUnknownWord(t *testing.T) {
	var got DefaultOnSwitch
	if err := got.Decode("flase"); err == nil {
		t.Fatal("want an error for an unrecognised value")
	}
}
