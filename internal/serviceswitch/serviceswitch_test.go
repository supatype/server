package serviceswitch

import (
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

func TestStartValues(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg     config.Config
		service string
		want    bool
	}{
		"storage, by default":                {config.Config{}, proxy.ServiceStorage, true},
		"storage, SUPATYPE_STORAGE_DISABLED": {config.Config{StorageDisabled: true}, proxy.ServiceStorage, false},
		"functions, no worker":               {config.Config{}, proxy.ServiceFunctions, false},
		"functions, a worker":                {config.Config{FunctionsWorkerURL: "http://worker"}, proxy.ServiceFunctions, true},
		"functions, a blank worker":          {config.Config{FunctionsWorkerURL: "  "}, proxy.ServiceFunctions, false},
		"realtime, whatever the URL":         {config.Config{RealtimeURL: "http://rt"}, proxy.ServiceRealtime, false},
		"a service nothing knows":            {config.Config{}, "mystery", false},
	} {
		cfg := tc.cfg
		if got := Start(&cfg, tc.service); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestOnTakesTheManifestOverTheStartValue(t *testing.T) {
	m := &proxy.RouteManifest{}
	if !On(m, &config.Config{}, proxy.ServiceStorage) {
		t.Error("storage said nothing about: off, want its start value, on")
	}
	m.SetService(proxy.ServiceStorage, false)
	if On(m, &config.Config{}, proxy.ServiceStorage) {
		t.Error("storage switched off by the manifest: on")
	}
	var none *proxy.RouteManifest
	if !On(none, &config.Config{FunctionsWorkerURL: "http://worker"}, proxy.ServiceFunctions) {
		t.Error("no manifest: functions off, want the start value, on")
	}
}
