package audit

import (
	"sort"
	"strconv"
	"strings"

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
	// sidecarWorks is the set of work slugs the works-community family holds an
	// entry for (either member).
	sidecarWorks map[string]bool

	// authorOf / narratorOf / creditedOn count a person's appearances, which is
	// what tells a P-DUP triage pass which spelling to keep.
	authorOf   map[string]int
	narratorOf map[string]int
	creditedOn map[string]int

	// seriesNameIdx resolves a series name embedded in free text.
	seriesNameIdx *seriesNameIndex

	// derivedCache memoizes the per-work title derivation (which series name the
	// title is cleaned against, the decorations it carries, the cleaned title).
	// Four detectors ask for it and the answer costs a series-name lookup, so
	// deriving it once per work rather than once per question is most of the
	// difference between a two-minute run and a five-minute one.
	derivedCache map[string]*workDerived

	// positions maps a series id to its slot -> work id map, keyed by
	// positionKey so "02" and "2" are one slot and a range is not a slot at
	// all. It answers the "the title says volume N" lookup.
	positions map[string]map[string]string
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
		sidecarWorks: map[string]bool{},
		authorOf:     map[string]int{},
		narratorOf:   map[string]int{},
		creditedOn:   map[string]int{},
		positions:    make(map[string]map[string]string, len(cat.Series)),
		derivedCache: make(map[string]*workDerived, len(cat.Works)),
	}
	for _, w := range cat.Works {
		if _, dup := ix.workByID[w.ID]; !dup {
			ix.workByID[w.ID] = w
		}
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
		if _, dup := ix.personByID[p.ID]; !dup {
			ix.personByID[p.ID] = p
		}
	}
	for _, s := range cat.Series {
		if _, dup := ix.seriesByID[s.ID]; !dup {
			ix.seriesByID[s.ID] = s
		}
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
	for _, c := range cat.Characters {
		ix.sidecarWorks[c.Work] = true
	}
	for _, r := range cat.Recaps {
		ix.sidecarWorks[r.Work] = true
	}
	ix.seriesNameIdx = newSeriesNameIndex(cat.Series)
	return ix
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
		if _, ok := seriesRefIn(lower, s.Name); ok {
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

// workRef builds the full evidence citation for a work.
func (ix *index) workRef(w *model.Work, cleaned string) WorkRef {
	ref := WorkRef{
		ID:          w.ID,
		Title:       w.Title,
		Subtitle:    w.Subtitle,
		Cleaned:     cleaned,
		Authors:     sortedUnique(w.Authors),
		Language:    w.Language,
		Series:      ix.membershipStrings(w.ID),
		Recordings:  len(w.Recordings),
		Sidecar:     ix.sidecarWorks[w.ID],
		SourceTypes: sourceTypes(w.Sources),
	}
	var nars, asins []string
	var runtimes []int
	for _, r := range w.Recordings {
		nars = append(nars, r.Narrators...)
		for _, a := range r.ASIN {
			asins = append(asins, a.ASIN)
		}
		if r.RuntimeMin > 0 {
			runtimes = append(runtimes, r.RuntimeMin)
		}
	}
	ref.Narrators = sortedUnique(nars)
	ref.ASINs = sortedUnique(asins)
	ref.RuntimeMin = sortedInts(runtimes)
	return ref
}

// workBrief is workRef without the recording-level evidence, for the classes that
// cite a work rather than compare two.
func (ix *index) workBrief(w *model.Work) WorkRef {
	return WorkRef{
		ID:          w.ID,
		Title:       w.Title,
		Subtitle:    w.Subtitle,
		Authors:     sortedUnique(w.Authors),
		Language:    w.Language,
		Series:      ix.membershipStrings(w.ID),
		Recordings:  len(w.Recordings),
		Sidecar:     ix.sidecarWorks[w.ID],
		SourceTypes: sourceTypes(w.Sources),
	}
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

// authorKey is a work's author set as a comparison key: the ids sorted and
// joined, so "same authors" is an exact set equality and never an ordering.
func authorKey(authors []string) string {
	return strings.Join(sortedUnique(authors), "+")
}

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
// It indexes every SPELLING of every series name (seriesForms - the same list
// stripSeries removes, so a form that links is a form that strips) by the form's
// first TWO words, and a lookup probes only the buckets the text's own adjacent
// word pairs name, comparing each candidate as a PREFIX at that pair's offset
// rather than scanning the whole text for it. That turns a 280k x 45k product
// into a handful of failed byte compares per title.
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
		forms []seriesForm
		owned map[string]struct{} // the series ids spelling this form
	}
	byKey := map[string]*entry{}
	for _, s := range all {
		for _, form := range seriesForms(s.Name) {
			if len(tokenize(form)) < minSeriesFormWords {
				continue
			}
			key := foldKey(form)
			if key == "" {
				continue
			}
			e := byKey[key]
			if e == nil {
				e = &entry{owned: map[string]struct{}{}}
				byKey[key] = e
			}
			e.forms = append(e.forms, seriesForm{form: form, lower: strings.ToLower(form), series: s.ID})
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
		f := e.forms[0]
		p := firstPair(f.lower)
		if p == "" {
			continue
		}
		idx.byPair[p] = append(idx.byPair[p], f)
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

// lowerWords splits an already-lowered string into its alphanumeric words with
// offsets. It is notAlnum's splitter written to keep the offsets, which
// strings.FieldsFunc discards.
func lowerWords(lower string) []wordAt {
	var out []wordAt
	start := -1
	for i := 0; i < len(lower); i++ {
		if !notAlnum(rune(lower[i])) {
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
			if strings.HasPrefix(lower[off:], f.lower) && boundedAt(lower, off, off+len(f.lower)) {
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

// positionKey normalizes a numeric position for comparison, so "02", "2" and
// "2.0" are one slot and "2.5" and "1-3" stay their own. It returns "" for
// anything that is not a plain number (a range is not a slot).
func positionKey(pos string) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(pos), 64)
	if err != nil {
		return ""
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// seqKey renders a float volume number in the same normal form as positionKey, so
// a number derived from a title can be looked up against a series' positions.
func seqKey(seq float64) string { return strconv.FormatFloat(seq, 'f', -1, 64) }
