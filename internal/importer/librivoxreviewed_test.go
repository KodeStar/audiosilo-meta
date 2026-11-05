package importer

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// reviewedOpts is a run under these decisions alone.
func reviewedOpts(rs ...LibrivoxReview) Options {
	table := make(map[int]LibrivoxReview, len(rs))
	for _, r := range rs {
		if r.Reason == "" {
			r.Reason = "r"
		}
		table[r.Project] = r
	}
	return Options{LibrivoxReviewed: table}
}

func reviewAttach(id int, title, work string) LibrivoxReview {
	return LibrivoxReview{Project: id, Title: title, Decision: LibrivoxReviewAttach, Work: work}
}

func reviewOwn(id int, title string) LibrivoxReview {
	return LibrivoxReview{Project: id, Title: title, Decision: LibrivoxReviewOwnWork}
}

// seedMetamorphosis is a tree holding the catalogued "Metamorphosis" by Franz Kafka, the
// author of the fixture project retitled() renames, dated as LibriVox dates him so no
// other title is held as a same-name question.
func seedMetamorphosis(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	seedDatedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}}, kafkaLife)
	return dataDir
}

var kafkaLife = map[string][2]int{"franz-kafka": {1883, 1924}}

func warnedAbout(sum Summary, want string) bool {
	return strings.Contains(strings.Join(sum.Warnings, "\n"), want)
}

func worklistReasons(sum Summary) []string {
	var out []string
	for _, e := range sum.LibrivoxWorklist {
		out = append(out, e.Reason)
	}
	return out
}

// The embedded decisions parse: a failure here is what would panic on the first run.
func TestLibrivoxReviewedParses(t *testing.T) {
	if _, err := parseLibrivoxReviewed(librivoxReviewedJSON); err != nil {
		t.Fatal(err)
	}
}

