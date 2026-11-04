package importer

// librivoxreviewed.go is the LibriVox import's REVIEWED DECISIONS: one project id ->
// which work its recording lands on, decided by a person (or an agent working the
// queue) after reading the evidence the match rules cannot: the text source, the
// translator, the catalogued recordings. It answers the worklist. A worklist row is a
// question librivoxmatch.go refuses to guess; a decision in librivoxreviewed.json is
// the answer, and it is an INPUT of the importer rather than a hand edit of the tree,
// so rebuilding the mirror from the snapshot reaches the same work.
//
// Two decisions:
//
//   - attach: the recording lands on the named work, whatever the title and author
//     rules say (a retailer's "The Wonderful Wizard of Oz" crediting its adapters, a
//     title rule 5 will not read past "(Forsyte Saga Vol. 1)"). A tombstoned slug
//     resolves to its survivor. moves is the rule 1 move (librivoxauthors.go) a loose
//     spelling is refused by automatically, row person -> work person: only a credit
//     whose own person is no record moves (splitLibrivoxMoves), so the LibriVox id is
//     stamped on the catalogued person and a later project of that id finds them.
//   - own-work: the project is a book of its own (a separately named translation, a
//     volume of a set, an edition the title states, another author's same-named book).
//     Rules 2-5 and the duplicate-identity guard stand down and the ordinary walk
//     decides, so it mints on its own title chain ("Iliad (Pope Translation)" at
//     iliad-pope-translation) and a second reading of the same text meets the first.
//
// A person hold (librivoxSameNameSkip) is asked first and never answered here: it is
// settled by putting the author id on the right person record, which the importer
// already honours. An attach naming no live work warns and leaves the row to the rules,
// so a retired decision is a worklist row again and never a new work.

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The two decisions of a LibrivoxReview.
const (
	LibrivoxReviewAttach  = "attach"
	LibrivoxReviewOwnWork = "own-work"
)

// LibrivoxReview is one reviewed decision. See this file's header.
type LibrivoxReview struct {
	Project  int    `json:"project"`
	Decision string `json:"decision"`
	// Work is the work an attach lands on; empty on an own-work.
	Work string `json:"work,omitempty"`
	// Moves is an attach's row person -> work person (librivoxauthors.go rule 1).
	Moves  map[string]string `json:"moves,omitempty"`
	Reason string            `json:"reason"`
}

//go:embed librivoxreviewed.json
var librivoxReviewedJSON []byte

// librivoxReviewed is the embedded decisions by project id, parsed once. A parse
// failure is a programming error (the file is embedded and covered by a test), so it
// panics rather than sending every decided row back to the worklist.
var librivoxReviewed = sync.OnceValue(func() map[int]LibrivoxReview {
	m, err := parseLibrivoxReviewed(librivoxReviewedJSON)
	if err != nil {
		panic(fmt.Sprintf("importer: embedded librivoxreviewed.json is invalid: %v", err))
	}
	return m
})

// parseLibrivoxReviewed reads the decisions, refusing a file a reviewer could misread:
// an unknown key, projects out of order or repeated, a decision missing what it needs
// or stating what it may not, a reason that is not one trimmed line in hyphens.
func parseLibrivoxReviewed(raw []byte) (map[int]LibrivoxReview, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rs []LibrivoxReview
	if err := dec.Decode(&rs); err != nil {
		return nil, err
	}
	out := make(map[int]LibrivoxReview, len(rs))
	for i, r := range rs {
		if err := r.validate(); err != nil {
			return nil, fmt.Errorf("entry %d (project %d): %w", i, r.Project, err)
		}
		if i > 0 && cmp.Compare(rs[i-1].Project, r.Project) >= 0 {
			return nil, fmt.Errorf("entry %d (project %d): the list must be sorted by project with no duplicates", i, r.Project)
		}
		out[r.Project] = r
	}
	return out, nil
}

func (r LibrivoxReview) validate() error {
	if r.Project < 1 {
		return fmt.Errorf("no project id")
	}
	switch r.Decision {
	case LibrivoxReviewAttach:
		if !model.ValidSlug(r.Work) {
			return fmt.Errorf("an attach names the work it lands on, a slug: %q", r.Work)
		}
	case LibrivoxReviewOwnWork:
		if r.Work != "" || len(r.Moves) > 0 {
			return fmt.Errorf("an own-work names no work and moves no credit: the ordinary walk decides")
		}
	default:
		return fmt.Errorf("unknown decision %q", r.Decision)
	}
	for _, from := range slices.Sorted(maps.Keys(r.Moves)) {
		if !model.ValidSlug(from) || !model.ValidSlug(r.Moves[from]) || from == r.Moves[from] {
			return fmt.Errorf("the move %q -> %q is not two different person slugs", from, r.Moves[from])
		}
	}
	if r.Reason == "" || r.Reason != strings.TrimSpace(r.Reason) || strings.ContainsAny(r.Reason, "\n\r–—") {
		return fmt.Errorf("the reason must be a one-line, trimmed reason in hyphens")
	}
	return nil
}

// reviews is the run's reviewed decisions.
func (s *librivoxState) reviews() map[int]LibrivoxReview {
	if s.reviewed != nil {
		return s.reviewed
	}
	return librivoxReviewed()
}

// routeLibrivoxReviewed is the row's reviewed decision as a route; ok=false for a row
// none names, or an attach whose work is gone (warned, and left to the rules).
func (p *planner) routeLibrivoxReviewed(b sourceBook, workTitle string) (librivoxRoute, bool) {
	r, ok := p.lv.reviews()[b.lv.id]
	if !ok {
		return librivoxRoute{}, false
	}
	if r.Decision == LibrivoxReviewOwnWork {
		if _, nonLatin := librivoxTitleKeyOf(workTitle); nonLatin {
			return p.librivoxFallback(b, workTitle, true), true // such a title has no chain to walk (rule 4)
		}
		return librivoxRoute{ownWork: true, reviewed: true}, true
	}
	ws, _ := p.workAt(r.Work)
	if ws == nil {
		p.summary.Warnings = append(p.summary.Warnings, fmt.Sprintf(
			"librivox %d: the reviewed decision attaches it to %q, which is no live work; left to the match rules", b.lv.id, r.Work))
		return librivoxRoute{}, false
	}
	moves, _ := p.splitLibrivoxMoves(r.Moves)
	return librivoxRoute{walk: &workWalk{title: workTitle, ws: ws}, moves: moves, reviewed: true}, true
}

// reportLibrivoxReviewed is the run's one note about the rows a reviewed decision placed.
func (p *planner) reportLibrivoxReviewed() {
	if n := p.summary.LibrivoxReviewedPlaced; n > 0 {
		p.summary.Notes = append(p.summary.Notes,
			fmt.Sprintf("%d LibriVox project(s) placed by a reviewed decision (internal/importer/librivoxreviewed.json)", n))
	}
}
