// Package titlerule holds the audiobook title, series-name and person-name RULE
// PRIMITIVES: what a retailer's decoration looks like, what a title reduces to
// once it is removed, what makes two spellings of a name one identity, and which
// of two records a repair should keep.
//
// WHY IT IS A LEAF. These rules were born inside internal/audit, which imports
// pkg/check - so nothing pkg/check itself can reach was able to call them, and
// metacheck, the intake bot and the repair pass that acts on an audit report would
// each have needed a copy. That is precisely the two-definitions failure CLAUDE.md
// forbids, and the reason a rule lives at the leaf both its callers can reach
// (model.Slugify and model.PersonSlug are here for the same reason). This package
// depends on pkg/model and, for the two rules that already have a HOME OF RECORD,
// on internal/importer - never on pkg/check, pkg/pack or internal/audit.
//
// The one consequence of reaching internal/importer: pkg/check and
// internal/importer cannot import this package (the importer imports check, so
// either direction would be a cycle). That is the deliberate trade. The
// alternative was re-spelling the importer's edition-marker rule here, and a
// SECOND definition of "what an edition marker is" is worse than a narrower
// consumer set - the audit's hand-written version had already drifted from the
// rule of record on bracket optionality and stacked markers. Neither blocked
// package is a plausible consumer: the importer has its own narrower identity
// cleaning that must NOT widen (it would change work identity for every import),
// and pkg/check's rules compare slugs, not cleaned titles.
//
// What is NOT here: anything that reads a catalogue. A rule takes strings and
// returns strings or booleans, so it can be tested without a tree and called from
// anywhere. Assembling records, choosing what to report and writing a report are
// internal/audit's job.
package titlerule

