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
// All three were added after an adversarial review of seed wave 5 found the
// importer producing exactly these shapes at scale - a translation merged into
// its original, two humans behind one courtesy title, one book split across two
// works. The importer rules that prevent them now
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

// checkIdentityEqualWorks reports two works that the importer's identity rule
// would treat as ONE book.
//
// This is the wrong-SPLIT class: a work forks in two when one edition lists a
// translator in its author column and another does not, so the tree ends up
// holding "le-sang-des-elfes" and "le-sang-des-elfes-andrzej-sapkowski", one
// book under two ids, each accreting its own recordings from then on.
//
// Works are compared within a TITLE group, which is both cheap and exactly
// right: a fork's slug is derived from the same title as its base, so a pair
// that is not in one group is not this class. Pairs in different languages are
// skipped - those are a translation and its original, which SHOULD be two works.
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
				if !languagesCompatible(a.Language, b.Language) || !IdentityEqualWorks(a, b) {
					continue
				}
				if differentVolumes(a, b) {
					continue
				}
				warn(idx.work[a], "work %q and %q have the same title and the same identity authors: "+
					"one book under two ids", a.ID, b.ID)
			}
		}
	}
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
	all = make(map[string]bool, len(w.Authors))
	for _, a := range w.Authors {
		all[a] = true
	}
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

// AdvisoryCensus renders the one-line count metacheck prints under the
// advisory lines, so a wave can be compared against the last one without
// diffing thousands of lines. It returns "" when no advisory class fired.
func AdvisoryCensus(warns []Problem) string {
	var lang, honor, ident int
	for _, w := range warns {
		switch {
		case strings.Contains(w.Msg, "a translation is a different work"):
			lang++
		case strings.Contains(w.Msg, "only by a courtesy title"):
			honor++
		case strings.Contains(w.Msg, "one book under two ids"):
			ident++
		}
	}
	if lang+honor+ident == 0 {
		return ""
	}
	return fmt.Sprintf("advisory classes: %d cross-language recordings, %d honorific person pairs, %d identity-equal work pairs",
		lang, honor, ident)
}
