package check

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
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

// pkPerson returns a valid person record. The id has to BE the slug of the name
// (checkPersonSlug), so both are given.
func pkPerson(id, name string) string {
	return `{"id":` + strconv.Quote(id) + `,"license":"CC0-1.0","name":` + strconv.Quote(name) +
		`,"sources":[{"type":"user"}]}`
}

// pkRec returns a valid recording record under work.
func pkRec(id, work, lang string) string {
	return `{"abridged":false,"id":` + strconv.Quote(id) + `,"language":` + strconv.Quote(lang) +
		`,"license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":` +
		strconv.Quote(work) + `}`
}

// pkTitled returns a valid work whose title is its own slug, so the fixture does
// not also trip the identity-equal-works advisory on every pair of its works -
// which would bury the advisories it is actually about.
func pkTitled(slug string) string {
	return `{"authors":["author-one"],"id":` + strconv.Quote(slug) +
		`,"language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":` +
		strconv.Quote(slug) + `}`
}

// parallelTree is a multi-pack, multi-family fixture carrying a violation or an
// advisory in most of its packs: four works packs, three people packs, two
// series packs and two works-community packs, with the problems deliberately
// NOT in listing order of severity so that any ordering bug shows up as a diff.
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

	// works: four packs under the family's one directory.
	//   0.json        clean
	//   book-b.json   one entry over the pack target -> advisory
	//   book-c.json   a cross-language recording -> advisory, plus an id/key
	//                 disagreement -> problem
	//   book-d.json   an entry the schema rejects, and an entry that belongs in
	//                 an earlier pack -> two problems
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
	})
	files["works/0/book-d.json"] = packOf(map[string]string{
		"book-a3": pkTitled("book-a3"),
		"book-d": `{"authors":["author-one"],"id":"book-d","language":"en","license":"CC-BY-SA-3.0",` +
			`"sources":[{"type":"user"}],"title":"book-d"}`,
	})

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

// TestParallelLoadExercisesEveryFamily is the coverage the real-data tree used
// to be the only source of: a load that runs the pool over more than one pack in
// every family, small enough to run under -race. Without it, -race would only
// ever see single-pack fixtures, where the pool degenerates to a sequential
// loop.
func TestParallelLoadExercisesEveryFamily(t *testing.T) {
	dir := t.TempDir()
	files := parallelTree()
	// A wide works pack on top of the fixture, so the pool has more work items
	// than it has workers however many cores the test machine has.
	wide := map[string]string{}
	for i := range 64 {
		slug := fmt.Sprintf("book-w%03d", i)
		wide[slug] = pkWork(slug)
	}
	files["works/0/book-w.json"] = packOf(wide)
	writeTree(t, dir, files)

	res := Load(dir)
	if len(res.Catalog.Works) != 71 {
		t.Fatalf("expected 71 works across five packs, got %d", len(res.Catalog.Works))
	}
	if len(res.Catalog.People) != 4 || len(res.Catalog.Series) != 2 ||
		len(res.Catalog.Characters) != 2 || len(res.Catalog.Recaps) != 1 {
		t.Fatalf("unexpected catalog counts: %d people, %d series, %d characters, %d recaps",
			len(res.Catalog.People), len(res.Catalog.Series),
			len(res.Catalog.Characters), len(res.Catalog.Recaps))
	}
	// Every family's packs have to be represented in the index too, or a merge
	// dropped one worker's contribution.
	if len(res.Catalog.Works) == 0 || res.Catalog.Works[0].ID != "book-a" {
		t.Fatalf("catalogue does not start at the first pack's first entry: %+v", res.Catalog.Works[0])
	}
}
