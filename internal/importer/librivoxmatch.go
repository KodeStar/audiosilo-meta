package importer

// librivoxmatch.go is the LibriVox import's WORK rule (meta#2743), on top of the
// planner's ordinary identity walk. It decides, for one LibriVox row the
// admission tests let through, which work its recording lands on:
//
//  1. THE ORDINARY WALK FIRST. A title whose slug chain holds a work by the same
//     authors in a compatible language is that work, exactly as for every other
//     source (resolveWork).
//  2. SPELLING VARIANTS. Otherwise the row attaches to the one work whose title
//     agrees with its own once a leading article (English, French, German,
//     Spanish, Italian, Dutch: librivoxArticles) and punctuation, case and
//     diacritics are set aside (librivoxTitleKey) - "The Metamorphosis" and
//     "Metamorphosis" by Franz Kafka are one book - when that work is by the
//     same authors (matchWork) in a compatible language; a volume number is
//     one key however it is spelled ("Vol. 1 of 3", "Volume 1":
//     librivoxVolumeSpelling), but two titles stating different totals ("Vol.
//     1 of 3", "Vol. 1 of 5") are two volume sets, never one work
//     (librivoxTitleWorks). LibriVox's catalogue is
//     public-domain texts whose titles differ from a retailer's mostly by exactly
//     that, so the duplicate-identity guard's "skip, never merge" would keep a
//     large share of the mirror out for want of an article.
//  3. STILL AMBIGUOUS IS A WORKLIST SKIP. Two or more such works (several-works),
//     or a work whose authors only partly agree with the row's
//     (author-unmatched), decide nothing: the row is skipped and listed in
//     Summary.LibrivoxWorklist for a person to resolve, never guessed. So is a
//     row the duplicate-identity guard refuses (differently-spelled-title: a
//     coarser match, a subtitle or decoration apart, which this rule does not
//     read as the same title). Before author-unmatched, a work by an author the
//     row spells with more or fewer words is attached, and one by an author it
//     only loosely resembles is listed author-spelling (librivoxauthors.go rule
//     1). A credit whose name slugs to nothing reaches this file only filed
//     by its LibriVox id (librivoxauthors.go rule 2), so no agreement can rest
//     on the catch-all person such a name would fall back to.
//  4. NON-LATIN TITLES. A title that slugs to nothing (Cyrillic, Greek, CJK...),
//     or whose letters are mostly in such a script (librivoxNonLatinTitle),
//     never walks a slug chain: the shared "untitled" one would file every such
//     title by one author as one work, and a Latin or number fragment's chain
//     would file it beside an unrelated book. It is matched by rule 2 alone
//     (its key keeps the letters of its own script), and otherwise creates a
//     new work at `librivox-<project id>`, titled as LibriVox writes it. A row
//     of any other source with that title reaches the work by the same key,
//     as resolveWork's last merge-only step (librivoxScriptWalk), since no slug
//     chain does.
//  5. A TRAILING QUALIFIER IS A WORKLIST SKIP. A Latin title about to create a
//     work, which rule 2 would match once its trailing parenthetical or
//     bracketed qualifiers are removed ("Frankenstein (1818)", "Black Beauty
//     (The Autobiography of a Horse)"), is listed differently-spelled-title
//     (several-works for more than one match), never attached: the qualifier
//     can as well be a volume ("King in Yellow (part 1)"). librivoxQualifiedTwin.
//
// A work by a DIFFERENT author under the same title is a different book: rule 1
// walks past it and rule 2 does not list it, so the row creates a work of its
// own, as decision 4 of LIBRIVOX-PLAN.md says.

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The worklist reasons (LibrivoxWorklistEntry.Reason). Stable codes: a person
// working through a tranche's list sorts and filters on them.
const (
	// LibrivoxSeveralWorks: two or more catalogued works by the row's authors
	// carry its title once articles and punctuation are set aside.
	LibrivoxSeveralWorks = "several-works"
	// LibrivoxAuthorUnmatched: a work carries the row's title, but its authors
	// only partly agree with the row's.
	LibrivoxAuthorUnmatched = "author-unmatched"
	// LibrivoxAuthorSpelling: a work carries the row's title by an author of
	// the same surname whose name only loosely agrees with the row's (an
	// initial, another given name), or alongside other candidates
	// (librivoxauthors.go rule 1).
	LibrivoxAuthorSpelling = "author-spelling"
	// LibrivoxAuthorUnsluggable: every individual author the row credits has a
	// name that slugs to nothing and no LibriVox author id to be filed by
	// (librivoxauthors.go rule 2); no candidates.
	LibrivoxAuthorUnsluggable = "author-unsluggable"
	// LibrivoxAuthorSlotTaken: as author-unsluggable, but an author's
	// LibriVox id could have filed them at librivox-author-<id> and another
	// person or a tombstone holds that slot (librivoxpeople.go rule 4).
	LibrivoxAuthorSlotTaken = "author-slot-taken"
	// LibrivoxDifferentlySpelled: the duplicate-identity guard (dupidentity.go)
	// found the row's book under a title this rule does not read as the same
	// (a subtitle or a decoration apart).
	LibrivoxDifferentlySpelled = "differently-spelled-title"
	// LibrivoxFallbackTaken: the row's title is non-Latin (librivoxNonLatinTitle)
	// and its `librivox-<id>` work slug is already taken (or retired) by another
	// record.
	LibrivoxFallbackTaken = "fallback-slug-taken"
	// LibrivoxVolumeTotal: a work carries the row's title (rule 2), by its
	// authors (or a spelling of them, or sharing one: rule 1), but a LibriVox
	// recording already on it states another volume total than the row's
	// title ("Vol. 1 of 3" on a work titled "Volume 1", the row "Vol. 1 of
	// 5"): the work may be either volume set.
	LibrivoxVolumeTotal = "volume-total"
	// LibrivoxSameNameAuthor: an author id no person carries meets, by name, a
	// catalogued person who may be someone else, and only recording release
	// dates say so (librivoxpeople.go rule 2), or no record of its own can be
	// minted (a collective's name, a taken slot: rule 4); the candidates are
	// that person's works. On a project the catalogue already holds it is
	// a translator id left uncredited on the recording: held so, or found the
	// same person as a record carrying ANOTHER author id
	// (worklistHeldLibrivoxTranslators).
	LibrivoxSameNameAuthor = "same-name-author"
	// LibrivoxReviewedWorkGone: a reviewed attach names a work that is no longer
	// live, nor retired onto one (librivoxreviewed.go).
	LibrivoxReviewedWorkGone = "reviewed-work-gone"
	// LibrivoxReviewedTitleChanged: a reviewed decision states another title than the
	// project now has: LibriVox reuses project numbers (librivoxreviewed.go).
	LibrivoxReviewedTitleChanged = "reviewed-title-changed"
	// LibrivoxReviewedOtherLanguage: a reviewed attach names a work in another
	// language than the project's: a work is language-scoped (librivoxreviewed.go).
	LibrivoxReviewedOtherLanguage = "reviewed-other-language"
	// LibrivoxReviewedOwnWorkTaken: a reviewed own-work's walk reaches a work it did
	// not found, which may be the one it was ruled apart from (librivoxreviewed.go).
	LibrivoxReviewedOwnWorkTaken = "reviewed-own-work-taken"
)

