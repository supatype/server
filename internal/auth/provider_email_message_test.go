package auth

import (
	"strings"
	"testing"
)

func TestUnverifiedProviderEmailMessageSaysWhatToDo(t *testing.T) {
	sent := unverifiedProviderEmailMessage("github", true)
	for _, want := range []string{"GitHub says the email on your GitHub account is not verified", "confirmation link", "sign in again", "GitHub settings"} {
		if !strings.Contains(sent, want) {
			t.Errorf("sent message %q does not contain %q", sent, want)
		}
	}
	notSent := unverifiedProviderEmailMessage("github", false)
	if strings.Contains(notSent, "confirmation link") {
		t.Errorf("message %q promises a link that was not sent", notSent)
	}
	if !strings.Contains(notSent, "Verify it in your GitHub settings, then sign in again.") {
		t.Errorf("message %q does not say how to fix it", notSent)
	}
}

func TestProviderDisplayName(t *testing.T) {
	cases := map[string]string{"github": "GitHub", "gitlab": "GitLab", "google": "Google", "kakao": "Kakao", "": ""}
	for in, want := range cases {
		if got := providerDisplayName(in); got != want {
			t.Errorf("providerDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}
