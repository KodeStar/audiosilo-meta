package check

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// parallelDo runs fn for every index in [0, n) on a bounded pool of workers and
// returns once all of them have finished.
//
// It is deliberately the smallest thing that does the job: no error channel, no
// context, no ordering. The caller owns a slot per index and writes only its own
// (see loadPackFamily), so the pool never needs to synchronize anything beyond
// handing out the next index - which is what keeps the load's determinism a
// property of the MERGE rather than of the scheduler.
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
		// GOMAXPROCS=1 (and a single-pack family) behaves identically.
		for i := range n {
			fn(i)
		}
		return
	}

	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
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
}
