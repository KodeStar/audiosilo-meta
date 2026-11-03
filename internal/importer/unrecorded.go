package importer

import (
	"fmt"
	"slices"
	"strings"
)

// UnrecordedCredit is one author or translator (or other role) credit a landed
// row stated that its work does not record (Summary.UnrecordedCredits). Every
// source reaches it the same way: the row attached to a work that does not name
// the person (attachRowAuthors). A LibriVox row is keyed by its project id, any
// other row by its ASIN.
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
	// can hold (the shared catch-all is not them): a LibriVox credit of any landed
	// row (librivoxauthors.go rule 2), or any other source's credit the attached
	// work does not name (attachRowAuthors). "" for every other credit the
	// attached work does not name.
	Reason string
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
		r := p.resolvePerson(c.name)
		if ws.names(r.slug) || (runCreated && len(c.roles) > 0 && !r.fellBack) {
			p.applyPerson(c.name, r, warn)
			continue
		}
		u := p.unrecordedOf(b, asin, c, r, ws.slug)
		if r.fellBack {
			// Only the shared catch-all could hold it, and it is not them: no
			// record was withheld, there is none to make.
			u.Reason = librivoxUnsluggable
		}
		out = append(out, u)
	}
	return out
}

// unrecordedOf is the report entry for credit c (r its resolution) of row b,
// which landed on work: keyed by the LibriVox project id, with the credit's
// librivox_author_id, or for any other source by the row's asin.
func (p *planner) unrecordedOf(b sourceBook, asin string, c credit, r personResolution, work string) UnrecordedCredit {
	u := UnrecordedCredit{Name: c.name, Roles: slices.Clone(c.roles), Work: work}
	if b.lv != nil {
		u.Project, u.AuthorID = b.lv.id, p.librivoxAuthorIDOf(b, c.name, r)
	} else {
		u.ASIN = asin
	}
	return u
}

// settleUnrecordedCredits sets each unrecorded credit's PersonExists from the
// planner as the run left it: the row's own narrator, or a later row's new
// work, can create the person after the credit was withheld. A name that slugs
// away to nothing is never found by its name (the catch-all is not them, and an
// unsluggable reader's own record is reached by reader id only).
func (p *planner) settleUnrecordedCredits() {
	for i := range p.summary.UnrecordedCredits {
		c := &p.summary.UnrecordedCredits[i]
		r := p.resolvePerson(c.Name)
		c.PersonExists = !r.create && !r.fellBack
	}
}