func TestParseLibrivoxReviewedRefuses(t *testing.T) {
	const own = `{"project":1,"title":"T","decision":"own-work","reason":"r"}`
	for name, tc := range map[string]struct{ raw, want string }{
		"null":               {`null`, "expected a list"},
		"trailing content":   {`[` + own + `] []`, "after the list"},
		"repeated key":       {`[{"project":1,"project":2,"title":"T","decision":"own-work","reason":"r"}]`, "project"},
		"unknown key":        {`[{"project":1,"title":"T","decision":"own-work","reason":"r","x":"y"}]`, "unknown field"},
		"no title":           {`[{"project":1,"decision":"own-work","reason":"r"}]`, "no title"},
		"unknown decision":   {`[{"project":1,"title":"T","decision":"merge","reason":"r"}]`, "unknown decision"},
		"attach names none":  {`[{"project":1,"title":"T","decision":"attach","reason":"r"}]`, "names the work"},
		"attach a non-slug":  {`[{"project":1,"title":"T","decision":"attach","work":"Not A Slug","reason":"r"}]`, "names the work"},
		"own-work names one": {`[{"project":1,"title":"T","decision":"own-work","work":"w","reason":"r"}]`, "names no work"},
		"own-work moves":     {`[{"project":1,"title":"T","decision":"own-work","moves":{"a":"b"},"reason":"r"}]`, "names no work"},
		"self move":          {`[{"project":1,"title":"T","decision":"attach","work":"w","moves":{"a":"a"},"reason":"r"}]`, "two different"},
		"no project":         {`[{"title":"T","decision":"own-work","reason":"r"}]`, "no project"},
		"no reason":          {`[{"project":1,"title":"T","decision":"own-work"}]`, "one-line"},
		"em dash":            {`[{"project":1,"title":"T","decision":"own-work","reason":"a — b"}]`, "hyphens"},
		"out of order": {`[{"project":2,"title":"T","decision":"own-work","reason":"r"},` + own + `]`,
			"sorted"},
		"repeated": {`[` + own + `,` + own + `]`, "sorted"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseLibrivoxReviewed([]byte(tc.raw)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// An attach lands where the decision says, past the qualified-twin rule that would
// otherwise send the row to the worklist, and a rerun holds the project by its id.
func TestLibrivoxReviewedAttach(t *testing.T) {
	const title = "Metamorphosis (A Novella)"
	dataDir := seedMetamorphosis(t)
	snap := writeSnapshot(t, retitled(t, 99700, title))

	undecided := runLibrivoxWith(t, snap, dataDir, allLibrivox, Options{DryRun: true, LibrivoxReviewed: map[int]LibrivoxReview{}})
	if len(undecided.LibrivoxWorklist) != 1 {
		t.Fatalf("without a decision the row is a worklist row, got %v", undecided.LibrivoxWorklist)
	}

	opts := reviewedOpts(reviewAttach(99700, title, "metamorphosis"))
	sum := runLibrivoxWith(t, snap, dataDir, allLibrivox, opts)
	if sum.NewWorks != 0 || sum.NewRecordings != 1 || sum.LibrivoxReviewedPlaced != 1 || len(sum.LibrivoxWorklist) != 0 {
		t.Fatalf("works/recordings/placed/worklist = %d/%d/%d/%v\nwarnings: %v", sum.NewWorks, sum.NewRecordings,
			sum.LibrivoxReviewedPlaced, sum.LibrivoxWorklist, sum.Warnings)
	}
	if !strings.Contains(strings.Join(sum.Notes, "\n"), "1 LibriVox project(s) placed by a reviewed decision") {
		t.Errorf("notes = %v", sum.Notes)
	}
	again := runLibrivoxWith(t, snap, dataDir, allLibrivox, opts)
	if again.NewRecordings != 0 || again.LibrivoxReviewedPlaced != 0 || len(again.LibrivoxWorklist) != 0 {
		t.Errorf("a rerun changed something: recordings/placed/worklist = %d/%d/%v", again.NewRecordings,
			again.LibrivoxReviewedPlaced, again.LibrivoxWorklist)
	}
}

// A tombstoned target resolves to its survivor and shows in the retired-slug note, so a
// stale decision is visible.
func TestLibrivoxReviewedAttachFollowsTombstone(t *testing.T) {
	const title = "Metamorphosis (A Novella)"
	dataDir := seedMetamorphosis(t)
	reds := model.Redirects{model.RedirectWorks: {"metamorphosis-old": "metamorphosis"}}
	if err := redirects.Write(dataDir, reds); err != nil {
		t.Fatalf("write redirects: %v", err)
	}
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99701, title)), dataDir, allLibrivox,
		reviewedOpts(reviewAttach(99701, title, "metamorphosis-old")))
	if sum.NewWorks != 0 || sum.NewRecordings != 1 || sum.LibrivoxReviewedPlaced != 1 {
		t.Fatalf("works/recordings/placed = %d/%d/%d\nwarnings: %v", sum.NewWorks, sum.NewRecordings, sum.LibrivoxReviewedPlaced, sum.Warnings)
	}
	if !strings.Contains(strings.Join(sum.Notes, "\n"), "metamorphosis-old") {
		t.Errorf("the retired slug is not in the notes: %v", sum.Notes)
	}
}

// An attach naming no live work never mints, not even a row the rules would otherwise
// create because nothing shares its title.
func TestLibrivoxReviewedAttachToNothing(t *testing.T) {
	const title = "A Book With No Twin"
	dataDir := seedMetamorphosis(t)
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99702, title)), dataDir, allLibrivox,
		reviewedOpts(reviewAttach(99702, title, "gone")))
	if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.LibrivoxReviewedPlaced != 0 {
		t.Fatalf("works/recordings/placed = %d/%d/%d", sum.NewWorks, sum.NewRecordings, sum.LibrivoxReviewedPlaced)
	}
	if got := worklistReasons(sum); !slices.Equal(got, []string{LibrivoxReviewedWorkGone}) {
		t.Errorf("worklist reasons = %v", got)
	}
	if !warnedAbout(sum, `attaches it to "gone"`) {
		t.Errorf("no warning names the decision: %v", sum.Warnings)
	}
}

// An attach overrides the title and author rules, not the language: a work is
// language-scoped, so a decision naming another language's work is never placed.
func TestLibrivoxReviewedAttachInAnotherLanguage(t *testing.T) {
	const title = "Metamorphosis"
	dataDir := seedMetamorphosis(t)
	row := retitled(t, 99719, title)
	row.Language = "German"
	sum := runLibrivoxWith(t, writeSnapshot(t, row), dataDir, allLibrivox, reviewedOpts(reviewAttach(99719, title, "metamorphosis")))
	if sum.NewWorks != 0 || sum.NewRecordings != 0 {
		t.Fatalf("works/recordings = %d/%d", sum.NewWorks, sum.NewRecordings)
	}
	if got := worklistReasons(sum); !slices.Equal(got, []string{LibrivoxReviewedOtherLanguage}) {
		t.Errorf("worklist reasons = %v", got)
	}
}

