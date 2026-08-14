package audit

import (
	"sort"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// index is the derived, read-only view of the catalogue every detector shares.
// It is built ONCE per run - a detector that needed its own pass over 280k works
// would pay for the pass, not for the rule.
//
// Nothing here mutates a catalogue record: the audit is read-only over data/ by
// construction (it never opens a pack.Store), and read-only over the loaded
// catalogue by discipline, so two detectors can never see different data.
type index struct {
	cat *model.Catalog

	workByID   map[string]*model.Work
	personByID map[string]*model.Person
	seriesByID map[string]*model.Series

	// memberships maps a work id to its series memberships, sorted by series id.
	memberships map[string][]membership
	// sidecars maps a work slug to the works-community members its entry holds
	// ("characters", "recaps"), built in this one pass. Before it existed, naming a
	// sidecar's members walked both catalogue slices per sidecar - the run's one
	// genuine quadratic.
	sidecars map[string][]string

	// authorOf / narratorOf / creditedOn count a person's appearances, which is
	// what tells a P-DUP triage pass which spelling to keep.
	authorOf   map[string]int
	narratorOf map[string]int
	creditedOn map[string]int

	// seriesNameIdx resolves a series name embedded in free text.
	seriesNameIdx *seriesNameIndex

	// positions maps a series id to its slot -> work id map, keyed by
	// positionKey so "02" and "2" are one slot and a range is not a slot at
	// all. It answers the "the title says volume N" lookup.
	positions map[string]map[string]string

	// seriesByAuthor maps a person id to the series holding a work they authored,
	// sorted and deduplicated. It is what lets a series name found inside a title
	// be checked against the book's own authorship rather than taken on the words
	// alone - a series name is often two ordinary words.
	seriesByAuthor map[string][]string

	// derivedCache memoizes the per-work title derivation. Four detectors ask for
	// it and the answer costs a series-name lookup, a clean and a volume probe, so
	// deriving it once per work rather than once per question is most of the
	// difference between a two-minute run and a half-minute one.
	derivedCache map[string]*workDerived
}

// membership is one work's place in one series.
type membership struct {
	series   string
	position string
}

func newIndex(cat *model.Catalog) *index {
	ix := &index{
		cat:          cat,
		workByID:     make(map[string]*model.Work, len(cat.Works)),
		personByID:   make(map[string]*model.Person, len(cat.People)),
		seriesByID:   make(map[string]*model.Series, len(cat.Series)),
		memberships:  map[string][]membership{},
		sidecars:     map[string][]string{},
		authorOf:     map[string]int{},
		narratorOf:   map[string]int{},
		creditedOn:   map[string]int{},
		positions:    make(map[string]map[string]string, len(cat.Series)),
		derivedCache: make(map[string]*workDerived, len(cat.Works)),
	}
	for _, w := range cat.Works {
		ix.workByID[w.ID] = w
		for _, a := range w.Authors {
			ix.authorOf[a]++
		}
		for _, c := range w.Credits {
			ix.creditedOn[c.Person]++
		}
		for _, r := range w.Recordings {
			for _, n := range r.Narrators {
				ix.narratorOf[n]++
			}
		}
	}
	for _, p := range cat.People {
		ix.personByID[p.ID] = p
	}
	for _, s := range cat.Series {
		ix.seriesByID[s.ID] = s
		pos := make(map[string]string, len(s.Works))
		for _, sw := range s.Works {
			ix.memberships[sw.Work] = append(ix.memberships[sw.Work], membership{series: s.ID, position: sw.Position})
			slot := positionKey(sw.Position)
			if slot == "" {
				continue // a range spans slots rather than filling one
			}
			// The FIRST work at a slot wins the lookup; a second one is a
			// duplicate position, which S-INTEGRITY reports in its own right.
			if _, dup := pos[slot]; !dup {
				pos[slot] = sw.Work
			}
		}
		ix.positions[s.ID] = pos
	}
	for _, ms := range ix.memberships {
		sort.Slice(ms, func(i, j int) bool {
			if ms[i].series != ms[j].series {
				return ms[i].series < ms[j].series
			}
			return ms[i].position < ms[j].position
		})
	}
	// The sidecar members, in the order the report names them.
	for _, c := range cat.Characters {
		ix.sidecars[c.Work] = append(ix.sidecars[c.Work], "characters")
	}
	for _, r := range cat.Recaps {
		ix.sidecars[r.Work] = append(ix.sidecars[r.Work], "recaps")
	}
	// Which series each author has works in, from the memberships just built.
	byAuthor := map[string]map[string]bool{}
	for _, s := range cat.Series {
		for _, sw := range s.Works {
			w := ix.workByID[sw.Work]
			if w == nil {
				continue
			}
			for _, a := range w.Authors {
				if byAuthor[a] == nil {
					byAuthor[a] = map[string]bool{}
				}
				byAuthor[a][s.ID] = true
			}
		}
	}
	ix.seriesByAuthor = make(map[string][]string, len(byAuthor))
	for a, set := range byAuthor {
		ids := make([]string, 0, len(set))
		for sid := range set {
			ids = append(ids, sid)
		}
		sort.Strings(ids)
		ix.seriesByAuthor[a] = ids
	}
	ix.seriesNameIdx = newSeriesNameIndex(cat.Series)
	return ix
}

// hasSidecar reports whether the works-community family holds an entry for a work.
func (ix *index) hasSidecar(workID string) bool { return len(ix.sidecars[workID]) > 0 }

// sidecarWorkIDs is every work slug the works-community family keys an entry by,
// sorted - the set the sidecar detector walks.
func (ix *index) sidecarWorkIDs() []string {
	out := make([]string, 0, len(ix.sidecars))
	for id := range ix.sidecars {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// seriesNameOf returns a work's series NAME for title cleaning: the membership
// whose name the title actually spells out if there is one, else the
// alphabetically first membership's. Both halves matter - a work in two series
// must clean the same way whichever detector asks, and a title that names one of
// them should be cleaned against THAT one.
func (ix *index) seriesNameOf(w *model.Work) (id, name string) {
	ms := ix.memberships[w.ID]
	if len(ms) == 0 {
		return "", ""
	}
	lower := strings.ToLower(w.Title)
	for _, m := range ms {
		s := ix.seriesByID[m.series]
		if s == nil {
			continue
		}
		if _, ok := titlerule.SeriesRefIn(lower, s.Name); ok {
			return s.ID, s.Name
		}
	}
	if s := ix.seriesByID[ms[0].series]; s != nil {
		return s.ID, s.Name
	}
	return "", ""
}

// membershipStrings renders a work's memberships as "<series-id>@<position>".
func (ix *index) membershipStrings(workID string) []string {
	ms := ix.memberships[workID]
	if len(ms) == 0 {
		return nil
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.series+"@"+m.position)
	}
	return out
}

// workBrief cites a work without the recording-level evidence, for the classes
// that name a work rather than compare two.
func (ix *index) workBrief(w *model.Work) WorkRef {
	return WorkRef{
		ID:          w.ID,
		Title:       w.Title,
		Subtitle:    w.Subtitle,
		Authors:     sortedUnique(w.Authors),
		Language:    w.Language,
		Series:      ix.membershipStrings(w.ID),
		Recordings:  len(w.Recordings),
		Sidecar:     ix.hasSidecar(w.ID),
		SourceTypes: sourceTypes(w.Sources),
	}
}

// workRef is workBrief PLUS the recording-level evidence a duplicate cluster is
// judged on. Composing rather than restating it is what keeps the two citations
// from drifting: a field added to a brief reaches the full ref for free.
func (ix *index) workRef(w *model.Work, cleaned string) WorkRef {
	ref := ix.workBrief(w)
	ref.Cleaned = cleaned
	var nars, asins, pubs, dates []string
	var runtimes []int
	for _, r := range w.Recordings {
		nars = append(nars, r.Narrators...)
		for _, a := range r.ASIN {
			asins = append(asins, a.ASIN)
		}
		if r.RuntimeMin > 0 {
			runtimes = append(runtimes, r.RuntimeMin)
		}
		pubs = append(pubs, r.Publisher)
		for _, p := range r.Publishers {
			pubs = append(pubs, p.Publisher)
		}
		dates = append(dates, r.ReleaseDate)
	}
	ref.Narrators = sortedUnique(nars)
	ref.ASINs = sortedUnique(asins)
	ref.RuntimeMin = sortedInts(runtimes)
	ref.Publishers = sortedUnique(pubs)
	ref.ReleaseDates = sortedUnique(dates)
	return ref
}

// longestRuntime is a work's longest recording runtime, or 0 when none states one.
// The LONGEST rather than an average: a work holding an abridgement beside the full
// text would otherwise look shorter than it is, and the runtime veto asks "could
// this be the same book at all".
func longestRuntime(w *model.Work) int {
	most := 0
	for _, r := range w.Recordings {
		if r.RuntimeMin > most {
			most = r.RuntimeMin
		}
	}
	return most
}

// positionSpans returns a work's series memberships as spans, keyed by series id: the
// numeric range each membership occupies, so a single position and an omnibus range
// are comparable. A membership whose position does not parse is skipped - it is
// S-INTEGRITY's finding, not evidence here.
func (ix *index) positionSpans(workID string) map[string][2]float64 {
	out := map[string][2]float64{}
	for _, m := range ix.memberships[workID] {
		norm, ok := importer.NormalizeSequence(m.position)
		if !ok {
			continue
		}
		lo, hi, ok := model.ParsePositionRange(norm)
		if !ok {
			continue
		}
		out[m.series] = [2]float64{lo, hi}
	}
	return out
}

// seriesIDs is the set of series a work belongs to.
func (ix *index) seriesIDs(workID string) map[string]bool {
	out := make(map[string]bool, len(ix.memberships[workID]))
	for _, m := range ix.memberships[workID] {
		out[m.series] = true
	}
	return out
}

// seriesSpan is the lowest and highest numeric position a series holds, and whether
// it holds any. It is what makes a position derived from a TITLE checkable: a series
// running 1..12 cannot have a volume 2140.
func (ix *index) seriesSpan(seriesID string) (lo, hi float64, ok bool) {
	s := ix.seriesByID[seriesID]
	if s == nil {
		return 0, 0, false
	}
	for _, sw := range s.Works {
		norm, valid := importer.NormalizeSequence(sw.Position)
		if !valid {
			continue
		}
		a, b, valid := model.ParsePositionRange(norm)
		if !valid {
			continue
		}
		if !ok || a < lo {
			lo = a
		}
		if !ok || b > hi {
			hi = b
		}
		ok = true
	}
	return lo, hi, ok
}

// authorSeriesIDs is every series that holds a work by any of these authors - the
// evidence that a series name found in a title is this book's series and not a
// coincidence of words.
func (ix *index) authorSeriesIDs(authors []string) map[string]bool {
	out := map[string]bool{}
	for _, a := range authors {
		for _, sid := range ix.seriesByAuthor[a] {
			out[sid] = true
		}
	}
	return out
}

// worksBrief cites a list of work ids, silently skipping the ones the catalogue
// does not hold (a dangling reference is its own finding).
func (ix *index) worksBrief(ids []string) []WorkRef {
	var out []WorkRef
	for _, id := range sortedUnique(ids) {
		if w := ix.workByID[id]; w != nil {
			out = append(out, ix.workBrief(w))
		}
	}
	return out
}

func (ix *index) personRef(p *model.Person) PersonRef {
	return PersonRef{
		ID:          p.ID,
		Name:        p.Name,
		Kind:        p.Kind,
		AuthorOf:    ix.authorOf[p.ID],
		NarratorOf:  ix.narratorOf[p.ID],
		CreditedOn:  ix.creditedOn[p.ID],
		SourceTypes: sourceTypes(p.Sources),
	}
}

func (ix *index) seriesRef(s *model.Series) SeriesRef {
	return SeriesRef{
		ID:       s.ID,
		Name:     s.Name,
		Works:    len(s.Works),
		Authors:  sortedUnique(s.Authors),
		Language: ix.seriesLanguage(s),
	}
}

// seriesLanguage is a series' majority member language, or "" when its members
// do not agree on one strictly (a 1-1 split has no majority).
func (ix *index) seriesLanguage(s *model.Series) string {
	counts := map[string]int{}
	for _, sw := range s.Works {
		if w := ix.workByID[sw.Work]; w != nil && w.Language != "" {
			counts[w.Language]++
		}
	}
	return strictMajority(counts)
}

// strictMajority returns the key with the highest count when it is STRICTLY
// higher than every other, else "". Ties are deliberately no answer: reporting a
// minority language needs a majority to be a minority OF.
func strictMajority(counts map[string]int) string {
	best, bestN, tied := "", 0, false
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch n := counts[k]; {
		case n > bestN:
			best, bestN, tied = k, n, false
		case n == bestN:
			tied = true
		}
	}
	if tied {
		return ""
	}
	return best
}

// languagesCompatible mirrors pkg/check's rule of the same name (and the
// importer's langCompatible behind it): an UNKNOWN language on either side never
// separates two works.
//
// The audit read this wrong at first, bucketing language=="" as a cluster of its
// own - so a work missing its language could never meet the twin it duplicates,
// which is exactly the record most likely to have one.
func languagesCompatible(a, b string) bool { return a == "" || b == "" || a == b }

func sourceTypes(ss []model.Source) []string {
	if len(ss) == 0 {
		return nil
	}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Type)
	}
	return sortedUnique(out)
}

