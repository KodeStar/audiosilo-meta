package check

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// advisories.go holds the rules that report a CLASS of suspect record rather
// than a violation of the contract. Every one of them is WARN-only, and that is
// the point: each names a shape that a bulk import can produce and that no
// schema or integrity rule can call wrong on its own evidence, so failing on it
// would block a maintainer's own correction as surely as it blocks the defect.
//
// The first three were added after an adversarial review of seed wave 5 found
// the importer producing exactly these shapes at scale - a translation merged
// into its original, two humans behind one courtesy title, one book split across
// two works; the fourth (a person record nothing credits) came out of the wave-6
// review of the finished seed. The importer rules that prevent them now
// (internal/importer/workidentity.go, honorific.go) can only act on the rows of
// their own run; these advisories watch the TREE, so a class that regrows -
// through a new source, a hand-edited PR, or a rule that stops firing - is
// counted in every metacheck run instead of being discovered a wave later.
//
// They are also what a migration is planned from: each one names the pairs, so
// "how many are there and which" is a command rather than a study.

// checkCrossLanguageRecordings reports a recording whose language differs from
// its work's.
//
// A work is language-scoped - a translation is a different work from its
// original - so a recording in another language is either a wrong merge or a
// work whose own language field is wrong. Both want a human, and neither can be
// decided here: the record is internally consistent and schema-valid either way.
//
// The empty-language guards are defensive: the schema requires a language on
// both records, so a tree that validates carries one everywhere. They are there
// because a rule that silently treats "unknown" as "different" is the kind that
// starts reporting nonsense the day the field becomes optional.
func checkCrossLanguageRecordings(cat *model.Catalog, idx *pathIndex, warn addFunc) {
	for _, w := range cat.Works {
		if w.Language == "" {
			continue
		}
		rel := idx.work[w]
		for _, r := range w.Recordings {
			if r.Language == "" || r.Language == w.Language {
				continue
			}
			warn(rel, "recording %q is in %q but its work is in %q: a translation is a different work",
				r.ID, r.Language, w.Language)
		}
	}
}

// honorificSlugPrefixes are the courtesy titles a person id may carry, as slug
// segments.
//
// The rule of record for the MERGE is internal/importer/honorific.go, whose
// vocabulary this mirrors; the two are held in step by a drift-guard test that
// lives on the importer side (it may import this package, and this package may
// not import it). The mirror is deliberate rather than a shared constant: the
// importer's list is about how a source SPELLS a credit, and this one is about
// what a person id looks like once written, and letting one grow without the
// other being considered is a decision worth making twice.
var honorificSlugPrefixes = []string{"sir", "dame", "dr", "mr", "mrs", "ms"}

// checkHonorificPersonPairs reports a pair of person records that differ only by
// a leading courtesy title.
//
// The pair is not necessarily one human. "Sir Richard Burton" the Victorian
// translator and Richard Burton the actor are two people, and the tree holds
// both; so are "Dr. Steve West" and the narrator Steve West. What the pair
// always is, is a QUESTION - either two records for one person, or two people
// whose ids look like a duplicate to every future importer run - and the answer
// is a maintainer's.
//
// Like the importer's rule it ignores a one-word remainder ("Mr. Peter" ->
// "Peter"), where the shape is coincidence far more often than it is a
// duplicate.
func checkHonorificPersonPairs(cat *model.Catalog, idx *pathIndex, warn addFunc) {
	byID := make(map[string]*model.Person, len(cat.People))
	for _, p := range cat.People {
		byID[p.ID] = p
	}
	for _, p := range cat.People {
		bare, ok := deHonorifiedSlug(p.ID)
		if !ok {
			continue
		}
		twin, exists := byID[bare]
		if !exists {
			continue
		}
		warn(idx.person[p], "person %q differs from %q only by a courtesy title: "+
			"either two records for one person, or two people whose ids read as duplicates",
			p.ID, twin.ID)
	}
}

