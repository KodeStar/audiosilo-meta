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
//     A walk reaching any work that is not such a home (ownWorkHomeOf) goes to the
//     worklist (reviewed-own-work-taken): it may be the work the decision rejected.
//
// A decision answers only a row that reaches routing. A row refused before it - a
// person hold (librivoxSameNameSkip, settled by putting the author id on the right
// person record, which the importer already honours), a row left with no author
// (author-unsluggable, author-slot-taken), a reader or language refusal - keeps its
// refusal, and a worklist row shadowing a decision warns (noteLibrivoxWorklist).
//
// Never a guess: every decision states the project's title, because LibriVox reuses
// project numbers, and a row whose title's key differs goes to the worklist
// (reviewed-title-changed). An attach naming no live work goes there too
// (reviewed-work-gone): it never falls through to the rules, which could mint. So does
// one naming a work in another language (reviewed-other-language): it overrides the title
// and author rules, and a work is language-scoped.
//
// ORDER. A rebuild from the snapshot holds nothing yet, and an attach may name a work a
// later project creates, so attach rows are planned after every other row of the batch
// (librivoxPlanOrder). Nothing an attach does decides another row's work, and its moves
// key the author id for the whole batch before any row is planned
// (keyLibrivoxAuthorVariants), so a project of that id planned first credits the same
// person.
//
// THE GUARD. An own-work sits beside the work it was ruled apart from, under one
// normalized identity key, and the duplicate-identity guard's ambiguity veto reads two
// candidates as "cannot say which", so a later row of ANY source would mint a third
// record. So the guard's and the intake gate's answer sets own-work homes apart
// (librivoxOwnWorkHomes, check.SetApart): a key that names other works names those, and
// one that names only own-work homes still names them. Only a refusal does, which is
// recoverable: the paths that place a recording on the one candidate (attachFor,
// relocationHomes) still read the two as ambiguous. The set is derived from the
// recordings, so a later run reads it back from the tree (the table says which projects
// are own-work, the catalogue where they sit); nothing about it is stored on a record.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// The two decisions of a LibrivoxReview.
const (
	LibrivoxReviewAttach  = "attach"
	LibrivoxReviewOwnWork = "own-work"
)