// librivoxReviewedReasonPrefix starts the reasons a reviewed decision itself refuses
// under, as opposed to a refusal that shadows the decision (noteLibrivoxWorklist).
const librivoxReviewedReasonPrefix = "reviewed-"

// LibrivoxWorklistEntry is one LibriVox project a run skipped for a person to
// resolve: re-importing its id once the catalogue says which work it is (or
// that it is a new one) lands it - or, for a project the catalogue already
// holds, credits the translator its recording was left without.
type LibrivoxWorklistEntry struct {
	Project    int                 `json:"librivox_id"`
	Title      string              `json:"title"`
	Authors    []string            `json:"authors"`
	Reason     string              `json:"reason"`
	Candidates []LibrivoxCandidate `json:"candidates"`
}

// LibrivoxCandidate is a catalogued work a worklist entry names.
type LibrivoxCandidate struct {
	Work  string `json:"work"`
	Title string `json:"title"`
}

// String is the entry as the import report prints it, one line:
//
//	527 "The Metamorphosis" by Franz Kafka [several-works]: metamorphosis "Metamorphosis"; metamorphosis-2 "Metamorphosis"
func (e LibrivoxWorklistEntry) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %q", e.Project, e.Title)
	if len(e.Authors) > 0 {
		b.WriteString(" by " + strings.Join(e.Authors, ", "))
	}
	b.WriteString(" [" + e.Reason + "]")
	for i, c := range e.Candidates {
		if i == 0 {
			b.WriteString(": ")
		} else {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s %q", c.Work, c.Title)
	}
	return b.String()
}

// librivoxArticles are the leading articles librivoxTitleKey sets aside: the
// English ones and the common French, German, Spanish, Italian and Dutch ones
// (the elided French "l'" is handled beside them). Read whatever the row's
// language: the match also needs the same authors and a compatible language, so
// an article read in the wrong language costs nothing a title by the same author
// could confuse.
var librivoxArticles = map[string]bool{
	"the": true, "a": true, "an": true,
	"le": true, "la": true, "les": true,
	"der": true, "die": true, "das": true,
	"el": true, "los": true, "las": true,
	"il": true, "lo": true,
	"de": true, "het": true, "een": true,
}

