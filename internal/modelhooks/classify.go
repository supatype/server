// Package modelhooks runs a project's schema-declared lifecycle hooks on the REST write path.
//
// A hook is an edge function called by this server before or after a write, so it can validate,
// enrich, reject, or react. Separate from internal/auth/hooks, which is the auth service's own hooks:
// same word, different feature, and merging them would confuse both.
//
// What a hook is not: a security boundary. It fires for writes through this API, so direct SQL, seeds
// and migrations bypass it. Invariants belong in RLS.
//
// Note what does *not* bypass it: `service_role`. That key changes what Postgres permits, not whether
// this middleware runs, so a service-role write over HTTP fires hooks like any other — which is why a
// hook writing to its own table would call itself forever without the depth guard.
package modelhooks

import (
	"net/http"
	"sort"
	"strings"

	"github.com/supatype/server/internal/proxy"
)

// Event names, matching what the CLI writes into the manifest.
const (
	EventBeforeChange = "beforeChange"
	// EventBeforeValidate is a per-field validator. Named `before` because it runs before the
	// write and, more importantly, because that is what decides the safe answer when it cannot
	// be reached.
	EventBeforeValidate = "beforeValidate"
	EventAfterChange    = "afterChange"
	EventBeforeDelete   = "beforeDelete"
	EventAfterDelete    = "afterDelete"
)

// Operation is the write a request performs, as a hook payload reports it.
type Operation string

const (
	OpInsert Operation = "insert"
	OpUpdate Operation = "update"
	OpDelete Operation = "delete"
)

// Target is the hook work a single request implies.
type Target struct {
	Table     string
	Operation Operation
	// Before is the hook to call before the write, if the table declares one.
	Before *HookConfigEntry
	// After is the hook to call once the write has succeeded, if the table declares one.
	After *HookConfigEntry
	// BeforeEvent and AfterEvent name the events, so a dispatcher can set the header without
	// re-deriving them from the operation.
	BeforeEvent string
	AfterEvent  string
	// Validators are per-field rules run before the write, keyed by column.
	Validators map[string]HookConfigEntry
	// ValidatorFields fixes the order they run in. A map's iteration order is random in Go, and a
	// row breaching two rules would otherwise be refused by a different one each time, which makes
	// the error a coin toss and any test of it flaky.
	ValidatorFields []string
}

// HasWork reports whether anything needs calling.
func (t Target) HasWork() bool {
	return t.Before != nil || t.After != nil || len(t.Validators) > 0
}

// tableFromPath reads the table from a `/rest/v1`-relative path.
//
// The prefix is already stripped by the mount, so the path is `/posts` or `/posts?select=…`. An RPC
// call (`/rpc/name`) is not a table write and must never match: a function could write anything, and
// pretending we know which table it touched would fire the wrong hook.
func tableFromPath(path string) string {
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return ""
	}
	if i := strings.IndexAny(trimmed, "/?"); i >= 0 {
		// A nested segment means RPC or something we do not model; only a bare table matches.
		if trimmed[i] == '/' {
			return ""
		}
		trimmed = trimmed[:i]
	}
	// `/rpc/name` already returned above on the nested-segment rule; this covers a bare `/rpc`,
	// which is not a table either.
	if trimmed == "rpc" {
		return ""
	}
	return trimmed
}

// operationForMethod maps an HTTP method to the write it performs, or false for a read.
//
// PUT is PostgREST's single-row upsert. It is treated as an insert because that is the shape of its
// body — a complete row — and a hook typed for a patch would receive something it could not read.
func operationForMethod(method string) (Operation, bool) {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut:
		return OpInsert, true
	case http.MethodPatch:
		return OpUpdate, true
	case http.MethodDelete:
		return OpDelete, true
	default:
		return "", false
	}
}

// Classify decides which hooks, if any, a request implies, from a manifest's hook map.
//
// A thin adapter over classifyViews: the matching rules live in one place, and this package stays
// indifferent to whether the map arrived from a manifest file or a control plane.
func Classify(req *http.Request, hooks map[string]proxy.TableHooks) Target {
	return classifyViews(req, ViewsFromManifest(hooks))
}

