package check

import (
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// The pack walk runs one pack file per worker, so everything a load reports has
// to be a function of the tree and not of the scheduler. The fixture below is
// built for that question specifically: every family holds SEVERAL packs, and
// the problems and advisories are spread across them, so a merge that took
// completion order instead of listing order would reorder the output.
//
// The real-data tree is the other half of this coverage and is deliberately not
// it: it proves the parallel loader against 133k works, but only in the non-race
// build (see race_off_test.go). These fixtures are what run under -race.
//
// The record builders the fixture is made of (pkPerson, pkRec, pkTitled) live in
// packcheck_test.go, beside the named records every other pack test uses.

// pinWorkers fixes GOMAXPROCS for the duration of a test whose point is that the
// pool actually RAN, and restores it afterwards.
//
// It is not a nicety: at GOMAXPROCS=1 parallelDo takes the goroutine-free path,
// so on a one-CPU runner a concurrency test gets ZERO race-detector coverage
// while still passing. Measured - a deliberately injected cache-fill in
// readPack goes uncaught at GOMAXPROCS=1 and is caught at 8.
func pinWorkers(t *testing.T, n int) {
	t.Helper()
	restore := runtime.GOMAXPROCS(n)
	t.Cleanup(func() { runtime.GOMAXPROCS(restore) })
}

// parallelTree is a multi-pack, multi-family fixture carrying a violation or an
// advisory in most of its packs: five works packs, three people packs, two
// series packs and two works-community packs, with the problems deliberately
// NOT in listing order of severity so that any ordering bug shows up as a diff.
//
// It is also the fixture every OTHER test about the pool uses (the LoadStore
// comparison's "many packs" case, the borrowed-cache case), which is why the
// wide pack below is here rather than in one of them: a fixture that gives the
// pool fewer work items than it has workers is not exercising a pool.
func parallelTree() map[string]string {
	files := map[string]string{}

	// people: three flat packs, the last one holding an entry whose id disagrees
	// with its key.
	files["people/0.json"] = packOf(map[string]string{
		"author-one": pkAuthorOne, "narrator-one": pkNarratorOne,
	})
	files["people/person-b.json"] = packOf(map[string]string{
		"person-b": pkPerson("person-b", "Person B"),
	})
	files["people/person-c.json"] = packOf(map[string]string{
		"person-c": pkPerson("person-see", "Person C"),
	})

	// works: five packs under the family's one directory.
	//   0.json        clean
	//   book-b.json   one entry over the pack target -> advisory
	//   book-c.json   a cross-language recording -> advisory, an id/key
	//                 disagreement -> problem, and an entry missing a required
	//                 field -> a schema problem
	//   book-d.json   an entry the schema rejects (the license lock), and an
	//                 entry that belongs in an earlier pack -> two problems
	//   book-w.json   64 clean entries, so the pool has more work items than
	//                 workers however many cores the test machine has
	//
	// The two SCHEMA violations sit in different packs on purpose: they are the
	// only path that reaches the jsonschema validator's detailed-output walk, and
	// one of them alone would only ever run it on one worker at a time.
	files["works/0/0.json"] = packOf(map[string]string{
		"book-a":  composite(pkTitled("book-a"), map[string]string{"rec-a": pkRec("rec-a", "book-a", "en")}),
		"book-a2": pkTitled("book-a2"),
	})
	files["works/0/book-b.json"] = packOf(map[string]string{
		"book-b": bigWork("book-b", 300_000),
	})
	files["works/0/book-c.json"] = packOf(map[string]string{
		"book-c": composite(pkTitled("book-c"), map[string]string{
			"rec-c": pkRec("rec-c", "book-c", "fr"),
		}),
		"book-c2": pkTitled("book-see-two"),
		// No title: the work schema requires one.
		"book-c3": `{"authors":["author-one"],"id":"book-c3","language":"en",` +
			`"license":"CC0-1.0","sources":[{"type":"user"}]}`,
	})
	files["works/0/book-d.json"] = packOf(map[string]string{
		"book-a3": pkTitled("book-a3"),
		"book-d": `{"authors":["author-one"],"id":"book-d","language":"en","license":"CC-BY-SA-3.0",` +
			`"sources":[{"type":"user"}],"title":"book-d"}`,
	})
	wide := map[string]string{}
	for i := range 64 {
		slug := fmt.Sprintf("book-w%03d", i)
		wide[slug] = pkTitled(slug)
	}
	files["works/0/book-w.json"] = packOf(wide)

	// works-community: two packs, the second holding a sidecar whose work
	// backref disagrees with its entry key.
	files["works-community/0/0.json"] = packOf(map[string]string{
		"book-a": `{"characters":` + validCharacters("book-a") + `,"recaps":` + validRecaps("book-a") + `}`,
	})
	files["works-community/0/book-c.json"] = packOf(map[string]string{
		"book-c": `{"characters":` + validCharacters("book-elsewhere") + `}`,
	})

	// series: two flat packs, the second placing two works at one position.
	files["series/0.json"] = packOf(map[string]string{
		"series-one": `{"id":"series-one","license":"CC0-1.0","name":"Series One",` +
			`"sources":[{"type":"user"}],"works":[{"position":"1","work":"book-a"}]}`,
	})
	files["series/series-two.json"] = packOf(map[string]string{
		"series-two": `{"id":"series-two","license":"CC0-1.0","name":"Series Two",` +
			`"sources":[{"type":"user"}],"works":[{"position":"1","work":"book-a2"},` +
			`{"position":"1","work":"book-c"}]}`,
	})

	return files
}

// renderResult flattens a load into the text a comparison can diff: the
// problems, the advisories, and the ORDER the catalogue was assembled in.
// Catalogue order is part of the contract - it decides which of two duplicate
// ids wins and what every cross-record rule iterates - so a determinism test
// that only compared problems would miss a merge in the wrong order.
func renderResult(res Result) string {
	var b strings.Builder
	b.WriteString("problems:\n")
	for _, p := range res.Problems {
		b.WriteString("  " + p.String() + "\n")
	}
	b.WriteString("warnings:\n")
	for _, w := range res.Warnings {
		b.WriteString("  " + w.String() + "\n")
	}
	b.WriteString("catalog:\n")
	for _, w := range res.Catalog.Works {
		b.WriteString("  work " + w.ID + "\n")
		for _, r := range w.Recordings {
			b.WriteString("    recording " + r.ID + "\n")
		}
	}
	for _, p := range res.Catalog.People {
		b.WriteString("  person " + p.ID + "\n")
	}
	for _, s := range res.Catalog.Series {
		b.WriteString("  series " + s.ID + "\n")
	}
	for _, c := range res.Catalog.Characters {
		b.WriteString("  characters " + c.Work + "\n")
	}
	for _, r := range res.Catalog.Recaps {
		b.WriteString("  recaps " + r.Work + "\n")
	}
	return b.String()
}

// distinctPaths counts the pack files a set of problems is spread over, so the
// determinism test can assert its fixture actually spans several of them.
func distinctPaths(ps []Problem) int {
	seen := map[string]bool{}
	for _, p := range ps {
		file, _, _ := strings.Cut(p.Path, ":")
		seen[file] = true
	}
	return len(seen)
}

// TestParallelLoadIsDeterministic is the ordering pin. The per-pack work runs on
// a pool, so the same tree loaded twice - and loaded at a different worker count
// - has to produce byte-identical problems, advisories and catalogue order.
//
// The test's real teeth are renderResult's CATALOGUE section, and it must not be
// "simplified" away: load sorts the problems and the advisories before returning
// them (sortProblems), so those two sections would compare equal even if the
// merge took completion order instead of listing order. Verified by injecting
// exactly that - a merge on completion fails this test ONLY through catalogue
// order. The problems and advisories are still compared because they are what a
// caller reads, but they cannot catch a merge-order regression on their own.
func TestParallelLoadIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, parallelTree())

	first := Load(dir)
	if len(first.Problems) == 0 || len(first.Warnings) == 0 {
		t.Fatalf("the fixture must carry both problems and advisories, got %d/%d",
			len(first.Problems), len(first.Warnings))
	}
	if n := distinctPaths(first.Problems); n < 3 {
		t.Fatalf("problems span only %d files: the fixture is not exercising cross-pack ordering:\n%s",
			n, joinProblems(first.Problems))
	}
	if n := distinctPaths(first.Warnings); n < 2 {
		t.Fatalf("advisories span only %d files: the fixture is not exercising cross-pack ordering:\n%s",
			n, joinProblems(first.Warnings))
	}

	want := renderResult(first)
	// Repeated at the default worker count: nothing may vary run to run.
	for i := range 4 {
		if got := renderResult(Load(dir)); got != want {
			t.Fatalf("load %d differs from the first:\n--- want ---\n%s\n--- got ---\n%s", i+1, want, got)
		}
	}
	// And at every worker count from sequential up, including the one-worker
	// path that skips the goroutines entirely.
	restore := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(restore)
	for _, procs := range []int{1, 2, 3, 8} {
		runtime.GOMAXPROCS(procs)
		if got := renderResult(Load(dir)); got != want {
			t.Fatalf("load at GOMAXPROCS=%d differs:\n--- want ---\n%s\n--- got ---\n%s", procs, want, got)
		}
	}
}