// librivoxTitleKey is a title's spelling-variant key: one leading article
// dropped (never the only word), then case, punctuation, spacing and diacritics
// folded away over the WHOLE title (no slug cap, so two long titles that differ
// only near their end stay apart). The one separator kept is the one between two
// numbers, so "Vol. 1, 2" and "Vol. 12" stay apart as their slugs do. A
// non-Latin title (librivoxNonLatinTitle) keys by its own letters, digits and
// marks (librivoxScriptKey), so it still meets its own spelling. "" for a title
// with no letter or digit at all.
func librivoxTitleKey(title string) string {
	key, _ := librivoxTitleKeyOf(title)
	return key
}

// librivoxTitleKeyOf is librivoxTitleKey together with whether the title is
// non-Latin, both judged after the one article drop, so the index and the
// routing can never read one title two ways ("The 論語" is non-Latin).
func librivoxTitleKeyOf(title string) (key string, nonLatin bool) {
	t := dropLibrivoxArticle(strings.TrimSpace(title))
	if mostlyNonLatinLetters(t) {
		return librivoxScriptKey(t), true
	}
	if s := model.SlugifyWhole(t); s != "" {
		return joinSlugWords(librivoxVolumeSpelling(s)), false
	}
	return librivoxScriptKey(t), true
}

// librivoxVolumeRE is a volume number in a whole-title slug, "vol" or
// "volume" with an optional "of <total>" after it. Bounded by \b rather than
// by consuming the hyphens around it, so two markers in a row ("vol-1-vol-2")
// are both found.
var librivoxVolumeRE = regexp.MustCompile(`\bvol(?:ume)?-(\d+)(?:-of-(\d+))?\b`)

// librivoxVolumeSpelling is a whole-title slug with every volume number
// spelled one way, "volume-<n>": "Vol. 1 of 3" and "Volume 1" are the same
// volume (meta#2778: 9711 "World as Will and Idea, Vol. 1 of 3" minted a work
// beside the catalogued "The World as Will and Idea, Volume 1"). Only the
// spelling folds - the number never does, so "Vol. 1" and "Vol. 2" stay apart.
// The total ("of 3") is dropped from the key, but not from the decision: two
// titles stating DIFFERENT totals for a volume are two volume sets
// (librivoxVolumeTotalsAgree, asked by every lookup of the key,
// librivoxTitleWorks).
func librivoxVolumeSpelling(slug string) string {
	if !strings.Contains(slug, "vol") {
		return slug
	}
	return librivoxVolumeRE.ReplaceAllString(slug, "volume-${1}")
}

// librivoxVolumeTotals is the total each volume marker of a Latin title states,
// in order, 0 for a marker stating none ("Vol. 1 of 3, Vol. 2" is [3 0]); nil
// for a title with no marker.
func librivoxVolumeTotals(title string) []int {
	s := model.SlugifyWhole(dropLibrivoxArticle(strings.TrimSpace(title)))
	if !strings.Contains(s, "vol") {
		return nil
	}
	var totals []int
	for _, m := range librivoxVolumeRE.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[2])
		totals = append(totals, n)
	}
	return totals
}

// librivoxVolumeTotalsAgree reports whether two titles of one key state no
// conflicting volume total: a marker stating a total matches one stating none
// ("Vol. 1 of 3" and "Volume 1"), never one stating another ("Vol. 1 of 3"
// and "Vol. 1 of 5": volume 1 of two different divisions of the text, which a
// match would merge). The key folds the totals away, so the markers line up
// by position.
func librivoxVolumeTotalsAgree(a, b string) bool {
	return librivoxTotalsAgree(librivoxVolumeTotals(a), librivoxVolumeTotals(b))
}

// librivoxTotalsAgree is librivoxVolumeTotalsAgree over two titles' totals
// (librivoxVolumeTotals), so a lookup reads its own title's once.
func librivoxTotalsAgree(ta, tb []int) bool {
	for i := range min(len(ta), len(tb)) {
		if ta[i] != 0 && tb[i] != 0 && ta[i] != tb[i] {
			return false
		}
	}
	return true
}

// librivoxStatesTotal reports whether any volume marker states a total.
func librivoxStatesTotal(totals []int) bool {
	return slices.ContainsFunc(totals, func(n int) bool { return n != 0 })
}

// librivoxTitleWorks is the spelling-variant lookup every rule asks: the works
// under title's key (librivoxTitleIndex) whose volume totals agree with the
// title's, in slug order, with whether the title is non-Latin. A work's totals
// are its own title's AND those of the LibriVox recordings already on it
// (workState.volumeTotals: a catalogued recording's librivox_title, or a row of
// this run that landed there), so a work titled "Volume 1" that holds "Vol. 1
// of 3" refuses "Vol. 1 of 5" in one run as in two. held is the works of the
// key refused ONLY by a recording's total, not by their own title: the row
// may be either volume set, and routeLibrivox lists it (volume-total) rather
// than minting a work beside them.
func (p *planner) librivoxTitleWorks(title string) (slugs, held []string, nonLatin bool) {
	key, nonLatin := librivoxTitleKeyOf(title)
	slugs = p.librivoxTitleIndex()[key]
	if nonLatin {
		return slugs, nil, nonLatin
	}
	// A title stating no total agrees with every work, so only one stating
	// one filters (and its totals are read once, not per candidate).
	totals := librivoxVolumeTotals(title)
	if !librivoxStatesTotal(totals) {
		return slugs, nil, nonLatin
	}
	var out []string
	for _, slug := range slugs {
		ws := p.works[slug]
		if ws == nil || !librivoxTotalsAgree(totals, librivoxVolumeTotals(ws.title)) {
			continue
		}
		if ws.recordingTotalsAgree(totals) {
			out = append(out, slug)
		} else {
			held = append(held, slug)
		}
	}
	return out, held, nonLatin
}

