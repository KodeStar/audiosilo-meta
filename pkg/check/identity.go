package check

import (
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// identity.go holds the NORMALIZED WORK IDENTITY index: the catalogue side of the
// duplicate-prevention rule whose title half is internal/titlerule
// (IdentityTitleKey).
//
// It lives here, in the package every writer already loads the tree through,
// because the same question is asked by three defences that must agree:
//
//   - internal/issueform's intake gate - is the work this form describes already in
//     the catalogue under a decorated title;
//   - internal/importer's create guard - would this row mint a second record of a
//     book we hold;
//   - checkNormalizedDuplicateWorks below - how many such collisions does the tree
//     hold today, counted in every metacheck run so a repair wave's progress is a
//     number rather than a study.
//
// Sharing the index is not only a no-duplicate-rules matter, it is what makes the
// two writers affordable: the key costs a title clean per work, so a run builds it
// ONCE (the audit's derivedCache lesson) instead of cleaning 280k titles per row.
//
// WHY pkg/check MAY IMPORT internal/titlerule. It is legal (an internal package is
// importable by anything under audiosilo-meta/, including from a package a sibling
// module consumes - pkg/scan already reaches internal/importer the same way) and it
// is what the rule of record demands: the alternative was a second spelling of
// "what a decorated title reduces to" inside a public package, which is the
// two-definitions failure this whole change exists to close. internal/titlerule was
// made a LEAF for it (see titlerule/edition.go).

// WorkIdentity is a catalogue's works indexed by their normalized identity key,
// plus the two derivations that key needs: which series name each work's title is
// read against, and which series a free-text title names.
//
// It is READ-ONLY over the catalogue it was built from and holds pointers into it,
// so it must not outlive the load that produced it.
type WorkIdentity struct {
	byKey map[string][]*model.Work
	// keyOf and seriesOf are per work id, so a caller that has a work in hand pays
	// nothing to ask what it was keyed by.
	keyOf    map[string]string
	seriesOf map[string]string
	// seriesNames are the catalogue's series, id and name, sorted by id so every
	// derivation over them is deterministic.
	seriesNames []seriesNamed
}

type seriesNamed struct {
	id   string
	name string
}

// NewWorkIdentity builds the index over a loaded catalogue. It cleans every work
// title once, which is the whole cost (measured at a couple of seconds over a
// 280k-work tree), so a caller builds one per run and asks it per row.
//
// A catalogue with no works yields a usable empty index rather than nil, so no
// caller needs a nil check.
func NewWorkIdentity(cat *model.Catalog) *WorkIdentity {
	ix := &WorkIdentity{
		byKey:    map[string][]*model.Work{},
		keyOf:    map[string]string{},
		seriesOf: map[string]string{},
	}
	if cat == nil {
		return ix
	}
	// Each work's series memberships, by series id, so SeriesNameFor is handed a
	// deterministic list whatever order the series family was walked in.
	names := map[string][]string{}
	for _, s := range cat.Series {
		ix.seriesNames = append(ix.seriesNames, seriesNamed{id: s.ID, name: s.Name})
		for _, sw := range s.Works {
			names[sw.Work] = append(names[sw.Work], s.ID)
		}
	}
	sort.Slice(ix.seriesNames, func(i, j int) bool { return ix.seriesNames[i].id < ix.seriesNames[j].id })
	byID := make(map[string]string, len(ix.seriesNames))
	for _, s := range ix.seriesNames {
		byID[s.id] = s.name
	}
	for _, w := range cat.Works {
		series := titlerule.SeriesNameFor(w.Title, seriesNamesOf(names[w.ID], byID))
		key := titlerule.IdentityTitleKey(w.Title, series)
		ix.seriesOf[w.ID] = series
		if key == "" {
			continue // a title that normalizes to nothing is no identity
		}
		ix.keyOf[w.ID] = key
		ix.byKey[key] = append(ix.byKey[key], w)
	}
	return ix
}

// seriesNamesOf resolves a work's membership series ids to names, in id order,
// dropping the ids the catalogue does not hold (a dangling membership is
// checkIntegrity's finding, not evidence here).
func seriesNamesOf(ids []string, byID map[string]string) []string {
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if name := byID[id]; name != "" {
			out = append(out, name)
		}
	}
	return out
}

// Key is the normalized identity key of a title read against a series name (which
// may be ""), and is the ONE key rule - a caller keying an incoming record asks
// this rather than composing titlerule calls of its own, so the two sides of every
// comparison are built identically.
func (ix *WorkIdentity) Key(title, series string) string {
	return titlerule.IdentityTitleKey(title, series)
}

// KeyOf is the key a catalogued work was indexed under, or "".
func (ix *WorkIdentity) KeyOf(workID string) string { return ix.keyOf[workID] }

// SeriesNameOf is the series name a catalogued work's title was read against, or
// "" - the derivation, so a caller reporting a collision can say what it cleaned
// against.
func (ix *WorkIdentity) SeriesNameOf(workID string) string { return ix.seriesOf[workID] }

