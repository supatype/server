package deno

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// The functions router writes JSON so that cloud's Promtail pipeline can label each line by
// project, level and request. This server reads the same output when it supervises Deno itself, so
// a line taken verbatim would put raw JSON in front of somebody reading logs in Studio.
//
// The fallback matters at least as much: Deno writes to this stream too (a compile error, a panic,
// a permission warning) and none of that is JSON. Dropping those would hide exactly the output
// somebody is looking for when a function will not start.

func TestAStructuredLineIsUnpacked(t *testing.T) {
	line := parseLine("info",
		`{"timestamp":"2026-09-07T17:04:23.020Z","level":"warn","service":"functions-worker","project_ref":"blog","message":"halfway there"}`)

	if line.Message != "halfway there" {
		t.Fatalf("message = %q, want the message field, not the whole line", line.Message)
	}
	// The router knows a warning from an error; the stream it arrived on only knows two levels.
	if line.Level != "warn" {
		t.Fatalf("level = %q, want the line's own level rather than the stream's", line.Level)
	}
	want, err := time.Parse(time.RFC3339, "2026-09-07T17:04:23.020Z")
	if err != nil {
		t.Fatal(err)
	}
	if !line.Timestamp.Equal(want) {
		t.Fatalf("timestamp = %v, want the line's own %v", line.Timestamp, want)
	}
}

// A line written during an invocation names the function, which is how one function's log is
// told apart from another's.
func TestAStructuredLineNamesItsFunction(t *testing.T) {
	line := parseLine("info", `{"timestamp":"2026-09-07T17:04:23.020Z","level":"info","request_id":"r1","function":"hello","message":"hi"}`)
	if line.Function != "hello" {
		t.Fatalf("function = %q, want hello", line.Function)
	}
	if line := parseLine("info", `{"level":"info","message":"functions router listening"}`); line.Function != "" {
		t.Fatalf("function = %q, want none for a line that names none", line.Function)
	}
	if line := parseLine("error", "error: Uncaught TypeError"); line.Function != "" {
		t.Fatalf("function = %q, want none for Deno's own output", line.Function)
	}
}

func TestOutputThatIsNotOursIsKeptWhole(t *testing.T) {
	// Deno's own output. Every one of these is what somebody needs to see when a function will not
	// start, so none of it may be dropped for failing to be JSON.
	for _, raw := range []string{
		"error: Uncaught TypeError: x is not a function",
		"Warning Implicitly using latest version",
		"    at file:///project/functions/hello.ts:3:9",
		"",
	} {
		line := parseLine("error", raw)
		if line.Message != raw {
			t.Fatalf("message = %q, want the line unchanged: %q", line.Message, raw)
		}
		if line.Level != "error" {
			t.Fatalf("level = %q, want the stream's level for a line that declares none", line.Level)
		}
	}
}

func TestJSONThatIsNotALogLineIsKeptWhole(t *testing.T) {
	// A function printing its own JSON payload. It starts with a brace and parses, but it is not a
	// log line, and rewriting it to an empty message would lose the output entirely.
	raw := `{"id":42,"name":"a thing the function printed"}`
	if line := parseLine("info", raw); line.Message != raw {
		t.Fatalf("message = %q, want the payload unchanged", line.Message)
	}
}

func TestABrokenTimestampDoesNotLoseTheLine(t *testing.T) {
	// Better to file the line under now than to drop it because a clock field was malformed.
	before := time.Now().UTC().Add(-time.Second)
	line := parseLine("info", `{"timestamp":"not a time","level":"info","message":"still useful"}`)

	if line.Message != "still useful" {
		t.Fatalf("message = %q, want the message", line.Message)
	}
	if line.Timestamp.Before(before) {
		t.Fatalf("timestamp = %v, want roughly now", line.Timestamp)
	}
}

// syncBuffer is a buffer the two drain goroutines may write to together.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// With a sink set, every worker line also goes to it, prefixed with the function that wrote it,
// so a server run in a terminal shows the function's console.log. A line naming no function
// (Deno's own output) takes the prefix "deno".
func TestLogSinkEchoesLinesWithTheFunctionName(t *testing.T) {
	m := manager(t, "")
	var sink syncBuffer
	m.LogTo(&sink)

	m.appendLog("info", routerLine("hello", "hello 1"))
	m.appendLog("error", "error: Uncaught TypeError: x is not a function")

	want := "[hello] hello 1\n[deno] error: Uncaught TypeError: x is not a function\n"
	if got := sink.String(); got != want {
		t.Fatalf("sink = %q, want %q", got, want)
	}
	if got := m.RecentLogs(time.Time{}, 10); len(got) != 2 {
		t.Errorf("ring buffer = %v, want both lines kept as well", got)
	}
}

// Without a sink nothing is written anywhere but the ring buffer: the default must not change
// what a deployed server prints.
func TestNoLogSinkByDefault(t *testing.T) {
	m := manager(t, "")
	m.appendLog("info", routerLine("hello", "hello 1"))
	if m.sink != nil {
		t.Fatal("a new Manager must have no sink")
	}
}