import (
	"regexp"
	"strings"
	"sync"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// maxPeel bounds every fixpoint peel below. A real title strands at most two
// segments; the bound only exists so a pathological string cannot spin.
const maxPeel = 4

// peel applies step until it stops changing s, at most maxPeel times, and NEVER
// returns empty for a non-empty input - a step that would consume the whole
// remaining string is refused, because a title made entirely of the thing being
// peeled still has to be called something.
//
// It is the one shape shared by the four peels (the wide genre subtitle, the
// dangling head, the dangling tail, and the series-name decoration suffixes), each
// of which had spelled its own bounded loop and its own never-empty guard.
func peel(s string, step func(string) string) string {
	for i := 0; i < maxPeel; i++ {
		next := step(s)
		if next == s || next == "" {
			return s
		}
		s = next
	}
	return s
}

// subtitleSeps are the separators a title hangs a subtitle or a volume marker off,
// in ONE list: dropFluffSubtitle reads the first two, and the comma is where a
// retailer usually puts ", Book 3". firstSepIn and lastSepIn are the only two
// questions asked of it.
var subtitleSeps = []string{": ", " - ", ", "}

// firstSepIn is the offset of the earliest subtitle separator in s, or -1.
func firstSepIn(s string) int {
	sep := -1
	for _, alt := range subtitleSeps {
		if i := strings.Index(s, alt); i >= 0 && (sep < 0 || i < sep) {
			sep = i
		}
	}
	return sep
}

// lastSepIn is the offset of the latest subtitle separator in s, or -1.
func lastSepIn(s string) int {
	sep := -1
	for _, alt := range subtitleSeps {
		if i := strings.LastIndex(s, alt); i > sep {
			sep = i
		}
	}
	return sep
}

// wideGenreFluff extends the server's genreFluff with the marketing vocabulary a
// retailer genre subtitle is written in. Every entry is a word that describes a
// book's CATEGORY or its PACKAGING rather than naming the book, and the rule that
// reads it is unchanged: a tail is dropped only when EVERY significant word in it
// is fluff, so ": A Novel of the Roman Empire" survives ("roman", "empire") while
// ": A Dark LitRPG Adventure" does not.
//
// The wider list is affordable here and not in the server because the two use it
// for different things: the server matches a user's library against a catalogue,
// where over-cleaning costs a missed match, while this only ever feeds a REPORT.
//
// Kept out deliberately: "book", "chronicle(s)", "legend(s)", "world" and
// "academy", each of which routinely names a real subtitle or sub-series.
var wideGenreFluff = func() map[string]bool {
	m := make(map[string]bool, len(genreFluff)+96)
	for w := range genreFluff {
		m[w] = true
	}
	for _, w := range []string{
		// Tone / setting adjectives a genre label is built from.
		"dark", "urban", "paranormal", "supernatural", "historical",
		"contemporary", "romantic", "steamy", "spicy", "sweet", "clean",
		"gritty", "grimdark", "military", "post", "apocalyptic", "dystopian",
		"medieval", "western", "gothic", "psychological", "cosmic",
		// Genre nouns and their abbreviations.
		"scifi", "sci", "fi", "science", "fiction", "nonfiction", "space",
		"opera", "cyberpunk", "steampunk", "horror", "suspense", "crime",
		"detective", "whodunit", "comedy", "humor", "humour", "satire",
		"isekai", "cultivation", "xianxia", "wuxia", "harem", "reverse",
		"dungeon", "crawler", "rpg", "mmorpg", "apocalypse", "superhero",
		// Audience labels.
		"young", "adult", "teen", "childrens", "middle", "grade",
		// "series" and "novels" are here as well as in the copied fluffWords
		// regexp, which removes them as standalone tokens but is applied AFTER the
		// subtitle rule - so without them a tail reading ": A GameLit Dark
		// Adventure Series" was not all-fluff and survived whole.
		"series", "novels",
		// Structural words a residual is left with once the series name comes off.
		// Measured need: the eight Pimsleur language courses all reduce to "Level 2
		// Lessons 21-25", which CarriesIdentity must call identity-less so the
		// duplicate detector keys them by their series and keeps them apart.
		"level", "levels", "lesson", "lessons", "unit", "units", "course",
		"season", "episode", "episodes", "volume", "volumes", "part", "parts",
		// Packaging / format words.
		"books", "trilogy", "duology", "quartet", "quintet", "boxed", "boxset",
		"box", "set", "bundle", "compilation", "compendium", "serial",
		"standalone", "prequel", "sequel", "spinoff", "companion", "guide",
		"performance", "production", "narration", "radio", "drama",
		"dramatization", "dramatisation", "recording", "audio", "audiobooks",
		"fullcast", "cast", "full", "digital", "special", "deluxe", "definitive",
		"anniversary", "revised", "updated", "expanded", "new", "classic",
		"bestselling", "bestseller", "award", "winning", "acclaimed",
	} {
		m[w] = true
	}
	return m
}()

// dropWideGenreSubtitle strips trailing all-fluff subtitles under wideGenreFluff,
// repeatedly: a retailer really does stack two ("...: A LitRPG Adventure: Book One
// of a New Series").
func dropWideGenreSubtitle(s string) string {
	return peel(s, func(t string) string { return dropFluffSubtitle(t, wideGenreFluff) })
}

// Clean is the cleaned title: the wide genre-subtitle drop, then the server's
// CleanTitle against series (which strips the series name, volume markers, edition
// markers and the narrow fluff, and falls back rather than emptying the title),
// then the stray-bracket and dangling-segment sweeps.
//
// series may be "" - a work with no membership and no series name embedded in its
// title still gets its markers and fluff removed.
func Clean(title, series string) string {
	s := CleanTitle(dropWideGenreSubtitle(title), series)
	s = dropStrayBrackets(s)
	s = collapseSeparatorRuns(s)
	s = peel(s, dropDanglingHeadOnce)
	s = peel(s, dropDanglingTailOnce)
	return trimStopwordTail(s)
}

// separatorRun matches two subtitle separators with nothing but whitespace between
// them - an EMPTY segment, which is what removing a series name from between two of
// them leaves ("A Murder of Crows: Shadows and Ash, Book Two" against that series
// reduces to "A Murder of Crows: , Book Two").
//
// The dangling-segment peels cannot fix it: the segment they would judge is the one
// AFTER the run, which here holds a real word.
var separatorRun = regexp.MustCompile(`[:,;|]\s*([:,;|])`)

// collapseSeparatorRuns drops the leading separator of every empty segment, keeping
// the one that introduces the surviving text.
func collapseSeparatorRuns(s string) string {
	return peel(s, func(t string) string {
		return tidyTitle(separatorRun.ReplaceAllString(t, "$1"))
	})
}

// dropStrayBrackets removes bracket characters left UNBALANCED by the bracket-group
// removal, which cannot see a nested group: bracketGroup's character class stops at
// the first closer, so "Eric (Mundodisco 9) [Eric (Discworld)]" loses
// "[Eric (Discworld)" and keeps the orphan "]".
//
// It only fires when the brackets do not balance, so a title that legitimately
// carries a matched pair is untouched.
func dropStrayBrackets(s string) string {
	if bracketsBalance(s, "(", ")") && bracketsBalance(s, "[", "]") {
		return s
	}
	out := tidyTitle(strings.Map(func(r rune) rune {
		if strings.ContainsRune("()[]", r) {
			return -1
		}
		return r
	}, s))
	if out == "" {
		return s
	}
	return out
}

func bracketsBalance(s, open, close string) bool {
	return strings.Count(s, open) == strings.Count(s, close)
}

// dropDanglingHeadOnce removes ONE leading segment left holding nothing but
// stopwords - the mirror of dropDanglingTailOnce, and the shape a retailer's
// article-plus-series prefix leaves once the series name comes off ("A How to Train
// Your Dragon: A Hero's Guide..." against that series reduces to "A : A Hero's
// Guide...").
//
// The head test is stopwords-ONLY rather than the tail's "no significant tokens",
// because tokenize also discards pure numbers and a leading number is routinely the
// whole identity of a title ("1984: The Novel" must keep its 1984).
func dropDanglingHeadOnce(s string) string {
	sep := firstSepIn(s)
	if sep <= 0 || !stopwordsOnly(s[:sep]) {
		return s
	}
	return tidyTitle(s[sep:])
}

// dropDanglingTailOnce removes ONE trailing segment left holding nothing
// significant - only articles, prepositions, single letters and numbers.
//
// It exists because removing a series name from the MIDDLE of a title leaves the
// article that introduced it behind: "Hammered: The Iron Druid Chronicles, Book 3"
// against that series reduces to "Hammered: The", which is not the same key as the
// plain "Hammered" it duplicates. CleanTitle's own tidyTitle only trims separator
// characters, not the words stranded between them.
//
// A tail is only dropped when nothing significant is in it, so a real subtitle
// ("Star Wars: A New Hope" -> "new", "hope") is never touched.
func dropDanglingTailOnce(s string) string {
	sep := lastSepIn(s)
	if sep <= 0 || hasSignificantToken(s[sep:]) {
		return s
	}
	return tidyTitle(s[:sep])
}

// stopwordsOnly reports whether a segment's every word is a title stopword. An
// empty or punctuation-only segment counts, since it holds nothing either.
func stopwordsOnly(s string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(s), notAlnum) {
		if !titleStopwords[w] {
			return false
		}
	}
	return true
}

