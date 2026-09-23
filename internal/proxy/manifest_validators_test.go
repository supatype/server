package proxy

import "testing"

// MergeRouteManifest copied Hooks from the overlay and not Validators, so on a managed pod — where
// the overlay is `tenant:{ref}:manifest` and carries what `supatype push` wrote — a schema's
// validators never took effect at all.
//
// The asymmetry is the bug. Both maps are written by the same push, for the same reason, and one
// was honoured while the other was silently discarded. A validator that never runs is the quiet
// half: the schema says the field is checked, no error appears anywhere, and the write succeeds.

func TestAnOverlaysValidatorsReachTheEffectiveManifest(t *testing.T) {
	base := &RouteManifest{}
	overlay := &RouteManifest{
		Validators: map[string]TableValidators{
			"posts": {"title": {Function: "check-title"}},
		},
	}

	MergeRouteManifest(base, overlay)

	got, ok := base.Validators["posts"]
	if !ok {
		t.Fatal("the overlay's validators were dropped, so nothing validates on a managed pod")
	}
	if got["title"].Function != "check-title" {
		t.Fatalf("validator = %+v, want check-title", got["title"])
	}
}

func TestAValidatorRemovedFromTheSchemaStopsRunning(t *testing.T) {
	// The same property the Hooks merge has, and for the same reason: the overlay is the current
	// truth about what exists. A per-key merge would keep calling a validator the schema dropped.
	base := &RouteManifest{Validators: map[string]TableValidators{
		"posts":  {"title": {Function: "check-title"}},
		"legacy": {"name": {Function: "check-name"}},
	}}
	overlay := &RouteManifest{Validators: map[string]TableValidators{
		"posts": {"title": {Function: "check-title"}},
	}}

	MergeRouteManifest(base, overlay)

	if _, still := base.Validators["legacy"]; still {
		t.Error("a validator dropped from the schema is still in force")
	}
	if len(base.Validators) != 1 {
		t.Fatalf("validators = %+v, want only posts", base.Validators)
	}
}

func TestAnOverlayWithNoValidatorsLeavesThemAlone(t *testing.T) {
	// nil is "this overlay says nothing about validators", not "there are none". An overlay
	// carrying only a URL override must not disable every validator on the stack.
	base := &RouteManifest{Validators: map[string]TableValidators{
		"posts": {"title": {Function: "check-title"}},
	}}

	MergeRouteManifest(base, &RouteManifest{PostgRESTURL: "http://example"})

	if len(base.Validators) != 1 {
		t.Fatalf("validators = %+v, want untouched", base.Validators)
	}
}

func TestAnEmptyOverlayMapClearsTheValidators(t *testing.T) {
	// Distinct from nil: a schema that now declares none has to be able to say so.
	base := &RouteManifest{Validators: map[string]TableValidators{
		"posts": {"title": {Function: "check-title"}},
	}}

	MergeRouteManifest(base, &RouteManifest{Validators: map[string]TableValidators{}})

	if len(base.Validators) != 0 {
		t.Fatalf("validators = %+v, want cleared", base.Validators)
	}
}
