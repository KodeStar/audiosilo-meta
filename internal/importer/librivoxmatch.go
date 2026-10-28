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
//     same authors (matchWork) in a compatible language. LibriVox's catalogue is
//     public-domain texts whose titles differ from a retailer's mostly by exactly
//     that, so the duplicate-identity guard's "skip, never merge" would keep a
//     large share of the mirror out for want of an article.
//  3. STILL AMBIGUOUS IS A WORKLIST SKIP. Two or more such works (several-works),
//     or a work whose authors only partly agree with the row's or whose agreement
//     rests on the catch-all person an unsluggable name falls back to
//     (author-unmatched), decide nothing: the row is skipped and listed in
//     Summary.LibrivoxWorklist for a person to resolve, never guessed. So is a
//     row the duplicate-identity guard refuses (differently-spelled-title: a
//     coarser match, a subtitle or decoration apart, which this rule does not
//     read as the same title).
//  4. NON-LATIN TITLES. A title that slugs to nothing (Cyrillic, Greek, CJK...),
//     or whose letters are mostly in such a script (librivoxNonLatinTitle),
//     never walks a slug chain: the shared "untitled" one would file every such
//     title by one author as one work, and a Latin or number fragment's chain
//     would file it beside an unrelated book. It is matched by rule 2 alone
//     (its key keeps the letters of its own script), and otherwise creates a
//     new work at `librivox-<project id>`, titled as LibriVox writes it.
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
	// only partly agree with the row's, or the agreement rests on the catch-all
	// person a name in an unsluggable script falls back to.
	LibrivoxAuthorUnmatched = "author-unmatched"
	// LibrivoxDifferentlySpelled: the duplicate-identity guard (dupidentity.go)
	// found the row's book under a title this rule does not read as the same
	// (a subtitle or a decoration apart).
	LibrivoxDifferentlySpelled = "differently-spelled-title"
	// LibrivoxFallbackTaken: the row's title is non-Latin (librivoxNonLatinTitle)
	// and its `librivox-<id>` work slug is already taken (or retired) by another
	// record.
	LibrivoxFallbackTaken = "fallback-slug-taken"
)

// LibrivoxWorklistEntry is one LibriVox project a run skipped for a person to
// resolve: re-importing its id once the catalogue says which work it is (or
// that it is a new one) lands it.
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
		return joinSlugWords(s), false
	}
	return librivoxScriptKey(t), true
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

// anyUnsluggedCredit reports whether any credit's name slugs to nothing, i.e.
// resolves to the catch-all person: a cheap test before resolving the credits.
func anyUnsluggedCredit(credits []credit) bool {
	for _, c := range credits {
		if _, fellBack := personSlug(c.name); fellBack {
			return true
		}
	}
	return false
}

// restsOnCatchAll reports whether a work's author agreement with a row rests
// on the catch-all person: the row's identity authors include it, and none of
// the row's other INDIVIDUAL identity authors (individualAuthor) is an author
// of the work. A shared translator or other role-credited person is not
// enough, nor is a shared collective such as Anonymous: a Cyrillic-named
// Chekhov and a Cyrillic-named Dostoevsky both translated by Constance Garnett
// are still two authors on one catch-all record.
func restsOnCatchAll(authors workAuthors, ws *workState) bool {
	if !slices.Contains(authors.identity, model.UnslugPersonID) {
		return false
	}
	for _, a := range authors.identity {
		if individualAuthor(a) && ws.authors[a] {
			return false
		}
	}
	return true
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
}