// hasSignificantToken reports whether s holds at least one token tokenize would
// keep. It is tokenize's predicate WITHOUT building the set, which is what the two
// callers that only ask "is there anything in here" actually need - and tokenize is
// on the hot path of every title.
func hasSignificantToken(s string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(s), notAlnum) {
		if len(w) >= 2 && !titleStopwords[w] && !isAllDigits(w) {
			return true
		}
	}
	return false
}

// trimStopwordTail drops the stopwords stranded at the END of a cleaned title.
// Removing a series name from the middle of a title leaves the preposition that led
// into it behind with no separator to hang off ("Two Tales of the Iron Druid
// Chronicles" against that series reduces to "Two Tales of the"), which the
// dangling-tail peel cannot see because there is no segment boundary there.
//
// Deliberately TAIL-ONLY. A leading article is part of a title ("A Deadly Cliché"
// is not "Deadly Cliché"), so trimming it here would make a retitle proposal wrong.
// Where article-insensitivity IS wanted - comparing two titles for identity - the
// caller drops it on the KEY instead (CompareKey), which is the same split the
// server's own NormalizeSeries makes one level up.
//
// It never empties the title: a title genuinely made of nothing but stopwords ("The
// One and Only") keeps its words.
func trimStopwordTail(s string) string {
	words := strings.Fields(s)
	hi := len(words)
	for hi > 0 && titleStopwords[strings.ToLower(strings.Trim(words[hi-1], ".,:;-"))] {
		hi--
	}
	if hi == 0 || hi == len(words) {
		return s
	}
	return tidyTitle(strings.Join(words[:hi], " "))
}

