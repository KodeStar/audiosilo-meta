package check

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

// parallelDo runs fn for every index in [0, n) on a bounded pool of workers and
// returns once all of them have finished.
//
// It is deliberately the smallest thing that does the job. What it omits, and
// why: no error channel and no context (nothing a pack load does is cancellable
// or fails in a way that should stop its siblings - a problem is data, not an
// error), and no ordering (the caller owns a slot per index and writes only its
// own - see loadPackFamily - so the pool never needs to synchronize anything
// beyond handing out the next index, which is what keeps the load's determinism
// a property of the MERGE rather than of the scheduler).
//
// What it does NOT omit is panics. A panic on a worker goroutine cannot be
// recovered by the caller and would take the process down, where the sequential
// walk this replaced propagated it up Load's own stack - and pkg/check is public
// API, so a consumer that recovers around a load must keep being able to. The
// first panic any worker takes is captured, the rest of the pool is allowed to
// drain, and it is re-raised here once every worker has stopped.
//
// The pool is bounded at GOMAXPROCS because the work is CPU-bound (JSON parse,
// schema validation, struct decode, canonical render) with one file's read in
// front of it, and because the bound is also the memory posture: at most one
// pack file is resident per worker, so a bigger pool would mean a bigger peak
// for no more throughput.
func parallelDo(n int, fn func(i int)) {
	if n <= 0 {
		return
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > n {
		workers = n
	}
	if workers <= 1 {
		// One worker is the sequential loop, spelled without a goroutine so that
		// GOMAXPROCS=1 (and a single-pack family) behaves identically - including
		// for a panic, which propagates straight out of this call.
		for i := range n {
			fn(i)
		}
		return
	}

	var next atomic.Int64
	var wg sync.WaitGroup
	var once sync.Once
	var failure *workerPanic
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			// A worker that panics stops, and takes no further indexes; the
			// others finish theirs, so nothing is left half-written for a
			// recovering caller to find. Only the FIRST panic is kept - the
			// others are almost certainly the same bug seen from a second
			// goroutine, and one trace beats a race between several.
			defer func() {
				if v := recover(); v != nil {
					p := &workerPanic{Value: v, Stack: debug.Stack()}
					once.Do(func() { failure = p })
				}
			}()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
	if failure != nil {
		panic(failure)
	}
}

// workerPanic is a panic that happened on one of parallelDo's workers, re-raised
// on the coordinating goroutine.
//
// It wraps the value rather than re-panicking it bare because the goroutine that
// panicked is gone by then: a bare re-panic would print the coordinator's stack
// and lose the only record of where the fault actually was. Value is the
// original, so a caller that recovers can still type-assert on it, and the
// String method is what the runtime prints for an unrecovered one - the value
// first, then the worker's own stack.
type workerPanic struct {
	Value any
	Stack []byte
}

func (p *workerPanic) String() string {
	return fmt.Sprintf("%v [recovered on a pkg/check worker goroutine]\n\noriginal stack:\n%s", p.Value, p.Stack)
}
