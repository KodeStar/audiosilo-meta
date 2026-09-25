package importer

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesauthors.go is the AUTHOR half of series resolution: which of the series a
// name's slug chain holds a row may JOIN.
//
// A series is found by its NAME (seriesChain, tombstone.go), and a name is not an
// identity: "Lost Fleet" is Jack Campbell's military SF and Sarah Hawke's space
// opera, "Heart of Stone", "Midnight" and "Legacy" are a dozen romance franchises
// each. Joining the first same-named series on the chain is what let an unrelated
// author's books SQUAT another author's slots - Campbell's Lost Fleet 4-6 sat at
// Hawke's positions 4-6 and pushed her real volumes out, and the 2026-08 sweeps
// (Omega Force, Quantum, Reawakened, Wicked Fae, Delirium, Das Marsprojekt, Carlisle
// Emergency, Conan, smoke, tangled, ...) were all this one mechanism.
//
// So a same-named series now JOINS only when it FITS the row (SeriesAuthors.Fit):
//
//   - SHARED: a row author credits one of the series' member works - by slug, or by
//     a spelling of the same name (samePersonName: the importer's initials identity,
//     a fold, one edit, a middle name, initial-plus-surname, a one-word pen name).
//   - OPEN: nothing is shared, but the series gives no evidence it belongs to
//     anyone else: it has fewer than seriesClosedMinMembers members that credit an
//     individual, or no author credits at least three quarters of them (a shared
//     universe, an anthology line, a Hoerspiel with rotating writers), or the row's
//     own TITLE names a member author ("Robert Ludlum's The Janson Equation", "Ted
//     Bell's Monarch" - a licensed continuation says whose series it is), or a
//     member was released by the row's own PUBLISHER (a publisher's multi-author
//     line; a pen name the name rungs cannot see).
//   - CLOSED otherwise: the walk steps past it exactly as it steps past a
//     differently-named holder, to the next candidate on the chain - so the row
//     joins its own author's `lost-fleet-2`, or mints the next free candidate,
//     rather than squatting.
//
// A SHARED candidate anywhere on the chain beats an OPEN one earlier in it: a
// Campbell row finds Campbell's `lost-fleet-2` even when an earlier same-named
// series would have admitted him.
//
// The rule was chosen by MEASUREMENT over the 2026-09-25 tree (165,361 series
// memberships, every member judged against the series' OTHER members): the
// strictest reading (any author-disjoint member) refuses 2,118 memberships, most of
// them legitimate shared universes; this rule refuses 258 memberships in 250
// series, and a hand review of 105 stratified refusals found 85 squatters (81%) -
// the rest licensed continuations (Colfer's Hitchhiker's, Kurland's Lord Darcy),
// anthologies placed between volumes (Rogues, Fearsome Journeys) and pen names no
// spelling rule can see. Each OPEN arm earns its place in that measurement:
// without the publisher arm the precision is 69%, a threshold of one member
// refuses the second author of every young shared series (a quarter of the
// two-member disjoint series are squats), and dominance at two thirds re-admits
// nothing but refuses the "4 volumes by 3 writers" shared lines. Replayed, it
// changed 7 series of the #2337 OpenAudible import and 56 of wave-2 tranche 01
// (the tranche that squatted Lost Fleet), every change hand-checked correct: 17
// squatting placements moved to their author's own series, and 61 rows that a
// position clash in another author's series used to leave unplaced now start
// their own.
//
// Refusing a legitimate member costs a same-named sibling series (`kingkiller-
// chronicle-2` holding Rogues) a human can merge; admitting a squatter costs a
// wrong book in another author's series that readers see and that displaces the
// real volume. The asymmetry is the reason the rule errs toward refusal.
//
// Collective credits (full-cast, various, anonymous, uncredited, unknown) and the
// catch-all person record state no individual, so they are evidence of nothing on
// either side: a row crediting only them is admitted, and a member crediting only
// them is not counted.

