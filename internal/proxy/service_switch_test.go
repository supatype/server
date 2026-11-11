package proxy

import (
	"testing"
)

// 0.9.2: realtime_enabled and functions_enabled are still read (Cloud and older CLIs write them),
// into the same services map, and an entry there wins over its legacy key.
func TestTheLegacySwitchKeysAreReadIntoServices(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest            string
		realtime, functions *bool
	}{
		"both on":                      {`{"realtime_enabled":true,"functions_enabled":true}`, ptr(true), ptr(true)},
		"realtime off":                 {`{"realtime_enabled":false}`, ptr(false), nil},
		"neither said":                 {`{"schema":"x"}`, nil, nil},
		"null is not a switch":         {`{"realtime_enabled":null}`, nil, nil},
		"a string is not a switch":     {`{"realtime_enabled":"yes"}`, nil, nil},
		"the services entry wins":      {`{"realtime_enabled":true,"services":{"realtime":{"enabled":false}}}`, ptr(false), nil},
		"a services entry with no say": {`{"realtime_enabled":true,"services":{"realtime":{}}}`, ptr(true), nil},
	} {
		m, err := ParseRouteManifestJSON([]byte(tc.manifest))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		assertSwitch(t, name+", realtime", m.Services[ServiceRealtime].Enabled, tc.realtime)
		assertSwitch(t, name+", functions", m.Services[ServiceFunctions].Enabled, tc.functions)
	}
}

func TestAManifestThatIsNotAnObjectIsRefused(t *testing.T) {
	for _, raw := range []string{`[1]`, `{"schema":1}`} {
		if _, err := ParseRouteManifestJSON([]byte(raw)); err == nil {
			t.Errorf("%s: no error", raw)
		}
	}
}

func TestServiceOn(t *testing.T) {
	var none *RouteManifest
	if !none.ServiceOn(ServiceStorage, true) || none.ServiceOn(ServiceStorage, false) {
		t.Error("a nil manifest did not answer the start value")
	}
	m := &RouteManifest{}
	if !m.ServiceOn(ServiceStorage, true) {
		t.Error("a manifest that says nothing did not answer the start value")
	}
	m.SetService(ServiceStorage, false)
	if m.ServiceOn(ServiceStorage, true) {
		t.Error("switched off, but on")
	}
}

// A copy's switches are its own: one tenant's reload must not write into another's.
func TestCloneCopiesTheSwitches(t *testing.T) {
	m := &RouteManifest{Services: SwitchesOf(map[string]bool{ServiceRealtime: true})}
	c := CloneRouteManifest(m)
	c.SetService(ServiceRealtime, false)
	if !m.ServiceOn(ServiceRealtime, false) {
		t.Error("switching the copy switched the original")
	}
	if CloneRouteManifest(&RouteManifest{}).Services != nil {
		t.Error("a manifest with no switches gained a map")
	}
}

func ptr(b bool) *bool { return &b }

func assertSwitch(t *testing.T, name string, got, want *bool) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil || *got != *want:
		t.Errorf("%s: got %v, want %v", name, describe(got), describe(want))
	}
}

func describe(b *bool) string {
	if b == nil {
		return "nothing said"
	}
	if *b {
		return "on"
	}
	return "off"
}
