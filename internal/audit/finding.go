package audit

import (
	"cmp"
	"slices"
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

// The proposal OPS: what a repair pass would DO about a finding. They are the
// machine-readable half of a record, and the prose in a report is rendered from
// them (report.go's renderAction) rather than written beside them - so a detector
// states an intent once and cannot describe one thing while proposing another.
//
// OpReview is the honest answer wherever a mechanical rule may not decide, and it
// always travels with Advisory set and a Note saying what a human must judge.
const (
	OpMergeWorks      = "merge-works"       // fold the cluster onto Target
	OpMergeSeries     = "merge-series"      // fold the members onto Target, then delete the empty spelling
	OpRetitle         = "retitle-work"      // set Field on Target from From to To
	OpAddSeriesMember = "add-series-member" // add Target to Series at To
	OpRestatePosition = "restate-position"  // rewrite a membership's position
	OpDropMembership  = "drop-membership"   // remove a membership (or restore what it names)
	OpFillField       = "fill-field"        // state a fact the record is missing
	OpRenameCandidate = "rename-candidate"  // a slug a rename pass should consider; never applied on this evidence alone
	OpRepointSidecar  = "repoint-sidecar"   // move a works-community entry onto the right work
	OpReview          = "review"            // no mechanical action: a human decides
	OpNone            = ""                  // a pass-through record (LOADER)
)

// Proposal is the typed repair a finding proposes.
//
// TYPED, not prose, because two consumers read it: a human skimming SUMMARY.md and
// the repair pass that will act on the report. A repair pass parsing English out of
// an Action string would be re-deriving what the detector already knew, and the two
// would drift the first time a message was reworded. The prose is DERIVED (see
// report.go), so there is one statement of intent.
type Proposal struct {
	// Op is what to do; see the Op* constants. Empty for a pass-through record.
	Op string `json:"op,omitempty"`
	// Target is the id the op acts ON: the cluster member to keep, the work to
	// retitle, the series to fold onto, the membership's work.
	Target string `json:"target,omitempty"`
	// Others are the ids the op acts on BESIDE the target (a cluster's losers).
	Others []string `json:"others,omitempty"`
	// Series names the series a membership op concerns.
	Series string `json:"series,omitempty"`
	// Field/From/To describe a single-field change. To is empty when the detector
	// deliberately proposes no replacement value.
	Field string `json:"field,omitempty"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	// Advisory marks a proposal a mechanical pass must NOT apply. It is set for
	// every OpReview, and for the classes that are advisory throughout.
	Advisory bool `json:"advisory,omitempty"`
	// Reason states, in one clause, WHY - the thing a human needs in order to
	// decide, or the risk an applier must respect. It is rendered into the action
	// prose after the op's own sentence.
	Reason string `json:"reason,omitempty"`
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
	// Propose is the typed repair. Action below is rendered from it.
	Propose Proposal `json:"propose"`
	// Action is the proposal in words, filled in by the report writer from
	// Propose. A detector never sets it.
	Action string `json:"action,omitempty"`

	Works  []WorkRef   `json:"works,omitempty"`
	People []PersonRef `json:"people,omitempty"`
	Series []SeriesRef `json:"series,omitempty"`
	// Recording is the recording id a recording-scoped finding is about.
	Recording string `json:"recording,omitempty"`
	// Markers lists every decoration a title carries, in priority order, when a
	// finding reports more than the one its subclass names.
	Markers []string `json:"markers,omitempty"`
	// Notes carry the evidence that is not a field: a runtime gap, a language
	// split, which spellings a cluster holds.
	Notes []string `json:"notes,omitempty"`
}

// findingLess orders two findings of one class deterministically. Every
// tiebreaker is data, never insertion order.
func findingLess(a, b Finding) int {
	return cmp.Or(
		cmp.Compare(a.Subclass, b.Subclass),
		cmp.Compare(a.Key, b.Key),
		cmp.Compare(a.Recording, b.Recording),
		cmp.Compare(a.Propose.Field, b.Propose.Field),
		cmp.Compare(a.Propose.From, b.Propose.From),
		cmp.Compare(a.Propose.To, b.Propose.To),
	)
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

// finalize sorts the class's rows IN PLACE and renders each row's action prose
// from its proposal. It runs once, at the end of analyze - the report writer and
// the summary then read the same already-ordered slice instead of each taking a
// sorted copy.
func (f *findings) finalize() {
	slices.SortStableFunc(f.rows, findingLess)
	for i := range f.rows {
		f.rows[i].Action = renderAction(f.rows[i].Propose)
	}
}

// total is the number of records in a class.
func (f *findings) total() int { return len(f.rows) }

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

// groupBy buckets items by a key and returns the buckets plus their keys SORTED.
//
// Sorted keys are the whole point: every detector that groups then iterates has to
// iterate in key order or the report depends on map iteration, and five detectors
// had each spelled the same accumulate-and-sort by hand. An empty key is skipped -
// every caller's key function returns "" for "this record cannot be grouped".
func groupBy[T any](items []T, key func(T) string) (map[string][]T, []string) {
	by := map[string][]T{}
	for _, it := range items {
		if k := key(it); k != "" {
			by[k] = append(by[k], it)
		}
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return by, keys
}

// allShareKey reports whether every member of a group has the same key - the test
// a looser grouping applies before reporting a cluster the tighter one already did.
func allShareKey[T any](group []T, key func(T) string) bool {
	if len(group) < 2 {
		return true
	}
	first := key(group[0])
	for _, it := range group[1:] {
		if key(it) != first {
			return false
		}
	}
	return true
}

// sortedUnique returns ss sorted with duplicates and empty strings removed. Every
// list an audit record carries goes through it, which is most of what makes the
// output deterministic.
//
// Small slices - which is nearly all of them, a work's authors or a cluster's ids -
// skip the map entirely: sorting and compacting in place is cheaper than allocating
// a set, and this is called several times per record over hundreds of thousands of
// records.
func sortedUnique(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	out := make([]string, 0, len(ss))
	if len(ss) <= smallSliceMax {
		for _, s := range ss {
			if s != "" {
				out = append(out, s)
			}
		}
		sort.Strings(out)
		out = slices.Compact(out)
		if len(out) == 0 {
			return nil
		}
		return out
	}
	seen := make(map[string]struct{}, len(ss))
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
	if len(out) == 0 {
		return nil
	}
	return out
}

// smallSliceMax is the length below which sortedUnique dedupes by sorting rather
// than by hashing. A work's authors, a cluster's members and a recording's ASINs
// are all far under it.
const smallSliceMax = 16

// sortedInts returns ns sorted ascending, duplicates kept (two recordings of the
// same runtime is evidence, not noise).
func sortedInts(ns []int) []int {
	if len(ns) == 0 {
		return nil
	}
	out := slices.Clone(ns)
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