// recordingTotalsAgree reports whether every LibriVox recording on ws states
// volume totals agreeing with totals (workState.volumeTotals).
func (ws *workState) recordingTotalsAgree(totals []int) bool {
	for _, t := range ws.volumeTotals {
		if !librivoxTotalsAgree(totals, t) {
			return false
		}
	}
	return true
}

// librivoxTitleWorksAll is librivoxTitleWorks with the works a recording's
// total holds back put back, in slug order: what a rule asks that never
// attaches (rule 5 only refuses) or asks WHO wrote a text rather than which
// division of it a work is (the edition-date evidence), for which a volume
// set held apart by its recordings is the same title as it was before they
// said so. The author-variant keying (keyLibrivoxAuthorVariants) asks
// librivoxTitleWorks itself: it proposes only what routeLibrivox decides, and
// a held work is a worklist row there, never an attach.
func (p *planner) librivoxTitleWorksAll(title string) (slugs []string, nonLatin bool) {
	slugs, held, nonLatin := p.librivoxTitleWorks(title)
	if len(held) == 0 {
		return slugs, nonLatin
	}
	all := slices.Concat(slugs, held)
	slices.Sort(all)
	return all, nonLatin
}

// librivoxHeldCandidates is what rule 1 and rule 2 find among the works a
// recording's volume total holds back (held, librivoxTitleWorks) for a row
// that agrees with no other work under its key: the works its authors agree
// with, else the one its authors are a spelling of or every work rule 1
// relates it to (soleAuthorVariant), else those sharing a person with it. The
// row may be either volume set, so routeLibrivox lists them as volume-total -
// never an attach, and never a work minted beside them.
func (p *planner) librivoxHeldCandidates(b sourceBook, held []string, lang string, authors workAuthors, credits []credit) []*workState {
	matched, partial := p.librivoxCandidates(held, lang, authors)
	if len(matched) > 0 {
		return matched
	}
	if ws, _, _, related, _ := p.soleAuthorVariant(b, held, lang, authors, credits, partial); ws != nil {
		return []*workState{ws}
	} else if len(related) > 0 {
		return related
	}
	return partial
}

// librivoxNonLatinTitle reports whether a title is read in its own script
// rather than through the slug rule: once its leading article is dropped, it
// slugs to nothing, or MORE THAN HALF of its letters are not Latin script
// (mostlyNonLatinLetters). The slug rule keeps only a non-Latin title's Latin
// and digit fragments, so "Сборник стихов 001" would walk as "001" and
// "The Война и мир" as "the", each beside unrelated works; read by its own
// letters it keys as itself and, matching nothing, gets `librivox-<id>`. A
// title exactly half Latin ("Anna Karenina (Анна Каренина)") stays on the slug
// rule: its Latin half is a whole title.
func librivoxNonLatinTitle(title string) bool {
	_, nonLatin := librivoxTitleKeyOf(title)
	return nonLatin
}

// mostlyNonLatinLetters reports whether more than half of t's letters are not
// Latin script; digits, punctuation, spaces and marks are not counted.
func mostlyNonLatinLetters(t string) bool {
	var letters, latin int
	for _, r := range t {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if unicode.Is(unicode.Latin, r) {
			latin++
		}
	}
	return 2*(letters-latin) > letters
}