// LibrivoxReview is one reviewed decision. See this file's header.
type LibrivoxReview struct {
	Project int `json:"project"`
	// Title is the project's title as LibriVox states it: the decision holds only
	// while the row's title has the same key, since LibriVox reuses project numbers.
	Title    string `json:"title"`
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
// a repeated or unknown key, a null or anything after the list, projects out of order or
// repeated, a decision missing what it needs or stating what it may not, a reason that
// is not one trimmed line in hyphens.
func parseLibrivoxReviewed(raw []byte) (map[int]LibrivoxReview, error) {
	if err := pack.CheckNoDuplicateKeys(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rs []LibrivoxReview
	if err := dec.Decode(&rs); err != nil {
		return nil, err
	}
	if rs == nil {
		return nil, errors.New("expected a list of decisions")
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("content after the list of decisions")
	}
	out := make(map[int]LibrivoxReview, len(rs))
	for i, r := range rs {
		if err := r.validate(); err != nil {
			return nil, fmt.Errorf("entry %d (project %d): %w", i, r.Project, err)
		}
		if i > 0 && rs[i-1].Project >= r.Project {
			return nil, fmt.Errorf("entry %d (project %d): the list must be sorted by project with no duplicates", i, r.Project)
		}
		out[r.Project] = r
	}
	return out, nil
}

// ValidReviewReason is the one rule for a reviewed decision's reason, here and in
// internal/audit's reviewed.json: one trimmed line, in hyphens rather than dashes.
func ValidReviewReason(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\n\r\u2013\u2014")
}

func (r LibrivoxReview) validate() error {
	if r.Project < 1 {
		return errors.New("no project id")
	}
	if strings.TrimSpace(r.Title) == "" {
		return errors.New("no title: a decision states the project's title, since LibriVox reuses project numbers")
	}
	switch r.Decision {
	case LibrivoxReviewAttach:
		if !model.ValidSlug(r.Work) {
			return fmt.Errorf("an attach names the work it lands on, a slug: %q", r.Work)
		}
	case LibrivoxReviewOwnWork:
		if r.Work != "" || len(r.Moves) > 0 {
			return errors.New("an own-work names no work and moves no credit: the ordinary walk decides")
		}
	default:
		return fmt.Errorf("unknown decision %q", r.Decision)
	}
	for from, to := range r.Moves {
		if !model.ValidSlug(from) || !model.ValidSlug(to) || from == to {
			return fmt.Errorf("the move %q -> %q is not two different person slugs", from, to)
		}
	}
	if !ValidReviewReason(r.Reason) {
		return errors.New("the reason must be a one-line, trimmed reason in hyphens")
	}
	return nil
}

// reviewedLibrivoxState is a run's LibriVox state under its decisions: a test's own, else
// the embedded file.
func reviewedLibrivoxState(opts Options) librivoxState {
	s := newLibrivoxState()
	s.reviewed = opts.LibrivoxReviewed
	if s.reviewed == nil {
		s.reviewed = librivoxReviewed()
	}
	return s
}

// LibrivoxOwnWorkHomes are the catalogued works holding a project whose reviewed decision
// is own-work: the works the duplicate-identity probe sets apart (this file's header).
func LibrivoxOwnWorkHomes(works []*model.Work) map[string]bool {
	return librivoxOwnWorkHomes(works, librivoxReviewed())
}

func librivoxOwnWorkHomes(works []*model.Work, table map[int]LibrivoxReview) map[string]bool {
	homes := map[string]bool{}
	for _, w := range works {
		for _, r := range w.Recordings {
			if r.LibrivoxID != 0 && table[r.LibrivoxID].Decision == LibrivoxReviewOwnWork {
				homes[w.ID] = true
			}
		}
	}
	return homes
}

// ownWorkHomeOf reports whether ws is the home an own-work's row may meet: an own-work home
// naming all the row's credits. One that does not is another text under the same title (a
// translator is role-credited, so the walk's identity set never saw the difference).
func (p *planner) ownWorkHomeOf(ws *workState, credits []credit) bool {
	return p.lv.ownWorks[ws.slug] && !slices.ContainsFunc(credits, func(c credit) bool {
		return !ws.names(p.resolveCredit(c).slug)
	})
}

// librivoxPlanOrder is the order a batch's rows are planned in: as they arrive, with the
// attach decisions after every other row (this file's header).
func (p *planner) librivoxPlanOrder(books []sourceBook) []int {
	order := make([]int, 0, len(books))
	var attaches []int
	for i := range books {
		if b := &books[i]; b.lv != nil && p.lv.reviewed[b.lv.id].Decision == LibrivoxReviewAttach {
			attaches = append(attaches, i)
			continue
		}
		order = append(order, i)
	}
	return append(order, attaches...)
}

// reviewedTitleHolds reports whether a decision is about the project as it now stands:
// LibriVox reuses project numbers, and only the key of the title is compared.
func reviewedTitleHolds(r LibrivoxReview, title string) bool {
	return librivoxTitleKey(librivoxTitle(r.Title)) == librivoxTitleKey(librivoxTitle(title))
}

// routeLibrivoxReviewed is the row's reviewed decision as a route; ok=false for a row
// none names.
func (p *planner) routeLibrivoxReviewed(b sourceBook, workTitle, lang string, credits []credit) (librivoxRoute, bool) {
	r, ok := p.lv.reviewed[b.lv.id]
	if !ok {
		return librivoxRoute{}, false
	}
	warn := p.bookWarn(b)
	if !reviewedTitleHolds(r, b.lv.title) {
		warn("the reviewed decision is for %q, and the project is now %q; not applied", r.Title, b.lv.title)
		p.noteLibrivoxWorklist(b, workTitle, LibrivoxReviewedTitleChanged, nil)
		return librivoxRoute{skip: true}, true
	}
	if r.Decision == LibrivoxReviewOwnWork {
		route := librivoxRoute{}
		if _, nonLatin := librivoxTitleKeyOf(workTitle); nonLatin {
			route = p.librivoxFallback(b, workTitle, true) // such a title has no chain to walk (rule 4)
		}
		route.reviewed = true
		return route, true
	}
	ws, via := p.workAt(r.Work)
	if ws == nil {
		warn("the reviewed decision attaches it to %q, which is no live work; not placed", r.Work)
		p.noteLibrivoxWorklist(b, workTitle, LibrivoxReviewedWorkGone, nil)
		return librivoxRoute{skip: true}, true
	}
	if !langCompatible(ws.lang, lang) {
		warn("the reviewed decision attaches it to %q, in %s where the project is in %s; not placed", ws.slug, ws.lang, lang)
		p.noteLibrivoxWorklist(b, workTitle, LibrivoxReviewedOtherLanguage, candidatesOf([]*workState{ws}))
		return librivoxRoute{skip: true}, true
	}
	return librivoxRoute{walk: &workWalk{title: workTitle, ws: ws, via: via}, moves: p.reviewedMoves(r, credits, warn), reviewed: true}, true
}

// liveReviewedMoves is a decision's moves onto the people who hold their targets; dropped
// names the moves whose target is no live person, in order.
func (p *planner) liveReviewedMoves(r LibrivoxReview) (live map[string]string, dropped []string) {
	for _, from := range slices.Sorted(maps.Keys(r.Moves)) {
		survivor, ok := p.livePerson(r.Moves[from])
		if !ok {
			dropped = append(dropped, from)
			continue
		}
		if live == nil {
			live = map[string]string{}
		}
		live[from] = survivor
	}
	return live, dropped
}

// reviewedMoves resolves an attach's moves. A target that is no live person is dropped, a
// move of nobody the row credits does nothing (LibriVox respelled the name, or the table
// misspells it), and a credit whose own person is a record stays there
// (splitLibrivoxMoves): moving it would be a person merge. All warn, since each leaves the
// decision's id unstamped. A credit keyLibrivoxAuthorVariants already moved is credited
// as the move's target.
func (p *planner) reviewedMoves(r LibrivoxReview, credits []credit, warn func(string, ...any)) map[string]string {
	if len(r.Moves) == 0 {
		return nil
	}
	live, dropped := p.liveReviewedMoves(r)
	for _, from := range dropped {
		warn("the reviewed decision moves %q onto %q, which is no live person; dropped", from, r.Moves[from])
	}
	credited := make(map[string]bool, len(credits))
	for _, c := range credits {
		credited[p.resolveCredit(c).slug] = true
	}
	for _, from := range slices.Sorted(maps.Keys(live)) {
		if !credited[from] && !credited[live[from]] {
			warn("the reviewed decision moves %q, whom the project does not credit; dropped", from)
			delete(live, from)
		}
	}
	moves, kept := p.splitLibrivoxMoves(live)
	for _, from := range kept {
		warn("the reviewed decision moves %q, already a record of its own; kept there", from)
	}
	return moves
}

// reportLibrivoxReviewed is the run's one note about the rows a reviewed decision placed.
func (p *planner) reportLibrivoxReviewed() {
	if n := p.summary.LibrivoxReviewedPlaced; n > 0 {
		p.summary.Notes = append(p.summary.Notes,
			fmt.Sprintf("%d LibriVox project(s) placed by a reviewed decision (internal/importer/librivoxreviewed.json)", n))
	}
}