// seriesClosedMinMembers is the fewest members crediting an individual a series
// needs before it can refuse anyone. One member is not a franchise yet: measured,
// only a quarter of the two-member series whose two works share no author are
// squats - the rest are young shared series (Warhammer - Vergeltung, Crimes Canada,
// It Calls From).
const seriesClosedMinMembers = 2

// seriesDominantNum/Den is the share of those members one author must credit for
// the series to belong to that author: three quarters. At a half and at two thirds
// the refused population is dominated by shared lines with two or three regular
// writers (Dark Sun, Star Trek: Picard, Marvel Novels); at three quarters it is
// dominated by squatters.
const (
	seriesDominantNum = 3
	seriesDominantDen = 4
)

// SeriesFit is how a series' existing membership receives a row naming it.
type SeriesFit int

const (
	// SeriesClosed: the series belongs to other authors; the chain steps past it.
	SeriesClosed SeriesFit = iota
	// SeriesOpen: no author is shared, but nothing says the series is anyone
	// else's either.
	SeriesOpen
	// SeriesShared: a row author credits a member.
	SeriesShared
)

// SeriesPerson is one author a row credits: the person slug it resolves to and
// the name it was spelled with.
type SeriesPerson struct {
	Slug, Name string
}

// SeriesRow is what the fit reads about a row naming a series.
type SeriesRow struct {
	Authors []SeriesPerson
	// Titles are the row's title spellings (work title, full title), read for a
	// member author's name.
	Titles []string
	// Publishers are the row's publisher names.
	Publishers []string
}

// SeriesAuthors is the author evidence one series' members carry.
type SeriesAuthors struct {
	// members counts the members that credit at least one individual.
	members int
	// credits maps an individual author slug to the members crediting it.
	credits map[string]int
	// publishers is every member recording's publisher key (publisherKey).
	publishers map[string]bool
}

// add records one member work: its author slugs and its recordings' publishers.
func (sa *SeriesAuthors) add(authors, publishers []string) {
	if sa.credits == nil {
		sa.credits = map[string]int{}
		sa.publishers = map[string]bool{}
	}
	seen := map[string]bool{}
	for _, a := range authors {
		if a == "" || seen[a] || !individualAuthor(a) {
			continue
		}
		seen[a] = true
		sa.credits[a]++
	}
	if len(seen) > 0 {
		sa.members++
	}
	for _, p := range publishers {
		if k := publisherKey(p); k != "" {
			sa.publishers[k] = true
		}
	}
}

// Fit judges the series for row. nameOf returns the name a person slug carries
// in the catalogue ("" when unknown). A nil receiver is a series with no members.
func (sa *SeriesAuthors) Fit(row SeriesRow, nameOf func(slug string) string) SeriesFit {
	var mine []SeriesPerson
	for _, a := range row.Authors {
		if individualAuthor(a.Slug) {
			mine = append(mine, a)
		}
	}
	if sa == nil || sa.members == 0 || len(mine) == 0 {
		return SeriesOpen
	}
	for _, a := range mine {
		if sa.credits[a.Slug] > 0 {
			return SeriesShared
		}
	}
	top := 0
	for slug, n := range sa.credits {
		top = max(top, n)
		name := nameOf(slug)
		if name == "" {
			continue
		}
		for _, a := range mine {
			if samePersonName(a.Name, name) {
				return SeriesShared
			}
		}
	}
	if sa.members < seriesClosedMinMembers || top*seriesDominantDen < sa.members*seriesDominantNum {
		return SeriesOpen
	}
	for slug := range sa.credits {
		if titleNamesPerson(row.Titles, nameOf(slug)) {
			return SeriesOpen
		}
	}
	for _, p := range row.Publishers {
		if k := publisherKey(p); k != "" && sa.publishers[k] {
			return SeriesOpen
		}
	}
	return SeriesClosed
}

// nonIndividualAuthors are the person slugs that state no individual: the five
// collective canonicals (collective.go) and the catch-all an unslugifiable name
// falls back to.
var nonIndividualAuthors = func() map[string]bool {
	out := map[string]bool{model.UnslugPersonID: true}
	for _, name := range collectiveCredits {
		slug, _ := model.PersonSlug(name)
		out[slug] = true
	}
	return out
}()

