package deno

import (
	"slices"
	"testing"
	"time"
)

func TestEnvForDenoProcess_overridesPORT(t *testing.T) {
	t.Setenv("PORT", "9999")
	t.Setenv("OTHER", "x")

	got := envForDenoProcess(8001, []string{"EXTRA=1"})

	if slices.ContainsFunc(got, func(s string) bool { return s == "PORT=9999" }) {
		t.Fatalf("old PORT=9999 should be stripped: %#v", got)
	}
	if !slices.Contains(got, "PORT=8001") {
		t.Fatalf("want PORT=8001, got %#v", got)
	}
	if !slices.Contains(got, "OTHER=x") {
		t.Fatalf("want OTHER preserved: %#v", got)
	}
	if !slices.Contains(got, "EXTRA=1") {
		t.Fatalf("want EXTRA: %#v", got)
	}
}

func TestMinDuration(t *testing.T) {
	t.Parallel()
	if min(2*time.Second, 5*time.Second) != 2*time.Second || min(7*time.Second, 3*time.Second) != 3*time.Second {
		t.Fatal("min")
	}
}

func TestBackoffConstants(t *testing.T) {
	t.Parallel()
	if backoffInitial <= 0 || backoffMax < backoffInitial {
		t.Fatalf("backoffInitial=%v backoffMax=%v", backoffInitial, backoffMax)
	}
}
