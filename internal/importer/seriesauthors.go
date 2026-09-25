package importer

import (
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesauthors.go is the AUTHOR half of series resolution: which of the series a
// name's slug chain holds a row may JOIN. seriesresolve.go decides it for a whole
// batch at once, over a snapshot, so no answer depends on row order.
//
// A series is found by its NAME (the chain walk, SeriesSlugAt), and a name is not
// an identity: "Lost Fleet" is Jack Campbell's military SF and Sarah Hawke's space
// opera, "Heart of Stone", "Midnight" and "Legacy" are a dozen romance franchises
// each. Joining the first same-named series on the chain is what let an unrelated
// author's books SQUAT another author's slots: Campbell's Lost Fleet 4-6 sat at
// Hawke's positions 4-6 and pushed her real volumes out.
//
// So a same-named series is JOINED only when it FITS the row (seriesAuthors.fit):
//
//   - SHARED: a row author credits a member work - by slug, or by a spelling of the
//     same name (personForm.same). When one author DOMINATES the series (credits at
//     least three quarters of its members), the shared author must be a dominant
//     one or have CO-CREDITED a member with one: a minority author who never wrote
//     beside the series' own author is exactly what an earlier squatter looks like,
//     and counting it would let every later volume by the squatter keep squatting,
//     while a co-author (Patterson beside Karp in NYPD Red, a shared world's
//     anthology) has written into the series with its author's own hand. An author
//     is every spelling of one name at once (seriesAuthors.parent), so a person
//     whose records forked into two slugs is not split into two minorities.
//   - OPEN: nothing counts as shared, but the series gives no evidence it belongs
//     to anyone else: fewer than seriesClosedMinMembers members credit an
//     individual, or no author dominates it (a shared universe, an anthology line, a
//     Hoerspiel with rotating writers), or the row's own TITLE names one of the
//     series' own authors ("Robert Ludlum's The Janson Equation", "Ted Bell's
//     Monarch" - a licensed continuation says whose series it is; a title naming
//     an earlier squatter says nothing of the kind), or a member was released by the row's
//     own PUBLISHER - unless that publisher is a catalogue-wide house
//     (largeHouses), which releases everybody's books and so says nothing.
//   - CLOSED otherwise: the row does not join, exactly as it would not join a
//     differently-named holder of the slug.
//
// The thresholds were chosen by measurement over the 2026-09-25 tree; the numbers
// each rests on are quoted beside it.
//
// Refusing a legitimate member costs a same-named sibling series a human can
// merge; admitting a squatter costs a wrong book in another author's series that
// readers see and that displaces the real volume. The asymmetry is the reason the
// rule errs toward refusal.
//
// Collective credits (full-cast, various, anonymous, uncredited, unknown) and the
// catch-all person record state no individual, so they are evidence of nothing on
// either side: a row crediting only them is admitted, and a member crediting only
// them is not counted.

// seriesClosedMinMembers is the fewest members crediting an individual a series
// needs before it can refuse anyone. One member is not a franchise yet: measured,
// about one in seven of the two-member series whose two works share no author are
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

// largePublisherShare sets the catalogue share above which a publisher is a
// catalogue-wide house whose name on two books says nothing about whether they
// belong to one series: more than one work in largePublisherShare - 1%. Over the
// 2026-09-25 tree (279k works) that is the twelve national houses (Tantor,
// Audible Studios, Recorded Books, Podium, Blackstone, Brilliance, Random House,
// Simon & Schuster, Penguin, Macmillan, Listening Library, Dreamscape), and the
// refusals that line adds are measured two thirds squatters; lowering it to 0.4%
// reaches the genre houses whose lines are real shared series (Lubbe's John
// Sinclair, Harlequin, Eins A Medien's Warhammer), where the next band is one
// third. A share rather than a count, so the line tracks a growing catalogue.
const largePublisherShare = 100

// minLargePublisherWorks is the fewest works a publisher needs before it can be
// a catalogue-wide house at all: in a catalogue of a few thousand works the 1%
// line falls to a handful of books, which is a small press's own series, not a
// house that releases everybody's.
const minLargePublisherWorks = 100

// seriesFit is how a series' existing membership receives a row naming it.
type seriesFit int

const (
	// seriesClosed: the series belongs to other authors; the row does not join.
	seriesClosed seriesFit = iota
	// seriesOpen: nothing is shared, but nothing says the series is anyone
	// else's either.
	seriesOpen
	// seriesShared: a row author is one of the series' own authors.
	seriesShared
)

// seriesPerson is one author a row credits: the person slug it resolves to and
// the name it was spelled with.
type seriesPerson struct {
	slug, name string
}

// SeriesRow is what the fit reads about a row naming a series. Build it with
// SeriesRowFor.
type SeriesRow struct {
	authors []seriesPerson
	// titles are the row's title spellings, read for a member author's name.
	titles []string
	// publishers are the row's publisher names.
	publishers []string

	// The comparison forms, computed once per row however many series it is
	// judged against (prepare).
	prepared   bool
	forms      []personForm // the individual authors
	titleSlugs []string     // each title slugged and hyphen-fenced
	pubKeys    []string     // each publisher's publisherKey
}

// SeriesRowFor is the one SeriesRow builder every writer uses: names are the
// row's author credits (already cleaned by the caller's own credit pipeline),
// each at the person slug slugOf resolves it to (nil: the name's own person
// slug); titles are its title spellings and publisher its publisher of record.
func SeriesRowFor(names, titles []string, publisher string, slugOf func(name string) string) *SeriesRow {
	row := &SeriesRow{titles: titles}
	for _, name := range names {
		var slug string
		if slugOf != nil {
			slug = slugOf(name)
		} else {
			slug, _ = personSlug(name)
		}
		row.authors = append(row.authors, seriesPerson{slug: slug, name: name})
	}
	if publisher != "" {
		row.publishers = []string{publisher}
	}
	return row
}

// prepare computes the row's comparison forms once.
func (r *SeriesRow) prepare() {
	if r.prepared {
		return
	}
	r.prepared = true
	for _, a := range r.authors {
		if individualAuthor(a.slug) {
			r.forms = append(r.forms, formOf(a.slug, a.name))
		}
	}
	for _, t := range r.titles {
		if t != "" {
			r.titleSlugs = append(r.titleSlugs, "-"+model.Slugify(t)+"-")
		}
	}
	for _, p := range r.publishers {
		if k := publisherKey(p); k != "" {
			r.pubKeys = append(r.pubKeys, k)
		}
	}
}

// individuals is the row's individual authors in comparison form.
func (r *SeriesRow) individuals() []personForm {
	r.prepare()
	return r.forms
}

// seriesAuthors is the author evidence one series' members carry.
type seriesAuthors struct {
	// members counts the members that credit at least one individual.
	members int
	// works are the keys of the members counted (a catalogued work's id, or a
	// batch row's work identity), so one book is one member however many rows
	// state it.
	works map[string]bool
	// forms is every credited individual slug's comparison form, and order the
	// slugs in first-seen order (the deterministic iteration order).
	forms map[string]personForm
	order []string
	index map[string]int
	// parent groups the slugs into PEOPLE: every spelling of one person
	// (personForm.same) is one group, a spelling that matches two groups bridging
	// them. A group's root is its earliest-seen slug.
	parent map[string]string
	// memberSlugs is each member's distinct individual slugs and memberKeys its
	// key, which is what the co-credit test and a merge read.
	memberSlugs [][]string
	memberKeys  []string
	// publishers is every member recording's publisher key (publisherKey).
	publishers map[string]bool

	// credits (members crediting any spelling in a group, by root) and top (the
	// most any group has) are derived, recomputed when stale.
	stale   bool
	credits map[string]int
	top     int
}

// add records one member: its key ("" for a member counted however often it is
// stated), its individual authors' forms and its publisher keys.
func (sa *seriesAuthors) add(key string, people []personForm, publisherKeys []string) {
	sa.init()
	if key != "" && sa.works[key] {
		return
	}
	var slugs []string
	for _, f := range people {
		if !slices.Contains(slugs, f.slug) {
			sa.addForm(f)
			slugs = append(slugs, f.slug)
		}
	}
	sa.addMember(key, slugs)
	for _, k := range publisherKeys {
		sa.publishers[k] = true
	}
}

func (sa *seriesAuthors) init() {
	if sa.works == nil {
		sa.works = map[string]bool{}
		sa.forms = map[string]personForm{}
		sa.index = map[string]int{}
		sa.parent = map[string]string{}
		sa.publishers = map[string]bool{}
	}
}

// addForm records a credited spelling, joining the group of every person it is
// a spelling of.
func (sa *seriesAuthors) addForm(f personForm) {
	if _, known := sa.forms[f.slug]; known {
		return
	}
	sa.forms[f.slug] = f
	sa.index[f.slug] = len(sa.order)
	sa.parent[f.slug] = f.slug
	for _, s := range sa.order {
		if sa.forms[s].same(f) {
			sa.union(s, f.slug)
		}
	}
	sa.order = append(sa.order, f.slug)
	sa.stale = true
}

// addMember records a member crediting slugs (already added as forms).
func (sa *seriesAuthors) addMember(key string, slugs []string) {
	if key != "" {
		if sa.works[key] {
			return
		}
		sa.works[key] = true
	}
	if len(slugs) == 0 {
		return
	}
	sa.members++
	sa.memberSlugs = append(sa.memberSlugs, slugs)
	sa.memberKeys = append(sa.memberKeys, key)
	sa.stale = true
}

func (sa *seriesAuthors) root(s string) string {
	for sa.parent[s] != s {
		sa.parent[s] = sa.parent[sa.parent[s]]
		s = sa.parent[s]
	}
	return s
}

func (sa *seriesAuthors) union(a, b string) {
	ra, rb := sa.root(a), sa.root(b)
	if ra == rb {
		return
	}
	if sa.index[ra] > sa.index[rb] {
		ra, rb = rb, ra
	}
	sa.parent[rb] = ra
	sa.stale = true
}

// refresh recomputes the per-person credit counts.
func (sa *seriesAuthors) refresh() {
	if !sa.stale && sa.credits != nil {
		return
	}
	sa.credits = map[string]int{}
	sa.top = 0
	for _, m := range sa.memberSlugs {
		for _, r := range sa.rootsOf(m) {
			sa.credits[r]++
			sa.top = max(sa.top, sa.credits[r])
		}
	}
	sa.stale = false
}

// rootsOf is the distinct people a member's slugs name.
func (sa *seriesAuthors) rootsOf(slugs []string) []string {
	var out []string
	for _, s := range slugs {
		if r := sa.root(s); !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// merge adds every spelling other credits and every member it holds that sa does
// not already count.
func (sa *seriesAuthors) merge(other *seriesAuthors) {
	sa.init()
	for _, s := range other.order {
		sa.addForm(other.forms[s])
	}
	for i, slugs := range other.memberSlugs {
		sa.addMember(other.memberKeys[i], slugs)
	}
}

// clone is an independent copy, for evidence a batch extends without touching
// the catalogue's.
func (sa *seriesAuthors) clone() *seriesAuthors {
	out := &seriesAuthors{}
	if sa == nil {
		return out
	}
	out.members = sa.members
	out.works = maps.Clone(sa.works)
	out.forms = maps.Clone(sa.forms)
	out.order = slices.Clone(sa.order)
	out.index = maps.Clone(sa.index)
	out.parent = maps.Clone(sa.parent)
	out.memberSlugs = slices.Clone(sa.memberSlugs)
	out.memberKeys = slices.Clone(sa.memberKeys)
	out.publishers = maps.Clone(sa.publishers)
	out.stale = true
	return out
}

// dominant reports whether n of the members is a dominating share.
func (sa *seriesAuthors) dominant(n int) bool {
	return n*seriesDominantDen >= sa.members*seriesDominantNum
}

// owns reports whether the person rooted at r is one the series belongs to: a
// dominating author, or one who co-credits a member with a dominating author.
func (sa *seriesAuthors) owns(r string) bool {
	sa.refresh()
	if sa.dominant(sa.credits[r]) {
		return true
	}
	for _, m := range sa.memberSlugs {
		roots := sa.rootsOf(m)
		if !slices.Contains(roots, r) {
			continue
		}
		for _, other := range roots {
			if other != r && sa.dominant(sa.credits[other]) {
				return true
			}
		}
	}
	return false
}

// fit judges the series for row; large is the set of catalogue-wide publisher
// keys the publisher arm ignores. A nil receiver is a series with no members.
func (sa *seriesAuthors) fit(row *SeriesRow, large map[string]bool) seriesFit {
	mine := row.individuals()
	if sa == nil || sa.members == 0 || len(mine) == 0 {
		return seriesOpen
	}
	sa.refresh()
	dominated := sa.members >= seriesClosedMinMembers && sa.dominant(sa.top)
	for _, s := range sa.order {
		if !slices.ContainsFunc(mine, sa.forms[s].same) {
			continue
		}
		// In a dominated series a minority author counts as shared only when it
		// co-credits a member with a dominant one: a minority author who never did
		// is what an earlier squatter looks like.
		if !dominated || sa.owns(sa.root(s)) {
			return seriesShared
		}
	}
	if !dominated {
		return seriesOpen
	}
	// The title arm reads the series' OWN authors only: a title naming an
	// earlier squatter says nothing about whose series this is.
	for _, s := range sa.order {
		if titleNamesPerson(row.titleSlugs, sa.forms[s].words) && sa.owns(sa.root(s)) {
			return seriesOpen
		}
	}
	for _, k := range row.pubKeys {
		if !large[k] && sa.publishers[k] {
			return seriesOpen
		}
	}
	return seriesClosed
}

// admits reports whether a batch cluster whose own evidence is add may join the
// series: the fit is not closed, and the join does not hand the series to an
// author it did not already belong to. The second test is what the fit cannot
// ask of one row alone: a series of one Sarah Hawke volume is OPEN to any one
// row, but six Jack Campbell rows arriving together would make Campbell its
// dominant author - the squat, arriving in one batch rather than one row at a
// time - and so would a batch that turns an earlier squatter's one volume into
// the series' majority.
func (sa *seriesAuthors) admits(row *SeriesRow, add *seriesAuthors, large map[string]bool) bool {
	if sa.fit(row, large) == seriesClosed {
		return false
	}
	if sa == nil || sa.members == 0 || add == nil || add.members == 0 {
		return true
	}
	merged := sa.clone()
	merged.merge(add)
	merged.refresh()
	if merged.members < seriesClosedMinMembers {
		return true
	}
	seen := map[string]bool{}
	for _, s := range merged.order {
		r := merged.root(s)
		if seen[r] {
			continue
		}
		seen[r] = true
		if !merged.dominant(merged.credits[r]) {
			continue
		}
		// A person dominating the merged series must be one the series already
		// belonged to (any of their spellings it already credited).
		owned := false
		for _, t := range merged.order {
			if merged.root(t) != r {
				continue
			}
			if _, had := sa.forms[t]; had && sa.owns(sa.root(t)) {
				owned = true
				break
			}
		}
		if !owned {
			return false
		}
	}
	return true
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

// SeriesAuthorIndex is the catalogue's series evidence: every series' member
// authors, and the catalogue-wide publishers the publisher arm ignores. Every
// writer resolves through one (catalogue).
type SeriesAuthorIndex struct {
	series map[string]*seriesAuthors
	large  map[string]bool
}

// NewSeriesAuthorIndex builds the index over cat. A nil catalogue is an empty
// index, under which every series is open.
func NewSeriesAuthorIndex(cat *model.Catalog) *SeriesAuthorIndex {
	return newSeriesAuthorIndex(cat, nil)
}

// newSeriesAuthorIndex builds the index in one pass over the catalogue; names
// maps a person slug to its record's name (nil reads cat.People).
func newSeriesAuthorIndex(cat *model.Catalog, names map[string]string) *SeriesAuthorIndex {
	ix := &SeriesAuthorIndex{series: map[string]*seriesAuthors{}}
	if cat == nil {
		return ix
	}
	if names == nil {
		names = make(map[string]string, len(cat.People))
		for _, p := range cat.People {
			names[p.ID] = p.Name
		}
	}
	keyOf := map[string]string{}
	pubKey := func(name string) string {
		k, ok := keyOf[name]
		if !ok {
			k = publisherKey(name)
			keyOf[name] = k
		}
		return k
	}
	counts := map[string]int{}
	forms := map[string]personForm{}
	works := make(map[string]*model.Work, len(cat.Works))
	pubs := make(map[string][]string, len(cat.Works))
	for _, w := range cat.Works {
		works[w.ID] = w
		keys := workPublisherKeys(w, pubKey)
		pubs[w.ID] = keys
		for _, k := range keys {
			counts[k]++
		}
	}
	for _, s := range cat.Series {
		sa := &seriesAuthors{}
		for _, sw := range s.Works {
			w := works[sw.Work]
			if w == nil {
				continue
			}
			var people []personForm
			for _, a := range w.Authors {
				if !individualAuthor(a) {
					continue
				}
				f, ok := forms[a]
				if !ok {
					f = formOf(a, names[a])
					forms[a] = f
				}
				people = append(people, f)
			}
			sa.add(w.ID, people, pubs[w.ID])
		}
		ix.series[s.ID] = sa
	}
	ix.large = largeHouses(counts, len(cat.Works))
	return ix
}

// catalogue is the resolution's view of the catalogue: stored reports the name a
// series slug holds and reds is the tombstone table. A nil index judges no
// authors (the name-only walk).
func (ix *SeriesAuthorIndex) catalogue(stored func(slug string) (string, bool), reds model.Redirects) seriesCatalogue {
	cat := seriesCatalogue{stored: stored, redirects: reds}
	if ix != nil {
		cat.evidence = func(slug string) *seriesAuthors { return ix.series[slug] }
		cat.large = ix.large
	}
	return cat
}

// workPublisherKeys is every distinct publisher key a catalogued work's
// recordings name.
func workPublisherKeys(w *model.Work, key func(string) string) []string {
	var out []string
	add := func(name string) {
		if k := key(name); k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for _, r := range w.Recordings {
		if r.Publisher != "" {
			add(r.Publisher)
		}
		for _, rp := range r.Publishers {
			add(rp.Publisher)
		}
	}
	return out
}

// largeHouses is the set of publisher keys holding at least
// minLargePublisherWorks works and more than one work in largePublisherShare of
// the total: the catalogue-wide houses.
func largeHouses(counts map[string]int, total int) map[string]bool {
	out := map[string]bool{}
	for k, n := range counts {
		if n >= minLargePublisherWorks && n*largePublisherShare > total {
			out[k] = true
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

// titleNamesPerson reports whether any of the hyphen-fenced title slugs spells
// the whole of a multi-word person name ("Robert Ludlum's The Janson Equation"
// names Robert Ludlum). One-word names are never read: a title holding "Tiye" or
// "Drako" says nothing about who wrote it.
func titleNamesPerson(titleSlugs []string, words []string) bool {
	if len(words) < 2 {
		return false
	}
	// Slugify drops an apostrophe rather than splitting on it, so the possessive
	// the continuations are titled with reads "robert-blochs".
	joined := "-" + strings.Join(words, "-")
	for _, slug := range titleSlugs {
		if strings.Contains(slug, joined+"-") || strings.Contains(slug, joined+"s-") {
			return true
		}
	}
	return false
}

// personForm is a name in the forms the identity rungs compare, computed once.
type personForm struct {
	slug, marked, fold string
	words              []string
	tokens             []nameToken
}

// formOf computes a name's comparison forms. The words drop a " - role" tail, a
// leading courtesy title and every credential, generational or legal-entity
// suffix - the same folds the importer's credit cleaning applies to a name
// (honorific.go, credential.go, suffixpiece.go), so the fit never tells apart two
// spellings the importer would have made one. The fold is titlerule.FoldKey of
// the cleaned name, read off the one Slugify the words are split from.
func formOf(slug, name string) personForm {
	cleaned := cleanedPersonName(name)
	slugged := model.Slugify(cleaned)
	f := personForm{
		slug:   slug,
		fold:   strings.ReplaceAll(slugged, "-", ""),
		words:  strings.FieldsFunc(slugged, func(r rune) bool { return r == '-' }),
		tokens: parseNameTokens(cleaned),
	}
	if name != "" {
		f.marked = markedKey(name)
	}
	return f
}

// MinPersonEditLen is the floor for a one-edit match between two folded names:
// below it, one edit is a different short name more often than a typo. It is
// P-DUP's floor too (internal/audit).
const MinPersonEditLen = 8

// same reports whether two authors may be one person. It reads the identity rungs
// the codebase already trusts: one person slug, one initials key (MarkedNameKey -
// "A.B. Kovacs" / "AB Kovacs"), one folded spelling, a one-edit typo over the
// WHOLE folded name (internal/audit's P-DUP rung), and a middle-name insertion
// with the first and last words fixed (MiddleNameVariant: "Cheree Alsop" /
// "Cheree Lynn Alsop"); and one more: INITIALS standing for the words they begin
// ("L. Frank Baum" / "Lyman Frank Baum", "Robert E. Howard" / "Robert Ervin
// Howard" - initialExpansion). Two WORDS sharing an initial ("Jack Campbell" /
// "Joseph Campbell", "James Patterson" / "Jennifer Patterson") and a one-word
// name matching an end of another are deliberately NOT rungs: they admitted
// strangers.
func (a personForm) same(b personForm) bool {
	if a.slug != "" && a.slug == b.slug {
		return true
	}
	if a.fold == "" || b.fold == "" {
		return false
	}
	if a.marked != "" && a.marked == b.marked {
		return true
	}
	if a.fold == b.fold {
		return true
	}
	if len(a.fold) >= MinPersonEditLen && len(b.fold) >= MinPersonEditLen && titlerule.OneEditApart(a.fold, b.fold) {
		return true
	}
	return MiddleNameVariant(a.words, b.words) || initialExpansion(a.tokens, b.tokens) || initialExpansion(b.tokens, a.tokens)
}

// initialExpansion reports whether a spells b with INITIALS where b has words.
// Every token before the surname is read as a sequence of units - each letter of
// an initials group one unit, each word one unit - and the two sequences must
// line up one to one: a unit is either identical on both sides or an initial of
// a's standing for a word of b's beginning with that letter, at least one unit is
// such an expansion, and the surnames are the same word. So "L. Frank Baum" is
// "Lyman Frank Baum" and "J.F. Holmes" is "John F. Holmes", but "J.F. Holmes" is
// not "Jane Holmes" (the F consumes nothing) and "SJ Bennett" is not "Sophia
// Bennett". It never makes two different WORDS one person.
func initialExpansion(a, b []nameToken) bool {
	if len(a) < 2 || len(b) < 2 {
		return false
	}
	la, lb := a[len(a)-1], b[len(b)-1]
	if la.initials || lb.initials || la.letters != lb.letters {
		return false
	}
	ua, ub := nameUnits(a[:len(a)-1]), nameUnits(b[:len(b)-1])
	if len(ua) != len(ub) {
		return false
	}
	expanded := false
	for i := range ua {
		switch {
		case ua[i] == ub[i]:
		case ua[i].initial && !ub[i].initial && firstRune(ub[i].text) == firstRune(ua[i].text):
			expanded = true
		default:
			return false
		}
	}
	return expanded
}

// nameUnit is one letter of an initials group, or one word.
type nameUnit struct {
	text    string
	initial bool
}

// nameUnits splits name tokens into units: an initials group into its letters.
func nameUnits(toks []nameToken) []nameUnit {
	var out []nameUnit
	for _, t := range toks {
		if !t.initials {
			out = append(out, nameUnit{text: t.letters})
			continue
		}
		for _, r := range t.letters {
			out = append(out, nameUnit{text: string(r), initial: true})
		}
	}
	return out
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

// MiddleNameVariant reports whether two word lists differ only by MIDDLE words:
// both open and close on the same word and the shorter's words appear in order in
// the longer ("Cheree Alsop" / "Cheree Lynn Alsop"). The first and last word must
// both match, which is what keeps it off the resemblance a surname alone is. It is
// the one rule internal/audit's series vetoes read too, over their own words.
func MiddleNameVariant(wa, wb []string) bool {
	if len(wa) < 2 || len(wb) < 2 || len(wa) == len(wb) {
		return false
	}
	if len(wa) > len(wb) {
		wa, wb = wb, wa
	}
	if wa[0] != wb[0] || wa[len(wa)-1] != wb[len(wb)-1] {
		return false
	}
	i := 0
	for _, w := range wb {
		if i < len(wa) && wa[i] == w {
			i++
		}
	}
	return i == len(wa)
}

// nameSeparators turns the punctuation that separates a name's words into spaces.
var nameSeparators = strings.NewReplacer(",", " ", "&", " ", "/", " ")

// cleanedPersonName is a name with a " - role" tail, a leading courtesy title
// (deHonorified: "Dr. Samuel Li") and every suffix word dropped; commas,
// ampersands and slashes separate words.
func cleanedPersonName(name string) string {
	if i := strings.Index(name, " - "); i > 0 {
		name = name[:i]
	}
	if bare, stripped := deHonorified(name); stripped {
		name = bare
	}
	var kept []string
	for _, tok := range strings.Fields(nameSeparators.Replace(name)) {
		if !isSuffixPiece(tok) {
			kept = append(kept, tok)
		}
	}
	return strings.Join(kept, " ")
}