func individualAuthor(slug string) bool { return slug != "" && !nonIndividualAuthors[slug] }

// SeriesAuthorIndex is SeriesAuthors for every series of a catalogue, plus the
// person names the fit compares spellings by - the view a writer that does not
// keep its own planner state (libex-select, the intake form) resolves with.
type SeriesAuthorIndex struct {
	series map[string]*SeriesAuthors
	names  map[string]string
}

// NewSeriesAuthorIndex builds the index over cat. A nil catalogue is an empty
// index, under which every series is open.
func NewSeriesAuthorIndex(cat *model.Catalog) *SeriesAuthorIndex {
	ix := &SeriesAuthorIndex{series: map[string]*SeriesAuthors{}, names: map[string]string{}}
	if cat == nil {
		return ix
	}
	for _, p := range cat.People {
		ix.names[p.ID] = p.Name
	}
	ix.series = seriesAuthorsOf(cat)
	return ix
}

// Fit judges the series stored at slug for row. A nil index admits every series
// as SeriesShared, the name-only walk.
func (ix *SeriesAuthorIndex) Fit(slug string, row SeriesRow) SeriesFit {
	if ix == nil {
		return SeriesShared
	}
	return ix.series[slug].Fit(row, func(s string) string { return ix.names[s] })
}

// seriesAuthorsOf reads every catalogued series' author evidence.
func seriesAuthorsOf(cat *model.Catalog) map[string]*SeriesAuthors {
	works := make(map[string]*model.Work, len(cat.Works))
	for _, w := range cat.Works {
		works[w.ID] = w
	}
	out := make(map[string]*SeriesAuthors, len(cat.Series))
	for _, s := range cat.Series {
		sa := &SeriesAuthors{}
		for _, sw := range s.Works {
			if w := works[sw.Work]; w != nil {
				sa.add(w.Authors, workPublishers(w))
			}
		}
		out[s.ID] = sa
	}
	return out
}

// workPublishers is every publisher a catalogued work's recordings name.
func workPublishers(w *model.Work) []string {
	var out []string
	for _, r := range w.Recordings {
		if r.Publisher != "" {
			out = append(out, r.Publisher)
		}
		for _, rp := range r.Publishers {
			out = append(out, rp.Publisher)
		}
	}
	return out
}

// publisherGeneric are the words a publisher name carries that say what kind of
// company it is rather than which: "Tantor Audio" and "Tantor Media" are one
// house, as are "Blackstone Audio, Inc." and "Blackstone Publishing".
var publisherGeneric = map[string]bool{
	"audio": true, "media": true, "publishing": true, "publishers": true, "publisher": true,
	"books": true, "book": true, "studios": true, "studio": true, "inc": true, "llc": true,
	"ltd": true, "gmbh": true, "co": true, "company": true, "verlag": true, "audiobooks": true,
	"audiobook": true, "entertainment": true, "group": true, "uk": true, "us": true, "the": true,
	"and": true, "productions": true, "production": true, "records": true, "recordings": true,
	"publications": true, "press": true, "corp": true, "corporation": true, "limited": true,
	"plc": true, "sl": true, "slu": true, "sa": true, "spa": true, "ab": true, "as": true,
	"bv": true, "kg": true, "horverlag": true, "amp": true,
}

// publisherKey is the comparison form of a publisher name: its slug words minus
// the generic ones, or "" when nothing distinctive is left.
func publisherKey(name string) string {
	var kept []string
	for _, w := range strings.Split(model.Slugify(name), "-") {
		if w != "" && !publisherGeneric[w] {
			kept = append(kept, w)
		}
	}
	return strings.Join(kept, " ")
}

// titleNamesPerson reports whether any of titles spells the whole of a
// multi-word person name ("Robert Ludlum's The Janson Equation" names Robert
// Ludlum). One-word names are never read: a title holding "Tiye" or "Drako" says
// nothing about who wrote it.
func titleNamesPerson(titles []string, name string) bool {
	words := nameWords(name)
	if len(words) < 2 {
		return false
	}
	// Slugify drops an apostrophe rather than splitting on it, so the possessive
	// the continuations are titled with reads "robert-blochs".
	joined := "-" + strings.Join(words, "-")
	for _, t := range titles {
		if t == "" {
			continue
		}
		slug := "-" + model.Slugify(t) + "-"
		if strings.Contains(slug, joined+"-") || strings.Contains(slug, joined+"s-") {
			return true
		}
	}
	return false
}