// Works are the catalogued works under one key, in id order. The returned slice
// must not be modified.
func (ix *WorkIdentity) Works(key string) []*model.Work { return ix.byKey[key] }

// Keys are every key the index holds, sorted, so a consumer walking the whole index
// (the census below) is deterministic.
func (ix *WorkIdentity) Keys() []string {
	out := make([]string, 0, len(ix.byKey))
	for k := range ix.byKey {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SeriesNameIn returns the series the free text names - the LONGEST spelling of any
// catalogued series name occurring in it at word boundaries - and that series' id.
//
// It is a LINEAR scan of the catalogue's series names, which is right for its
// callers and wrong for a tree-wide pass: the intake bot asks it once per
// submission, where a scan of 45k names costs a fraction of the load it already
// paid for, while internal/audit asks it per work over 280k works and therefore
// builds a two-word-bucketed prefix index instead (audit's seriesNameIndex). Do not
// call it in a loop over the catalogue.
//
// Two narrowings, both the audit's, both there to hold down false positives on a
// rule whose job is to notice a series name inside a title: a name must carry at
// least two significant words (a one-word series name is embedded in unrelated
// titles constantly), and a name whose fold collides with another series' name is
// ambiguous and matches nothing.
func (ix *WorkIdentity) SeriesNameIn(text string) (name, seriesID string, ok bool) {
	lower := strings.ToLower(text)
	var best, bestID string
	for _, s := range ix.seriesNames {
		if titlerule.CountSignificantWords(s.name) < minSeriesNameWords {
			continue
		}
		form, hit := titlerule.SeriesRefIn(lower, s.name)
		if !hit || len(form) <= len(best) {
			continue
		}
		best, bestID = s.name, s.id
	}
	if best == "" {
		return "", "", false
	}
	return best, bestID, true
}

// minSeriesNameWords is the significant-word floor for a series name this index
// will look for inside free text. It mirrors internal/audit's minSeriesFormWords
// and exists for the same measured reason.
const minSeriesNameWords = 2

// IdentityMatch is one catalogued work an incoming record collides with, and what
// the collision rests on.
type IdentityMatch struct {
	Work *model.Work
	// Key is the normalized identity both sides reduced to.
	Key string
	// Series is the series name the CATALOGUED work's title was read against, or
	// "" - the evidence a report needs, since a key derived against a membership is
	// a stronger claim than one derived against nothing.
	Series string
}

// Match returns the catalogued works an incoming record would duplicate: same
// normalized title identity, compatible language, and author sets that meet under
// the importer's own nesting rule. Results are in work-id order, so a caller's
// verdict never depends on catalogue order.
//
// all and identity are the incoming record's author slug sets (the whole credit
// list, and the subset that is not role-credited - see IdentityAuthorsMatch). A
// caller with no role information passes the same set twice.
//
// It deliberately does NOT report a pair whose two titles state DIFFERENT volume
// numbers: those are siblings of a serial, not two records of one book, and the
// audit measured that shape at 14% of its clusters. Everything else a merge would
// need to clear - a collection on one side, an impossible runtime ratio, a position
// conflict - is left to the caller, because the callers of this are REFUSING a new
// record (recoverable) rather than merging two existing ones (not).
func (ix *WorkIdentity) Match(title, series, lang string, all, identity map[string]bool) []IdentityMatch {
	key := ix.Key(title, series)
	if key == "" {
		return nil
	}
	var out []IdentityMatch
	for _, w := range ix.byKey[key] {
		if !languagesCompatible(w.Language, lang) {
			continue
		}
		if !IdentityAuthorsMatch(w, all, identity) {
			continue
		}
		if !titlerule.SameStatedVolume(title, series, w.Title, ix.seriesOf[w.ID]) {
			continue
		}
		out = append(out, IdentityMatch{Work: w, Key: key, Series: ix.seriesOf[w.ID]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Work.ID < out[j].Work.ID })
	return out
}

// IdentityAuthorsMatch reports whether an incoming record's author sets meet a
// catalogued work's under the importer's identity rule
// (internal/importer/workidentity.go's matchWork).
//
// It is the pairwise half IdentityEqualWorks is written in terms of, exported
// separately because a WRITER holds an incoming row rather than a second
// *model.Work: the importer has resolved author slugs and the intake bot has a
// form's author list, and neither should have to fabricate a work record to ask the
// question - nor spell the nesting rule a second time.
//
// The rule: a person credited with a contributor ROLE is not an author for identity
// purposes, and the two sides' reduced author sets must be NESTED - one contains
// the other - with the containing set itself credited by the other side. The
// nesting is what keeps a mutual-translation pair apart (see IdentityEqualWorks).
func IdentityAuthorsMatch(w *model.Work, all, identity map[string]bool) bool {
	wAll, wID := identitySets(w)
	return (subset(identity, wID) && subset(wID, all)) ||
		(subset(wID, identity) && subset(identity, wAll))
}
