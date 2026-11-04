package importer

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// The embedded decisions parse, and every one names a project at most once: a failure
// here is what would otherwise panic on the first LibriVox run.
func TestLibrivoxReviewedParses(t *testing.T) {
	if _, err := parseLibrivoxReviewed(librivoxReviewedJSON); err != nil {
		t.Fatal(err)
	}
}

func TestParseLibrivoxReviewedRefuses(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"unknown key":        {`[{"project":1,"decision":"own-work","reason":"r","title":"x"}]`, "unknown field"},
		"unknown decision":   {`[{"project":1,"decision":"merge","reason":"r"}]`, "unknown decision"},
		"attach names none":  {`[{"project":1,"decision":"attach","reason":"r"}]`, "names the work"},
		"attach a non-slug":  {`[{"project":1,"decision":"attach","work":"Not A Slug","reason":"r"}]`, "names the work"},
		"own-work names one": {`[{"project":1,"decision":"own-work","work":"w","reason":"r"}]`, "names no work"},
		"own-work moves":     {`[{"project":1,"decision":"own-work","moves":{"a":"b"},"reason":"r"}]`, "names no work"},
		"self move":          {`[{"project":1,"decision":"attach","work":"w","moves":{"a":"a"},"reason":"r"}]`, "two different"},
		"no project":         {`[{"decision":"own-work","reason":"r"}]`, "no project"},
		"no reason":          {`[{"project":1,"decision":"own-work"}]`, "one-line"},
		"em dash":            {`[{"project":1,"decision":"own-work","reason":"a — b"}]`, "hyphens"},
		"out of order": {`[{"project":2,"decision":"own-work","reason":"r"},{"project":1,"decision":"own-work","reason":"r"}]`,
			"sorted"},
		"repeated": {`[{"project":1,"decision":"own-work","reason":"r"},{"project":1,"decision":"own-work","reason":"r"}]`,
			"sorted"},
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
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}})
	snap := writeSnapshot(t, retitled(t, 99700, "Metamorphosis (A Novella)"))

	undecided := t.TempDir()
	seedWorks(t, undecided, map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}})
	if sum := runLibrivox(t, snap, undecided, allLibrivox); len(sum.LibrivoxWorklist) != 1 {
		t.Fatalf("without a decision the row is a worklist row, got %v", sum.LibrivoxWorklist)
	}

	opts := Options{LibrivoxReviewed: map[int]LibrivoxReview{
		99700: {Project: 99700, Decision: LibrivoxReviewAttach, Work: "metamorphosis", Reason: "r"},
	}}
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

// A tombstoned target resolves to its survivor, as every other reader of a slug does.
func TestLibrivoxReviewedAttachFollowsTombstone(t *testing.T) {
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}})
	reds := model.Redirects{model.RedirectWorks: {"metamorphosis-old": "metamorphosis"}}
	if err := redirects.Write(dataDir, reds); err != nil {
		t.Fatalf("write redirects: %v", err)
	}
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99701, "Metamorphosis (A Novella)")), dataDir, allLibrivox,
		Options{LibrivoxReviewed: map[int]LibrivoxReview{
			99701: {Project: 99701, Decision: LibrivoxReviewAttach, Work: "metamorphosis-old", Reason: "r"},
		}})
	if sum.NewWorks != 0 || sum.NewRecordings != 1 || sum.LibrivoxReviewedPlaced != 1 {
		t.Fatalf("works/recordings/placed = %d/%d/%d\nwarnings: %v", sum.NewWorks, sum.NewRecordings, sum.LibrivoxReviewedPlaced, sum.Warnings)
	}
}

// An attach naming no live work never mints one: the row is the rules' again, and says so.
func TestLibrivoxReviewedAttachToNothing(t *testing.T) {
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}})
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99702, "Metamorphosis (A Novella)")), dataDir, allLibrivox,
		Options{LibrivoxReviewed: map[int]LibrivoxReview{
			99702: {Project: 99702, Decision: LibrivoxReviewAttach, Work: "gone", Reason: "r"},
		}})
	if sum.NewRecordings != 0 || sum.LibrivoxReviewedPlaced != 0 || len(sum.LibrivoxWorklist) != 1 {
		t.Fatalf("recordings/placed/worklist = %d/%d/%v", sum.NewRecordings, sum.LibrivoxReviewedPlaced, sum.LibrivoxWorklist)
	}
	if !strings.Contains(strings.Join(sum.Warnings, "\n"), `attaches it to "gone"`) {
		t.Errorf("no warning names the decision: %v", sum.Warnings)
	}
}

// An own-work mints on its own title chain past the guard that refused it, and a second
// project of the same title meets the first through the ordinary walk.
func TestLibrivoxReviewedOwnWork(t *testing.T) {
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}})
	sum := runLibrivoxWith(t, writeSnapshot(t,
		retitled(t, 99703, "Metamorphosis (Wyllie Translation)"),
		retitled(t, 99704, "Metamorphosis (Wyllie Translation Version 2)"),
	), dataDir, allLibrivox, Options{LibrivoxReviewed: map[int]LibrivoxReview{
		99703: {Project: 99703, Decision: LibrivoxReviewOwnWork, Reason: "r"},
		99704: {Project: 99704, Decision: LibrivoxReviewOwnWork, Reason: "r"},
	}})
	if sum.NewWorks != 1 || sum.NewRecordings != 2 || sum.LibrivoxReviewedPlaced != 2 || len(sum.LibrivoxWorklist) != 0 {
		t.Fatalf("works/recordings/placed/worklist = %d/%d/%d/%v\nwarnings: %v", sum.NewWorks, sum.NewRecordings,
			sum.LibrivoxReviewedPlaced, sum.LibrivoxWorklist, sum.Warnings)
	}
	if !entryExists(t, dataDir, workAddr("metamorphosis-wyllie-translation")) {
		t.Errorf("the own work is not at metamorphosis-wyllie-translation")
	}
}

// A reviewed move carries the credit, and with it the LibriVox id, onto the catalogued
// person a loose spelling is otherwise refused for - and only when the row's own
// person is no record: moving a record's id would be a person merge.
func TestLibrivoxReviewedMoves(t *testing.T) {
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "f-kafka"}})
	sum := runLibrivoxWith(t, writeSnapshot(t, retitled(t, 99705, "Metamorphosis")), dataDir, allLibrivox,
		Options{LibrivoxReviewed: map[int]LibrivoxReview{
			99705: {Project: 99705, Decision: LibrivoxReviewAttach, Work: "metamorphosis",
				Moves: map[string]string{"franz-kafka": "f-kafka"}, Reason: "r"},
		}})
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
