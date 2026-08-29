package gateway

import (
	"testing"

	"github.com/supatype/server/internal/deno"
)

// A nil *deno.Manager placed in an interface is not a nil interface. The
// functions API checks its log source for nil and would otherwise call a method
// on a nil pointer, so a deployment with edge functions disabled would panic on
// the log endpoint rather than answering with none.
//
// The same trap has already been hit once in this service, converting the
// Valkey client to an interface, which is why the conversion happens here where
// the concrete type is still visible.
func TestFunctionLogsIsNilWhenThereIsNoWorker(t *testing.T) {
	d := &Deps{}
	if got := d.FunctionLogs(); got != nil {
		t.Errorf("FunctionLogs() = %#v, want a nil interface", got)
	}

	d.Deno = deno.New("deno", "router.ts", 8001, nil, false)
	if d.FunctionLogs() == nil {
		t.Error("a real worker was dropped")
	}
}