// routeLibrivox applies the rules in this file's header to one LibriVox row. It
// only reads (the authors are resolved read-only), so a skipped row leaves no
// person behind. The zero route for every other row.
//
// The spelling-variant index is probed FIRST: a title whose key names no work
// leaves rules 1-3 nothing to decide (the ordinary path, or rule 4), so the row's
// credits are resolved and its chain walked only when the key holds a work - the
// duplicate-identity guard's own gate, for the same reason.
//
// A rule-1 hit whose author agreement rests on the catch-all person
// (restsOnCatchAll) decides nothing either: two "Childhood"s by two
// Cyrillic-named authors both credit that one record, so the hit is an
// author-unmatched worklist row, never an attach. Such
// a row is the one case probed even when the index holds nothing, since a chain
// hit need not share the key (a full or qualified title's walk).
func (p *planner) routeLibrivox(b sourceBook, workTitle, fullTitle, posSuffix, lang string, credits []credit, claim *seriesClaim) librivoxRoute {
	if b.lv == nil {
		return librivoxRoute{}
	}
	key, nonLatin := librivoxTitleKeyOf(workTitle)
	slugs := p.librivoxTitleIndex()[key]
	if len(slugs) == 0 && (nonLatin || !anyUnsluggedCredit(credits)) {
		return p.librivoxUndecided(b, workTitle, fullTitle, posSuffix, lang, credits, claim, nonLatin)
	}
	authors := p.rowWorkAuthorsRO(credits)
	if !nonLatin {
		if ws := p.resolveWork(workTitle, fullTitle, b.qualifiedTitle, posSuffix, authors, lang, claim).ws; ws != nil {
			if restsOnCatchAll(authors, ws) {
				p.noteLibrivoxWorklist(b, workTitle, LibrivoxAuthorUnmatched, candidatesOf([]*workState{ws}))
				return librivoxRoute{skip: true}
			}
			return librivoxRoute{}
		}
	}
	if len(slugs) > 0 {
		matched, partial := p.librivoxCandidates(slugs, lang, authors)
		switch {
		case len(matched) == 1 && !restsOnCatchAll(authors, matched[0]):
			p.summary.LibrivoxVariantsAttached++
			if len(p.lv.variantExamples) < maxWarnExamples {
				p.lv.variantExamples = append(p.lv.variantExamples,
					fmt.Sprintf("%d %q -> %q (%q)", b.lv.id, workTitle, matched[0].slug, matched[0].title))
			}
			return librivoxRoute{walk: &workWalk{title: workTitle, ws: matched[0]}}
		case len(matched) > 1:
			p.noteLibrivoxWorklist(b, workTitle, LibrivoxSeveralWorks, candidatesOf(matched))
			return librivoxRoute{skip: true}
		case len(matched)+len(partial) > 0:
			p.noteLibrivoxWorklist(b, workTitle, LibrivoxAuthorUnmatched, candidatesOf(slices.Concat(matched, partial)))
			return librivoxRoute{skip: true}
		}
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
		key, nonLatin := librivoxTitleKeyOf(t)
		slugs := p.librivoxTitleIndex()[key]
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
		matched = slices.DeleteFunc(matched, func(ws *workState) bool { return restsOnCatchAll(*authors, ws) })
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
	p.summary.LibrivoxFallbackSlugs++
	return librivoxRoute{walk: &workWalk{title: workTitle, chain: workChain{base: slug}, free: slug}}
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
		titles := map[string][]string{}
		for _, ws := range p.works {
			if key := librivoxTitleKey(ws.title); key != "" {
				titles[key] = append(titles[key], ws.slug)
			}
		}
		for _, slugs := range titles {
			slices.Sort(slugs)
		}
		p.lv.titles = titles
	}
	return p.lv.titles
}

// indexLibrivoxWork adds a work the run created to the spelling-variant index.
// A no-op before the index is built (the build reads every work then), so a run
// with no LibriVox row never pays for it.
func (p *planner) indexLibrivoxWork(ws *workState) {
	if p.lv.titles == nil {
		return
	}
	key := librivoxTitleKey(ws.title)
	if key == "" {
		return
	}
	slugs := p.lv.titles[key]
	i, found := slices.BinarySearch(slugs, ws.slug)
	if !found {
		p.lv.titles[key] = slices.Insert(slugs, i, ws.slug)
	}
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
	p.summary.LibrivoxWorklist = append(p.summary.LibrivoxWorklist, LibrivoxWorklistEntry{
		Project:    b.lv.id,
		Title:      title,
		Authors:    slices.Clone(b.authors),
		Reason:     reason,
		Candidates: candidates,
	})
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