// CompareKey is a cleaned title's comparison identity: FoldKey with a leading
// article dropped first, so "A Deadly Cliché" and "Deadly Cliché" are one key while
// the titles themselves stay as their authors wrote them.
//
// The split is deliberate and is the same one the server's NormalizeSeries makes
// for series names: article-insensitivity belongs to the COMPARISON, never to the
// value a repair pass would write back.
func CompareKey(cleaned string) string { return FoldKey(dropLeadingArticle(cleaned)) }

// CarriesIdentity reports whether a cleaned title holds at least one word that is
// not packaging vocabulary. A title that does not ("Omnibus", "Box Set", "Complete
// Collection" - all that is left once the series name comes off) names no book of
// its own, so a duplicate detector must not group two of them on the title alone.
func CarriesIdentity(cleaned string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(cleaned), notAlnum) {
		if len(w) >= 2 && !titleStopwords[w] && !isAllDigits(w) && !wideGenreFluff[w] {
			return true
		}
	}
	return false
}

// FoldKey is the identity key for a free-text name: the project's own diacritic
// folding and punctuation rules (model.Slugify, the ONE definition of what text
// becomes a slug) with the hyphens removed, so a difference that is only spacing or
// punctuation is not an identity.
//
// Removing the hyphens is what makes it COARSER than a slug, which matters: a
// person's id already IS model.PersonSlug(name) (pkg/check enforces it), so
// grouping people by their slug would find nothing by construction, while "A.B.
// Kovacs" (a-b-kovacs) and "AB Kovacs" (ab-kovacs) meet here.
//
// It is also why the server's Normalize is not among the copied functions (see
// match.go's tail): that one DROPS every non-ASCII rune where this folds them, so
// "Café Society" and "Cafe Society" are one key.
func FoldKey(s string) string { return strings.ReplaceAll(model.Slugify(s), "-", "") }

// ---- series name forms -------------------------------------------------------

// formsMemo caches SeriesForms by name. A series name's forms are asked for on
// every title clean and on every containment probe, and computing them costs up to
// eight tidyTitle calls and a slice; a run over the real tree asks tens of millions
// of times for tens of thousands of distinct names.
//
// The key space is the distinct series names a process sees (~45k on the real
// tree), so the memo is bounded by the catalogue rather than by traffic. A
// long-lived consumer that walked unbounded user input through it would want its own
// cache instead; nothing in this module does.
var formsMemo sync.Map // string -> []string

// SeriesForms enumerates the spellings one series name appears in, most specific
// first. It is seriesForms memoized; the returned slice must not be modified.
func SeriesForms(series string) []string {
	if v, ok := formsMemo.Load(series); ok {
		return v.([]string)
	}
	forms := seriesForms(series)
	formsMemo.Store(series, forms)
	return forms
}

// SeriesRefIn reports which spelling of series occurs in lowerTitle (an
// already-lowercased title), at alphanumeric boundaries.
func SeriesRefIn(lowerTitle, series string) (string, bool) {
	for _, form := range SeriesForms(series) {
		if containsPhraseLower(lowerTitle, form) {
			return form, true
		}
	}
	return "", false
}

