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
func IdentityEqualWorks(a, b *model.Work) bool {
	aAll, aID := identitySets(a)
	bAll, bID := identitySets(b)
	return (subset(aID, bID) && subset(bID, aAll)) ||
		(subset(bID, aID) && subset(aID, bAll))
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

// AdvisoryCensus renders the one-line count metacheck prints under the
// advisory lines, so a wave can be compared against the last one without
// diffing thousands of lines. It returns "" when no advisory class fired.
func AdvisoryCensus(warns []Problem) string {
	var lang, honor, ident, orphan int
	for _, w := range warns {
		switch {
		case strings.Contains(w.Msg, "a translation is a different work"):
			lang++
		case strings.Contains(w.Msg, "only by a courtesy title"):
			honor++
		case strings.Contains(w.Msg, "one book under two ids"):
			ident++
		case strings.Contains(w.Msg, "an orphan record"):
			orphan++
		}
	}
	if lang+honor+ident+orphan == 0 {
		return ""
	}
	return fmt.Sprintf("advisory classes: %d cross-language recordings, %d honorific person pairs, "+
		"%d identity-equal work pairs, %d orphan people",
		lang, honor, ident, orphan)
}
