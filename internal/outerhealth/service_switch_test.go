package outerhealth

import (
	"reflect"
	"testing"
	"time"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// 0.9.2: /health follows the manifest's services map, the same switch the gateway reads, over the
// server's start value.
func TestProbeConfigFromFollowsTheServicesMap(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg                          config.Config
		manifest                     string
		wantStorageOff, wantRealtime bool
	}{
		"nothing said":             {config.Config{}, `{}`, false, false},
		"storage started off":      {config.Config{StorageDisabled: true}, `{}`, true, false},
		"storage switched on":      {config.Config{StorageDisabled: true}, `{"services":{"storage":{"enabled":true}}}`, false, false},
		"storage switched off":     {config.Config{}, `{"services":{"storage":{"enabled":false}}}`, true, false},
		"realtime switched on":     {config.Config{}, `{"services":{"realtime":{"enabled":true}}}`, false, true},
		"realtime, the legacy key": {config.Config{}, `{"realtime_enabled":true}`, false, true},
	} {
		m, err := proxy.ParseRouteManifestJSON([]byte(tc.manifest))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg := tc.cfg
		got := ProbeConfigFrom(&cfg, m, "")
		if got.StorageDisabled != tc.wantStorageOff || got.RealtimeEnabled != tc.wantRealtime {
			t.Errorf("%s: storage off %v, realtime on %v; want %v and %v",
				name, got.StorageDisabled, got.RealtimeEnabled, tc.wantStorageOff, tc.wantRealtime)
		}
	}
}

// A service switched off reads the same on /health whichever it is: realtime said "ready": true
// where storage and GraphQL said "status": "disabled".
func TestEveryServiceSwitchedOffReadsTheSame(t *testing.T) {
	want := map[string]any{"enabled": false, "skipped": true, "status": "disabled"}
	for name, got := range map[string]map[string]any{
		"graphql":  graphqlComponent(ProbeConfig{GraphQLDisabled: true}, time.Millisecond),
		"storage":  storageComponent(ProbeConfig{StorageDisabled: true}, time.Millisecond),
		"realtime": realtimeComponent(ProbeConfig{}, time.Millisecond),
	} {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s switched off: %v, want %v", name, got, want)
		}
	}
}