// LibriVox reuses project numbers: a decision about another title is not this row's.
func TestLibrivoxReviewedTitleChanged(t *testing.T) {
	dataDir := seedMetamorphosis(t)
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99710, "A New Book On An Old Number")), dataDir, allLibrivox,
		reviewedOpts(reviewAttach(99710, "Metamorphosis (A Novella)", "metamorphosis")))
	if sum.NewWorks != 0 || sum.NewRecordings != 0 {
		t.Fatalf("works/recordings = %d/%d", sum.NewWorks, sum.NewRecordings)
	}
	if got := worklistReasons(sum); !slices.Equal(got, []string{LibrivoxReviewedTitleChanged}) {
		t.Errorf("worklist reasons = %v", got)
	}
	// The key is what is compared: LibriVox's repeat markers and an article are no change.
	same := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99711, "The Metamorphosis, Version 2")), dataDir, allLibrivox,
		reviewedOpts(reviewAttach(99711, "Metamorphosis", "metamorphosis")))
	if same.LibrivoxReviewedPlaced != 1 || len(same.LibrivoxWorklist) != 0 {
		t.Errorf("placed/worklist = %d/%v", same.LibrivoxReviewedPlaced, same.LibrivoxWorklist)
	}
}

// A rebuild holds nothing yet, and an attach may name a work a LATER project creates: it
// reaches the same work whichever order the projects arrive in.
func TestLibrivoxReviewedAttachWaitsForTheWorkItNames(t *testing.T) {
	const qualified = "Later Work (A Novella)"
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{})
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99712, qualified), retitled(t, 99713, "Later Work")), dataDir, allLibrivox,
		reviewedOpts(reviewAttach(99712, qualified, "later-work")))
	if sum.NewWorks != 1 || sum.NewRecordings != 2 || sum.LibrivoxReviewedPlaced != 1 || len(sum.LibrivoxWorklist) != 0 {
		t.Fatalf("works/recordings/placed/worklist = %d/%d/%d/%v\nwarnings: %v", sum.NewWorks, sum.NewRecordings,
			sum.LibrivoxReviewedPlaced, sum.LibrivoxWorklist, sum.Warnings)
	}
	if !entryExists(t, dataDir, workAddr("later-work")) {
		t.Errorf("the work is not at later-work")
	}
}

// An own-work mints on its own title chain past the guard that refused it, and a second
// project of the same title meets the first through the ordinary walk.
func TestLibrivoxReviewedOwnWork(t *testing.T) {
	dataDir := seedMetamorphosis(t)
	sum := runLibrivoxWith(t, writeSnapshot(t,
		retitled(t, 99703, "Metamorphosis (Wyllie Translation)"),
		retitled(t, 99704, "Metamorphosis (Wyllie Translation Version 2)"),
	), dataDir, allLibrivox, reviewedOpts(
		reviewOwn(99703, "Metamorphosis (Wyllie Translation)"),
		reviewOwn(99704, "Metamorphosis (Wyllie Translation Version 2)"),
	))
	if sum.NewWorks != 1 || sum.NewRecordings != 2 || sum.LibrivoxReviewedPlaced != 2 || len(sum.LibrivoxWorklist) != 0 {
		t.Fatalf("works/recordings/placed/worklist = %d/%d/%d/%v\nwarnings: %v", sum.NewWorks, sum.NewRecordings,
			sum.LibrivoxReviewedPlaced, sum.LibrivoxWorklist, sum.Warnings)
	}
	if !entryExists(t, dataDir, workAddr("metamorphosis-wyllie-translation")) {
		t.Errorf("the own work is not at metamorphosis-wyllie-translation")
	}
	if warnedAbout(sum, "walked onto") {
		t.Errorf("the second reading of one own-work text warned: %v", sum.Warnings)
	}
}

