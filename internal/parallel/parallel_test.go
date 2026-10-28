package parallel

import (
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// TestDoRaisesAWorkerPanic pins the one thing Do does NOT omit.
// A panic on a worker goroutine cannot be recovered by the caller and would kill
// the process; the sequential walks this pool replaced propagated it up the caller's
// own stack, and its callers are public API, so a consumer that recovers around a
// load has to keep being able to. Both paths are covered, because the
// single-worker one is a plain loop with no recover in it at all.
func TestDoRaisesAWorkerPanic(t *testing.T) {
	for _, procs := range []int{1, 8} {
		t.Run(fmt.Sprintf("GOMAXPROCS=%d", procs), func(t *testing.T) {
			restore := runtime.GOMAXPROCS(procs)
			t.Cleanup(func() { runtime.GOMAXPROCS(restore) })

			var ran atomic.Int64
			got := func() (v any) {
				defer func() { v = recover() }()
				Do(64, func(i int) {
					ran.Add(1)
					if i == 7 {
						panic("boom")
					}
				})
				return nil
			}()
			if got == nil {
				t.Fatal("a panicking worker did not reach the caller")
			}
			if procs == 1 {
				// The sequential path panics straight out, unwrapped.
				if got != "boom" {
					t.Fatalf("recovered %#v, want the original panic value", got)
				}
				return
			}
			wp, ok := got.(*WorkerPanic)
			if !ok {
				t.Fatalf("recovered %#v, want a *WorkerPanic", got)
			}
			if wp.Value != "boom" {
				t.Errorf("Value = %#v, want the original panic value", wp.Value)
			}
			// The worker's own stack is the whole reason for the wrapper: a bare
			// re-panic would report the coordinator and lose it.
			if !strings.Contains(string(wp.Stack), "TestDoRaisesAWorkerPanic") {
				t.Errorf("the captured stack is not the worker's:\n%s", wp.Stack)
			}
			if !strings.Contains(wp.String(), "boom") {
				t.Errorf("String() does not print the value: %s", wp.String())
			}
			// The pool drains rather than abandoning the indexes in flight, so a
			// caller that recovers is not looking at half-written results.
			if n := ran.Load(); n < 8 {
				t.Errorf("only %d of 64 indexes ran: the pool abandoned its work", n)
			}
		})
	}
}