// samePersonName reports whether two names may be one person under two
// spellings. It only ever ADMITS a row to a series (a wrong yes is the behaviour
// before the author rule existed; a wrong no refuses a real member), so it is
// deliberately wider than internal/audit's samePersonSpelling, which WEAKENS a
// merge veto:
//
//   - the importer's own identities: one person slug, or one initials key
//     (MarkedNameKey - "A.B. Kovacs" / "AB Kovacs");
//   - one folded spelling, or one edit apart over eight letters ("Shevlin"
//     typed "Shelvin" needs two, and is not caught);
//   - a one-word name (three letters or more) that is the other's first or last
//     word ("Aijan" / "Aijan Kashkaeva", "Tiye" / "Tiye Tiye");
//   - the same first word with the shorter name's words in order inside the
//     longer ("Julian Gyll" / "Julian Gyll-Murray", "Cheree Alsop" / "Cheree
//     Lynn Alsop");
//   - the same surname and first initial ("M.S. Olney" / "Matthew Olney", "Doug
//     Hirt" / "Douglas Hirt");
//   - the same first name and surnames one edit apart ("Lowe Key" / "Lowe Keye").
//
// Credential, generational and legal-entity suffixes are ignored (isSuffixPiece:
// "Michael Salla PH.D.", "Innovative Language Learning LLC"), as are a leading
// courtesy title ("Dr. Samuel Li") and a " - role" tail a record kept ("Eric
// Flint - edited").
func samePersonName(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	sa, _ := model.PersonSlug(a)
	sb, _ := model.PersonSlug(b)
	if sa == sb || markedKey(a) == markedKey(b) {
		return true
	}
	wa, wb := nameWords(a), nameWords(b)
	if len(wa) == 0 || len(wb) == 0 {
		return false
	}
	fa, fb := strings.Join(wa, ""), strings.Join(wb, "")
	if fa == fb || (len(fa) >= 8 && len(fb) >= 8 && titlerule.OneEditApart(fa, fb)) {
		return true
	}
	if len(wa) > len(wb) {
		wa, wb = wb, wa
	}
	switch {
	case len(wa) == 1:
		return len(wa[0]) >= 3 && (wa[0] == wb[0] || wa[0] == wb[len(wb)-1])
	case wa[0] == wb[0] && isSubsequence(wa, wb):
		return true
	case wa[len(wa)-1] == wb[len(wb)-1] && wa[0][0] == wb[0][0]:
		return true
	case wa[0] == wb[0] && len(wa[len(wa)-1]) >= 3 && len(wb[len(wb)-1]) >= 3 &&
		titlerule.OneEditApart(wa[len(wa)-1], wb[len(wb)-1]):
		return true
	}
	return false
}

// nameWords is a name's words in slug form, a " - role" tail, a leading courtesy
// title (deHonorified: "Dr. Samuel Li") and every suffix word dropped. Dots,
// hyphens, slashes and ampersands separate words.
func nameWords(name string) []string {
	if i := strings.Index(name, " - "); i > 0 {
		name = name[:i]
	}
	if bare, stripped := deHonorified(name); stripped {
		name = bare
	}
	var out []string
	for _, tok := range strings.Fields(strings.NewReplacer(",", " ", "&", " ", "/", " ").Replace(name)) {
		if isSuffixPiece(tok) {
			continue
		}
		for _, w := range strings.Split(model.Slugify(tok), "-") {
			if w != "" {
				out = append(out, w)
			}
		}
	}
	return out
}

// isSubsequence reports whether short's words appear in long in order.
func isSubsequence(short, long []string) bool {
	i := 0
	for _, w := range long {
		if i < len(short) && short[i] == w {
			i++
		}
	}
	return i == len(short)
}
