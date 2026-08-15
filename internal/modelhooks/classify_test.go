package modelhooks

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/proxy"
)

func hooksFor(table string, events map[string]string) map[string]proxy.TableHooks {
	tableHooks := proxy.TableHooks{}
	for event, fn := range events {
		tableHooks[event] = proxy.HookConfig{Function: fn}
	}
	return map[string]proxy.TableHooks{table: tableHooks}
}

func request(method, target string) *http.Request {
	return httptest.NewRequest(method, target, nil)
}

func TestClassifyMatchesWritesToHookedTables(t *testing.T) {
	hooks := hooksFor("posts", map[string]string{
		EventBeforeChange: "moderate",
		EventAfterChange:  "reindex",
		EventBeforeDelete: "guard",
	})

	cases := []struct {
		name      string
		method    string
		target    string
		operation Operation
		before    string
		after     string
	}{
		{"insert", http.MethodPost, "/posts", OpInsert, "moderate", "reindex"},
		{"upsert is an insert", http.MethodPut, "/posts?id=eq.1", OpInsert, "moderate", "reindex"},
		{"update", http.MethodPatch, "/posts?status=eq.draft", OpUpdate, "moderate", "reindex"},
		// afterDelete is not declared above, so only the before hook resolves.
		{"delete", http.MethodDelete, "/posts?id=eq.1", OpDelete, "guard", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(request(tc.method, tc.target), hooks)
			if got.Table != "posts" || got.Operation != tc.operation {
				t.Fatalf("table/operation = %q/%q, want posts/%q", got.Table, got.Operation, tc.operation)
			}
			if tc.before == "" && got.Before != nil {
				t.Fatalf("before = %+v, want none", got.Before)
			}
			if tc.before != "" && (got.Before == nil || got.Before.Function != tc.before) {
				t.Fatalf("before = %+v, want %q", got.Before, tc.before)
			}
			if tc.after == "" && got.After != nil {
				t.Fatalf("after = %+v, want none", got.After)
			}
			if tc.after != "" && (got.After == nil || got.After.Function != tc.after) {
				t.Fatalf("after = %+v, want %q", got.After, tc.after)
			}
		})
	}
}

func TestClassifyIgnoresWhatIsNotAHookedWrite(t *testing.T) {
	hooks := hooksFor("posts", map[string]string{EventBeforeChange: "moderate"})

	cases := []struct {
		name   string
		method string
		target string
		why    string
	}{
		{"GET", http.MethodGet, "/posts", "a read has no hook, and buffering its body would be waste"},
		{"HEAD", http.MethodHead, "/posts", "same"},
		{"OPTIONS", http.MethodOptions, "/posts", "CORS preflight is not a write"},
		{"another table", http.MethodPost, "/comments", "only declared tables"},
		// An RPC can write anything; guessing which table it touched would fire the wrong hook.
		{"rpc", http.MethodPost, "/rpc/publish_post", "a function is not a table write"},
		{"root", http.MethodPost, "/", "no table in the path"},
		{"bare rpc", http.MethodPost, "/rpc", "not a table either"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(request(tc.method, tc.target), hooks); got.HasWork() {
				t.Fatalf("Classify matched %s %s (%s): %+v", tc.method, tc.target, tc.why, got)
			}
		})
	}
}

func TestClassifyWithNoHooksDeclared(t *testing.T) {
	// The overwhelmingly common case: every request must take the cheapest possible path.
	if got := Classify(request(http.MethodPost, "/posts"), nil); got.HasWork() {
		t.Fatalf("Classify matched with no manifest hooks: %+v", got)
	}
	empty := map[string]proxy.TableHooks{"posts": {}}
	if got := Classify(request(http.MethodPost, "/posts"), empty); got.HasWork() {
		t.Fatalf("Classify matched an empty hook set: %+v", got)
	}
}

func TestClassifyIgnoresAHookWithNoFunction(t *testing.T) {
	// A malformed manifest entry must not produce a dispatch to "": that would 404 at the worker,
	// which onUnavailable would then turn into a failed write for no stated reason.
	hooks := map[string]proxy.TableHooks{
		"posts": {EventBeforeChange: proxy.HookConfig{Function: ""}},
	}
	if got := Classify(request(http.MethodPost, "/posts"), hooks); got.Before != nil {
		t.Fatalf("Classify used a hook with no function: %+v", got.Before)
	}
}

func TestClassifyCarriesTheHookConfig(t *testing.T) {
	// The timeout and unavailable policy have to survive classification, or the dispatcher would
	// silently use its own defaults and a per-hook setting in the schema would do nothing.
	hooks := map[string]proxy.TableHooks{
		"posts": {
			EventBeforeChange: proxy.HookConfig{
				Function:      "moderate",
				TimeoutMs:     4500,
				OnUnavailable: "log",
			},
		},
	}
	got := Classify(request(http.MethodPost, "/posts"), hooks)
	if got.Before == nil {
		t.Fatal("no before hook resolved")
	}
	if got.Before.TimeoutMs != 4500 || got.Before.OnUnavailable != "log" {
		t.Fatalf("hook config = %+v, want timeout 4500 and onUnavailable log", *got.Before)
	}
}