// BareSeq derives the volume number a title itself spells out, against a series
// name. It states a number the title WRITES ("... Volume 9", or a residual that is
// nothing but the number), so it never grabs an incidental one - which is what makes
// it, and only it, safe to disqualify a match on.
func BareSeq(title, series string) (float64, bool) { return bareSeq(title, series) }

// TidyTitle collapses whitespace and trims stray leading/trailing separators.
func TidyTitle(s string) string { return tidyTitle(s) }

// BoundedAt reports whether s[start:end] sits on alphanumeric boundaries. It is
// exported because a caller with an INDEX over series names tests a candidate as a
// prefix at a known offset rather than searching for it, and the boundary rule that
// makes such a hit legitimate has to be the same one SeriesRefIn applies - a short
// name ("Land") must not link itself to "The Landlord's Daughter" through either
// door.
func BoundedAt(s string, start, end int) bool { return boundedAt(s, start, end) }

// StripParenGroups removes parenthetical and bracketed groups from a name.
func StripParenGroups(s string) string { return stripParenGroups(s) }

// DropLeadingArticle removes a leading "the "/"a "/"an ", never emptying the
// string. It is the ONE article vocabulary: the decoration detector's
// article-plus-series rule and the series-name comparison key both read it, so
// neither can grow a fourth article the other does not know.
func DropLeadingArticle(s string) string { return dropLeadingArticle(s) }

// seriesDecorSuffixes are the trailing words a retailer's catalogue appends to a
// series name that the name itself does not carry: Audible lists "Dragon Heart
// Series" and "Richard Sharpe Novels" where the series is called "Dragon Heart" and
// "Richard Sharpe".
//
// " Saga" is deliberately NOT here. It is part of the real name far more often than
// it is decoration ("Vorkosigan Saga", "The Saga of Seven Suns"), so it gets its own
// looser key (SeriesSagaKey) whose findings propose nothing.
var seriesDecorSuffixes = []string{
	" series", " novels", " novel", " books", " book", " trilogy",
	" audiobooks", " audiobook", " collection", " box set", " boxed set",
}

// sagaSuffix is the one suffix held back from seriesDecorSuffixes.
var sagaSuffix = []string{" saga"}

// SeriesKey is a series name's comparison identity: parentheticals removed, a
// leading article dropped, trailing catalogue decoration peeled, then folded - so
// case, diacritics, punctuation and spacing are not identity.
func SeriesKey(name string) string { return seriesKey(name, seriesDecorSuffixes) }

// SeriesSagaKey is SeriesKey with a trailing " Saga" peeled too - strictly looser,
// and used only to REPORT, never to propose.
func SeriesSagaKey(name string) string {
	return seriesKey(name, append(append([]string{}, seriesDecorSuffixes...), sagaSuffix...))
}

// seriesKey peels a name's decoration through the copied dropOneSuffix rule, so the
// wider list is that rule under a longer vocabulary rather than a second peel.
func seriesKey(name string, suffixes []string) string {
	t := strings.ToLower(strings.TrimSpace(stripParenGroups(name)))
	t = strings.TrimSpace(dropLeadingArticle(t))
	t = peel(t, func(s string) string { return dropOneSuffix(s, suffixes) })
	return FoldKey(t)
}

// ---- decoration detectors ----------------------------------------------------

// The decoration codes. They name what a title carries and are the subclass values
// of the reports that read them, so they are written once here.
const (
	DecArticleSeries = "article-series-prefix"
	DecBracketSuffix = "bracket-suffix"
	DecSeriesName    = "series-name"
	DecVolume        = "volume-marker"
	DecEdition       = "edition-marker"
	DecGenreSubtitle = "genre-subtitle"
	DecTrailingPunct = "trailing-separator"
)

// SeriesResolver resolves a series name occurring in a piece of text: form is the
// spelling that matched, name is the series' CANONICAL name (which may differ - a
// form is allowed to be the name minus its leading article or its " Series"
// decoration). Both are needed, and by different rules: the form is what strips, the
// name is what says whether an article belongs to the series or to nobody.
type SeriesResolver func(text string) (form, name string, ok bool)