// An own-work whose walk reaches a work it did not found is never placed there: that may
// be the very work it was ruled apart from, and the project would stay on it.
func TestLibrivoxReviewedOwnWorkWalkingOntoAWorkIsRefused(t *testing.T) {
	const title = "Metamorphosis"
	dataDir := seedMetamorphosis(t)
	before := snapshotTree(t, dataDir)
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99714, title)), dataDir, allLibrivox,
		reviewedOpts(reviewOwn(99714, title)))
	if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.LibrivoxReviewedPlaced != 0 {
		t.Fatalf("works/recordings/placed = %d/%d/%d", sum.NewWorks, sum.NewRecordings, sum.LibrivoxReviewedPlaced)
	}
	if got := worklistReasons(sum); !slices.Equal(got, []string{LibrivoxReviewedOwnWorkTaken}) {
		t.Fatalf("worklist reasons = %v", got)
	}
	if got := sum.LibrivoxWorklist[0].Candidates; len(got) != 1 || got[0].Work != "metamorphosis" {
		t.Errorf("candidates = %v", got)
	}
	if !warnedAbout(sum, `walks onto the existing work "metamorphosis"`) || warnedAbout(sum, "refused before routing") {
		t.Errorf("warnings: %v", sum.Warnings)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, dataDir)) {
		t.Error("the refused row changed the tree: it left a person behind")
	}
}

// A second own-work of one title with another translator is another text: the translator
// is role-credited, so the walk reaches the first's home, and the row is not placed on it.
func TestLibrivoxReviewedOwnWorkMeetsOnlyItsOwnText(t *testing.T) {
	const title = "Metamorphosis (Some Translation)"
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{})
	other := retitled(t, 99724, title)
	other.Translators = []LibrivoxPerson{lvDated(9924, "Willa", "Muir", "1890", "1970")}
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99723, title), other), dataDir, allLibrivox,
		reviewedOpts(reviewOwn(99723, title), reviewOwn(99724, title)))
	if sum.NewWorks != 1 || sum.NewRecordings != 1 || sum.LibrivoxReviewedPlaced != 1 {
		t.Fatalf("works/recordings/placed = %d/%d/%d\nwarnings: %v", sum.NewWorks, sum.NewRecordings, sum.LibrivoxReviewedPlaced, sum.Warnings)
	}
	if got := worklistReasons(sum); !slices.Equal(got, []string{LibrivoxReviewedOwnWorkTaken}) {
		t.Errorf("worklist reasons = %v", got)
	}
}

// A non-Latin own-work has no chain to walk, and is counted like any other.
func TestLibrivoxReviewedOwnWorkNonLatin(t *testing.T) {
	const title = "Война и мир"
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{})
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99715, title)), dataDir, allLibrivox, reviewedOpts(reviewOwn(99715, title)))
	if sum.LibrivoxReviewedPlaced != 1 || !entryExists(t, dataDir, workAddr("librivox-99715")) {
		t.Errorf("placed = %d\nwarnings: %v", sum.LibrivoxReviewedPlaced, sum.Warnings)
	}
}

// Rule 1's pre-pass never pins a decided row's author id onto rule 1's person: the row's
// own-work would then walk onto the very work it was ruled apart from.
func TestLibrivoxReviewedOwnWorkIsNotKeyedOntoRuleOnesPerson(t *testing.T) {
	dataDir := t.TempDir()
	seedDatedWorks(t, dataDir, map[string][]string{"quiet-book": {"Quiet Book", "henrietta-marshall"}},
		map[string][2]int{"henrietta-marshall": {1867, 1941}})
	row := byAuthor(t, 99716, "Quiet Book", "English", lvDated(9916, "Henrietta Elizabeth", "Marshall", "1867", "1941"))
	sum := runLibrivoxWith(t, writeSnapshot(t, row), dataDir, allLibrivox, reviewedOpts(reviewOwn(99716, "Quiet Book")))
	if sum.NewWorks != 1 || warnedAbout(sum, "walked onto") {
		t.Fatalf("new works = %d\nwarnings: %v", sum.NewWorks, sum.Warnings)
	}
	if got := workAuthorsOf(t, dataDir, "quiet-book"); !slices.Equal(got, []string{"henrietta-marshall"}) {
		t.Errorf("the catalogued work's authors = %v", got)
	}
}

