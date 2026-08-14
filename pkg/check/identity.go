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
//   - checkNormalizedDuplicateWorks (advisories.go) - how many such collisions does
//     the tree hold today, counted in every metacheck run so a repair wave's
//     progress is a number rather than a study.
//
// ONE index per LOAD, not per consumer: the census builds it during the load and
// Result.Identity hands it to the writers, so the title-clean pass over every
// catalogued work happens once per run however many defences ask. A writer that
// rebuilt it would pay that pass twice for the same answer.
//
// THE COST IS THE CENSUS'S, and it is unconditional. Every check.Load pays it
// because the census is a rule of every load - measured at 20.6s -> 22.3s over the
// 279k-work tree, about 8% - and no consumer can opt out of that, including a run
// whose mode never probes the index (enrichment, recordings-only, every non-add-work
// intake template). Those runs pay nothing EXTRA, which is the honest version of the
// claim: taking the index costs nothing, having a census costs 1.7s.
//
// Building it lazily was considered and does not help: the census needs it in the
// same load, so the only thing a lazy accessor would defer is work that has already
// happened. What it WOULD buy is retention, and that is the real trade below.
//
// RETENTION. The index holds a pointer to every keyed work, so a consumer that keeps
// it keeps the CATALOGUE alive - recordings and chapter lists included - for as long
// as it holds the index. That is new for the bulk importer, which used to let the
// catalogue go once loadExisting had derived its own maps; it is nothing new for the
// intake bot, which already holds every work in its dedup map. It is taken only for
// the mode that probes it (create), so enrichment and recordings-only release the
// catalogue exactly as before, and the create path needs random access to every key
// for the whole planning pass - there is no window in which a smaller structure
// would do. A compact projection was rejected for a concrete reason rather than a
// vague one: the census reports through pathIndex, which is keyed by the *model.Work
// pointer, so a value-typed index would need the pointers anyway and the tree would
// be held by that map instead.
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
	// seriesOf is the series name each work's title was read against, by work id,
	// so a caller that has a work in hand pays nothing to ask.
	seriesOf map[string]string
	// seriesNames are the catalogue's series that SeriesNameIn may match, already
	// narrowed at build time (see newSeriesNameList) and sorted by id, so every
	// derivation over them is deterministic and no lookup re-applies the filters.
	seriesNames []seriesNamed
	// droppedFolds are the folds newSeriesNameList refused for AMBIGUITY - two or
	// more series spell them - so Admits can tell "the catalogue does not hold this
	// name" from "it holds it twice", which are opposite answers.
	droppedFolds map[string]bool
}

type seriesNamed struct {
	id   string
	name string
}

// NewWorkIdentity builds the index over a loaded catalogue. It cleans every work
// title once, which is the whole cost (measured at a couple of seconds over a
// 280k-work tree); the load builds one and passes it on Result.Identity.
//
// A catalogue with no works yields a usable empty index rather than nil, so no
// caller needs a nil check.
func NewWorkIdentity(cat *model.Catalog) *WorkIdentity {
	ix := &WorkIdentity{
		byKey:        map[string][]*model.Work{},
		seriesOf:     map[string]string{},
		droppedFolds: map[string]bool{},
	}
	if cat == nil {
		return ix
	}
	// Each work's series memberships, by series id, so SeriesNameFor is handed a
	// deterministic list whatever order the series family was walked in.
	members := map[string][]string{}
	byID := make(map[string]string, len(cat.Series))
	for _, s := range cat.Series {
		byID[s.ID] = s.Name
		for _, sw := range s.Works {
			members[sw.Work] = append(members[sw.Work], s.ID)
		}
	}
	ix.seriesNames, ix.droppedFolds = newSeriesNameList(cat.Series)
	for _, w := range cat.Works {
		series := titlerule.SeriesNameFor(w.Title, seriesNamesOf(members[w.ID], byID))
		ix.seriesOf[w.ID] = series
		if key := titlerule.IdentityTitleKey(w.Title, series); key != "" {
			// A title that normalizes to nothing is no identity: it is keyed by
			// nobody and collides with nobody.
			ix.byKey[key] = append(ix.byKey[key], w)
		}
	}
	return ix
}

