package modelhooks

import (
	"net/http"
	"strconv"
)

// HookDepthHeader carries how many hooks deep a write already is.
//
// A hook receives the service-role key and can write through the API. If it writes to a table that
// declares the hook it is running as, that write re-enters this middleware and calls the same hook
// again — the classic trigger loop, except each hop is a fresh HTTP request holding a connection and a
// function slot. Nothing stops it: `service_role` changes what Postgres permits, not whether this
// middleware runs.
//
// So the chain counts itself. The server stamps this header on every hook invocation, and the worker
// re-emits it on stack-bound requests a handler makes, so the count survives the hop through code we do
// not control.
const HookDepthHeader = "X-Supatype-Hook-Depth"

// MaxHookDepth is how deep a chain may go before a write is refused.
//
// Not 1: fanning out to another table's hook is legitimate and useful — a `beforeChange` on `posts`
// writing an `audit_log` row whose own hook ships it somewhere is two levels and entirely reasonable.
// A runaway loop, by contrast, passes any small number immediately, so the limit only needs to be low
// enough to fail fast.
const MaxHookDepth = 4

// hookDepth reads the depth a request arrived with. Absent, negative or unparseable reads as 0: a
// caller cannot lower its own depth, and a malformed value must not read as "very deep" either, which
// would let anyone disable hooks for a table by sending a header.
func hookDepth(req *http.Request) int {
	raw := req.Header.Get(HookDepthHeader)
	if raw == "" {
		return 0
	}
	depth, err := strconv.Atoi(raw)
	if err != nil || depth < 0 {
		return 0
	}
	if depth > MaxHookDepth {
		return MaxHookDepth
	}
	return depth
}