// deHonorifiedSlug strips a leading courtesy-title segment from a person id,
// reporting whether one was there and left at least two segments behind.
func deHonorifiedSlug(id string) (bare string, ok bool) {
	parts := strings.Split(id, "-")
	if len(parts) < 3 {
		return "", false
	}
	for _, h := range honorificSlugPrefixes {
		if parts[0] == h {
			return strings.Join(parts[1:], "-"), true
		}
	}
	return "", false
}

// checkIdentityEqualWorks reports two works that are one book under two ids.
//
// This is the wrong-SPLIT class: a work forks in two when one edition lists a
// contributor in its author column and another does not, so the tree ends up
// holding "le-sang-des-elfes" and "le-sang-des-elfes-andrzej-sapkowski", one
// book under two ids, each accreting its own recordings from then on.
//
// TWO shapes are reported, and the difference is only how much the records say
// about the extra person:
//
//   - the identity rule's own match (IdentityEqualWorks): the author lists
//     reduce to one set once role-credited contributors are set aside, so the
//     importer itself would treat the pair as one work;
//   - a strict author SUBSET: one work's authors are all of the other's and the
//     other lists more, with nothing stating what the extra people did.
//
// The second is the same defect with the evidence missing, and it is the bulk of
// it - a source that puts a translator, an editor or an illustrator in the
// author column rarely also says so. Requiring the role qualifier found 20 pairs
// in the seeded tree where dropping the requirement finds 381, and the 361 it
// was missing are the same shape with nothing stating the extra person's part.
// So the advisory does not require it: the pair is a maintainer's reading list,
// and "these two differ by a person one of them never explains" is exactly the
// pair worth reading.
//
// Works are compared within a TITLE group, which is both cheap and exactly
// right: a fork's slug is derived from the same title as its base, so a pair
// that is not in one group is not this class. Pairs in different languages are
// skipped - those are a translation and its original, which SHOULD be two works.
// The subset shape is STRICT on purpose: two works with the very same author
// list are the identity rule's own business, and the one case where that rule
// says no - a mutual translation, each side crediting the other's author as its
// translator - is a pair it deliberately keeps apart.
func checkIdentityEqualWorks(cat *model.Catalog, idx *pathIndex, warn addFunc) {
	groups := map[string][]*model.Work{}
	for _, w := range cat.Works {
		key := model.Slugify(w.Title)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], w)
	}
	keys := make([]string, 0, len(groups))
	for k, g := range groups {
		if len(g) > 1 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := groups[k]
		for i := 0; i < len(g); i++ {
			for j := i + 1; j < len(g); j++ {
				a, b := g[i], g[j]
				if !languagesCompatible(a.Language, b.Language) {
					continue
				}
				reason := oneBookReason(a, b)
				if reason == "" || differentVolumes(a, b) {
					continue
				}
				warn(idx.work[a], "work %q and %q have the same title and %s: "+
					"one book under two ids", a.ID, b.ID, reason)
			}
		}
	}
}

// oneBookReason reports, in the advisory's own words, WHY two same-title works
// read as one book - or "" if they do not. The two shapes are the two the
// advisory reports (see checkIdentityEqualWorks), and they are tried in that
// order so a pair whose extra person IS role-credited reads as the identity
// rule's match rather than as an unexplained subset.
//
// The wording is load-bearing for triage: both phrasings end in the class's own
// marker ("one book under two ids", which AdvisoryCensus counts), and the reason
// in front of it is what a maintainer greps to split the explained forks from
// the unexplained ones. A subset's reason NAMES the extra people, because they
// are the whole finding.
func oneBookReason(a, b *model.Work) string {
	if IdentityEqualWorks(a, b) {
		return "the same identity authors"
	}
	aAll, bAll := authorSet(a), authorSet(b)
	switch {
	case strictSubset(aAll, bAll):
		return fmt.Sprintf("%q lists the same authors plus %s", b.ID, quotedList(extraOf(bAll, aAll)))
	case strictSubset(bAll, aAll):
		return fmt.Sprintf("%q lists the same authors plus %s", a.ID, quotedList(extraOf(aAll, bAll)))
	}
	return ""
}