// TitleFacts is everything a decoration detector reads about one title.
//
// Resolve is the caller's index over the catalogue's series names; it may be nil, in
// which case the rules that need it simply do not fire.
type TitleFacts struct {
	Title string
	// Series is the series name the title is read against, or "".
	Series  string
	Resolve SeriesResolver
}

// decorations is the detector table, in PRIORITY order - most specific first. It
// replaces a hand-kept const block, a separate priority list and a run of
// sequential ifs, which were three places one code had to be spelled and could
// disagree.
var decorations = []struct {
	code  string
	match func(TitleFacts) bool
}{
	{DecArticleSeries, func(f TitleFacts) bool {
		_, _, _, ok := ArticleSeriesPrefix(f.Title, f.Resolve)
		return ok
	}},
	// A trailing bracket group that IS an edition marker belongs to the edition
	// code, not this one: "[Abridged]" matches both shapes, and filing it as a
	// second language's title would name the wrong defect. The two rules are made
	// mutually exclusive here rather than by ordering, so a title carrying a
	// bracketed edition marker AND a volume marker still files under the volume.
	{DecBracketSuffix, func(f TitleFacts) bool {
		return bracketSuffixRE.MatchString(f.Title) && !importer.HasEditionMarker(f.Title)
	}},
	{DecSeriesName, func(f TitleFacts) bool {
		if f.Series == "" {
			return false
		}
		_, ok := SeriesRefIn(strings.ToLower(f.Title), f.Series)
		return ok
	}},
	{DecVolume, func(f TitleFacts) bool { return markerSeq.MatchString(f.Title) }},
	// The edition marker is the IMPORTER's rule, not a local one: it strips this
	// exact shape before work identity, so "does this title still carry one" must
	// be the same question. A hand-written version here had already drifted (it
	// made the brackets optional and missed stacked markers).
	{DecEdition, func(f TitleFacts) bool { return importer.HasEditionMarker(f.Title) }},
	{DecGenreSubtitle, func(f TitleFacts) bool { return dropWideGenreSubtitle(f.Title) != f.Title }},
	{DecTrailingPunct, func(f TitleFacts) bool { return trailingSeparatorRE.MatchString(f.Title) }},
}

// Decorations returns the codes a title carries, in the table's PRIORITY order
// (most specific first), so the slice is both deterministic and informative.
func Decorations(f TitleFacts) []string {
	var out []string
	for _, d := range decorations {
		if d.match(f) {
			out = append(out, d.code)
		}
	}
	return out
}

// PrimaryDecoration is the first code in priority order, or "" - the one a report
// files a title under when it carries several.
func PrimaryDecoration(codes []string) string {
	for _, d := range decorations {
		for _, c := range codes {
			if c == d.code {
				return d.code
			}
		}
	}
	return ""
}

// DecorationCodes lists every code in priority order, for a consumer that needs the
// vocabulary without a title in hand.
func DecorationCodes() []string {
	out := make([]string, 0, len(decorations))
	for _, d := range decorations {
		out = append(out, d.code)
	}
	return out
}

// bracketSuffixRE matches a trailing bracketed group, the shape a retailer uses to
// print a second language's title after the local one ("Eric (Mundodisco 9) [Eric
// (Discworld)]"). Parentheses are NOT this rule - a parenthetical is usually the
// series or the volume, which have their own codes.
var bracketSuffixRE = regexp.MustCompile(`\[[^\]]*\]\s*$`)

// trailingSeparatorRE matches a title left ending in a separator, which is what a
// half-cleaned title looks like.
var trailingSeparatorRE = regexp.MustCompile(`[\s\-:;,|]+$`)

// leadingIndefiniteRE captures a leading INDEFINITE article and the text after it.
//
// "the" is deliberately excluded. The defect this rule reports is a retailer
// prepending an article that belongs to nothing - "A How to Train Your Dragon: A
// Hero's Guide..." - and what makes that visible is that it is UNGRAMMATICAL: no
// English title reads "a <series name>". A series name preceded by "the" is
// ordinary English and usually the series' own natural form ("The Legend of Dave
// the Villager, Books 16-20", "The Minder Project, Seasons 1-4"), which the rule
// reported as a defect for as long as it accepted "the" - hundreds of them.
var leadingIndefiniteRE = regexp.MustCompile(`(?i)^(an?)\s+(.+)$`)

