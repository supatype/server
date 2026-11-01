package gateway

import (
	"net/http"
	"testing"
)

// The storage service judges an object name as the client wrote it, and refuses one that steps
// through folders (packages/storage, round 7 N31). It can only do that when the gateway forwards a
// /storage/v1 path unchanged: folded on the way, "x/../a" would arrive as "a" and write over it.
// The path the upstream receives is the one the client sent, less the mount prefix, in every mode a
// stack runs.
func TestTheStorageProxyForwardsThePathAsSent(t *testing.T) {
	names := []string{"x/../a", "x//a", "%2E/a", "./a", "x/./a", "/a", "x/a/.."}
	for _, mode := range []string{"dev", "standalone"} {
		r := newRig(t, mode)
		for _, name := range names {
			rec := r.do(http.MethodGet, "/storage/v1/object/public/bucket/"+name, nil)
			got, ok := r.lastHit()
			want := "/object/public/bucket/" + name
			if rec.Code != http.StatusOK || !ok || got.upstream != "storage" || got.requestURI != want {
				t.Errorf("%s %q: %d, upstream %q saw %q, want storage to see %q", mode, name, rec.Code, got.upstream, got.requestURI, want)
			}
		}
	}
}