// authorSet is a work's whole author list as a set. The schema forbids a
// repeated slug, so its size is the list's.
func authorSet(w *model.Work) map[string]bool {
	all := make(map[string]bool, len(w.Authors))
	for _, a := range w.Authors {
		all[a] = true
	}
	return all
}

// strictSubset reports whether every member of a is in b and b holds more.
func strictSubset(a, b map[string]bool) bool { return len(a) < len(b) && subset(a, b) }

// extraOf returns the members of sup that sub does not hold, sorted, so one pair
// always reports one line.
func extraOf(sup, sub map[string]bool) []string {
	var extra []string
	for k := range sup {
		if !sub[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return extra
}

// quotedList renders person slugs as a quoted, comma-separated list.
func quotedList(ids []string) string {
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, fmt.Sprintf("%q", id))
	}
	return strings.Join(quoted, ", ")
}

// volumeSuffixRE matches the serial-disambiguation tail the importer's
// same-title pre-pass mints (internal/importer/workidentity.go), capturing the
// base and the position it names.
var volumeSuffixRE = regexp.MustCompile(`^(.*)-book-([0-9]+(?:-(?:to-)?[0-9]+)*)$`)

// differentVolumes reports whether two identity-equal works are two VOLUMES of
// one serial rather than one book under two ids.
//
// A serial published under its bare series name is exactly the shape this
// advisory would otherwise flag hardest: six works all titled "Bravelands", all
// by Erin Hunter, deliberately separated because they are books 1 to 6. They
// are the pre-pass working, not damage, and reporting fifteen pairs of them
// would bury the real finding under the fix for a different one.
//
// The evidence it accepts is the SLUGS: both ids carry the pre-pass's
// "-book-<position>" tail on a common base, naming different positions. That
// tail is minted by one rule and by nothing else, so it is a statement of
// intent rather than an inference - and it covers the case the series file
// cannot, where every placement was dropped because a sibling edition already
// held the position.
//
// A DIFFERENT series position is deliberately NOT accepted as evidence here,
// though it is what the migration that drains this class uses to decide what it
// may merge. The two thresholds are different on purpose: merging is
// irreversible and must be conservative, while an advisory is a maintainer's
// reading list and "these two look like one book, and the series disagrees" is
// exactly the pair worth reading. Silencing it would hide the-silmarillion
// against the-silmarillion-j-r-r-tolkien, one book at two positions of one
// series.
func differentVolumes(a, b *model.Work) bool {
	ma, mb := volumeSuffixRE.FindStringSubmatch(a.ID), volumeSuffixRE.FindStringSubmatch(b.ID)
	return ma != nil && mb != nil && ma[1] == mb[1] && ma[2] != mb[2]
}

// languagesCompatible mirrors the importer's langCompatible: an unknown
// language on either side never separates two works.
func languagesCompatible(a, b string) bool { return a == "" || b == "" || a == b }

// IdentityEqualWorks reports whether two catalogued works would match as ONE
// work under the importer's identity rule
// (internal/importer/workidentity.go's matchWork).
//
// It restates the rule rather than calling it, because the importer imports
// THIS package and the dependency cannot run the other way. A drift-guard test
// on the importer side runs a table of author sets through both and asserts
// they agree, so the restatement cannot quietly diverge.
//
// The rule: a person credited with a contributor ROLE is not an author for
// identity purposes, and the two sides' reduced author sets must be nested -
// one contains the other - with the containing set itself credited by the other
// side. The nesting is what keeps a mutual-translation pair (each one's author
// credited as the other's translator) apart: their identities are disjoint.
// It is IdentityAuthorsMatch (identity.go) with one side's sets read off a work,
// so the nesting rule has one implementation whether the caller holds two
// catalogued works or a work and an incoming row.
func IdentityEqualWorks(a, b *model.Work) bool {
	aAll, aID := identitySets(a)
	return IdentityAuthorsMatch(b, aAll, aID)
}

