package utilities

import (
	"fmt"
	"net/url"
	"strings"
)

// authMount is where the gateway mounts the auth API. Dev sets the external URL to it, self-host
// to the gateway's root; Studio is mounted beside it either way.
const authMount = "/auth/v1"

// StudioURL is the address Studio is served at for an API whose public address is externalURL:
// the gateway's base plus /studio/, the path the gateway mounts Studio on.
//
// Built only from configuration, never from a request, so a Studio sign-in cannot be sent
// anywhere else. Query and fragment are dropped: nothing but the origin and the base path survive.
func StudioURL(externalURL string) (string, error) {
	u, err := url.Parse(externalURL)
	if err != nil {
		return "", fmt.Errorf("API external URL %q is not a URL: %w", externalURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("API external URL %q has no http(s) origin", externalURL)
	}
	base := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), authMount)
	studio := url.URL{Scheme: u.Scheme, Host: u.Host, Path: strings.TrimSuffix(base, "/") + "/studio/"}
	return studio.String(), nil
}
