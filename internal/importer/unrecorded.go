package importer

import (
	"fmt"
	"slices"
	"strings"
)

// UnrecordedCredit is one author or translator (or other role) credit a landed
// row stated that its work does not record (Summary.UnrecordedCredits): the row
// attached to a work that does not name the person (attachRowAuthors), or the
// name slugs to nothing (reportUnsluggable), which can be a narrator's too. A
// LibriVox row is keyed by its project id, any other row by its ASIN.
type UnrecordedCredit struct {
	// Project is the LibriVox project id, 0 for every other source.
	Project int
	// ASIN is the row's ASIN for every source but LibriVox ("" when the row
	// carries none).
	ASIN string
	// AuthorID is the person's librivox_author_id, 0 when the row states none
	// (always, outside LibriVox).
	AuthorID int
	Name     string
	Roles    []string
	// Work is the work the row's recording landed on.
	Work string
	// PersonExists says the person record exists (the catalogue or the run
	// already held it); false means the run withheld it rather than mint an
	// orphan.
	PersonExists bool
	// Reason is "unsluggable" for a name that slugs to nothing, which no record
	// can hold (the shared catch-all is not them): an author-side credit of any
	// landed row of any source, whether it attached or created its work, and a
	// narrator of any source but LibriVox (Roles ["narrator"]; reportUnsluggable).
	// "" for every other credit the attached work does not name.
	Reason string
	// person is the credit's decided person (credit.person: a LibriVox author
	// id's, or the work person an attach moved it onto), carried until
	// settleUnrecordedCredits asks whether it exists, then dropped; nil for a
	// credit its name decides.
	person *creditPerson
}

// String is the credit as the import report prints it, one line, led by the
// row's key (the LibriVox project id, otherwise the ASIN, "-" for a row with
// neither):
//
//	2027 "Amy Coulter" (translator, librivox_author_id 9876) -> siddhartha, person not created
//	2607 "Йордан Йовков" (author, librivox_author_id 6294) -> staroplaninski-legendi, unsluggable
//	B0CRED0001 "Tessa Word" (translator) -> a-translated-book, person not created
func (c UnrecordedCredit) String() string {
	var b strings.Builder
	switch {
	case c.Project > 0:
		fmt.Fprintf(&b, "%d", c.Project)
	case c.ASIN != "":
		b.WriteString(c.ASIN)
	default:
		b.WriteString("-")
	}
	fmt.Fprintf(&b, " %q (", c.Name)
	if len(c.Roles) > 0 {
		b.WriteString(strings.Join(c.Roles, ", "))
	} else {
		b.WriteString("author")
	}
	if c.AuthorID > 0 {
		fmt.Fprintf(&b, ", librivox_author_id %d", c.AuthorID)
	}
	fmt.Fprintf(&b, ") -> %s", c.Work)
	switch {
	case c.Reason != "":
		b.WriteString(", " + c.Reason)
	case !c.PersonExists:
		b.WriteString(", person not created")
	}
	return b.String()
}

// attachRowAuthors creates the people of a row that ATTACHES to ws (a work the
// walk found), for every source alike - but only those the run will credit
// there: a person ws already names (in its authors or its stored credits), or a
// role-credited person (a translator) when ws is a work THIS run created, onto
// which mergeCreatedWorkFacts adds the row's role credits. Anyone else would be
// a record credited nowhere: a create run never edits a catalogued work's
// credit list, and the row's recording credits only its narrators. Each such
// credit is returned for the run's report (and its person, when it would be
// new, is not created; PersonExists is settled once the run has planned every
// row, settleUnrecordedCredits). Identity authors always qualify (matchWork
// admits only a work whose credit list holds them); the per-person test covers
// every attach route alike - the ordinary walk, a LibriVox spelling variant and
// a work created earlier in the run.
func (p *planner) attachRowAuthors(b sourceBook, asin string, ws *workState, credits []credit, warn func(string, ...any)) []UnrecordedCredit {
	_, runCreated := p.runCredits[ws.slug]
	var out []UnrecordedCredit
	for _, c := range credits {
		// No credit here slugs to nothing: the row's credits were split before
		// any walk read them (rowAuthorCreditSplitIn).
		r, kept := p.getOrCreateCreditIf(c, warn, func(r personResolution) bool {
			return ws.names(r.slug) || (runCreated && len(c.roles) > 0)
		})
		if kept {
			continue
		}
		out = append(out, p.unrecordedOf(b, asin, c, r, ws.slug))
	}
	return out
}

// unrecordedOf is the report entry for credit c (r its resolution) of row b,
// which landed on work: keyed by the LibriVox project id, with the credit's
// librivox_author_id, or for any other source by the row's asin.
func (p *planner) unrecordedOf(b sourceBook, asin string, c credit, r personResolution, work string) UnrecordedCredit {
	u := UnrecordedCredit{Name: c.name, Roles: slices.Clone(c.roles), Work: work, person: c.person}
	if b.lv != nil {
		u.Project, u.AuthorID = b.lv.id, p.librivoxAuthorIDOf(b, c, r)
	} else {
		u.ASIN = asin
	}
	return u
}