// TestParallelDoRaisesAWorkerPanic pins the one thing parallelDo does NOT omit.
// A panic on a worker goroutine cannot be recovered by the caller and would kill
// the process; the sequential walk this pool replaced propagated it up Load's
// own stack, and pkg/check is public API, so a consumer that recovers around a
// load has to keep being able to. Both paths are covered, because the
// single-worker one is a plain loop with no recover in it at all.
func TestParallelDoRaisesAWorkerPanic(t *testing.T) {
	for _, procs := range []int{1, 8} {
		t.Run(fmt.Sprintf("GOMAXPROCS=%d", procs), func(t *testing.T) {
			pinWorkers(t, procs)

			var ran atomic.Int64
			got := func() (v any) {
				defer func() { v = recover() }()
				parallelDo(64, func(i int) {
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
			wp, ok := got.(*workerPanic)
			if !ok {
				t.Fatalf("recovered %#v, want a *workerPanic", got)
			}
			if wp.Value != "boom" {
				t.Errorf("Value = %#v, want the original panic value", wp.Value)
			}
			// The worker's own stack is the whole reason for the wrapper: a bare
			// re-panic would report the coordinator and lose it.
			if !strings.Contains(string(wp.Stack), "TestParallelDoRaisesAWorkerPanic") {
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