// seriesNameIndex resolves "does this text name a series we hold" without
// comparing every text against all 45k series names.
//
// It indexes every SPELLING of every series name (titlerule.SeriesForms - the same
// list stripSeries removes, so a form that links is a form that strips) by the
// form's first TWO words, and a lookup probes only the buckets the text's own
// adjacent word pairs name, comparing each candidate as a PREFIX at that pair's
// offset rather than scanning the whole text for it. That turns a 280k x 45k
// product into a handful of failed byte compares per title.
//
// The two-word key is what makes it affordable rather than merely bounded: keyed
// by the first word alone, a common one ("the", "dragon") collects hundreds of
// forms and every title carrying it pays for all of them. Every indexed form has
// at least two words by construction (see the floor below), so no form is lost to
// the wider key.
//
// Two deliberate narrowings, both to hold the false-positive rate down on a rule
// whose whole job is to notice a series name inside a title:
//
//   - a form must carry at least minSeriesFormWords significant words. A
//     one-word series name ("Hexed", "Ascend") is embedded in unrelated titles
//     constantly, and cleaning a title against it deletes the title's own words.
//   - a form whose folded key equals the folded key of ANOTHER series' name is
//     ambiguous and is dropped from the index entirely, since the text cannot
//     say which of them it meant.
type seriesNameIndex struct {
	byPair map[string][]seriesForm
}