// newSeriesNameList is the series SeriesNameIn is allowed to find inside free text,
// with both narrowings applied ONCE here rather than per lookup.
//
// Both are internal/audit's, and both hold down the false-positive rate of a rule
// whose job is to notice a series name inside a title:
//
//   - a name must carry at least minSeriesNameWords significant words. A one-word
//     series name ("Hexed", "Ascend") is embedded in unrelated titles constantly,
//     and reading a title against it deletes the title's own words.
//   - a name whose FOLD another series also spells is ambiguous and is dropped
//     entirely: the text cannot say which of them it meant, and the safe answer to
//     an ambiguous identity is no answer (the same posture the importer's guard
//     takes on an ambiguous key).
func newSeriesNameList(all []*model.Series) (admitted []seriesNamed, droppedFolds map[string]bool) {
	byFold := map[string][]seriesNamed{}
	for _, s := range all {
		if titlerule.CountSignificantWords(s.Name) < minSeriesNameWords {
			continue
		}
		fold := titlerule.FoldKey(s.Name)
		if fold == "" {
			continue
		}
		byFold[fold] = append(byFold[fold], seriesNamed{id: s.ID, name: s.Name})
	}
	droppedFolds = map[string]bool{}
	admitted = make([]seriesNamed, 0, len(byFold))
	for fold, group := range byFold {
		if len(group) == 1 {
			admitted = append(admitted, group[0])
			continue
		}
		droppedFolds[fold] = true
	}
	sort.Slice(admitted, func(i, j int) bool { return admitted[i].id < admitted[j].id })
	return admitted, droppedFolds
}

// minSeriesNameWords is the significant-word floor for a series name this index
// will look for inside free text. It mirrors internal/audit's minSeriesFormWords
// and exists for the same measured reason.
const minSeriesNameWords = 2

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

// SeriesNameOf is the series name a catalogued work's title was read against, or
// "" - the derivation, so a caller reporting a collision can say what it cleaned
// against.
func (ix *WorkIdentity) SeriesNameOf(workID string) string { return ix.seriesOf[workID] }

// Works are the catalogued works under one key, in id order. The returned slice
// must not be modified. It is also the cheapest possible "is this key worth
// resolving at all" test, which is how the bulk guard avoids paying for a row whose
// identity nothing holds.
func (ix *WorkIdentity) Works(key string) []*model.Work { return ix.byKey[key] }

