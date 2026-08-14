package repair

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// The fixture builders. Every test seeds a small, SCHEMA-VALID tree with them - the
// repo's rule for a rule's fixtures - because this pass refuses to write into a tree
// that does not validate, so a malformed fixture would test the refusal rather than
// the repair.
//
// They are internal/audit's builders, deliberately: a fixture here has to produce a
// non-advisory proposal from the REAL detectors, so it has to look like the data those
// were calibrated against.

type workOpt func(map[string]any)

func withAuthors(ids ...string) workOpt {
	return func(m map[string]any) { m["authors"] = ids }
}

func withGenres(gs ...string) workOpt {
	return func(m map[string]any) { m["genres"] = gs }
}

// withCredits sets the work's role-qualified credits, as alternating person and role
// arguments. A credit is load-bearing for the IDENTITY rule as well as for the union:
// check.IdentityEqualWorks discounts a role-credited person from a work's identity set,
// which is what lets two records whose author lists differ by a contributor meet.
func withCredits(pairs ...string) workOpt {
	return func(m map[string]any) {
		credits := make([]map[string]string, 0, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			credits = append(credits, map[string]string{"person": pairs[i], "role": pairs[i+1]})
		}
		m["credits"] = credits
	}
}

func withSubtitle(s string) workOpt {
	return func(m map[string]any) { m["subtitle"] = s }
}

func withoutLanguage() workOpt {
	return func(m map[string]any) { delete(m, "language") }
}

func withWorkXref(isbns ...string) workOpt {
	return func(m map[string]any) { m["xref"] = map[string]any{"isbn": isbns} }
}

func workJSON(t testing.TB, id, title string, opts ...workOpt) string {
	t.Helper()
	m := map[string]any{
		"id":       id,
		"title":    title,
		"authors":  []string{"jane-doe"},
		"language": "en",
		"license":  "CC0-1.0",
		"added_at": "2026-01-01",
		"sources":  []map[string]string{{"type": "libex-import", "ref": id, "imported_at": "2026-01-01"}},
	}
	for _, o := range opts {
		o(m)
	}
	return mustJSON(t, m)
}

type recOpt func(map[string]any)

func withRuntime(min int) recOpt {
	return func(m map[string]any) { m["runtime_min"] = min }
}

func withNarrators(ids ...string) recOpt {
	return func(m map[string]any) { m["narrators"] = ids }
}

func withASIN(asin string) recOpt {
	return func(m map[string]any) { m["asin"] = []map[string]string{{"region": "us", "asin": asin}} }
}

func withISBN(isbn string) recOpt {
	return func(m map[string]any) { m["isbn"] = []string{isbn} }
}

func withAbridged(v bool) recOpt {
	return func(m map[string]any) { m["abridged"] = v }
}

func withoutPublisher() recOpt {
	return func(m map[string]any) { delete(m, "publisher") }
}

func recJSON(t testing.TB, id, work string, opts ...recOpt) string {
	t.Helper()
	m := map[string]any{
		"id":        id,
		"work":      work,
		"narrators": []string{"nate-narrator"},
		"language":  "en",
		"license":   "CC0-1.0",
		"added_at":  "2026-01-01",
		"asin":      []map[string]string{{"region": "us", "asin": fixtureASIN(work + "/" + id)}},
		// The ref names the record it came from, so two recordings of one production
		// carry DIFFERENT provenance - which is what makes a source union observable.
		"sources":      []map[string]string{{"type": "libex-import", "ref": work + "/" + id, "imported_at": "2026-01-01"}},
		"publisher":    "Fixture Audio",
		"release_date": "2020-01-01",
	}
	for _, o := range opts {
		o(m)
	}
	return mustJSON(t, m)
}

// fixtureASIN derives a unique, schema-valid ASIN from a string. pkg/check enforces
// global ASIN uniqueness, so two fixture recordings sharing one would be refused
// before any detector ran.
func fixtureASIN(seed string) string {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(seed); i++ {
		h = (h ^ uint64(seed[i])) * 1099511628211
	}
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	out := []byte("B000000000")
	for i := 1; i < len(out); i++ {
		out[i] = alphabet[h%uint64(len(alphabet))]
		h /= uint64(len(alphabet))
	}
	return string(out)
}

func personJSON(t testing.TB, id, name string) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"id":      id,
		"name":    name,
		"license": "CC0-1.0",
		"sources": []map[string]string{{"type": "libex-import", "imported_at": "2026-01-01"}},
	})
}

// seriesJSON renders a series over "<work>@<position>" pairs.
func seriesJSON(t testing.TB, id, name string, members ...string) string {
	t.Helper()
	works := make([]map[string]string, 0, len(members))
	for _, m := range members {
		work, pos := cut(t, m)
		works = append(works, map[string]string{"work": work, "position": pos})
	}
	return mustJSON(t, map[string]any{
		"id":      id,
		"name":    name,
		"works":   works,
		"license": "CC0-1.0",
		"sources": []map[string]string{{"type": "libex-import", "imported_at": "2026-01-01"}},
	})
}

