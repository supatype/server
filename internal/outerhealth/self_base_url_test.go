package outerhealth

import "testing"

func TestSelfBaseURLForRealtimeProbe(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		override string
		mode     string
		tlsDom   string
		host     string
		port     string
		want     string
	}{
		{"override", "https://edge.example/", "standalone", "x.example", "0.0.0.0", "443", "https://edge.example/"},
		{"standalone_tls_default_port", "", "standalone", "api.example.com", "0.0.0.0", "", "https://api.example.com"},
		{"standalone_tls_explicit_443", "", "standalone", "api.example.com", "0.0.0.0", "443", "https://api.example.com"},
		{"standalone_tls_custom_port", "", "standalone", "api.example.com", "0.0.0.0", "8443", "https://api.example.com:8443"},
		{"dev_loopback", "", "dev", "", "0.0.0.0", "9999", "http://127.0.0.1:9999"},
		{"dev_explicit_host", "", "dev", "", "10.0.0.5", "8080", "http://10.0.0.5:8080"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SelfBaseURLForRealtimeProbe(tc.override, tc.mode, tc.tlsDom, tc.host, tc.port)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