// classifyViews is Classify against the decoupled view types the middleware uses.
//
// The middleware deliberately does not depend on `proxy` for this: the hook map may arrive from a
// manifest today and from the control plane tomorrow, and the matching logic should not care.
func classifyViews(req *http.Request, hooks map[string]TableHooksView) Target {
	return classifyWith(req, hooks, nil)
}

// classifyWith decides what a request needs, given the declared hooks and field validators.
//
// A table may declare validators and no hooks, or the reverse, so neither map alone can rule the
// request out.
func classifyWith(
	req *http.Request,
	hooks map[string]TableHooksView,
	validators map[string]TableValidatorsView,
) Target {
	if len(hooks) == 0 && len(validators) == 0 {
		return Target{}
	}
	op, isWrite := operationForMethod(req.Method)
	if !isWrite {
		return Target{}
	}
	table := tableFromPath(req.URL.Path)
	if table == "" {
		return Target{}
	}
	declared := hooks[table]
	tableValidators := validators[table]
	if len(declared) == 0 && len(tableValidators) == 0 {
		return Target{}
	}

	target := Target{Table: table, Operation: op}
	if op == OpDelete {
		target.BeforeEvent, target.AfterEvent = EventBeforeDelete, EventAfterDelete
	} else {
		target.BeforeEvent, target.AfterEvent = EventBeforeChange, EventAfterChange
	}
	if cfg, ok := declared[target.BeforeEvent]; ok && cfg.Function != "" {
		target.Before = &HookConfigEntry{
			Function: cfg.Function, TimeoutMs: cfg.TimeoutMs, OnUnavailable: cfg.OnUnavailable,
		}
	}
	// A delete carries no field values, so a per-field rule has nothing to run against.
	if op != OpDelete && len(tableValidators) > 0 {
		target.Validators = make(map[string]HookConfigEntry, len(tableValidators))
		for field, cfg := range tableValidators {
			if cfg.Function == "" {
				continue
			}
			target.Validators[field] = cfg
			target.ValidatorFields = append(target.ValidatorFields, field)
		}
		sort.Strings(target.ValidatorFields)
	}
	if cfg, ok := declared[target.AfterEvent]; ok && cfg.Function != "" {
		target.After = &HookConfigEntry{
			Function: cfg.Function, TimeoutMs: cfg.TimeoutMs, OnUnavailable: cfg.OnUnavailable,
		}
	}
	return target
}

// ViewsFromManifest adapts a manifest's hook map into the view types this package works in.
//
// Exported so the mount can build a HooksFunc without the middleware depending on how the map was
// delivered — a manifest file today, possibly a control-plane push later.
func ViewsFromManifest(hooks map[string]proxy.TableHooks) map[string]TableHooksView {
	if len(hooks) == 0 {
		return nil
	}
	views := make(map[string]TableHooksView, len(hooks))
	for table, events := range hooks {
		view := make(TableHooksView, len(events))
		for event, cfg := range events {
			view[event] = HookConfigEntry{
				Function:      cfg.Function,
				TimeoutMs:     cfg.TimeoutMs,
				OnUnavailable: cfg.OnUnavailable,
			}
		}
		views[table] = view
	}
	return views
}

// ValidatorViewsFromManifest converts the manifest's validator map into the view the middleware
// reads, mirroring ViewsFromManifest so neither shape leaks into the other package.
func ValidatorViewsFromManifest(
	validators map[string]proxy.TableValidators,
) map[string]TableValidatorsView {
	if len(validators) == 0 {
		return nil
	}
	views := make(map[string]TableValidatorsView, len(validators))
	for table, fields := range validators {
		view := make(TableValidatorsView, len(fields))
		for field, cfg := range fields {
			view[field] = HookConfigEntry{
				Function:      cfg.Function,
				TimeoutMs:     cfg.TimeoutMs,
				OnUnavailable: cfg.OnUnavailable,
			}
		}
		views[table] = view
	}
	return views
}