// joinSlugWords drops a slug's hyphens ("monte-cristo" and "montecristo" are one
// spelling) except one between two digits, the only thing telling "vol-1-2"
// from "vol-12".
func joinSlugWords(slug string) string {
	var b strings.Builder
	b.Grow(len(slug))
	for i := 0; i < len(slug); i++ {
		if slug[i] == '-' && (i == 0 || i == len(slug)-1 || !isASCIIDigit(slug[i-1]) || !isASCIIDigit(slug[i+1])) {
			continue
		}
		b.WriteByte(slug[i])
	}
	return b.String()
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// librivoxScriptKey is the key of a non-Latin title (librivoxNonLatinTitle): its
// letters, digits and marks, lower-cased, with only the generic combining
// diacritics (model.IsCombiningMark: a Greek accent, a Cyrillic breve) folded
// away. A script's OWN marks stay - a Devanagari vowel sign, the kana voicing
// mark - since dropping them spells another word ("कमला" is not "कमल"). As in
// joinSlugWords, one separator is kept between two numbers, so "Том 1, 2" and
// "Том 12" stay apart.
func librivoxScriptKey(t string) string {
	var b strings.Builder
	afterDigit, gap := false, false
	for _, r := range norm.NFD.String(strings.ToLower(t)) {
		switch {
		case model.IsCombiningMark(r):
		case unicode.IsDigit(r):
			if afterDigit && gap {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			afterDigit, gap = true, false
		case unicode.IsLetter(r) || unicode.IsMark(r):
			b.WriteRune(r)
			afterDigit, gap = false, false
		default:
			gap = true
		}
	}
	return b.String()
}

// dropLibrivoxArticle removes one leading article: a librivoxArticles word
// followed by a space, or an elided "l'" (either apostrophe) followed by a
// letter. A title that is only its article keeps it.
func dropLibrivoxArticle(t string) string {
	lower := strings.ToLower(t)
	for _, elided := range []string{"l'", "l’"} {
		if rest, ok := strings.CutPrefix(lower, elided); ok && strings.TrimSpace(rest) != "" {
			return strings.TrimSpace(t[len(elided):])
		}
	}
	word, rest, ok := strings.Cut(t, " ")
	if ok && librivoxArticles[strings.ToLower(word)] && strings.TrimSpace(rest) != "" {
		return strings.TrimSpace(rest)
	}
	return t
}

// librivoxWorkSlug is the work slug a project with a non-Latin title
// (librivoxNonLatinTitle) is created at when no work matches it.
func librivoxWorkSlug(project int) string { return "librivox-" + strconv.Itoa(project) }

// librivoxRoute is where routeLibrivox sends a row: a decided walk, a skip, or
// (the zero value) the ordinary path - resolveWork and the duplicate-identity
// guard, exactly as for every non-LibriVox row.
type librivoxRoute struct {
	// walk is the decided walk, set only for the two decisions the ordinary
	// path cannot make: a spelling variant to attach to (walk.ws), or a
	// `librivox-<id>` slug to create at (walk.free). nil = the ordinary path.
	walk *workWalk
	// skip says the row is on the worklist and must be dropped.
	skip bool
	// moves is an author-variant attach's move of the row's credits onto
	// the catalogued people it credits, person slug to person slug
	// (librivoxauthors.go rule 1).
	moves map[string]string
	// variant is a rule 2 attach's example for the run's note and fallback a
	// rule 4 `librivox-<id>` create: both counted only once the row's
	// recording has landed (noteLibrivoxLanded), so a row the planner still
	// refuses after routing is not reported as attached or created.
	variant  string
	fallback bool
	// authorVariant is a librivoxauthors.go rule 1 attach's example, counted
	// the same way (Summary.LibrivoxAuthorVariants).
	authorVariant string
	// reviewed says a reviewed decision placed the row (Summary.LibrivoxReviewedPlaced;
	// librivoxreviewed.go). With no walk it is an own-work: the ordinary walk places the
	// row, the duplicate-identity guard standing down.
	reviewed bool
}

// noteLibrivoxLanded counts a routed row that landed: a rule 2 attach
// (Summary.LibrivoxVariantsAttached, with its example), a rule 4 create
// (Summary.LibrivoxFallbackSlugs) or an author-variant attach, per row or
// keyed for the batch (Summary.LibrivoxAuthorVariants).
func (p *planner) noteLibrivoxLanded(b sourceBook, route librivoxRoute) {
	if b.lv != nil {
		if ex, keyed := p.lv.keyedVariants[b.lv.id]; keyed {
			p.noteLibrivoxAuthorVariant(ex)
		}
	}
	if route.variant != "" {
		p.summary.LibrivoxVariantsAttached++
		if len(p.lv.variantExamples) < maxWarnExamples {
			p.lv.variantExamples = append(p.lv.variantExamples, route.variant)
		}
	}
	if route.fallback {
		p.summary.LibrivoxFallbackSlugs++
	}
	if route.authorVariant != "" {
		p.noteLibrivoxAuthorVariant(route.authorVariant)
	}
	if route.reviewed {
		p.summary.LibrivoxReviewedPlaced++
	}
}

// routeLibrivox applies the rules in this file's header to one LibriVox row. It
// only reads (the authors are resolved read-only), so a skipped row leaves no
// person behind. The zero route for every other row.
//
// A reviewed decision answers first (librivoxreviewed.go). The spelling-variant
// index is probed next: a title whose key names no work
// leaves rules 1-3 nothing to decide (the ordinary path, or rule 4), so the row's
// credits are resolved and its chain walked only when the key holds a work - the
// duplicate-identity guard's own gate, for the same reason.
func (p *planner) routeLibrivox(b sourceBook, workTitle, fullTitle, posSuffix, lang string, credits []credit, claim *seriesClaim) librivoxRoute {
	if b.lv == nil {
		return librivoxRoute{}
	}
	if route, ok := p.routeLibrivoxReviewed(b, workTitle, lang, credits); ok {
		return route
	}
	slugs, held, nonLatin := p.librivoxTitleWorks(workTitle)
	if len(slugs)+len(held) == 0 {
		return p.librivoxUndecided(b, workTitle, fullTitle, posSuffix, lang, credits, claim, nonLatin)
	}
	authors := p.rowWorkAuthorsRO(credits)
	if !nonLatin && p.resolveWork(workTitle, fullTitle, b.qualifiedTitle, posSuffix, authors, lang, claim).ws != nil {
		return librivoxRoute{}
	}
	matched, partial := p.librivoxCandidates(slugs, lang, authors)
	if len(matched) == 0 && len(held) > 0 {
		if near := p.librivoxHeldCandidates(b, held, lang, authors, credits); len(near) > 0 {
			p.noteLibrivoxWorklist(b, workTitle, LibrivoxVolumeTotal, candidatesOf(near))
			return librivoxRoute{skip: true}
		}
	}
	switch {
	case len(matched) == 1:
		return librivoxRoute{walk: &workWalk{title: workTitle, ws: matched[0]},
			variant: fmt.Sprintf("%d %q -> %q (%q)", b.lv.id, workTitle, matched[0].slug, matched[0].title)}
	case len(matched) > 1:
		p.noteLibrivoxWorklist(b, workTitle, LibrivoxSeveralWorks, candidatesOf(matched))
		return librivoxRoute{skip: true}
	}
	if route, ok := p.routeLibrivoxAuthorVariant(b, workTitle, slugs, lang, authors, credits, partial); ok {
		return route
	}
	if len(partial) > 0 {
		p.noteLibrivoxWorklist(b, workTitle, LibrivoxAuthorUnmatched, candidatesOf(partial))
		return librivoxRoute{skip: true}
	}
	return p.librivoxUndecided(b, workTitle, fullTitle, posSuffix, lang, credits, claim, nonLatin)
}

// librivoxUndecided is a row rules 1-3 left undecided. A Latin title first
// meets rule 5 (librivoxQualifiedTwin); otherwise it is librivoxFallback's.
func (p *planner) librivoxUndecided(b sourceBook, workTitle, fullTitle, posSuffix, lang string, credits []credit, claim *seriesClaim, nonLatin bool) librivoxRoute {
	if !nonLatin && p.librivoxQualifiedTwin(b, workTitle, fullTitle, posSuffix, lang, credits, claim) {
		return librivoxRoute{skip: true}
	}
	return p.librivoxFallback(b, workTitle, nonLatin)
}

// librivoxQualifiedTwin is rule 5 (meta#2743 follow-up): a row about to create
// a work whose title, with its trailing parenthetical or bracketed qualifiers
// removed one at a time, is a work's title by the same authors (rule 2's key and
// author test) is refused onto the worklist - differently-spelled-title for one
// such work, several-works for more - and never attached: the qualifier may be
// an edition ("(1818)", "(Lippincott)"), a subtitle or a series note, but just
// as well a volume ("(part 1)"), which only a person can tell apart. A row the
// ordinary walk resolves to a work, a row the serial pre-pass suffixed, and a
// "Part N" stated outside brackets ("Anna Karenina, Book 1") are untouched.
func (p *planner) librivoxQualifiedTwin(b sourceBook, workTitle, fullTitle, posSuffix, lang string, credits []credit, claim *seriesClaim) bool {
	if posSuffix != "" {
		return false
	}
	var authors *workAuthors
	for t := workTitle; ; {
		stripped := stripTrailingQualifier(t)
		if stripped == t || stripped == "" {
			return false
		}
		t = stripped
		slugs, nonLatin := p.librivoxTitleWorksAll(t)
		if nonLatin || len(slugs) == 0 {
			continue
		}
		if authors == nil {
			a := p.rowWorkAuthorsRO(credits)
			// The ordinary walk answers first: a row it resolves to a work is
			// that work's recording, as rule 1 says.
			if p.resolveWork(workTitle, fullTitle, b.qualifiedTitle, posSuffix, a, lang, claim).ws != nil {
				return false
			}
			authors = &a
		}
		matched, _ := p.librivoxCandidates(slugs, lang, *authors)
		switch {
		case len(matched) == 1:
			p.noteIdentityDuplicate(b, workTitle, duplicateIdentityMatch{work: matched[0].slug, title: matched[0].title})
			return true
		case len(matched) > 1:
			p.noteLibrivoxWorklist(b, workTitle, LibrivoxSeveralWorks, candidatesOf(matched))
			return true
		}
	}
}

// stripTrailingQualifier is t without its last trailing parenthetical or
// bracketed qualifier ("(1818)", "(The Autobiography of a Horse)", "[Forsyte
// Saga Vol. 1]"), trimmed: trailingParenRE is the package's one locator.
func stripTrailingQualifier(t string) string {
	return strings.TrimSpace(trailingParenRE.ReplaceAllString(t, ""))
}

// stripTrailingQualifiers is t with every trailing qualifier removed
// ("Poems (1915) [Johnston]" is "Poems").
func stripTrailingQualifiers(t string) string {
	for {
		stripped := stripTrailingQualifier(t)
		if stripped == t {
			return t
		}
		t = stripped
	}
}

// librivoxFallback is a row rules 1-3 left undecided: the ordinary path, or for
// a non-Latin title (rule 4) its `librivox-<id>` slug, a worklist skip when
// another record already holds that slug.
func (p *planner) librivoxFallback(b sourceBook, workTitle string, nonLatin bool) librivoxRoute {
	if !nonLatin {
		return librivoxRoute{}
	}
	slug := librivoxWorkSlug(b.lv.id)
	if ws, via := p.workAt(slug); ws != nil || via != "" {
		var held []LibrivoxCandidate
		if ws != nil {
			held = candidatesOf([]*workState{ws})
		}
		p.noteLibrivoxWorklist(b, workTitle, LibrivoxFallbackTaken, held)
		return librivoxRoute{skip: true}
	}
	return librivoxRoute{walk: &workWalk{title: workTitle, chain: workChain{base: slug}, free: slug}, fallback: true}
}

// librivoxCandidates sorts the works under one spelling-variant key (slugs, in
// slug order; catalogued or created by this run) that are in a compatible
// language: matched holds those whose authors agree with the row's (matchWork),
// partial those whose credit list shares a person with the row's without
// agreeing. Each in slug order.
func (p *planner) librivoxCandidates(slugs []string, lang string, authors workAuthors) (matched, partial []*workState) {
	rowAll := authors.allSet()
	for _, slug := range slugs {
		ws := p.works[slug]
		if ws == nil || !langCompatible(ws.lang, lang) {
			continue
		}
		if matchWork(ws, authors) != matchNone {
			matched = append(matched, ws)
			continue
		}
		for person := range rowAll {
			if ws.all[person] {
				partial = append(partial, ws)
				break
			}
		}
	}
	return matched, partial
}

// librivoxTitleIndex is the run's spelling-variant index, key -> work slugs in
// slug order: every work the planner holds, built on first use (a LibriVox run
// only), and kept current as the run creates works (indexLibrivoxWork, called
// where a work is created).
func (p *planner) librivoxTitleIndex() map[string][]string {
	if p.lv.titles == nil {
		p.lv.titles = p.buildWorkKeyIndex(librivoxTitleKeyOfWork)
	}
	return p.lv.titles
}

// librivoxTitleKeyOfWork is a work's spelling-variant index key.
func librivoxTitleKeyOfWork(ws *workState) (string, bool) {
	key := librivoxTitleKey(ws.title)
	return key, key != ""
}

// indexLibrivoxWork adds a work the run created to the spelling-variant and
// own-script indexes. A no-op for an index not built yet (its build reads
// every work then), so a run with no LibriVox row never pays for it.
func (p *planner) indexLibrivoxWork(ws *workState) {
	for _, ix := range []struct {
		idx   map[string][]string
		keyOf func(*workState) (string, bool)
	}{{p.lv.titles, librivoxTitleKeyOfWork}, {p.lv.scriptWorks, librivoxScriptKeyOf}} {
		if ix.idx == nil {
			continue
		}
		if key, ok := ix.keyOf(ws); ok {
			insertWorkKey(ix.idx, key, ws.slug)
		}
	}
}

// librivoxScriptWalk is rule 4 seen from every OTHER source (meta#2778),
// resolveWork's last merge-only step: a non-Latin title
// (librivoxNonLatinTitle) whose chains hold no match may be a book LibriVox
// already filed at `librivox-<id>`, which no slug chain reaches - "Ο γάμος της
// Λίτσας (Litsa's Marriage)" slugs to `litsas-marriage`, a pure Cyrillic title
// to the shared "untitled" - so a libex or user-library row of that title
// would mint a duplicate beside it. The row is that work when exactly ONE
// `librivox-<id>` work carries its own-script key (librivoxTitleKey) by the
// same authors in a compatible language (librivoxCandidates) that the series
// claim does not contradict; nothing otherwise (two such works are not guessed
// between). In resolveWork, so the duplicate-identity guard, the series
// evidence key and every other caller see the same answer as the create path.
// A LibriVox row never reaches it with a non-Latin title (routeLibrivox decides
// those itself).
func (p *planner) librivoxScriptWalk(title string, authors workAuthors, lang string, claim *seriesClaim) (workWalk, bool) {
	idx := p.librivoxScriptIndex()
	if len(idx) == 0 {
		return workWalk{}, false
	}
	key, nonLatin := librivoxTitleKeyOf(title)
	if !nonLatin || key == "" {
		return workWalk{}, false
	}
	matched, _ := p.librivoxCandidates(idx[key], lang, authors)
	matched = slices.DeleteFunc(matched, func(ws *workState) bool { return !claim.compatible(ws) })
	if len(matched) != 1 {
		return workWalk{}, false
	}
	return workWalk{title: title, ws: matched[0]}, true
}

// librivoxScriptIndex is the own-script key of every work rule 4 created (a
// `librivox-<id>` slug whose title is non-Latin), key -> slugs, built on first
// use and kept current as the run creates works (indexLibrivoxWork): the few
// works librivoxScriptWalk can reach, so an ordinary run never builds the whole
// spelling-variant index for it.
func (p *planner) librivoxScriptIndex() map[string][]string {
	if p.lv.scriptWorks == nil {
		p.lv.scriptWorks = p.buildWorkKeyIndex(librivoxScriptKeyOf)
	}
	return p.lv.scriptWorks
}

// librivoxScriptKeyOf is a work's librivoxScriptIndex key: its own-script title
// key when it sits at a `librivox-<id>` slug with a non-Latin title.
func librivoxScriptKeyOf(ws *workState) (string, bool) {
	if !librivoxFallbackSlug(ws.slug) {
		return "", false
	}
	key, nonLatin := librivoxTitleKeyOf(ws.title)
	return key, nonLatin && key != ""
}

// buildWorkKeyIndex indexes every work the planner holds by keyOf (false: not
// indexed), key -> slugs in slug order.
func (p *planner) buildWorkKeyIndex(keyOf func(*workState) (string, bool)) map[string][]string {
	idx := map[string][]string{}
	for _, ws := range p.works {
		if key, ok := keyOf(ws); ok {
			idx[key] = append(idx[key], ws.slug)
		}
	}
	for _, slugs := range idx {
		slices.Sort(slugs)
	}
	return idx
}

// insertWorkKey adds slug under key to a built index, keeping slug order.
func insertWorkKey(idx map[string][]string, key, slug string) {
	slugs := idx[key]
	if i, found := slices.BinarySearch(slugs, slug); !found {
		idx[key] = slices.Insert(slugs, i, slug)
	}
}

// librivoxFallbackSlug reports whether slug is rule 4's `librivox-<id>` form
// (librivoxWorkSlug).
func librivoxFallbackSlug(slug string) bool {
	rest, ok := strings.CutPrefix(slug, "librivox-")
	if !ok {
		return false
	}
	id, err := strconv.Atoi(rest)
	return err == nil && librivoxWorkSlug(id) == slug
}

// candidatesOf names works for a worklist entry.
func candidatesOf(works []*workState) []LibrivoxCandidate {
	out := make([]LibrivoxCandidate, 0, len(works))
	for _, ws := range works {
		out = append(out, LibrivoxCandidate{Work: ws.slug, Title: ws.title})
	}
	return out
}

// noteLibrivoxWorklist puts a skipped LibriVox row on the run's worklist.
func (p *planner) noteLibrivoxWorklist(b sourceBook, title, reason string, candidates []LibrivoxCandidate) {
	if _, decided := p.lv.reviewed[b.lv.id]; decided && !p.lv.ids[b.lv.id] && !strings.HasPrefix(reason, librivoxReviewedReasonPrefix) {
		p.bookWarn(b)("a reviewed decision names it, but it is refused before routing as %s; the decision does not apply", reason)
	}
	p.summary.LibrivoxWorklist = append(p.summary.LibrivoxWorklist, LibrivoxWorklistEntry{
		Project:    b.lv.id,
		Title:      title,
		Authors:    librivoxAuthorList(b),
		Reason:     reason,
		Candidates: candidates,
	})
}

// librivoxAuthorList is the row's author list as the worklist prints it: each
// credit whose author id names it by another spelling (a catalogued holder's
// name, a person rule's: creditPerson.respelled) spelled so, its role suffix
// kept, and the rest as LibriVox spells them.
func librivoxAuthorList(b sourceBook) []string {
	out := slices.Clone(b.authors)
	for _, c := range b.lv.credits {
		if c.kind == librivoxAuthor && c.person != nil && c.person.respelled && c.index < len(out) {
			out[c.index] = c.person.spelled + c.suffix
		}
	}
	return out
}

// reportLibrivoxVariants is the run's one note about the rows rule 2 attached,
// with a few examples: a decision a reviewer of the tranche should see.
func (p *planner) reportLibrivoxVariants() {
	if n := p.summary.LibrivoxVariantsAttached; n > 0 {
		p.summary.Notes = append(p.summary.Notes, withExamples(
			fmt.Sprintf("%d LibriVox project(s) attached to a work by the same authors whose title differs only by a leading article or punctuation", n),
			p.lv.variantExamples))
	}
}
