package gateway

// The one file in the gateway that has to say the old prefix out loud.
//
// Kept apart from the rest of the bootstrap tests so the repo-wide guard in
// tools/envsurface can allow it by name without also allowing a large file
// where a genuinely stale variable could hide.

import (
	"context"
	"strings"
	"testing"
)

// A deployment that still names the old prefix is told so, by name, before
// anything else is decoded.
//
// This is the whole point of the rename having been a hard break: GOTRUE_JWT_SECRET
// set and SUPATYPE_JWT_SECRET not means the service has no JWT secret, and every
// request 401s a long way from the cause. The check existed and nothing called
// it, so the binary started and failed on some unrelated required key instead.
func TestNewRefusesTheOldPrefix(t *testing.T) {
	requireAuthEnvironment(t)

	t.Setenv("GOTRUE_JWT_SECRET", "the-old-name")

	ctx, cancel := context.WithCancel(context.Background())
	_, drain, err := New(ctx)
	cancel()
	if err == nil {
		drain()
		t.Fatal("started with a variable nothing reads")
	}
	for _, want := range []string{"GOTRUE_JWT_SECRET", "SUPATYPE_JWT_SECRET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
}