// minSeriesFormWords is the significant-word floor for an indexed series form.
const minSeriesFormWords = 2

type seriesForm struct {
	form   string // the spelling, as the series writes it
	lower  string // strings.ToLower(form), precomputed for the containment probe
	series string // the series id
}

func newSeriesNameIndex(all []*model.Series) *seriesNameIndex {
	// Fold every form first, so an ambiguous key (two different series spelling a
	// form the same way) can be dropped before the index is built.
	type entry struct {
		form  seriesForm
		owned map[string]struct{} // the series ids spelling this form
	}
	byKey := map[string]*entry{}
	for _, s := range all {
		for _, form := range titlerule.SeriesForms(s.Name) {
			if titlerule.CountSignificantWords(form) < minSeriesFormWords {
				continue
			}
			key := titlerule.FoldKey(form)
			if key == "" {
				continue
			}
			e := byKey[key]
			if e == nil {
				e = &entry{
					form:  seriesForm{form: form, lower: strings.ToLower(form), series: s.ID},
					owned: map[string]struct{}{},
				}
				byKey[key] = e
			}
			e.owned[s.ID] = struct{}{}
		}
	}
	idx := &seriesNameIndex{byPair: map[string][]seriesForm{}}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := byKey[k]
		if len(e.owned) != 1 {
			continue // ambiguous: two series spell this form identically
		}
		if p := firstPair(e.form.lower); p != "" {
			idx.byPair[p] = append(idx.byPair[p], e.form)
		}
	}
	// Longest form first inside a bucket, so a lookup returns the most specific
	// spelling it can match; slug order breaks a length tie.
	for p := range idx.byPair {
		fs := idx.byPair[p]
		sort.Slice(fs, func(i, j int) bool {
			if len(fs[i].lower) != len(fs[j].lower) {
				return len(fs[i].lower) > len(fs[j].lower)
			}
			if fs[i].lower != fs[j].lower {
				return fs[i].lower < fs[j].lower
			}
			return fs[i].series < fs[j].series
		})
		idx.byPair[p] = fs
	}
	return idx
}

