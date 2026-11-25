package gateway

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// fakeDenoEnv marks a child process as a stand-in for the Deno worker. DENO_ is one of the
// prefixes the supervisor lets a worker inherit, so a test can set it and the child sees it.
const fakeDenoEnv = "DENO_GATEWAY_FAKE_WORKER"

// TestMain lets this test binary stand in for Deno: the supervisor runs it as "deno run ...", it
// prints one worker line and waits to be stopped. Every other run is the ordinary test run.
func TestMain(m *testing.M) {
	if os.Getenv(fakeDenoEnv) == "1" {
		fmt.Println("hello from the fake worker")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