// An own-work beside its twin leaves the guard armed: a later decorated spelling is
// refused in favour of the twin, in one run, in a later run reading the tree, and from
// another source. Before the set-apart, two candidates read as ambiguous and all three
// minted.
func TestLibrivoxReviewedOwnWorkLeavesTheGuardArmed(t *testing.T) {
	const own = "Metamorphosis (Wyllie Translation)"
	const later = "Metamorphosis [Classic Tales Edition]"
	opts := reviewedOpts(reviewOwn(99717, own))

	t.Run("one run", func(t *testing.T) {
		dataDir := seedMetamorphosis(t)
		sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99717, own), retitled(t, 99718, later)), dataDir, allLibrivox, opts)
		if sum.NewWorks != 1 || sum.SkippedDuplicateIdentity != 1 {
			t.Fatalf("works/refused = %d/%d\nwarnings: %v", sum.NewWorks, sum.SkippedDuplicateIdentity, sum.Warnings)
		}
		if got := sum.LibrivoxWorklist[0].Candidates[0].Work; got != "metamorphosis" {
			t.Errorf("refused in favour of %q", got)
		}
	})
	t.Run("a later run", func(t *testing.T) {
		dataDir := seedMetamorphosis(t)
		runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99717, own)), dataDir, allLibrivox, opts)
		sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99718, later)), dataDir, allLibrivox, opts)
		if sum.NewWorks != 0 || sum.SkippedDuplicateIdentity != 1 {
			t.Fatalf("works/refused = %d/%d\nwarnings: %v", sum.NewWorks, sum.SkippedDuplicateIdentity, sum.Warnings)
		}
	})
	t.Run("another source", func(t *testing.T) {
		dataDir := seedMetamorphosis(t)
		runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99717, own)), dataDir, allLibrivox, opts)
		sum := runLibexWith(t, dataDir, opts, tombRow("B0TOMBS099", later, "Franz Kafka", "Bea Reader", 150, "", ""))
		if sum.NewWorks != 0 || sum.SkippedDuplicateIdentity != 1 {
			t.Fatalf("works/refused = %d/%d\nwarnings: %v", sum.NewWorks, sum.SkippedDuplicateIdentity, sum.Warnings)
		}
	})
	t.Run("an own-work alone still answers its key", func(t *testing.T) {
		dataDir := t.TempDir()
		seedWorks(t, dataDir, map[string][]string{})
		sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99717, own), retitled(t, 99718, later)), dataDir, allLibrivox, opts)
		if sum.NewWorks != 1 || sum.SkippedDuplicateIdentity != 1 {
			t.Fatalf("works/refused = %d/%d\nwarnings: %v", sum.NewWorks, sum.SkippedDuplicateIdentity, sum.Warnings)
		}
	})
}

// Set apart is the refusal's answer alone. The paths that PLACE a recording on the one
// candidate keep reading an own-work beside its twin as ambiguous, since the row may be
// that very text: it was refused before the decision, and is after it.
func TestLibrivoxReviewedOwnWorkKeepsThePlacingPathsAmbiguous(t *testing.T) {
	opts := reviewedOpts(reviewOwn(99720, "any"))
	t.Run("attach editions", func(t *testing.T) {
		dataDir := seedAttachCatalogue(t)
		const home = "the-lost-coast-pope-translation"
		seedTree(t, dataDir, map[string]string{
			workAddr(home): testpack.WorkJSON(t, home, "The Lost Coast (Pope Translation)", testpack.WithAuthors("ada-mapmaker")),
			recAddr(home, "bea-reader"): testpack.RecJSON(t, "bea-reader", home, testpack.WithNarrators("bea-reader"), func(m map[string]any) {
				m["librivox_id"] = 99720
				delete(m, "asin")
			}),
		})
		opts := opts
		opts.ExistingSeriesOnly, opts.AttachEditions = true, true
		sum := runLibexWith(t, dataDir, opts,
			attachExportRow("B0ATTACH77", "The Lost Coast [Classic Edition]", "us", "english", "Ada Mapmaker", "Cal Voice", "1"))
		if sum.Attached != 0 || sum.NewRecordings != 0 {
			t.Errorf("attached/recordings = %d/%d: the row merged past the ambiguity veto\nwarnings: %v", sum.Attached, sum.NewRecordings, sum.Warnings)
		}
	})
	t.Run("relocation", func(t *testing.T) {
		dir := relocationFixture(t, true)
		seedTree(t, dir, map[string]string{
			"works/da/das-buch-pope-translation/work.json": `{"authors":["anne-author"],"id":"das-buch-pope-translation","language":"de",` +
				`"license":"CC0-1.0","sources":[{"type":"user"}],"title":"Das Buch (Pope Translation)"}`,
			"works/da/das-buch-pope-translation/recordings/other.json": `{"id":"other","language":"de","librivox_id":99720,"license":"CC0-1.0",` +
				`"narrators":["english-reader"],"sources":[{"type":"user"}],"work":"das-buch-pope-translation"}`,
		})
		opts := opts
		opts.DataDir, opts.ImportDate, opts.Mode = dir, testImportDate, ModeRelocate
		sum, err := RunLibex(writeBooks(t, relocateRow), opts)
		if err != nil {
			t.Fatal(err)
		}
		if sum.RelocationRefusals[RefusalRelocateAmbiguousHome] != 1 || len(sum.Relocations) != 0 {
			t.Errorf("refusals/relocations = %v/%v", sum.RelocationRefusals, sum.Relocations)
		}
	})
}

