package auth

import (
	"fmt"
	"strings"
)

// providerDisplayNames spells the providers whose names are not plain title case.
var providerDisplayNames = map[string]string{
	"github":    "GitHub",
	"gitlab":    "GitLab",
	"linkedin":  "LinkedIn",
	"workos":    "WorkOS",
	"bitbucket": "Bitbucket",
}

// providerDisplayName is how a person would write the provider's name, for messages they read.
func providerDisplayName(provider string) string {
	if name, ok := providerDisplayNames[strings.ToLower(provider)]; ok {
		return name
	}
	if provider == "" {
		return provider
	}
	return strings.ToUpper(provider[:1]) + provider[1:]
}

// unverifiedProviderEmailMessage says what to do when the provider reports the account's email as not
// verified: open the confirmation link when one was sent, or verify the email with the provider.
func unverifiedProviderEmailMessage(provider string, confirmationSent bool) string {
	name := providerDisplayName(provider)
	if confirmationSent {
		return fmt.Sprintf("%s says the email on your %s account is not verified yet. "+
			"We have sent a confirmation link to that address: open it, then sign in again. "+
			"You can also verify the email in your %s settings.", name, name, name)
	}
	return fmt.Sprintf("%s says the email on your %s account is not verified yet. "+
		"Verify it in your %s settings, then sign in again.", name, name, name)
}
