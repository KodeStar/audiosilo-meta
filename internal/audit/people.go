package audit

import (
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// P-DUP subclasses. The whole class is ADVISORY: near-duplicate people carry a
// high false-positive rate by nature (two real people share a name; a middle name
// is a real distinction), so no record here proposes a mechanical action. Each
// cites the counts a triage pass needs to see which spelling is the one to keep.
const (
	pDupInitials = "initials-spelling" // one identity spelled with and without initials punctuation
	pDupTight    = "punctuation-only"  // the names differ only in punctuation or spacing
	pDupEdit1    = "edit-distance-1"   // one insertion, deletion, substitution or transposition apart
)

// minEditDistanceLen is the name-length floor for the edit-distance rule. Short
// names one edit apart ("Jon Ray"/"Jan Ray") are far more often two people than
// one, so the rule only speaks about names long enough for a typo to be the
// likelier explanation.
const minEditDistanceLen = 8

// personKeys is one person's three derived keys. Each folds their name through
// model.Slugify, so computing them once per person rather than at each of the three
// or four sites that ask is the whole optimization: the marked key parses the name,
// and FoldKey runs an NFD normalization and a builder.
type personKeys struct {
	person *model.Person
	marked string // the importer's initials identity
	folded string // punctuation-and-spacing-insensitive
	first  string // the folded FIRST word, the edit-distance bucket
}

func personKeyIndex(all []*model.Person) []personKeys {
	out := make([]personKeys, 0, len(all))
	for _, p := range all {
		out = append(out, personKeys{
			person: p,
			marked: titlerule.MarkedNameKey(p.Name),
			folded: titlerule.FoldKey(p.Name),
			first:  firstNameToken(p.Name),
		})
	}
	return out
}

// detectPersonDup groups people who may be one person spelled two ways.
func detectPersonDup(ix *index) *findings {
	f := &findings{class: ClassPersonDup}
	keys := personKeyIndex(ix.cat.People)

	// The importer's own initials identity, through the one definition of it, so
	// the audit and the importer can never disagree about which two spellings are
	// one person (titlerule.MarkedNameKey -> importer.markedKey).
	byMarked, markedOrder := groupBy(keys, func(k personKeys) string { return k.marked })
	for _, key := range markedOrder {
		if group := byMarked[key]; len(group) >= 2 {
			f.add(personDupFinding(ix, pDupInitials, key, group,
				"the importer treats these spellings as one person; a merge is a rename and needs a human"))
		}
	}

	// The punctuation-only key is coarser than a slug (which pkg/check pins to the
	// name) and coarser in a DIFFERENT direction than the marked key, which needs
	// case evidence before it reads a cluster as initials.
	byTight, tightOrder := groupBy(keys, func(k personKeys) string { return k.folded })
	for _, key := range tightOrder {
		group := byTight[key]
		if len(group) < 2 || allShareKey(group, func(k personKeys) string { return k.marked }) {
			continue
		}
		f.add(personDupFinding(ix, pDupTight, key, group,
			"the names differ only in punctuation or spacing"))
	}

	detectPersonEdit1(ix, f, keys)
	return f
}

func personDupFinding(ix *index, sub, key string, group []personKeys, reason string) Finding {
	fd := Finding{Subclass: sub, Key: key}
	var names, ids []string
	for _, k := range group {
		fd.People = append(fd.People, ix.personRef(k.person))
		names = append(names, k.person.Name)
		ids = append(ids, k.person.ID)
	}
	fd.Propose = Proposal{
		Op:       OpReview,
		Others:   sortedUnique(ids),
		Advisory: true,
		Reason:   reason,
	}
	fd.Notes = []string{"spellings: " + truncateList(sortedUnique(names), 8)}
	return fd
}

// detectPersonEdit1 reports pairs of people whose names are one Damerau-Levenshtein
// edit apart, share a first token, and are both long enough for the rule to mean
// anything ("Rachel Rene Russell" / "Rachel Renee Russell").
//
// The first token is what makes it affordable: the comparison is bucketed by it,
// and inside a bucket only names within one character of each other's length can
// possibly be one edit apart, so the quadratic is over a small window rather than
// over 123k people.
func detectPersonEdit1(ix *index, f *findings, keys []personKeys) {
	long := make([]personKeys, 0, len(keys))
	for _, k := range keys {
		if len(k.folded) >= minEditDistanceLen && k.first != "" {
			long = append(long, k)
		}
	}
	buckets, order := groupBy(long, func(k personKeys) string { return k.first })
	for _, b := range order {
		g := buckets[b]
		sort.Slice(g, func(i, j int) bool {
			if len(g[i].folded) != len(g[j].folded) {
				return len(g[i].folded) < len(g[j].folded)
			}
			return g[i].person.ID < g[j].person.ID
		})
		for i := range g {
			for j := i + 1; j < len(g); j++ {
				if len(g[j].folded)-len(g[i].folded) > 1 {
					break // sorted by length: nothing later can be within one edit
				}
				if g[i].folded == g[j].folded {
					continue // an exact fold match is pDupTight's business
				}
				if !titlerule.OneEditApart(g[i].folded, g[j].folded) {
					continue
				}
				a, b := g[i].person, g[j].person
				f.add(Finding{
					Subclass: pDupEdit1,
					Key:      pairKey(a.ID, b.ID),
					People:   []PersonRef{ix.personRef(a), ix.personRef(b)},
					Propose: Proposal{
						Op:       OpReview,
						Others:   sortedUnique([]string{a.ID, b.ID}),
						Advisory: true,
						Reason: "high false-positive rate: two real people can be one edit apart - confirm against the works " +
							"before touching anything",
					},
					Notes: []string{"spellings: " + strings.Join(sortedUnique([]string{a.Name, b.Name}), ", ")},
				})
			}
		}
	}
}

// firstNameToken is the folded first word of a name, the bucket key.
func firstNameToken(name string) string {
	for _, w := range strings.Fields(name) {
		if f := titlerule.FoldKey(w); f != "" {
			return f
		}
	}
	return ""
}