// LibriVox reuses project numbers: a home is the work holding the project the decision is
// about, not whatever now sits under that number. A recording that states no project
// title is read by its work's.
func TestLibrivoxOwnWorkHomesRequireTheDecisionsTitle(t *testing.T) {
	table := map[int]LibrivoxReview{
		1: reviewOwn(1, "Iliad (Pope Translation)"),
		2: reviewOwn(2, "Iliad (Pope Translation)"),
		3: reviewOwn(3, "Odyssey (Pope Translation, Version 2)"),
		4: reviewAttach(4, "Iliad", "the-iliad"),
	}
	rec := func(id int, title string) []*model.Recording {
		return []*model.Recording{{LibrivoxID: id, LibrivoxTitle: title}}
	}
	got := librivoxOwnWorkHomes([]*model.Work{
		{ID: "held", Title: "x", Recordings: rec(1, "Iliad (Pope Translation)")},
		{ID: "reused-number", Title: "x", Recordings: rec(2, "A Book On The Reused Number")},
		{ID: "no-project-title", Title: "Odyssey (Pope Translation)", Recordings: rec(3, "")},
		{ID: "an-attach", Title: "x", Recordings: rec(4, "Iliad")},
		{ID: "undecided", Title: "x", Recordings: rec(5, "Iliad (Pope Translation)")},
		{ID: "retail", Title: "Iliad (Pope Translation)", Recordings: rec(0, "")},
	}, table)
	want := map[string]bool{"held": true, "no-project-title": true}
	if !maps.Equal(got, want) {
		t.Errorf("homes = %v, want %v", got, want)
	}
}

// A reviewed move carries the credit, and with it the LibriVox id, onto the catalogued
// person a loose spelling is otherwise refused for.
func TestLibrivoxReviewedMoves(t *testing.T) {
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "f-kafka"}})
	rev := reviewAttach(99705, "Metamorphosis", "metamorphosis")
	rev.Moves = map[string]string{"franz-kafka": "f-kafka"}
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99705, "Metamorphosis")), dataDir, allLibrivox, reviewedOpts(rev))
	if sum.LibrivoxReviewedPlaced != 1 {
		t.Fatalf("placed = %d\nwarnings: %v", sum.LibrivoxReviewedPlaced, sum.Warnings)
	}
	if p := personOf(t, dataDir, "f-kafka"); p.AuthorID == 0 {
		t.Errorf("the moved credit's id is not on f-kafka: %+v", p)
	}
	if entryExists(t, dataDir, personAddr("franz-kafka")) {
		t.Errorf("the moved credit left a person behind")
	}
}