// Keys are every key the index holds, sorted, so a consumer walking the whole index
// (the census) is deterministic.
func (ix *WorkIdentity) Keys() []string {
	out := make([]string, 0, len(ix.byKey))
	for k := range ix.byKey {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Admits reports whether a series NAME is one this index will read a title against
// at all - it passes the two narrowings newSeriesNameList applies.
//
// It is the door a caller knocks on when the name did not come from the index: the
// intake bot's submitter may TYPE a series name, and reading a title against a name
// the index would refuse to find is the same rule with two answers. Both halves
// matter there. A one-word name ("Cars") strips its own titles down to their volume
// number, which is the very hazard IdentityTitleKey refuses to key; a fold-ambiguous
// name makes what a submission COMPOSES depend on whether some unrelated series
// happens to spell the name the same way.
func (ix *WorkIdentity) Admits(name string) bool {
	if titlerule.CountSignificantWords(name) < minSeriesNameWords {
		return false
	}
	fold := titlerule.FoldKey(name)
	if fold == "" {
		return false
	}
	for _, s := range ix.seriesNames {
		if titlerule.FoldKey(s.name) == fold {
			return true
		}
	}
	// A name the catalogue does not hold at all is admitted on its own merits: the
	// index cannot say whether it is ambiguous, and refusing every new series' name
	// would stop the strip working for the books that arrive before their series does.
	return !ix.holdsFold(fold)
}

// holdsFold reports whether any series the index DROPPED spells this fold - the
// ambiguity case, where two or more series share it.
func (ix *WorkIdentity) holdsFold(fold string) bool {
	return ix.droppedFolds[fold]
}

// SeriesNameIn returns the series the free text names - the LONGEST spelling of any
// catalogued series name occurring in it at word boundaries - and that series' id.
// The candidates are the narrowed list newSeriesNameList built.
//
// It is a LINEAR scan of those names, which is right for its callers and wrong for
// a tree-wide pass: the intake bot asks it once per submission, where a scan of 45k
// names costs a fraction of the load it already paid for, while internal/audit asks
// it per work over 280k works and therefore builds a two-word-bucketed prefix index
// instead (audit's seriesNameIndex). Do not call it in a loop over the catalogue.
func (ix *WorkIdentity) SeriesNameIn(text string) (name, seriesID string, ok bool) {
	lower := strings.ToLower(text)
	var best, bestID string
	for _, s := range ix.seriesNames {
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

// IdentityMatch is one catalogued work an incoming record collides with, and what
// the collision rests on.
type IdentityMatch struct {
	Work *model.Work
	// Series is the series name the CATALOGUED work's title was read against, or
	// "" - the evidence a verdict quotes, since a key derived against a membership
	// is a stronger claim than one derived against nothing.
	Series string
}

// Match returns the catalogued works an incoming record would duplicate. It is
// MatchKey over the key the record's own title and series produce; a caller that
// already has the key (the bulk guard derives it once per row) calls MatchKey.
func (ix *WorkIdentity) Match(title, series, lang string, all, identity map[string]bool) []IdentityMatch {
	return ix.MatchKey(ix.Key(title, series), title, series, lang, all, identity)
}

// MatchKey is Match against an already-derived key: same normalized title identity,
// compatible language, and author sets that meet under the importer's own nesting
// rule. Results are in work-id order, so a caller's verdict never depends on
// catalogue order.
//
// all and identity are the incoming record's author slug sets (the whole credit
// list, and the subset that is not role-credited - see IdentityAuthorsMatch). A
// caller with no role information passes the same set twice.
//
// What it applies and what it leaves to the caller is matches' business - see there.
// The callers of this are REFUSING a new record (recoverable) rather than merging two
// existing ones (not), which is why the vetoes it cannot express are the caller's to
// add rather than a reason to widen the key.
func (ix *WorkIdentity) MatchKey(key, title, series, lang string, all, identity map[string]bool) []IdentityMatch {
	if key == "" {
		return nil
	}
	var out []IdentityMatch
	for _, w := range ix.byKey[key] {
		if !ix.matches(w, title, series, lang, all, identity) {
			continue
		}
		out = append(out, IdentityMatch{Work: w, Series: ix.seriesOf[w.ID]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Work.ID < out[j].Work.ID })
	return out
}

// matches is THE pairwise predicate: given a candidate work already keyed the same,
// does the incoming record name the same book? Four rules, and each of them is a
// question the KEY cannot answer:
//
//   - the languages are compatible (a translation is a different work; an unknown
//     language never separates);
//   - the author sets are nested (IdentityAuthorsMatch);
//   - neither side states a volume the other contradicts (SameStatedVolume);
//   - exactly one side does not announce itself a COLLECTION (titlerule.IsCollection,
//     the audit's multilingual rule). A boxed set and the volume it collects
//     normalize alike once the packaging words come off - "Bravelands: Books 1-3"
//     against "Bravelands", "Red Rising: The Complete Boxed Set" against "Red
//     Rising" - and are not two records of one book. This lives in the predicate
//     rather than in each caller because all three consumers need it and a veto
//     documented as "the caller's" was implemented by none of them.
//
// Every consumer goes through it, in both directions of the seam: MatchKey asks it
// per candidate for an incoming ROW, and checkNormalizedDuplicateWorks asks it for
// two CATALOGUED works by reading one side's title, language and author sets off
// that work (the same adaptation IdentityEqualWorks makes of IdentityAuthorsMatch).
// A second spelling of these rules is exactly what would let the census and the
// writers disagree about what one book is.
//
// What is deliberately NOT here: the two vetoes that need evidence this index does
// not hold - a runtime ratio (recordings) and a series POSITION conflict (a row's
// claim, or a membership) - which each writer applies with what it has.
func (ix *WorkIdentity) matches(w *model.Work, title, series, lang string, all, identity map[string]bool) bool {
	return languagesCompatible(w.Language, lang) &&
		IdentityAuthorsMatch(w, all, identity) &&
		titlerule.SameStatedVolume(title, series, w.Title, ix.seriesOf[w.ID]) &&
		titlerule.IsCollection(title) == titlerule.IsCollection(w.Title)
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
