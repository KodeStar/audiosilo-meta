package audit

import (
	"sort"
	"strconv"
	"strings"
)

// The audit's detector classes. One class is one NDJSON file in the report
// directory, and the order below is the order SUMMARY.md lists them in - so it is
// the report's table of contents as much as it is a list of codes.
const (
	ClassWorkDup      = "W-DUP"      // near-duplicate work clusters
	ClassWorkTitle    = "W-TITLE"    // retailer decoration left on a title
	ClassWorkNoSeries = "W-NOSERIES" // a series named in a title but not modeled
	ClassSeriesInteg  = "S-INTEGRITY"
	ClassSeriesDup    = "SER-DUP"     // near-duplicate series
	ClassSeriesParen  = "SER-PAREN"   // parenthetical-decorated series names
	ClassPersonDup    = "P-DUP"       // near-duplicate people (advisory)
	ClassRefSidecar   = "REF-SIDECAR" // sidecar hazards
	ClassHygiene      = "F-HYGIENE"   // field hygiene
	ClassLoader       = "LOADER"      // pkg/check's own problems and advisories
)

// classOrder is every class, in report order.
var classOrder = []string{
	ClassWorkDup, ClassWorkTitle, ClassWorkNoSeries, ClassSeriesInteg,
	ClassSeriesDup, ClassSeriesParen, ClassPersonDup, ClassRefSidecar,
	ClassHygiene, ClassLoader,
}

// WorkRef is a work as an audit record cites it. Recordings is always written
// (never omitempty) so "no recordings" and "the field was not filled in" cannot
// be confused - a work with none is itself a finding.
type WorkRef struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Cleaned  string   `json:"cleaned_title,omitempty"`
	Authors  []string `json:"authors,omitempty"`
	Language string   `json:"language,omitempty"`
	// Series lists the work's memberships as "<series-id>@<position>", sorted.
	Series     []string `json:"series,omitempty"`
	Recordings int      `json:"recordings"`
	Narrators  []string `json:"narrators,omitempty"`
	RuntimeMin []int    `json:"runtime_min,omitempty"`
	ASINs      []string `json:"asins,omitempty"`
	// Sidecar reports that the works-community family holds an entry for this
	// work slug - which is what makes a duplicate cluster a spoiler hazard.
	Sidecar bool `json:"sidecar,omitempty"`
	// SourceTypes are the distinct sources[].type values on the work, sorted:
	// the provenance a triage pass needs to tell a bulk-mirror record from a
	// hand-curated one.
	SourceTypes []string `json:"source_types,omitempty"`
}

// PersonRef is a person as an audit record cites them, with the counts triage
// needs to see which spelling is the canonical one.
type PersonRef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind,omitempty"`
	AuthorOf   int    `json:"author_of"`
	NarratorOf int    `json:"narrator_of"`
	CreditedOn int    `json:"credited_on"`
	// SourceTypes are the distinct sources[].type values on the record, sorted.
	SourceTypes []string `json:"source_types,omitempty"`
}

// SeriesRef is a series as an audit record cites it.
type SeriesRef struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Works    int      `json:"works"`
	Authors  []string `json:"authors,omitempty"`
	Language string   `json:"majority_language,omitempty"`
}

// Finding is one audit record: ONE shape for every class, so a consumer parses
// one thing and a new class needs no new reader. Fields a class does not use are
// omitted.
//
// Key is the record's grouping identity (a cluster key, a slug, a slug pair). It
// is what the findings of a class are sorted by, after Subclass, so a report is
// byte-identical between runs over the same tree.
type Finding struct {
	Class    string `json:"class"`
	Subclass string `json:"subclass,omitempty"`
	Key      string `json:"key"`
	// Canonical is the member of a cluster the audit proposes keeping.
	Canonical string      `json:"canonical,omitempty"`
	Works     []WorkRef   `json:"works,omitempty"`
	People    []PersonRef `json:"people,omitempty"`
	Series    []SeriesRef `json:"series,omitempty"`
	// Recording is the recording id a recording-scoped finding is about.
	Recording string `json:"recording,omitempty"`
	// Markers lists every decoration a title carries, sorted, when a finding
	// reports more than the one its subclass names.
	Markers []string `json:"markers,omitempty"`
	// Field/Have/Want describe a single-field proposal ("title" / the recorded
	// value / the proposed one).
	Field string `json:"field,omitempty"`
	Have  string `json:"have,omitempty"`
	Want  string `json:"want,omitempty"`
	// Action is the proposed repair, in words. The audit never applies it.
	Action string `json:"action,omitempty"`
	// Notes carry the evidence that is not a field: a runtime gap, a language
	// split, why a proposal was withheld.
	Notes []string `json:"notes,omitempty"`
}

// findingLess orders two findings of one class deterministically. Every
// tiebreaker is data, never insertion order.
func findingLess(a, b Finding) bool {
	if a.Subclass != b.Subclass {
		return a.Subclass < b.Subclass
	}
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.Recording != b.Recording {
		return a.Recording < b.Recording
	}
	if a.Field != b.Field {
		return a.Field < b.Field
	}
	if a.Have != b.Have {
		return a.Have < b.Have
	}
	return a.Want < b.Want
}

// findings accumulates one class's records.
type findings struct {
	class string
	rows  []Finding
}

func (f *findings) add(fd Finding) {
	fd.Class = f.class
	f.rows = append(f.rows, fd)
}

// sorted returns the class's records in report order.
func (f *findings) sorted() []Finding {
	out := make([]Finding, len(f.rows))
	copy(out, f.rows)
	sort.SliceStable(out, func(i, j int) bool { return findingLess(out[i], out[j]) })
	return out
}

// counts returns the per-subclass record counts, subclasses sorted.
func (f *findings) counts() []subclassCount {
	byName := map[string]int{}
	for _, r := range f.rows {
		byName[r.Subclass]++
	}
	out := make([]subclassCount, 0, len(byName))
	for name, n := range byName {
		out = append(out, subclassCount{Subclass: name, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subclass < out[j].Subclass })
	return out
}

type subclassCount struct {
	Subclass string
	Count    int
}

// sortedUnique returns ss sorted with duplicates and empty strings removed. Every
// list an audit record carries goes through it, which is most of what makes the
// output deterministic.
func sortedUnique(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// sortedInts returns ns sorted ascending, duplicates kept (two recordings of the
// same runtime is evidence, not noise).
func sortedInts(ns []int) []int {
	if len(ns) == 0 {
		return nil
	}
	out := make([]int, len(ns))
	copy(out, ns)
	sort.Ints(out)
	return out
}

// pairKey is the stable key of an unordered pair of slugs.
func pairKey(a, b string) string {
	if b < a {
		a, b = b, a
	}
	return a + "|" + b
}

// joinCount renders "<n> <noun>" with a plural s, for note text.
func joinCount(n int, noun string) string {
	s := strconv.Itoa(n) + " " + noun
	if n != 1 {
		s += "s"
	}
	return s
}

// truncateList renders at most max items of ss, appending "... (+N more)" when it
// had to cut, so a note stays one readable line however big the finding is.
func truncateList(ss []string, max int) string {
	if len(ss) <= max {
		return strings.Join(ss, ", ")
	}
	return strings.Join(ss[:max], ", ") + ", ... (+" + strconv.Itoa(len(ss)-max) + " more)"
}