// identitySets returns a work's whole author set and its identity subset (the
// authors that carry no contributor-role credit).
func identitySets(w *model.Work) (all, identity map[string]bool) {
	all = authorSet(w)
	if len(w.Credits) == 0 {
		return all, all
	}
	credited := make(map[string]bool, len(w.Credits))
	for _, c := range w.Credits {
		credited[c.Person] = true
	}
	identity = make(map[string]bool, len(all))
	for a := range all {
		if !credited[a] {
			identity[a] = true
		}
	}
	if len(identity) == 0 {
		// Every author is role-credited: the fallback is the whole list, or the
		// work would match every other authorless work in the catalogue.
		return all, all
	}
	return all, identity
}

// subset reports whether every member of a is in b.
func subset(a, b map[string]bool) bool {
	if len(a) > len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// checkNormalizedDuplicateWorks reports a group of works whose titles NORMALIZE to
// one identity - the same book recorded twice under two retailer spellings of its
// title.
//
// It is the tree-side census of the class the intake gate and the importer's create
// guard now refuse: a bulk import cannot see through a retailer's decoration, so
// "Hammered" and "Hammered: The Iron Druid Chronicles, Book 3" were minted as two
// works, and a full audit of the tree found 4,596 such clusters. The prevention
// stops the class GROWING; this counts what is already there, in every metacheck
// run, so a repair wave's progress is a number and a regrowth is visible the wave
// after it happens.
//
// ADVISORY, and it must stay advisory: the fix is a MERGE, which moves recordings,
// series memberships and sidecars onto a survivor and retires a public slug. No rule
// may demand one on its own reading - internal/audit measured five separate vetoes
// a merge has to clear, every one of them a question a title cannot answer - and
// failing the check on a class the repair waves are still draining would block every
// unrelated contribution until they finish.
//
// It is deliberately DISJOINT from checkIdentityEqualWorks, its neighbour above: a
// group is reported only when two of its members' RAW title slugs differ. A pair
// that spells its title identically is that rule's finding ("one book under two
// ids"), and reporting the same pair twice would make the census line count one
// defect as two. So this class is exactly "titles that differ and mean the same
// book", which is the decoration-minted population. What each pair must clear
// otherwise is normalizedDuplicateGroup's business, and it is the index's own
// predicate - the same rules the two writers refuse a new record on.
func checkNormalizedDuplicateWorks(ix *WorkIdentity, idx *pathIndex, warn addFunc) {
	for _, key := range ix.Keys() {
		works := ix.Works(key)
		if len(works) < 2 {
			continue
		}
		group := normalizedDuplicateGroup(ix, works)
		if len(group) < 2 {
			continue
		}
		lead := group[0]
		others := make([]string, 0, len(group)-1)
		for _, w := range group[1:] {
			others = append(others, w.ID)
		}
		// The wording deliberately does NOT carry checkIdentityEqualWorks' marker
		// ("one book under two ids"): an advisory is classified by the marker its
		// message ends in, the classifier takes the FIRST marker that matches, and a
		// message carrying both would be counted as its neighbour's class - which is
		// exactly what the first draft of this rule did, reporting 0 of its own
		// findings while its lines were in the log.
		warn(idx.work[lead], "work %q normalizes to the same title and authors as %s "+
			"(%q): the same book under two spellings of its title",
			lead.ID, quotedList(others), lead.Title)
	}
}

// dupCandidate is one key-group member with everything the pair loop asks of it
// computed ONCE: a group of n members is n*(n-1)/2 pairs, and slugifying a title or
// reducing an author list per pair is that many times more work than per member.
type dupCandidate struct {
	work *model.Work
	// slug is the RAW title's slug, the disjointness test against
	// checkIdentityEqualWorks.
	slug string
	// all and identity are the work's author sets, as the nesting rule reads them.
	all      map[string]bool
	identity map[string]bool
}

// normalizedDuplicateGroup returns the members of one key group that really are a
// finding, in id order, or fewer than two of them.
//
// A member is kept when it pairs with at least one other member under the index's
// own pairwise predicate (WorkIdentity.matches - languages, author nesting, no
// stated-volume disagreement, the SAME rules the intake gate and the bulk guard
// refuse a new record on), plus the two vetoes that belong to this class alone: RAW
// titles that differ (a same-title pair is checkIdentityEqualWorks' finding) and no
// serial position suffix telling the two apart.
//
// Pairwise rather than group-wide because the identity rule matches NESTED author
// sets, so a key group can hold two unrelated books by different authors.
func normalizedDuplicateGroup(ix *WorkIdentity, works []*model.Work) []*model.Work {
	cands := make([]dupCandidate, 0, len(works))
	for _, w := range works {
		all, identity := identitySets(w)
		cands = append(cands, dupCandidate{work: w, slug: model.Slugify(w.Title), all: all, identity: identity})
	}
	keep := map[string]*model.Work{}
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			a, b := cands[i], cands[j]
			if a.slug == b.slug {
				continue // checkIdentityEqualWorks' finding, not this one
			}
			if differentVolumes(a.work, b.work) {
				continue
			}
			if !ix.matches(b.work, a.work.Title, ix.SeriesNameOf(a.work.ID), a.work.Language, a.all, a.identity) {
				continue
			}
			keep[a.work.ID], keep[b.work.ID] = a.work, b.work
		}
	}
	out := make([]*model.Work, 0, len(keep))
	for _, w := range keep {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// checkOrphanPeople reports a person record that nothing in the tree credits.
//
// A person exists to be credited: the record is minted by whatever imported the
// work that names them, so one that no work's authors, no work's credits and no
// recording's narrators mention is the residue of something that went wrong -
// a work dropped or re-slugged after its people were written, a credit corrected
// to another spelling, a hand-edited PR that moved the last credit away. The
// record then sits in the people family forever, searchable and empty.
//
// It cannot be a failure. The fix is a DELETION, and no rule may demand one on
// its own reading: the same shape is what a person record added ahead of the
// work that will credit it looks like, and what a catalogue mid-rework looks
// like. What it always is, is a question.
//
// The credited set is built from forEachPersonRef - the same enumeration
// checkIntegrity verifies - so this rule can never call a record an orphan
// while a reference site checkIntegrity still enforces points at it.
func checkOrphanPeople(cat *model.Catalog, recs []recordWithPath, idx *pathIndex, warn addFunc) {
	credited := make(map[string]bool, len(cat.People))
	forEachPersonRef(cat, recs, idx, func(r personRef) {
		credited[r.id] = true
	})
	for _, p := range cat.People {
		if credited[p.ID] {
			continue
		}
		warn(idx.person[p], "person %q is credited by no work, recording or series: an orphan record", p.ID)
	}
}

// The advisory CLASS names. An advisory is classified by the marker its message
// ends in - the wording each rule was deliberately given - and these are the
// names of those classes, so a consumer can group advisories by class rather than
// re-spelling the markers. AdvisoryUnclassified is the answer for a message no
// class claims, which is what a NEW advisory rule looks like until it is added
// here.
const (
	AdvisoryCrossLanguage   = "cross-language-recording"
	AdvisoryHonorificPerson = "honorific-person-pair"
	AdvisoryIdentityEqual   = "identity-equal-works"
	AdvisoryNormalizedDup   = "normalized-duplicate-works"
	AdvisoryOrphanPerson    = "orphan-person"
	AdvisorySidecarScale    = "mis-scaled-sidecar"
	AdvisoryOversizedEntry  = "oversized-entry"
	// AdvisoryRetiredSidecarKey is the ONE class no single-root load can produce:
	// a composed build resolving a community sidecar's key through the core tree's
	// slug tombstone table (compose.go). Its count is the size of the open re-key
	// sweep, which is why it is an advisory at all rather than silence.
	AdvisoryRetiredSidecarKey = "retired-sidecar-key"
	AdvisoryUnclassified      = "unclassified"
)

// advisoryMarkers maps each class to the marker its rule's message carries. It is
// the ONE place those substrings are written: AdvisoryCensus counts through it and
// AdvisoryClass names through it, so the census line and an outside consumer's
// grouping can never disagree about what a class is.
//
// Order is the census line's order, which is also the order AdvisoryClasses
// returns - a rendered census and a grouped report list the classes the same way.
var advisoryMarkers = []struct {
	class  string
	marker string
	label  string
}{
	{AdvisoryCrossLanguage, "a translation is a different work", "cross-language recordings"},
	{AdvisoryHonorificPerson, "only by a courtesy title", "honorific person pairs"},
	{AdvisoryIdentityEqual, "one book under two ids", "identity-equal work pairs"},
	{AdvisoryOrphanPerson, "an orphan record", "orphan people"},
	// The normalized-identity collisions (checkNormalizedDuplicateWorks). APPENDED
	// rather than filed next to its identity-equal sibling on purpose: the census
	// line's order is this table's, and a wave is compared against the last one by
	// reading that line, so a new class goes at the END where it cannot shift the
	// columns a maintainer reads by position.
	{AdvisoryNormalizedDup, "under two spellings of its title", "normalized-identity duplicate work groups"},
	{AdvisorySidecarScale, "scaled to something other than the work's chapters", "mis-scaled sidecars"},
	// The pack-storage advisory (packcheck.go's single-entry-over-target warning).
	// It was MISSING from the census, which therefore reported fewer advisories
	// than the load produced - 979 of 991 on the seeded tree. It surfaced when
	// internal/audit started filing advisories under these class names and 12 of
	// them came back unclassified.
	{AdvisoryOversizedEntry, "an oversized entry cannot be split", "oversized entries"},
	// The composed build's tombstone rides (compose.go). Appended for the same
	// reason as its predecessors: the census line is read by column position.
	{AdvisoryRetiredSidecarKey, "the community re-key sweep is pending", "sidecar keys riding a redirect"},
}

// AdvisoryClass names the advisory class a warning belongs to, or
// AdvisoryUnclassified. Exported so a consumer grouping advisories by class -
// internal/audit files them as its LOADER subclasses - reads the same
// classification the census line counts, rather than a second copy of the
// markers.
func AdvisoryClass(w Problem) string {
	for _, m := range advisoryMarkers {
		if strings.Contains(w.Msg, m.marker) {
			return m.class
		}
	}
	return AdvisoryUnclassified
}

// AdvisoryCensus renders the one-line count metacheck prints under the
// advisory lines, so a wave can be compared against the last one without
// diffing thousands of lines. It returns "" when no advisory class fired.
func AdvisoryCensus(warns []Problem) string {
	counts, total := map[string]int{}, 0
	for _, w := range warns {
		if c := AdvisoryClass(w); c != AdvisoryUnclassified {
			counts[c]++
			total++
		}
	}
	if total == 0 {
		return ""
	}
	parts := make([]string, 0, len(advisoryMarkers))
	for _, m := range advisoryMarkers {
		parts = append(parts, fmt.Sprintf("%d %s", counts[m.class], m.label))
	}
	return "advisory classes: " + strings.Join(parts, ", ")
}

// sidecarScaleFloor is the fraction of a work's chapter count that its sidecar
// positions must reach before the rule stays quiet.
//
// Measured over the sidecars that had the defect and the ones that did not: the
// broken set gated its last position at 0.10-0.24 of the book's chapters (the
// affair, 9 of 88; worth dying for, 8 of 62; never go back, 11 of 69), while the
// sound set reached 0.59-0.77 (one shot, 10 of 17; killing floor, 25 of 34;
// running blind, 24 of 31). The gap between the two populations is wide enough
// that the threshold does not have to be argued about; 0.4 sits in the middle of
// it with room on both sides.
const sidecarScaleFloor = 0.4

// sidecarScaleMinPositions is the number of DISTINCT positions a sidecar must
// use before the rule will judge its scale.
//
// The defect this rule looks for is a sidecar that stages its entries across a
// gradient - some early, some late - but built that gradient on the wrong
// ruler. A sidecar using one position throughout is not that: an all-at-chapter-1
// cast list, or a recaps member holding only the chapter-0 "previously, in
// earlier books" entry, has no gradient to be scaled wrongly. Measured over the
// tree, those two shapes are 77 characters members and 93 recaps members - by
// far the largest class the rule would otherwise report, and none of them
// mis-scaled. Whether an unstaged sidecar is worth its own advisory is a
// separate question from this one.
const sidecarScaleMinPositions = 3

// sidecarScaleMinChapters is the shortest chapter list the rule will judge.
//
// A work with few chapters gives the ratio too little resolution - a sidecar
// describing three of eight chapters is a normal partial contribution, not a
// mis-scaled one - and short recording chapter lists are also where credit and
// part-divider tracks distort the count most.
const sidecarScaleMinChapters = 20

// checkSidecarPositionScale reports a characters or recaps sidecar whose
// positions stop far short of the chapters its work's recordings actually have.
//
// A sidecar's position is the logical WORK chapter, and a consumer gates on it:
// a character card appears once the listener passes its reveal, a recap once
// they pass its through. Author those positions against something other than the
// work's own chapters - an audiobook's parts, a summary written in quarters, a
// partial read - and every gate opens early. That is not a cosmetic error. In
// the affair the murder victim's true identity was gated at chapter 7 of an
// 88-chapter book and is not disclosed until chapter 47, and each of the four
// books in that wave carried a final recap, stating the ending, gated at chapter
// 11.
//
// Nothing in the tree states a work's chapter count, so the rule reads the one
// independent measure the data does carry: the chapter list on the work's own
// recordings. That is an audiobook's track list rather than the work's chapter
// numbering, and the two differ - credits tracks, combined chapters, part
// dividers - so the comparison is deliberately coarse. It fires only on an
// order-of-magnitude mismatch, and it compares against the SMALLEST chapter list
// among the recordings, so the recording that splits the book most finely cannot
// be what condemns the sidecar.
//
// WARN-only, like its neighbours here, and for the same reason: a sidecar that
// genuinely covers only the opening of a long book is a legitimate partial
// contribution. The rule cannot tell that from a mis-scaled one on the tree's
// own evidence - only the source text can - so it names the sidecar and leaves
// the judgement to a human.
//
// It is CROSS-FAMILY - the measure it reads is the works family's recordings -
// but unlike checkIntegrity's sidecar arm it needs no profile gate: under a tree
// holding no works the floor map is empty and every sidecar takes the !ok
// continue, so the rule is vacuous by construction rather than switched off
// (see check.LoadProfile for the cross-family skip rule it satisfies for free).
func checkSidecarPositionScale(cat *model.Catalog, idx *pathIndex, warn addFunc) {
	// Smallest non-empty chapter list per work id.
	floor := map[string]int{}
	for _, w := range cat.Works {
		for _, r := range w.Recordings {
			n := len(r.Chapters)
			if n == 0 {
				continue
			}
			if cur, ok := floor[w.ID]; !ok || n < cur {
				floor[w.ID] = n
			}
		}
	}

	report := func(rel, kind, workID string, top, chapters int) {
		warn(rel, "%s sidecar for %q stops at chapter %d but the work's recordings carry %d chapters: "+
			"the positions may be scaled to something other than the work's chapters",
			kind, workID, top, chapters)
	}

	for _, c := range cat.Characters {
		chapters, ok := floor[c.Work]
		if !ok || chapters < sidecarScaleMinChapters {
			continue
		}
		top, distinct := 0, map[int]bool{}
		for _, ch := range c.Characters {
			distinct[ch.Reveal.Chapter] = true
			if ch.Reveal.Chapter > top {
				top = ch.Reveal.Chapter
			}
		}
		if len(distinct) >= sidecarScaleMinPositions && float64(top) < float64(chapters)*sidecarScaleFloor {
			report(idx.characters[c], "characters", c.Work, top, chapters)
		}
	}

	for _, rc := range cat.Recaps {
		chapters, ok := floor[rc.Work]
		if !ok || chapters < sidecarScaleMinChapters {
			continue
		}
		top, distinct := 0, map[int]bool{}
		for _, r := range rc.Recaps {
			distinct[r.Through.Chapter] = true
			if r.Through.Chapter > top {
				top = r.Through.Chapter
			}
		}
		if len(distinct) >= sidecarScaleMinPositions && float64(top) < float64(chapters)*sidecarScaleFloor {
			report(idx.recaps[rc], "recaps", rc.Work, top, chapters)
		}
	}
}
