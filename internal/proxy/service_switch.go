package proxy

import "encoding/json"

// The services a route manifest switches on and off, by their key in RouteManifest.Services.
const (
	ServiceRealtime  = "realtime"
	ServiceStorage   = "storage"
	ServiceFunctions = "functions"
)

// ServiceSwitch is how a route manifest switches one upstream service on or off. It is the one
// shape every switchable service uses, and the base any service's own settings extend.
//
// Enabled is a pointer because "said nothing" and "said false" are different instructions: nil
// leaves the server's start value (its environment) in force, and false switches off a service the
// environment started on. Realtime had a bool of its own, which could not say nothing, and storage
// an inverted one; each had its own merge and health rule (0.9.2).
type ServiceSwitch struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// ServiceOn is whether the named service is switched on: what the manifest says, or start when it
// says nothing. A nil manifest says nothing.
func (m *RouteManifest) ServiceOn(name string, start bool) bool {
	if m == nil {
		return start
	}
	if s := m.Services[name].Enabled; s != nil {
		return *s
	}
	return start
}

// SetService switches the named service on or off.
func (m *RouteManifest) SetService(name string, on bool) {
	if m.Services == nil {
		m.Services = make(map[string]ServiceSwitch)
	}
	m.Services[name] = ServiceSwitch{Enabled: &on}
}

// mergeServices takes every switch the overlay sets. One the overlay says nothing about is left as
// the base has it: absence is not "off".
func mergeServices(base, overlay *RouteManifest) {
	for name, s := range overlay.Services {
		if s.Enabled != nil {
			base.SetService(name, *s.Enabled)
		}
	}
}

// cloneServices copies the map, so one tenant's reload cannot write into another's.
func cloneServices(services map[string]ServiceSwitch) map[string]ServiceSwitch {
	if services == nil {
		return nil
	}
	copied := make(map[string]ServiceSwitch, len(services))
	for name, s := range services {
		copied[name] = s
	}
	return copied
}

// legacySwitches are the keys that switched a service before Services existed. Cloud and older
// CLIs still write them, so they are read into Services. A Services entry wins over its legacy key.
var legacySwitches = map[string]string{
	"realtime_enabled":  ServiceRealtime,
	"functions_enabled": ServiceFunctions,
}

// UnmarshalJSON reads a manifest, the legacy switch keys included.
func (m *RouteManifest) UnmarshalJSON(data []byte) error {
	// Keys first: what is not an object fails here, and what is one, with a field of the wrong
	// type, fails the plain decode below.
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	// A type without this method, so the plain decode does not recurse.
	type plain RouteManifest
	if err := json.Unmarshal(data, (*plain)(m)); err != nil {
		return err
	}
	for key, name := range legacySwitches {
		var on *bool
		// Not a bool (a null, say) is not a switch; decoding it only leaves on nil.
		if raw, ok := legacy[key]; !ok || json.Unmarshal(raw, &on) != nil || on == nil {
			continue
		}
		if m.Services[name].Enabled == nil {
			m.SetService(name, *on)
		}
	}
	return nil
}

// SwitchesOf is a Services map from each service's on or off.
func SwitchesOf(on map[string]bool) map[string]ServiceSwitch {
	services := make(map[string]ServiceSwitch, len(on))
	for name, enabled := range on {
		services[name] = ServiceSwitch{Enabled: &enabled}
	}
	return services
}