// wordAt is one alphanumeric word of an already-lowered string, with its byte
// offset in it - the offset is what lets a candidate be tested as a PREFIX rather
// than searched for.
type wordAt struct {
	off  int
	word string
}

// lowerWords splits an already-lowered string into its alphanumeric ASCII words
// with offsets, the same partition titlerule's tokenizer makes but keeping the
// offsets strings.FieldsFunc discards.
func lowerWords(lower string) []wordAt {
	var out []wordAt
	start := -1
	for i := 0; i < len(lower); i++ {
		if isLowerAlnum(lower[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, wordAt{off: start, word: lower[start:i]})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, wordAt{off: start, word: lower[start:]})
	}
	return out
}

func isLowerAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

// firstPair is the bucket key of an already-lowered form: its first two
// alphanumeric words joined by a space, or "" when it has fewer than two.
func firstPair(lower string) string {
	ws := lowerWords(lower)
	if len(ws) < 2 {
		return ""
	}
	return ws[0].word + " " + ws[1].word
}

// find returns the longest indexed series form occurring in text, at
// alphanumeric boundaries, and the series it belongs to.
func (si *seriesNameIndex) find(text string) (form, seriesID string, ok bool) {
	lower := strings.ToLower(text)
	ws := lowerWords(lower)
	best := seriesForm{}
	for i := 0; i+1 < len(ws); i++ {
		off := ws[i].off
		for _, f := range si.byPair[ws[i].word+" "+ws[i+1].word] {
			if len(f.lower) <= len(best.lower) {
				// The buckets are longest-first, so once a candidate cannot beat
				// the incumbent none after it in this bucket can either.
				break
			}
			// A word start is a left boundary by construction, so only the right
			// edge needs the boundary test.
			if strings.HasPrefix(lower[off:], f.lower) && titlerule.BoundedAt(lower, off, off+len(f.lower)) {
				best = f
				break
			}
		}
	}
	if best.form == "" {
		return "", "", false
	}
	return best.form, best.series, true
}

// ---- position slots ----------------------------------------------------------

// positionKey is a position's SLOT identity: the number a single position names,
// in one canonical spelling, so "02", "2" and "2.0" are one slot. A RANGE is not a
// slot (it spans several) and neither is anything the grammar rejects, both of
// which return "".
//
// Acceptance goes through importer.NormalizeSequence - the rule of record for what
// a position may be and how it is spelled - BEFORE the span is read. That order is
// load-bearing: model.ParsePositionRange is a span reader over ParseFloat, which
// admits "1e2", "+2" and "Inf", so reading the span first would mint slots for
// values the data model rejects and then report those same values as malformed.
func positionKey(pos string) string {
	norm, ok := importer.NormalizeSequence(pos)
	if !ok || strings.Contains(norm, "-") {
		return ""
	}
	lo, _, ok := model.ParsePositionRange(norm)
	if !ok {
		return ""
	}
	return formatSeq(lo)
}

// formatSeq renders a volume number in positionKey's canonical spelling, so a
// number derived from a title can be looked up against a series' slots.
func formatSeq(seq float64) string { return strconv.FormatFloat(seq, 'f', -1, 64) }
