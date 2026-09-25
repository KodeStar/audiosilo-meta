package importer

import (
	"slices"
	"strings"

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
// author's books SQUAT another author's slots - Campbell's Lost Fleet 4-6 sat at
// Hawke's positions 4-6 and pushed her real volumes out, and the 2026-08 sweeps
// (Omega Force, Quantum, Reawakened, Wicked Fae, Delirium, Das Marsprojekt, Carlisle
// Emergency, Conan, smoke, tangled, ...) were all this one mechanism.
//
// So a same-named series is JOINED only when it FITS the row (SeriesAuthors.fit):
//
//   - SHARED: a row author credits a member work - by slug, or by a spelling of the
//     same name (personForm.same). When one author DOMINATES the series (credits at
//     least three quarters of its members), the shared author must be a dominant
//     one or have CO-CREDITED a member with one: a minority author who never wrote
//     beside the series' own author is exactly what an earlier squatter looks like,
//     and counting it would let every later volume by the squatter keep squatting,
//     while a co-author (Patterson beside Karp in NYPD Red, a shared world's
//     anthology) has written into the series with its author's own hand.
//   - OPEN: nothing counts as shared, but the series gives no evidence it belongs
//     to anyone else: fewer than seriesClosedMinMembers members credit an
//     individual, or no author dominates it (a shared universe, an anthology line, a
//     Hoerspiel with rotating writers), or the row's own TITLE names a member author
//     ("Robert Ludlum's The Janson Equation", "Ted Bell's Monarch" - a licensed
//     continuation says whose series it is), or a member was released by the row's
//     own PUBLISHER - unless that publisher is a catalogue-wide house
//     (largePublishersOf), which releases everybody's books and so says nothing.
//   - CLOSED otherwise: the row does not join, exactly as it would not join a
//     differently-named holder of the slug.
//
// The rule was chosen by MEASUREMENT over the 2026-09-25 tree - see the PR that
// introduced it (#2371) for the full tables and the hand-labelled sample; the
// numbers the thresholds rest on are quoted beside each constant.
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
// band it closes is measured two thirds squatters; lowering it to 0.4% reaches
// the genre houses whose lines are real shared series (Lubbe's John Sinclair,
// Harlequin, Eins A Medien's Warhammer), where the next band is one third. A
// share rather than a count, so the line tracks a growing catalogue.
var largePublisherShare = 100

// SeriesFit is how a series' existing membership receives a row naming it.
type SeriesFit int

const (
	// SeriesClosed: the series belongs to other authors; the row does not join.
	SeriesClosed SeriesFit = iota
	// SeriesOpen: nothing is shared, but nothing says the series is anyone
	// else's either.
	SeriesOpen
	// SeriesShared: a row author is one of the series' own authors.
	SeriesShared
)

// SeriesPerson is one author a row credits: the person slug it resolves to and
// the name it was spelled with.
type SeriesPerson struct {
	Slug, Name string
}

// SeriesRow is what the fit reads about a row naming a series. Build it with
// SeriesRowOf (the importer and libex-select) or by hand (the intake form).
type SeriesRow struct {
	Authors []SeriesPerson
	// Titles are the row's title spellings, read for a member author's name.
	Titles []string
	// Publishers are the row's publisher names.
	Publishers []string

	forms    []personForm // the individual authors' comparison forms, once
	prepared bool
}

// individuals is the row's individual authors in comparison form, computed once
// per row however many series it is judged against.
func (r *SeriesRow) individuals() []personForm {
	if !r.prepared {
		for _, a := range r.Authors {
			if individualAuthor(a.Slug) {
				r.forms = append(r.forms, formOf(a.Slug, a.Name))
			}
		}
		r.prepared = true
	}
	return r.forms
}

// SeriesAuthors is the author evidence one series' members carry.
type SeriesAuthors struct {
	// members counts the members that credit at least one individual.
	members int
	// credits maps an individual author slug to the members crediting it.
	credits map[string]int
	// forms is each credited slug's comparison form, computed when it is added.
	forms map[string]personForm
	// publishers is every member recording's publisher key (publisherKey).
	publishers map[string]bool
	// memberAuthors is each member's individual author slugs, which is what the
	// co-credit test reads.
	memberAuthors [][]string
}

// add records one member work: its authors and its recordings' publishers.
func (sa *SeriesAuthors) add(authors []SeriesPerson, publishers []string) {
	if sa.credits == nil {
		sa.credits = map[string]int{}
		sa.forms = map[string]personForm{}
		sa.publishers = map[string]bool{}
	}
	seen := map[string]bool{}
	var member []string
	for _, a := range authors {
		if !individualAuthor(a.Slug) || seen[a.Slug] {
			continue
		}
		seen[a.Slug] = true
		member = append(member, a.Slug)
		sa.credits[a.Slug]++
		if _, known := sa.forms[a.Slug]; !known {
			sa.forms[a.Slug] = formOf(a.Slug, a.Name)
		}
	}
	if len(member) > 0 {
		sa.members++
		sa.memberAuthors = append(sa.memberAuthors, member)
	}
	for _, p := range publishers {
		if k := publisherKey(p); k != "" {
			sa.publishers[k] = true
		}
	}
}

// clone is an independent copy, for evidence a batch extends without touching
// the catalogue's.
func (sa *SeriesAuthors) clone() *SeriesAuthors {
	out := &SeriesAuthors{}
	if sa == nil {
		return out
	}
	out.members = sa.members
	out.credits = make(map[string]int, len(sa.credits))
	for k, v := range sa.credits {
		out.credits[k] = v
	}
	out.forms = make(map[string]personForm, len(sa.forms))
	for k, v := range sa.forms {
		out.forms[k] = v
	}
	out.publishers = make(map[string]bool, len(sa.publishers))
	for k := range sa.publishers {
		out.publishers[k] = true
	}
	out.memberAuthors = append([][]string(nil), sa.memberAuthors...)
	return out
}

// coCredits reports whether slug shares a member with an author holding a
// dominating share of the series.
func (sa *SeriesAuthors) coCredits(slug string) bool {
	for _, m := range sa.memberAuthors {
		if !slices.Contains(m, slug) {
			continue
		}
		for _, other := range m {
			if other != slug && sa.dominant(sa.credits[other]) {
				return true
			}
		}
	}
	return false
}

// dominant reports whether n of the members is a dominating share.
func (sa *SeriesAuthors) dominant(n int) bool {
	return n*seriesDominantDen >= sa.members*seriesDominantNum
}

// fit judges the series for row; large is the set of catalogue-wide publisher
// keys the publisher arm ignores. A nil receiver is a series with no members.
func (sa *SeriesAuthors) fit(row *SeriesRow, large map[string]bool) SeriesFit {
	mine := row.individuals()
	if sa == nil || sa.members == 0 || len(mine) == 0 {
		return SeriesOpen
	}
	top := 0
	for _, n := range sa.credits {
		top = max(top, n)
	}
	dominated := sa.members >= seriesClosedMinMembers && sa.dominant(top)
	for slug, n := range sa.credits {
		f := sa.forms[slug]
		for _, a := range mine {
			// In a dominated series a minority author counts as shared only when
			// it co-credits a member with a dominant one: a minority author who
			// never did is what an earlier squatter looks like.
			if a.same(f) && (!dominated || sa.dominant(n) || sa.coCredits(slug)) {
				return SeriesShared
			}
		}
	}
	if !dominated {
		return SeriesOpen
	}
	for slug := range sa.credits {
		if titleNamesPerson(row.Titles, sa.forms[slug].words) {
			return SeriesOpen
		}
	}
	for _, p := range row.Publishers {
		if k := publisherKey(p); k != "" && !large[k] && sa.publishers[k] {
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

// seriesAuthorsOf reads every catalogued series' author evidence.
func seriesAuthorsOf(cat *model.Catalog) map[string]*SeriesAuthors {
	names := make(map[string]string, len(cat.People))
	for _, p := range cat.People {
		names[p.ID] = p.Name
	}
	works := make(map[string]*model.Work, len(cat.Works))
	for _, w := range cat.Works {
		works[w.ID] = w
	}
	out := make(map[string]*SeriesAuthors, len(cat.Series))
	for _, s := range cat.Series {
		sa := &SeriesAuthors{}
		for _, sw := range s.Works {
			if w := works[sw.Work]; w != nil {
				sa.add(peopleOf(w.Authors, names), workPublishers(w))
			}
		}
		out[s.ID] = sa
	}
	return out
}

// peopleOf pairs author slugs with the names their records carry.
func peopleOf(slugs []string, names map[string]string) []SeriesPerson {
	out := make([]SeriesPerson, len(slugs))
	for i, s := range slugs {
		out[i] = SeriesPerson{Slug: s, Name: names[s]}
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

// largePublishersOf is the set of publisher keys holding more than one work in
// largePublisherShare of the catalogue: the catalogue-wide houses.
func largePublishersOf(cat *model.Catalog) map[string]bool {
	counts := map[string]int{}
	for _, w := range cat.Works {
		seen := map[string]bool{}
		for _, p := range workPublishers(w) {
			if k := publisherKey(p); k != "" && !seen[k] {
				seen[k] = true
				counts[k]++
			}
		}
	}
	limit := len(cat.Works) / largePublisherShare
	out := map[string]bool{}
	for k, n := range counts {
		if n > limit {
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

// titleNamesPerson reports whether any of titles spells the whole of a
// multi-word person name ("Robert Ludlum's The Janson Equation" names Robert
// Ludlum). One-word names are never read: a title holding "Tiye" or "Drako" says
// nothing about who wrote it.
func titleNamesPerson(titles []string, words []string) bool {
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
// spellings the importer would have made one.
func formOf(slug, name string) personForm {
	cleaned := cleanedPersonName(name)
	words := nameWords(cleaned)
	f := personForm{slug: slug, fold: strings.Join(words, ""), words: words, tokens: parseNameTokens(cleaned)}
	if name != "" {
		f.marked = markedKey(name)
	}
	return f
}

// minPersonEditLen is P-DUP's floor for a one-edit match between two folded
// names (internal/audit minEditDistanceLen): below it, one edit is a different
// short name more often than a typo.
const minPersonEditLen = 8

// same reports whether two authors may be one person. It reads the identity rungs
// the codebase already trusts: one person slug, one initials key (MarkedNameKey -
// "A.B. Kovacs" / "AB Kovacs"), one folded spelling, a one-edit typo over the
// WHOLE folded name (internal/audit's P-DUP rung), and a middle-name insertion
// with the first and last words fixed (internal/audit's middleNameVariant:
// "Cheree Alsop" / "Cheree Lynn Alsop"); and one more, measured over the tree:
// an INITIAL standing for a word it begins ("J.F. Holmes" / "John Holmes", "L.
// Frank Baum" / "Lyman Frank Baum", "Robert E. Howard" / "Robert Ervin Howard" -
// initialExpansion). Two WORDS sharing an initial ("Jack Campbell" / "Joseph
// Campbell", "James Patterson" / "Jennifer Patterson") and a one-word name
// matching an end of another are deliberately NOT rungs: they admitted strangers.
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
	if len(a.fold) >= minPersonEditLen && len(b.fold) >= minPersonEditLen && titlerule.OneEditApart(a.fold, b.fold) {
		return true
	}
	return middleNameVariant(a.words, b.words) || initialExpansion(a.tokens, b.tokens) || initialExpansion(b.tokens, a.tokens)
}

// initialExpansion reports whether a spells b with an INITIAL where b has a
// word beginning with that letter, the surname identical: a leading initials
// group against b's first word ("J.F. Holmes" / "John Holmes", "SJ Bennett" /
// "Sophia Bennett"), or, with the first and last words identical, middle
// initials against middle words ("Robert E. Howard" / "Robert Ervin Howard").
// Measured over the 2026-09-25 tree: of the 14 spellings of one author that
// dropping the surname-plus-initial rung split apart, it reads 10 back, and it
// re-admits none of the squatters the stricter rungs caught. It never makes two
// different WORDS one person.
func initialExpansion(a, b []nameToken) bool {
	if len(a) < 2 || len(b) < 2 {
		return false
	}
	la, lb := a[len(a)-1], b[len(b)-1]
	if la.initials || lb.initials || la.letters != lb.letters {
		return false
	}
	if a[0].initials && !b[0].initials {
		return a[0].letters[0] == b[0].letters[0]
	}
	if len(a) != len(b) || len(a) < 3 || a[0].initials || b[0].initials || a[0].letters != b[0].letters {
		return false
	}
	expanded := false
	for i := 1; i < len(a)-1; i++ {
		switch {
		case a[i].letters == b[i].letters && a[i].initials == b[i].initials:
		case a[i].initials && !b[i].initials && len(a[i].letters) == 1 && a[i].letters[0] == b[i].letters[0]:
			expanded = true
		default:
			return false
		}
	}
	return expanded
}

// middleNameVariant reports whether two word lists differ only by MIDDLE words:
// both open and close on the same word and the shorter's words appear in order
// in the longer. It is internal/audit's rule of the same name over the same
// folded words.
func middleNameVariant(wa, wb []string) bool {
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
	for _, tok := range strings.Fields(strings.NewReplacer(",", " ", "&", " ", "/", " ").Replace(name)) {
		if !isSuffixPiece(tok) {
			kept = append(kept, tok)
		}
	}
	return strings.Join(kept, " ")
}

// nameWords is a cleaned name's words in slug form; dots and hyphens separate
// words too.
func nameWords(name string) []string {
	var out []string
	for _, tok := range strings.Fields(name) {
		for _, w := range strings.Split(model.Slugify(tok), "-") {
			if w != "" {
				out = append(out, w)
			}
		}
	}
	return out
}

// samePersonName is personForm.same over two bare names.
func samePersonName(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	sa, _ := model.PersonSlug(a)
	sb, _ := model.PersonSlug(b)
	return formOf(sa, a).same(formOf(sb, b))
}