// ArticleSeriesPrefix reports whether title is "<indefinite article> <series
// name><sep> <rest>" - a retailer prefixing a book with an article and its SERIES
// name, so the article belongs to nothing ("A How to Train Your Dragon: A Hero's
// Guide to Deadly Dragons", whose slug then leads with a dangling `a-`).
//
// THREE things bound it, and each removed a false-positive class measured over the
// real tree:
//
//   - The article must be INDEFINITE. "The <series name>" is ordinary English and
//     usually the series' own natural form; accepting it reported 1,200 good titles.
//   - The series name must fill the WHOLE span between the article and the
//     separator, so an ordinary "A Study in Scarlet: ..." is untouched unless a
//     series is really called "Study in Scarlet".
//   - The series must not be NAMED with that article. A form is allowed to be the
//     name minus its leading article, so "A Thousand Li: Descent from the Mountain"
//     resolves through the form "Thousand Li" - but the series really is called "A
//     Thousand Li", the article is its own, and nothing is wrong with the title.
//     This is what the resolver's second return value is for.
//
// resolve may be nil (the rule never fires).
func ArticleSeriesPrefix(title string, resolve SeriesResolver) (article, series, rest string, ok bool) {
	if resolve == nil {
		return "", "", "", false
	}
	m := leadingIndefiniteRE.FindStringSubmatch(title)
	if m == nil {
		return "", "", "", false
	}
	// The regexp and dropLeadingArticle must agree on the vocabulary: if dropping
	// the article does not shorten the title, the regexp matched something
	// dropLeadingArticle does not consider one, and the rule has no business firing.
	if DropLeadingArticle(title) == strings.TrimSpace(title) {
		return "", "", "", false
	}
	sep := firstSepIn(m[2])
	if sep <= 0 {
		return "", "", "", false
	}
	head, tail := strings.TrimSpace(m[2][:sep]), tidyTitle(m[2][sep:])
	if head == "" || tail == "" {
		return "", "", "", false
	}
	form, name, found := resolve(head)
	if !found || FoldKey(form) != FoldKey(head) {
		return "", "", "", false
	}
	// The series' own name already carries this article: it belongs to the series,
	// not to nothing, so the title is written correctly.
	if FoldKey(name) == FoldKey(m[1]+" "+head) {
		return "", "", "", false
	}
	return m[1], head, tail, true
}

// ---- slug shape predicates ---------------------------------------------------

// slugArticles are the article prefixes a slug can lead with. They mirror
// dropLeadingArticle's vocabulary in the slug DOMAIN (hyphen-separated, folded), so
// the two lists are the same three words in two spellings; longest first, so "the-"
// is tested before "a-".
var slugArticles = []string{"the", "an", "a"}

// LeadingSlugArticle reports the article a slug leads with, if any.
func LeadingSlugArticle(slug string) (string, bool) {
	for _, art := range slugArticles {
		if strings.HasPrefix(slug, art+"-") {
			return art, true
		}
	}
	return "", false
}

// TitleStartsWith reports whether a title's first SLUG word is art.
//
// The slug word, not the whitespace token: "A.A. Milne's Winnie-the-Pooh" slugs to
// `a-a-milnes-...`, and its first token "A.A." folds to "a-a", which is not the
// article "a" - so a token comparison called the slug's leading `a-` dangling when
// it is an initial. Reading the same words the slug is BUILT from is what makes the
// two agree.
func TitleStartsWith(title, art string) bool {
	first, _, _ := strings.Cut(model.Slugify(title), "-")
	return first == art
}