func cut(t testing.TB, member string) (work, pos string) {
	t.Helper()
	for i := len(member) - 1; i >= 0; i-- {
		if member[i] == '@' {
			return member[:i], member[i+1:]
		}
	}
	t.Fatalf("seriesJSON: %q is not <work>@<position>", member)
	return "", ""
}

// charactersJSON and recapsJSON render the two works-community members. They are the
// most expensive data in the repository, which is why so much of this package is about
// not losing them.
func charactersJSON(t testing.TB, work, charID string) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"work": work,
		"characters": []map[string]any{{
			"id": charID, "name": "Someone", "reveal": map[string]int{"chapter": 1},
			"description": "A character, described in the community's own words.",
		}},
		"license": "CC-BY-SA-3.0",
		"sources": []map[string]string{{"type": "community", "imported_at": "2026-01-01"}},
	})
}

func recapsJSON(t testing.TB, work string) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"work": work,
		"recaps": []map[string]any{{
			"through": map[string]int{"chapter": 3},
			"text":    "The story so far, in the community's own words.",
		}},
		"license": "CC-BY-SA-3.0",
		"sources": []map[string]string{{"type": "community", "imported_at": "2026-01-01"}},
	})
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("fixture marshal: %v", err)
	}
	return string(b)
}

// hammeredCluster is the calibration pair, reduced to a fixture: one work modeled as
// volume 3 of its series with a clean title, and a second record of the same book
// whose retailer title spells the series and the volume out. The audit clusters them
// and proposes a non-advisory merge onto the modeled one.
//
// Both records carry a recording under the SAME key, which is what a real cluster
// looks like (a recording slug is derived from its narrator and year) and what makes
// the collision branches reachable. recOpts customize the LOSER's recording, which is
// the only thing the collision turns on.
func hammeredCluster(t testing.TB, recOpts ...recOpt) map[string]string {
	t.Helper()
	loser := append([]recOpt{withRuntime(600), withASIN("B0LOSER001")}, recOpts...)
	return map[string]string{
		"works/ha/hammered/work.json": workJSON(t, "hammered", "Hammered", withGenres("fantasy")),
		"works/ha/hammered/recordings/luke-daniels-2011.json": recJSON(t, "luke-daniels-2011", "hammered",
			withNarrators("luke-daniels"), withRuntime(576), withASIN("B0KEEPER01")),
		"works/ha/hammered-book-3/work.json": workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3",
			withGenres("action-adventure"), withSubtitle("An Iron Druid Adventure")),
		"works/ha/hammered-book-3/recordings/luke-daniels-2011.json": recJSON(t, "luke-daniels-2011", "hammered-book-3",
			append([]recOpt{withNarrators("luke-daniels")}, loser...)...),
		"people/ja/jane-doe.json":       personJSON(t, "jane-doe", "Jane Doe"),
		"people/lu/luke-daniels.json":   personJSON(t, "luke-daniels", "Luke Daniels"),
		"people/na/nate-narrator.json":  personJSON(t, "nate-narrator", "Nate Narrator"),
		"people/ot/other-narrator.json": personJSON(t, "other-narrator", "Other Narrator"),
		"series/dr/druid-tales.json":    seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3"),
	}
}

// mergeFinding is the merge-works proposal the audit makes for a cluster, as a literal.
// The planners are what the fresh-audit gate protects, so the refusal branches a real
// detector vetoes (a position conflict) are reached through one of these.
func mergeFinding(target string, losers ...string) audit.Finding {
	works := []audit.WorkRef{{ID: target, Title: "Hammered", Cleaned: "Hammered"}}
	for _, l := range losers {
		works = append(works, audit.WorkRef{ID: l, Title: "Hammered", Cleaned: "Hammered"})
	}
	return audit.Finding{
		Class: audit.ClassWorkDup, Subclass: "title-author", Key: "hammered#" + target,
		Works:   works,
		Propose: audit.Proposal{Op: audit.OpMergeWorks, Target: target, Others: losers},
	}
}

// assertRefusal checks that err is a refusal of the given category whose reason names
// what a human has to know.
func assertRefusal(t testing.TB, err error, category, mentions string) {
	t.Helper()
	var ref *refusal
	if !errors.As(err, &ref) {
		t.Fatalf("error = %v, want a refusal", err)
	}
	if ref.category != category {
		t.Errorf("category = %q, want %q (reason: %s)", ref.category, category, ref.reason)
	}
	if !strings.Contains(ref.reason, mentions) {
		t.Errorf("reason = %q, want it to mention %q", ref.reason, mentions)
	}
}

