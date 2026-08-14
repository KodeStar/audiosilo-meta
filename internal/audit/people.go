package audit

import (
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// P-DUP subclasses. The whole class is ADVISORY: near-duplicate people carry a
// high false-positive rate by nature (two real people share a name; a middle name
// is a real distinction), so no record here proposes an action. Each cites the
// counts a triage pass needs to see which spelling is the one to keep.
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

// detectPersonDup groups people who may be one person spelled two ways.
func detectPersonDup(ix *index) *findings {
	f := &findings{class: ClassPersonDup}

	// The importer's own initials identity, through the one definition of it, so
	// the audit and the importer can never disagree about which two spellings are
	// one person (importer.MarkedNameKey -> markedKey).
	byMarked, markedOrder := groupPeople(ix.cat.People, importer.MarkedNameKey)
	marked := map[string]bool{}
	for _, key := range markedOrder {
		g := byMarked[key]
		if len(g) < 2 {
			continue
		}
		marked[key] = true
		f.add(personDupFinding(ix, pDupInitials, key, g,
			"advisory: the importer treats these spellings as one person; a merge is a rename and needs a human"))
	}

	// The punctuation-only key is coarser than a slug (which pkg/check pins to the
	// name) and coarser in a DIFFERENT direction than the marked key, which needs
	// case evidence before it reads a cluster as initials.
	byTight, tightOrder := groupPeople(ix.cat.People, foldKey)
	for _, key := range tightOrder {
		g := byTight[key]
		if len(g) < 2 {
			continue
		}
		if allShareMarkedKey(g) {
			continue // already reported under pDupInitials
		}
		f.add(personDupFinding(ix, pDupTight, key, g,
			"advisory: the names differ only in punctuation or spacing"))
	}

	detectPersonEdit1(ix, f)
	return f
}

func allShareMarkedKey(g []*model.Person) bool {
	first := importer.MarkedNameKey(g[0].Name)
	for _, p := range g[1:] {
		if importer.MarkedNameKey(p.Name) != first {
			return false
		}
	}
	return true
}

func groupPeople(all []*model.Person, key func(string) string) (map[string][]*model.Person, []string) {
	by := map[string][]*model.Person{}
	var order []string
	for _, p := range all {
		k := key(p.Name)
		if k == "" {
			continue
		}
		if _, seen := by[k]; !seen {
			order = append(order, k)
		}
		by[k] = append(by[k], p)
	}
	sort.Strings(order)
	for k := range by {
		g := by[k]
		sort.Slice(g, func(i, j int) bool { return g[i].ID < g[j].ID })
		by[k] = g
	}
	return by, order
}

func personDupFinding(ix *index, sub, key string, group []*model.Person, action string) Finding {
	fd := Finding{Subclass: sub, Key: key, Action: action}
	var names []string
	for _, p := range group {
		fd.People = append(fd.People, ix.personRef(p))
		names = append(names, p.Name)
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
func detectPersonEdit1(ix *index, f *findings) {
	type cand struct {
		person *model.Person
		folded string
	}
	buckets := map[string][]cand{}
	for _, p := range ix.cat.People {
		folded := foldKey(p.Name)
		if len(folded) < minEditDistanceLen {
			continue
		}
		first := firstNameToken(p.Name)
		if first == "" {
			continue
		}
		buckets[first] = append(buckets[first], cand{person: p, folded: folded})
	}
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := buckets[k]
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
				if !oneEditApart(g[i].folded, g[j].folded) {
					continue
				}
				a, b := g[i].person, g[j].person
				f.add(Finding{
					Subclass: pDupEdit1,
					Key:      pairKey(a.ID, b.ID),
					People:   []PersonRef{ix.personRef(a), ix.personRef(b)},
					Action:   "advisory only, high false-positive rate: two real people can be one edit apart - confirm against the works before touching anything",
					Notes:    []string{"spellings: " + strings.Join(sortedUnique([]string{a.Name, b.Name}), ", ")},
				})
			}
		}
	}
}

// firstNameToken is the folded first word of a name, the bucket key.
func firstNameToken(name string) string {
	for _, w := range strings.Fields(name) {
		if f := foldKey(w); f != "" {
			return f
		}
	}
	return ""
}

// oneEditApart reports whether a and b are exactly one Damerau-Levenshtein edit
// apart - one insertion, deletion, substitution, or transposition of adjacent
// characters. It is a direct single-edit test rather than a full distance matrix:
// the answer is only ever "is it 1", and the strings are names, so this walks each
// pair once instead of filling an n*m table.
//
// Equal strings return false: zero edits is not one, and an identical fold is a
// different subclass.
func oneEditApart(a, b string) bool {
	if a == b {
		return false
	}
	la, lb := len(a), len(b)
	switch {
	case la == lb:
		return oneSubstitutionOrTransposition(a, b)
	case la+1 == lb:
		return oneInsertion(a, b)
	case lb+1 == la:
		return oneInsertion(b, a)
	}
	return false
}

// oneSubstitutionOrTransposition handles the equal-length cases: exactly one
// differing byte, or exactly two adjacent bytes swapped.
func oneSubstitutionOrTransposition(a, b string) bool {
	first := -1
	diffs := 0
	for i := 0; i < len(a); i++ {
		if a[i] == b[i] {
			continue
		}
		diffs++
		if diffs > 2 {
			return false
		}
		if first < 0 {
			first = i
			continue
		}
		// The second difference must be adjacent to the first and be the swap of
		// it for this to be a transposition.
		if i != first+1 || a[first] != b[i] || a[i] != b[first] {
			return false
		}
	}
	return diffs == 1 || diffs == 2
}

// oneInsertion reports whether short becomes long by inserting exactly one byte.
// long is one byte longer than short by construction.
func oneInsertion(short, long string) bool {
	i := 0
	for i < len(short) && short[i] == long[i] {
		i++
	}
	// Everything after the insertion point must line up shifted by one.
	return short[i:] == long[i+1:]
}