// DoubledSlugToken reports an immediately repeated token run in a slug - one token
// ("italian-italian") or a pair ("iron-druid-iron-druid") - and returns the repeated
// run.
//
// A run must carry a LETTER and, for the single-token case, be at least three
// characters. Both bounds are there because a slug's numbers repeat innocently: a
// title with a grouped number slugs to `10-000-000-marriage-proposal`, and an
// initials pair to `a-a-milnes-...`, neither of which is a title restating itself.
func DoubledSlugToken(slug string) (string, bool) {
	toks := strings.Split(slug, "-")
	for i := 0; i+1 < len(toks); i++ {
		if toks[i] == toks[i+1] && len(toks[i]) >= 3 && hasLetter(toks[i]) {
			return toks[i], true
		}
	}
	for i := 0; i+3 < len(toks); i++ {
		if toks[i] == toks[i+2] && toks[i+1] == toks[i+3] &&
			hasLetter(toks[i]) && hasLetter(toks[i+1]) {
			return toks[i] + "-" + toks[i+1], true
		}
	}
	return "", false
}

// hasLetter reports whether s holds an ASCII letter. Slug tokens are ASCII by
// construction (model.Slugify), so this is the whole test.
func hasLetter(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'a' && s[i] <= 'z' {
			return true
		}
	}
	return false
}

// ---- person name identity ----------------------------------------------------

// MarkedNameKey is the importer's initials identity for a name, re-exported so a
// consumer grouping possible duplicate people reads the rule the importer MERGES
// spellings on rather than a second copy of it.
func MarkedNameKey(name string) string { return importer.MarkedNameKey(name) }

// OneEditApart reports whether a and b are exactly one Damerau-Levenshtein edit
// apart - one insertion, deletion, substitution, or transposition of adjacent
// characters. It is a direct single-edit test rather than a distance matrix: the
// answer is only ever "is it 1", so it walks each pair once instead of filling an
// n*m table.
//
// It compares BYTES, which is correct for its intended input: a FoldKey output is
// ASCII by construction. Equal strings return false - zero edits is not one.
func OneEditApart(a, b string) bool {
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
	first, diffs := -1, 0
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

// ---- the canonical-choice ladders --------------------------------------------

// WorkRank is one work's standing in a duplicate cluster: the evidence that decides
// which record a repair should KEEP. Better is the ladder.
//
// It lives here, in values rather than in catalogue lookups, so the reporting pass
// and the repair pass that acts on its report rank identically. A ladder spelled
// twice is a repair that keeps the record the report did not name.
type WorkRank struct {
	// InSeries: the work already has a series membership - it is the modeled one.
	InSeries bool
	// HasSidecar: a spoiler-gated works-community entry is keyed by this slug, and
	// re-pointing one is the expensive, human-judgement move.
	HasSidecar bool
	// Recordings is how many recordings hang off the work.
	Recordings int
	// Decorations is how many retailer decorations its title carries (fewer wins).
	Decorations int
	// TitleLen is its title's length in bytes (shorter wins).
	TitleLen int
	// ID breaks every remaining tie, so the choice never depends on input order.
	ID string
}

// Better reports whether r outranks o. Every rung is data, so the answer is the
// same on every run and in every consumer.
func (r WorkRank) Better(o WorkRank) bool {
	switch {
	case r.InSeries != o.InSeries:
		return r.InSeries
	case r.HasSidecar != o.HasSidecar:
		return r.HasSidecar
	case r.Recordings != o.Recordings:
		return r.Recordings > o.Recordings
	case r.Decorations != o.Decorations:
		return r.Decorations < o.Decorations
	case r.TitleLen != o.TitleLen:
		return r.TitleLen < o.TitleLen
	default:
		return r.ID < o.ID
	}
}

// SeriesRank is one series' standing among near-duplicate spellings: the one
// holding the most works wins, because folding onto it moves the fewest
// memberships, and the id breaks the tie.
type SeriesRank struct {
	Works int
	ID    string
}

// Better reports whether r outranks o.
func (r SeriesRank) Better(o SeriesRank) bool {
	if r.Works != o.Works {
		return r.Works > o.Works
	}
	return r.ID < o.ID
}