// settleUnrecordedCredits sets each unrecorded credit's PersonExists from the
// planner as the run left it: the row's own narrator, or a later row's new
// work, can create the person after the credit was withheld. A name that slugs
// away to nothing is never found by its name (the catch-all is not them, and an
// unsluggable reader's own record is reached by reader id only). A credit
// whose person was decided (UnrecordedCredit.person) is asked as that person,
// not by its name.
func (p *planner) settleUnrecordedCredits() {
	for i := range p.summary.UnrecordedCredits {
		c := &p.summary.UnrecordedCredits[i]
		r := p.resolveCredit(credit{name: c.Name, person: c.person})
		c.PersonExists = !r.create && !r.fellBack
		c.person = nil
	}
}

// unsluggableReason is the reason a credit not recorded carries when its name
// slugs to nothing (UnrecordedCredit.Reason).
const unsluggableReason = "unsluggable"

// narratorRole is the role an unrecorded NARRATOR credit is reported under: a
// report word, not a work credit role (narration is a recording fact).
const narratorRole = "narrator"

// THE CATCH-ALL IS NEVER CREDITED (meta#2751, meta#2778). A name that slugs to
// nothing - one written entirely in a script the slug rule folds away
// ("Йордан Йовков", "김영하") - would land on the one shared catch-all person
// (model.UnslugPersonID), which every such name shares: crediting it says one
// record wrote, translated or read every such book. No importer credits it:
//
//   - libex refuses the whole row at its parse layer (firstUnnamedCredit,
//     credit-not-a-person);
//   - every other source (LibriVox and the user-library importers: OpenAudible,
//     Libation, audiosilo-books) imports the book and leaves the credit off:
//     the row's credits are split ONCE, where they are read and cached
//     (rowAuthorCreditSplitIn, rowNarratorSplitIn), so every reader of them -
//     the batch pre-passes, the work walk, the recordings-only match - sees the
//     set the row lands with; a narrator is kept by every source but LibriVox,
//     whose unsluggable reader takes model.LibrivoxUnslugReaderSlug off their
//     reader id; and each credit left off is reported as a credit not
//     recorded, reason unsluggable (reportUnsluggable);
//   - a row the drop leaves with no individual identity author, or with no
//     narrator, has no identity left and does not land: a LibriVox row is a
//     worklist row (author-unsluggable), any other a skip (missing-author,
//     missing-narrator) with a warning naming the dropped names.
//
// pkg/check's checkUnslugFallback refuses the catch-all wherever it appears.

// unslugs reports whether a name slugs to nothing: personSlug falls back to the
// catch-all for it. It is exactly resolveCredit's fellBack for a credit with no
// decided person, asked without resolving anything.
func unslugs(name string) bool {
	_, fellBack := personSlug(name)
	return fellBack
}

// splitUnsluggableCredits is credits without those whose name slugs to nothing,
// and those left off. A credit carrying a decided person (credit.person: a
// LibriVox author id's) is that person, whatever its name slugs to.
func splitUnsluggableCredits(credits []credit) (kept, dropped []credit) {
	for _, c := range credits {
		if c.person == nil && unslugs(c.name) {
			dropped = append(dropped, c)
		} else {
			kept = append(kept, c)
		}
	}
	if len(dropped) == 0 {
		return credits, nil
	}
	return kept, dropped
}

// splitUnsluggableNames is splitUnsluggableCredits for a list of names.
func splitUnsluggableNames(names []string) (kept, dropped []string) {
	for _, name := range names {
		if unslugs(name) {
			dropped = append(dropped, name)
		} else {
			kept = append(kept, name)
		}
	}
	if len(dropped) == 0 {
		return names, nil
	}
	return kept, dropped
}

// unsluggableLeftNoAuthor reports that credits were left off a row (dropped)
// and no individual identity author is left among the kept ones, so the row
// must not land: a work credited to nobody, to a translator alone or to
// "Anonymous" alone would merge two authors' same-titled books. A row nothing
// was dropped from is never judged here.
func (p *planner) unsluggableLeftNoAuthor(kept, dropped []credit) bool {
	if len(dropped) == 0 {
		return false
	}
	// identityAuthors falls back to the whole list when every credit is
	// role-qualified, so a translator alone would read as the identity: only a
	// person some kept credit names WITHOUT a role counts (an author who also
	// translated the book still does).
	bare := map[string]bool{}
	for _, c := range kept {
		if len(c.roles) == 0 {
			bare[p.resolveCredit(c).slug] = true
		}
	}
	for _, a := range p.rowWorkAuthorsRO(kept).identity {
		if bare[a] && individualAuthor(a) {
			return false
		}
	}
	return true
}

// reportUnsluggable reports the credits of row b left off because their names
// slug to nothing (authors, its author-side credits; narrators, its narrator
// names) as credits not recorded, once the row landed a recording on work.
func (p *planner) reportUnsluggable(b sourceBook, asin, work string, authors []credit, narrators []string) {
	if p.landedRec == "" {
		return
	}
	all := slices.Clone(authors)
	for _, name := range narrators {
		all = append(all, credit{name: name, roles: []string{narratorRole}})
	}
	for _, c := range all {
		u := p.unrecordedOf(b, asin, c, p.resolveCredit(c), work)
		u.Reason = unsluggableReason
		if _, taken := p.lv.slotTaken[u.AuthorID]; taken && b.lv != nil && u.AuthorID > 0 {
			// The author id would have filed them at librivox-author-<id>,
			// but that slot is another person's (librivoxpeople.go rule 4).
			u.Reason = librivoxSlotTaken
		}
		p.summary.UnrecordedCredits = append(p.summary.UnrecordedCredits, u)
	}
}

// quotedNames is names as a warning lists them: each quoted, comma-separated.
func quotedNames(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(out, ", ")
}