// seedTree writes a fixture tree and asserts it validates, so a test that then plans
// over it is testing the repair rather than the refusal a bad fixture would earn.
func seedTree(t testing.TB, files map[string]string) string {
	t.Helper()
	data := seedTreeAllowingProblems(t, files)
	if res := check.Load(data); len(res.Problems) > 0 {
		t.Fatalf("fixture does not validate: %v", res.Problems)
	}
	return data
}

// seedTreeAllowingProblems seeds a tree that deliberately does NOT validate. Exactly
// one class of fixture needs it: F-HYGIENE reports a missing required field, so the
// only tree that produces one is a tree the schema rejects - which is also why --write
// refuses such a tree and those tests are dry runs.
func seedTreeAllowingProblems(t testing.TB, files map[string]string) string {
	t.Helper()
	data := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	testpack.Seed(t, data, files)
	return data
}

// run plans (and with Write applies) a repair over a fixture, failing the test on an
// error. Every test goes through the real Run, so the fresh-audit gate is never
// bypassed.
func run(t testing.TB, opts Options) *Report {
	t.Helper()
	rep, err := Run(opts)
	if err != nil {
		t.Fatalf("repair.Run: %v", err)
	}
	return rep
}

// planFixture opens a plan over a fixture WITHOUT the fresh-audit gate, for the
// refusal branches a real detector cannot be made to propose (the audit vetoes a
// position conflict, and no detector states a value for a missing field). The planners
// themselves are what the gate protects, so exercising them directly is what proves
// the refusal exists rather than being unreachable.
func planFixture(t testing.TB, data string) (*runner, *txn) {
	t.Helper()
	store, err := pack.OpenFor(data, writeFamilies...)
	if err != nil {
		t.Fatal(err)
	}
	res := check.LoadStore(store)
	if res.Catalog == nil {
		t.Fatalf("fixture could not be loaded: %v", res.Problems)
	}
	table, err := redirects.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	rn := &runner{opts: Options{DataDir: data}, plan: newPlan(store, res.Catalog, table), rep: &Report{DataDir: data}, filter: &filter{}}
	return rn, rn.plan.begin()
}

// workEntry reads a work's composite entry off the tree.
func workEntry(t testing.TB, data, slug string) entry {
	t.Helper()
	return readEntry(t, data, pack.FamilyWorks, slug)
}

func readEntry(t testing.TB, data string, f pack.Family, slug string) entry {
	t.Helper()
	store, err := pack.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok, err := store.Get(f, slug)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("no %s entry %q", f.Root(), slug)
	}
	e, err := decodeEntry(raw)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func entryExists(t testing.TB, data string, f pack.Family, slug string) bool {
	t.Helper()
	store, err := pack.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.Has(f, slug)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// census counts what a merge must never lose. The sidecar entries are counted by
// their own ids and positions rather than by the work they hang off: a merge MOVES
// them, so keying by the work would report a moved sidecar as a lost one.
type census struct {
	recordings int
	asins      map[string]bool
	isbns      map[string]bool
	characters map[string]bool
	recaps     map[int]bool
}

func takeCensus(t testing.TB, data string) census {
	t.Helper()
	res := check.Load(data)
	if res.Catalog == nil {
		t.Fatalf("census load failed: %v", res.Problems)
	}
	c := census{
		asins: map[string]bool{}, isbns: map[string]bool{},
		characters: map[string]bool{}, recaps: map[int]bool{},
	}
	for _, w := range res.Catalog.Works {
		for _, r := range w.Recordings {
			c.recordings++
			for _, a := range r.ASIN {
				c.asins[a.ASIN] = true
			}
			for _, i := range r.ISBN {
				c.isbns[i.ISBN] = true
			}
		}
	}
	for _, ch := range res.Catalog.Characters {
		for _, e := range ch.Characters {
			c.characters[e.ID] = true
		}
	}
	for _, rc := range res.Catalog.Recaps {
		for _, e := range rc.Recaps {
			c.recaps[e.Through.Chapter] = true
		}
	}
	return c
}

// gitRepo makes a fixture tree a committed git repository, which is what --write
// requires. It returns the data directory inside it.
func gitRepo(t testing.TB, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	testpack.Seed(t, data, files)
	if res := check.Load(data); len(res.Problems) > 0 {
		t.Fatalf("fixture does not validate: %v", res.Problems)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "fixture@example.com"},
		{"config", "user.name", "Fixture"},
		{"config", "commit.gpgsign", "false"},
		{"add", "-A"},
		{"commit", "-q", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return data
}

// loadRedirects reads the tombstone table off a tree.
func loadRedirects(t testing.TB, data string) model.Redirects {
	t.Helper()
	table, err := redirects.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return table
}
