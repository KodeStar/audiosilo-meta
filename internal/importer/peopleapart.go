package importer

import (
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// PeopleApart is the people half of the reviewed "not one record" decisions
// (Options.Distinct): every pair of person slugs a merge-people rejection in
// internal/audit/reviewed.json recorded as two people, resolved through the
// tombstone table with check.ResolveDistinct - the census's own resolution, so
// the importer, the intake form and metacheck agree on which pairs a decision
// names.
//
// It is read wherever a rule would otherwise make the two ONE person: the
// honorific and credential folds (creditCensus.keptApart), the LibriVox rule 1
// paths, and the series same-person rule (personForm.same), which is what the
// series-author fit and internal/issueform's work-slug and takeover gates
// decide by. "Dr. Alex George" the TV doctor and Alex George the novelist strip
// to one name, and without the review the doctor's book joins the novelist's
// series - the series-hijack pattern, arriving through a rule meant to keep one
// author's spellings together.
//
// A nil *PeopleApart keeps nothing apart, so a caller with no decisions (a
// test, a probe) is unchanged.
type PeopleApart struct {
	pairs check.DistinctPairs
	reds  model.Redirects
}

// NewPeopleApart resolves ds's people decisions through reds, or returns nil
// when none names a pair.
func NewPeopleApart(ds []check.ReviewedDistinct, reds model.Redirects) *PeopleApart {
	pairs := check.ResolveDistinct(ds, reds)
	if len(pairs[model.RedirectPeople]) == 0 {
		return nil
	}
	return &PeopleApart{pairs: pairs, reds: reds}
}

// live is the slug a decision names for person slug s: its survivor when the
// tombstone table retires it, as the decisions were resolved.
func (pa *PeopleApart) live(s string) string { return pa.reds.Live(model.RedirectPeople, s) }

// Has reports whether a review recorded the person slugs a and b as two
// people. A retired slug is asked as its survivor, as the decisions were.
func (pa *PeopleApart) Has(a, b string) bool {
	if pa == nil || a == "" || b == "" {
		return false
	}
	return pa.pairs.Has(model.RedirectPeople, pa.live(a), pa.live(b))
}

// SamePerson is the series same-person rule (personForm.same) over two credited
// names, each with the slug it is recorded or would be minted under: whether
// the two may be one person. A pair a review kept apart never is. It is
// exported for internal/issueform's work-slug and takeover gates, which must
// decide as the importer does whether a credit is the recorded author - one
// rule, so the two doors never split one author's books or join two authors'.
// A nil receiver applies no review.
func (pa *PeopleApart) SamePerson(slugA, nameA, slugB, nameB string) bool {
	return pa.form(slugA, nameA).same(pa.form(slugB, nameB))
}

// NearPerson reports whether two names that are NOT one person by SamePerson
// are still close enough that a reader cannot tell a misspelling from a
// different author (nearPerson) - never a pair a review kept apart, which a
// maintainer has already told apart ("Dr. Alex George" is not Alex George). It
// is SamePerson's other door: internal/issueform's work-slug gate asks it to
// decide when a taken title slug is safe to step past rather than a question
// for a maintainer. A nil receiver applies no review.
func (pa *PeopleApart) NearPerson(slugA, nameA, slugB, nameB string) bool {
	return !pa.Has(slugA, slugB) && nearPerson(nameA, nameB)
}

// form is formOf carrying this review.
func (pa *PeopleApart) form(slug, name string) personForm {
	f := formOf(slug, name)
	f.apart = pa
	return f
}