// A move is about the author id, so it keys the id for the whole batch: another project of
// the id, planned before the attach, credits the same person rather than minting the
// spelling the move is there to avoid.
func TestLibrivoxReviewedMoveKeysTheIDForTheBatch(t *testing.T) {
	const title = "Metamorphosis"
	dataDir := t.TempDir()
	seedDatedWorks(t, dataDir, map[string][]string{"metamorphosis": {title, "f-kafka"}}, map[string][2]int{"f-kafka": kafkaLife["franz-kafka"]})
	rev := reviewAttach(99721, title, "metamorphosis")
	rev.Moves = map[string]string{"franz-kafka": "f-kafka"}
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99721, title), retitled(t, 99722, "In the Penal Colony")), dataDir, allLibrivox, reviewedOpts(rev))
	if sum.LibrivoxReviewedPlaced != 1 || warnedAbout(sum, "kept there") {
		t.Fatalf("placed = %d\nwarnings: %v", sum.LibrivoxReviewedPlaced, sum.Warnings)
	}
	if entryExists(t, dataDir, personAddr("franz-kafka")) {
		t.Errorf("the other project minted the spelling the move avoids")
	}
	if p := personOf(t, dataDir, "f-kafka"); p.AuthorID == 0 {
		t.Errorf("the id is not on f-kafka: %+v", p)
	}
	if got := workAuthorsOf(t, dataDir, "in-the-penal-colony"); !slices.Contains(got, "f-kafka") {
		t.Errorf("the other project's authors = %v", got)
	}
}

// A move onto nobody is dropped, one off a record is kept, and one of nobody the row
// credits does nothing: each says so, since each leaves the decision's id unstamped.
func TestLibrivoxReviewedMovesThatCannotApplyWarn(t *testing.T) {
	const title = "Metamorphosis"
	t.Run("onto no live person", func(t *testing.T) {
		dataDir := t.TempDir()
		seedWorks(t, dataDir, map[string][]string{"metamorphosis": {title, "f-kafka"}})
		rev := reviewAttach(99706, title, "metamorphosis")
		rev.Moves = map[string]string{"franz-kafka": "nobody-at-all"}
		sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99706, title)), dataDir, allLibrivox, reviewedOpts(rev))
		if sum.LibrivoxReviewedPlaced != 1 || !warnedAbout(sum, `"nobody-at-all", which is no live person`) {
			t.Errorf("placed = %d\nwarnings: %v", sum.LibrivoxReviewedPlaced, sum.Warnings)
		}
	})
	t.Run("off a record of its own", func(t *testing.T) {
		dataDir := t.TempDir()
		seedDatedWorks(t, dataDir, map[string][]string{
			"metamorphosis": {title, "f-kafka"},
			"other-book":    {"Other Book", "franz-kafka"},
		}, kafkaLife)
		rev := reviewAttach(99707, title, "metamorphosis")
		rev.Moves = map[string]string{"franz-kafka": "f-kafka"}
		sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99707, title)), dataDir, allLibrivox, reviewedOpts(rev))
		if sum.LibrivoxReviewedPlaced != 1 || !warnedAbout(sum, "already a record of its own") {
			t.Errorf("placed = %d\nwarnings: %v", sum.LibrivoxReviewedPlaced, sum.Warnings)
		}
	})
	t.Run("of nobody the row credits", func(t *testing.T) {
		dataDir := t.TempDir()
		seedWorks(t, dataDir, map[string][]string{"metamorphosis": {title, "f-kafka"}})
		rev := reviewAttach(99709, title, "metamorphosis")
		rev.Moves = map[string]string{"frantz-kafka": "f-kafka"}
		sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99709, title)), dataDir, allLibrivox, reviewedOpts(rev))
		if sum.LibrivoxReviewedPlaced != 1 || !warnedAbout(sum, `moves "frantz-kafka", whom the project does not credit`) {
			t.Errorf("placed = %d\nwarnings: %v", sum.LibrivoxReviewedPlaced, sum.Warnings)
		}
	})
}

// A row refused before routing keeps its refusal, and the decision it shadows is named.
func TestLibrivoxReviewedShadowedDecisionWarns(t *testing.T) {
	const title = "Book By Nobody Sluggable"
	dataDir := seedMetamorphosis(t)
	row := byAuthor(t, 99708, title, "English", LibrivoxPerson{FirstName: "Йордан", LastName: "Йовков"})
	sum := runLibrivoxWith(t, writeSnapshot(t, row), dataDir, allLibrivox, reviewedOpts(reviewOwn(99708, title)))
	if got := worklistReasons(sum); !slices.Equal(got, []string{LibrivoxAuthorUnsluggable}) {
		t.Fatalf("worklist reasons = %v", got)
	}
	if !warnedAbout(sum, "refused before routing as "+LibrivoxAuthorUnsluggable) {
		t.Errorf("no warning: %v", sum.Warnings)
	}
}
