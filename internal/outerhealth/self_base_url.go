package outerhealth

import (
	"net"
	"strings"
)

// SelfBaseURLForRealtimeProbe returns the outer base URL used to GET /realtime/v1/health.
// override is SUPATYPE_HEALTH_SELF_BASE_URL when non-empty; otherwise standalone+TLS uses
// https://TLSDomain (and non-443 port when apiPort is set); else loopback http://... from apiHost/apiPort.
func SelfBaseURLForRealtimeProbe(override, mode, tlsDomain, apiHost, apiPort string) string {
	if u := strings.TrimSpace(override); u != "" {
		return u
	}
	if strings.TrimSpace(mode) == "standalone" && strings.TrimSpace(tlsDomain) != "" {
		host := strings.TrimSpace(tlsDomain)
		ap := strings.TrimSpace(apiPort)
		if ap == "" || ap == "443" {
			return "https://" + host
		}
		return "https://" + net.JoinHostPort(host, ap)
	}
	h := strings.TrimSpace(apiHost)
	if h == "" || h == "0.0.0.0" {
		h = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(h, strings.TrimSpace(apiPort))
}
