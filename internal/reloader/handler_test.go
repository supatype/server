package reloader

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAtomicHandler(t *testing.T) {
	// for ptr identity
	type testHandler struct{ http.Handler }

	var calls atomic.Int64
	hrFn := func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
		})
	}

	hrFunc1 := &testHandler{hrFn()}
	hrFunc2 := &testHandler{hrFn()}
	assert.NotEqual(t, hrFunc1, hrFunc2)

	// a new AtomicHandler should be non-nil
	hr := NewAtomicHandler(hrFunc1)
	assert.NotNil(t, hr)
	assert.Equal(t, "reloader.AtomicHandler", hr.String())

	// should implement http.Handler
	{
		v := (http.Handler)(hr)
		before := calls.Load()
		v.ServeHTTP(nil, nil)
		after := calls.Load()
		if exp, got := before+1, after; exp != got {
			t.Fatalf("exp %v to be %v after handler was called", got, exp)
		}
	}

	// should be non-nil after store
	for i := 0; i < 3; i++ {
		hr.Store(hrFunc1)
		assert.NotNil(t, hr.load())
		assert.Equal(t, hr.load(), hrFunc1)
		assert.Equal(t, hr.load() == hrFunc1, true)

		// should update to hrFunc2
		hr.Store(hrFunc2)
		assert.NotNil(t, hr.load())
		assert.Equal(t, hr.load(), hrFunc2)
		assert.Equal(t, hr.load() == hrFunc2, true)
	}
}

// LoadHandler is the exported reader used by serve to reach the live API, and it
// was the one statement in this package no test touched. The package is held at
// 100% by hack/coverage-floors.json, so it needs one.
func TestAtomicHandlerLoadHandler(t *testing.T) {
	type testHandler struct{ http.Handler }

	first := &testHandler{http.NotFoundHandler()}
	second := &testHandler{http.NotFoundHandler()}

	hr := NewAtomicHandler(first)
	assert.Equal(t, http.Handler(first), hr.LoadHandler())

	hr.Store(second)
	assert.Equal(t, http.Handler(second), hr.LoadHandler())
}
