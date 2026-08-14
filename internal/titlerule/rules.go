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
// depends on pkg/model and on NOTHING ELSE in the module - not pkg/check, not
// pkg/pack, not internal/importer, not internal/audit.
//
// It reached internal/importer until the intake-time duplicate prevention landed, and
// the documented consequence was that internal/importer and pkg/check could not
// import this package at all. Both are now consumers of IdentityTitleKey, so the
// edition-marker rule moved down here and the initials re-export went away
// (internal/audit asks the importer for it directly). edition.go carries that move's
// full rationale; nothing else restates it.
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
		// The blurb adjectives. They matter because a residual made only of these
		// is a DESCRIPTION of the book, not its name: "Fluff 3: A Wholesome LitRPG"
		// reduced to "A Wholesome LitRPG", keeping the blurb and dropping the title.
		// CarriesIdentity is what refuses that, and it can only refuse what the
		// vocabulary knows.
		"wholesome", "slowburn", "hilarious", "thrilling", "gripping",
		"heartwarming", "unputdownable", "addictive", "steamy", "wholesale",
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
	if sep <= 0 || hasSignificantToken(s[sep:]) || numbersAreIdentity(s[sep:]) {
		return s
	}
	return tidyTitle(s[:sep])
}

// numbersAreIdentity reports whether a segment's NUMBERS are part of what the title
// says rather than a volume marker: two or more numbers (a date range, "1881-1968") or
// a single number of four digits or more (a year, "1984").
//
// tokenize discards numbers as insignificant, which is right for ": 2" and wrong for
// ": 1881-1968" - two genuinely different books, "Without a Trace: 1881-1968" and
// "Without a Trace: 1970-2016", were reduced to one title and proposed for merge.
func numbersAreIdentity(seg string) bool {
	nums := digitRuns.FindAllString(seg, -1)
	if len(nums) >= 2 {
		return true
	}
	return len(nums) == 1 && len(nums[0]) >= 4
}

var digitRuns = regexp.MustCompile(`\d+`)

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
// It also never reduces a title to ONE word. "All In: The Blackstone Affair Part 2"
// stripped to "All In", whose trailing "In" is a stopword - and trimming it left
// "All". A single trailing function word on a two-word title is part of the phrase;
// a stranded preposition shows up with something in front of it to strand from.
func trimStopwordTail(s string) string {
	words := strings.Fields(s)
	hi := len(words)
	for hi > 1 && titleStopwords[strings.ToLower(strings.Trim(words[hi-1], ".,:;-"))] {
		hi--
	}
	if hi < 2 || hi == len(words) {
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

// packagingWord is the vocabulary of words that describe a book's PACKAGING rather
// than name it, in every language the catalogue holds. It is wideGenreFluff plus two
// groups that must NOT be in that list:
//
//   - "book"/"books" and the spelled-out numbers. wideGenreFluff drives the
//     genre-subtitle drop, where "book" is a real title word ("The Book Thief"), but
//     a RESIDUAL of "Book One" or "Blue Core, Book Three" minus its series name
//     names no book at all - and those were being proposed as titles.
//   - the multilingual packaging nouns. The English-only test let three different
//     Tao Wong series' omnibuses merge and proposed "- Band 5" as a German title.
//
// It is read by CarriesIdentity (does this residual name a book) and by
// IsCollection (is this title a collection). Both are refusal tests, so a word
// wrongly present costs a missed finding, never a wrong one - which is why the list
// can afford to be generous. Words that are ordinary title nouns in English are
// still kept out ("roman" is the Roman Empire as often as it is a novel).
var packagingWord = func() map[string]bool {
	m := make(map[string]bool, len(wideGenreFluff)+128)
	for w := range wideGenreFluff {
		m[w] = true
	}
	for _, w := range []string{
		// "book" alone. The spelled-out NUMBERS are deliberately absent: a bare
		// "Two" or "Three" is an ordinary title word ("Two Tales", "Three Men in a
		// Boat"), so a residual of "Book One" is reduced by stripping the word
		// volume MARKER as a phrase (see stripVolumePhrases) rather than by
		// declaring every number word packaging.
		"book", "books",
		// German.
		"band", "baende", "bande", "buch", "buecher", "bucher", "sammlung",
		"sammelband", "gesamtausgabe", "teil", "folge", "staffel", "hoerspiel",
		"hoerbuch", "ungekuerzt", "gekuerzt", "reihe", "gesamt", "ausgabe",
		// "Jahr N" numbers a volume in German serial fiction ("Die Akademie der
		// Goetter - Jahr 10"), and a residual of it names no book.
		"jahr", "jahre",
		// Spanish / Portuguese.
		"libro", "libros", "tomo", "tomos", "volumen", "volumenes", "coleccion",
		"completa", "completo", "edicion", "livro", "livros", "colecao", "serie",
		"novela", "parte", "partes", "episodio", "temporada",
		// French.
		"tome", "tomes", "livre", "livres", "recueil", "integrale", "coffret",
		"episode", "partie", "saison",
		// Italian.
		"libri", "raccolta", "volumi", "romanzo", "cofanetto",
		// Dutch / Nordic.
		"boek", "boeken", "deel", "delen", "verzameling", "bok", "boecker",
		"bocker", "bind", "samling",
	} {
		m[w] = true
	}
	return m
}()

// stripVolumePhrases removes volume markers - in digits or in words - as PHRASES,
// so "Book One" goes together. Reducing it word by word would need every number
// word in the packaging vocabulary, and a bare "Two" or "Three" is an ordinary
// title word ("Two Tales", "Three Men in a Boat").
func stripVolumePhrases(s string) string {
	s = matchNoise.ReplaceAllString(s, " ")
	return wordVolumeMarker.ReplaceAllString(s, " ")
}

// apostropheSplit turns the apostrophe glyphs into word boundaries. It runs BEFORE
// model.Slugify, which strips them and so WELDS the words either side: "L'integrale"
// became the single token "lintegrale", which no vocabulary holds, and a French
// omnibus therefore read as a book title.
var apostropheSplit = strings.NewReplacer(
	"'", " ", "’", " ", "‘", " ", "´", " ", "`", " ", "ʼ", " ", "ʻ", " ",
)

// identityWords splits a text into the ASCII-folded words the vocabulary tests read.
//
// FOLDING FIRST is what makes it work on the whole catalogue: notAlnum is ASCII-only,
// so splitting the raw text fragments every accented word ("integrale" arrived as
// "int" + "grale" and matched nothing). model.Slugify is the project's own folding,
// and the apostrophe pre-split above repairs the one thing it does that a word test
// must not inherit.
func identityWords(s string) []string {
	folded := model.Slugify(apostropheSplit.Replace(s))
	return strings.FieldsFunc(folded, func(r rune) bool { return r == '-' })
}

// numberWord are the spelled-out numbers. They are NOT packaging on their own - "Two
// Tales" and "Three Men in a Boat" are titles - so they only count as packaging
// alongside a packaging word, which is what "Episodes One and Two" is and "Two Tales"
// is not. See CarriesIdentity.
var numberWord = map[string]bool{
	"one": true, "two": true, "three": true, "four": true, "five": true,
	"six": true, "seven": true, "eight": true, "nine": true, "ten": true,
	"eleven": true, "twelve": true, "first": true, "second": true, "third": true,
	"fourth": true, "fifth": true, "sixth": true, "seventh": true, "eighth": true,
	"ninth": true, "tenth": true,
}

// structuralWord names a DIVISION of a product - a book, an episode, a volume, a
// collection - as opposed to describing its genre. Only these pair with a number
// word, because "Episodes One and Two" enumerates parts while "Two Tales" is a title
// whose second word happens to be a genre noun.
var structuralWord = func() map[string]bool {
	m := map[string]bool{}
	for w := range collectionWord {
		m[w] = true
	}
	for _, w := range []string{
		"book", "books", "episode", "episodes", "volume", "volumes", "part",
		"parts", "band", "baende", "bande", "buch", "teil", "tome", "tomes",
		"libro", "libri", "livre", "livres", "deel", "delen", "bind", "season",
		"staffel", "folge", "temporada", "saison", "jahr", "jahre", "level",
		"levels", "lesson", "lessons", "unit", "units",
	} {
		m[w] = true
	}
	return m
}()

// CarriesIdentity reports whether a cleaned title names a book of its own, or only
// describes its packaging ("Omnibus", "Box Set", "Book One", "- Band 5",
// "L'integrale", "Episodes One and Two" - all of them what is left once a series name
// comes off).
//
// A word is discounted when it is packaging vocabulary, or when it is a spelled-out
// NUMBER standing beside a STRUCTURAL word. That pairing is why the sets are separate:
// a number word alone is ordinary title material, and discounting every one of them
// turned "Two Tales" into a title that names nothing.
func CarriesIdentity(cleaned string) bool {
	words := identityWords(stripVolumePhrases(cleaned))
	structural := false
	for _, w := range words {
		if significantToken(w) && structuralWord[w] {
			structural = true
			break
		}
	}
	for _, w := range words {
		if !significantToken(w) || packagingWord[w] {
			continue
		}
		if structural && numberWord[w] {
			continue
		}
		return true
	}
	return false
}

// collectionWord is the subset of packagingWord that says "this is several books in
// one product" rather than merely "this is a book". A title carrying one on ONE side
// of a duplicate cluster is a companion collection beside a single volume, not a
// second record of it.
var collectionWord = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range []string{
		"omnibus", "boxset", "boxed", "collection", "collections", "collected",
		"gesammelte", "compendium",
		"anthology", "compilation", "bundle", "complete", "trilogy", "duology",
		"quartet", "quintet", "sammlung", "sammelband", "gesamtausgabe",
		"coleccion", "colecao", "raccolta", "recueil", "integrale", "coffret",
		"cofanetto", "verzameling", "samling", "volumes", "volumen", "volumenes",
		"tomos", "libros", "livres", "buecher", "bucher", "boeken", "baende",
		"bande", "bocker", "boecker", "gesamt",
	} {
		m[w] = true
	}
	return m
}()

// boxSetPhrase catches the collection shapes that are two words rather than one, so
// "Box Set" and "Complete Series" count however they are spaced.
var boxSetPhrase = regexp.MustCompile(`(?i)\b(box\s*set|boxed\s*set|complete\s+(?:series|collection|trilogy|saga)|books?\s+\d+\s*-\s*\d+)\b`)

// IsCollection reports whether a title announces itself as several books in one
// product, in any of the languages the catalogue holds.
func IsCollection(title string) bool {
	if boxSetPhrase.MatchString(title) {
		return true
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(model.Slugify(title)), notAlnum) {
		if collectionWord[w] {
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

// ---- the retitle proposal ----------------------------------------------------
//
// Clean above is a COMPARISON key: it may be lossy, because two titles that reduce
// to the same lossy string are still worth looking at. A PROPOSAL may not be lossy
// at all - it is a value a repair pass would write into the record - so it is a
// different function with different mechanics and a set of refusals.
//
// The mechanics that differ: a series name is removed only at a TITLE BOUNDARY (a
// whole leading segment, a whole trailing segment, or a whole bracketed group),
// never excised from the middle. Mid-title excision is what turned "More Than Words"
// into "Words", "The Screwtape Letters" into "The Letters" and "Do You Take This
// Man" into "Do You Take" - a measured 55% wrong rate, all of it this one mechanism.

// sepOffsets lists the offsets of every subtitle separator in s, ascending.
func sepOffsets(s string) []int {
	var out []int
	for i := 0; i < len(s); i++ {
		for _, alt := range subtitleSeps {
			if strings.HasPrefix(s[i:], alt) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// segmentIsSeriesOnly reports whether a title segment is made of NOTHING but a
// spelling of the series name, packaging vocabulary, volume markers and stopwords -
// and that at least one spelling actually occurred in it. That is what makes a
// segment safe to drop whole: everything it says, the series already says.
func segmentIsSeriesOnly(seg string, forms []string) bool {
	hit := false
	s := seg
	for _, f := range forms {
		before := s
		s = removeFoldBounded(s, f)
		if s != before {
			hit = true
		}
	}
	if !hit {
		return false
	}
	for _, w := range identityWords(stripVolumePhrases(s)) {
		if significantToken(w) && !packagingWord[w] {
			return false
		}
	}
	return true
}

// dropTrailingSeriesSegment drops the earliest trailing segment that is series-only -
// the "<title>: <Series>, Book N" shape.
//
// THE SEPARATOR DECIDES how much evidence is needed, and it is the whole difference
// between a catalogue tail and a title. A colon or a dash introduces a SUBTITLE, and
// a subtitle that is only the series name is decoration - "Hammered: The Iron Druid
// Chronicles, Book 3". A COMMA can be an apposition inside the sentence the title is:
// "Look Out, Secret Seven" is Enid Blyton's actual title and "Lights Out, Full
// Throttle" is a real one, and both were being stripped to their first half. After a
// comma the segment must also state a volume or call itself a collection.
func dropTrailingSeriesSegment(t string, forms []string) string {
	for _, off := range sepOffsets(t) {
		if off <= 0 {
			continue
		}
		head, tail := tidyTitle(t[:off]), t[off:]
		if head == "" || !segmentIsSeriesOnly(tail, forms) {
			continue
		}
		if strings.HasPrefix(tail, ", ") && !hasVolumeMarker(tail) && !hasCollectionWord(tail) {
			continue
		}
		return head
	}
	return t
}

// hasCollectionWord reports whether a text calls itself a collection or an omnibus.
//
// It reads collectionWord, NOT the whole packaging vocabulary. The wider list holds
// generic words that a real title uses ("full" is in it for "A Full Cast
// Dramatization"), and "Lights Out, Full Throttle" - a genuine title whose trailing
// segment happens to name a series - was stripped to "Lights Out" because "full"
// counted as packaging. Announcing a collection is a much narrower claim.
func hasCollectionWord(s string) bool {
	if boxSetPhrase.MatchString(s) {
		return true
	}
	for _, w := range identityWords(s) {
		if collectionWord[w] {
			return true
		}
	}
	return false
}

// dropLeadingSeriesSegment drops the SHORTEST leading segment that is series-only -
// the "<Series>: <title>" shape. Shortest on purpose: a longer head is a superset
// and dropping it would risk eating the book's own words.
//
// It refuses entirely when a LATER segment states a volume, because then the tail is
// the catalogue's series reference and the head is the book's own title - the
// opposite assignment. "Fate of the Fallen: The Song of the Tears, Book 1" was
// reduced to "The Song of the Tears", keeping the series and throwing the book away,
// and "The Faraway Paladin: Volume Three Secundus" to "Volume Three Secundus". A
// volume marker is what says which side of the separator the catalogue is on.
func dropLeadingSeriesSegment(t string, forms []string) string {
	offs := sepOffsets(t)
	for _, off := range offs {
		if off <= 0 || !segmentIsSeriesOnly(t[:off], forms) {
			continue
		}
		rest := tidyTitle(t[off:])
		// A BARE volume marker (one outside any bracketed group) is what says the
		// tail is the catalogue's series reference. A marker inside brackets is just
		// a volume: "The Last Apprentice: Curse of the Bane (Book 2)" is the leading
		// shape working correctly.
		if rest == "" || hasBareVolumeMarker(rest) {
			continue
		}
		return rest
	}
	return t
}

// dropSeriesBracketGroup drops a bracketed group whose contents are series-only.
// A bracketed group IS a boundary, so this needs no separator.
func dropSeriesBracketGroup(t string, forms []string) string {
	for _, loc := range parenGroup.FindAllStringIndex(t, -1) {
		inner := t[loc[0]+1 : loc[1]-1]
		if !segmentIsSeriesOnly(inner, forms) {
			continue
		}
		if out := tidyTitle(t[:loc[0]] + " " + t[loc[1]:]); out != "" {
			return out
		}
	}
	return t
}

// stripSeriesAtBoundary removes the series name only where it forms a whole segment
// or a whole bracketed group.
func stripSeriesAtBoundary(title string, forms []string) string {
	return peel(title, func(t string) string {
		if next := dropSeriesBracketGroup(t, forms); next != t {
			return next
		}
		if next := dropTrailingSeriesSegment(t, forms); next != t {
			return next
		}
		return dropLeadingSeriesSegment(t, forms)
	})
}

// dropDecorativeGroups removes only the bracketed groups that are DECORATION: an
// edition marker, a group made of nothing but packaging vocabulary and numbers, or
// one that is series-only. Every other group stays.
//
// The comparison key (Clean) removes every bracketed group, through the copied
// matchNoise, and for a key that is right - two titles that differ only inside
// brackets are worth comparing. For a PROPOSAL it is wrong: "Calming Nature Sounds
// (without music) for Deep Sleep" says something inside those brackets, and a
// retitle that drops it changes what the record claims.
func dropDecorativeGroups(s string, forms []string) string {
	return peel(s, func(t string) string {
		// The trailing SQUARE-bracket group first, and through its OWN span: it is
		// decoration whatever it holds (it restates the title in another language),
		// and parenGroup's character class stops at the first closer, so removing it
		// through that span leaves an orphan bracket behind and then dropStrayBrackets
		// strips a legitimate "(IV)" out of the title as collateral.
		if loc := bracketSuffixRE.FindStringIndex(t); loc != nil {
			if out := tidyTitle(t[:loc[0]]); out != "" {
				return out
			}
		}
		for _, loc := range parenGroup.FindAllStringIndex(t, -1) {
			inner := t[loc[0]+1 : loc[1]-1]
			decorative := HasEditionMarker(t[loc[0]:loc[1]]) ||
				segmentIsSeriesOnly(inner, forms) ||
				namesNoBook(inner) ||
				// "(Mundodisco 9)" - a name followed by a bare number is a series
				// and its volume, whether or not the catalogue holds that series.
				endsInBareNumber(inner)
			if !decorative {
				continue
			}
			if out := tidyTitle(t[:loc[0]] + " " + t[loc[1]:]); out != "" {
				return out
			}
		}
		return t
	})
}

// namesNoBook reports whether a text, once its volume markers are removed, holds
// nothing but packaging vocabulary - "(Book 2)", "(Unabridged Edition)", "(Books
// 1-3)", "(Complete Collection)". A group that reduces to nothing at all counts: it
// was only a volume marker.
//
// It requires the text to have held SOME alphanumeric content, so an empty group is
// not "decorative" by vacuity.
func namesNoBook(s string) bool {
	if !hasAlnum(s) {
		return false
	}
	for _, w := range identityWords(stripVolumePhrases(s)) {
		if significantToken(w) && !packagingWord[w] {
			return false
		}
	}
	return true
}

func hasAlnum(s string) bool {
	for _, r := range s {
		if isAlnumRune(r) {
			return true
		}
	}
	return false
}

// danglingLead are the words a residual must not BEGIN with: conjunctions and
// prepositions, which only ever join something to something else, so one at the
// front means the thing before it was cut away ("und die grosse Liebe"). Articles
// are deliberately absent - "The Jungle" is a title.
var danglingLead = map[string]bool{
	"and": true, "or": true, "nor": true, "of": true, "in": true, "on": true,
	"for": true, "with": true, "to": true, "at": true, "by": true, "from": true,
	"und": true, "oder": true, "mit": true, "von": true, "y": true, "et": true,
	"e": true, "och": true, "con": true, "avec": true, "van": true, "di": true,
	"da": true, "des": true, "du": true,
}

// hasDanglingConnective reports whether a residual reads as a fragment: it begins
// with a joining word, ends with any stopword or joining word, or holds a doubled
// function word ("The the Wandering Trader", the shape a stripped series name leaves
// when the title spelled its article too).
func hasDanglingConnective(s string) bool {
	ws := strings.FieldsFunc(strings.ToLower(s), notAlnum)
	if len(ws) == 0 {
		return true
	}
	if danglingLead[ws[0]] {
		return true
	}
	last := ws[len(ws)-1]
	if titleStopwords[last] || danglingLead[last] {
		return true
	}
	for i := 1; i < len(ws); i++ {
		if ws[i] == ws[i-1] && (titleStopwords[ws[i]] || danglingLead[ws[i]]) {
			return true
		}
	}
	return false
}

// SameModuloArticles reports whether two texts are the same name once articles and
// punctuation are set aside. A work whose title IS its series' name has nothing to
// propose: the "decoration" is the whole title.
func SameModuloArticles(a, b string) bool {
	return CompareKey(a) != "" && CompareKey(a) == CompareKey(b)
}

// The strip REFUSALS. StripDecoration returns one of these instead of a title
// when it will not propose one, so a consumer branches on the reason rather than
// on prose - the intake gate treats two of them as "a human must title this book"
// and the rest as "leave the title as submitted" (see internal/issueform).
const (
	// RefuseNothingToStrip: the rules removed nothing (or removed everything, or
	// somehow grew the title). There is no cleaner title to write.
	RefuseNothingToStrip = "nothing-to-strip"
	// RefuseNoIdentity: what is left names no book - "Book One", "- Band 5",
	// "Las", "2" (CarriesIdentity, multilingual). Judged on the RESIDUAL ALONE: a
	// guard that also required the ORIGINAL to carry identity let every all-fluff
	// title through, which is precisely where the degenerate proposals came from.
	RefuseNoIdentity = "residual-names-no-book"
	// RefuseFragment: what is left reads as a fragment - it begins or ends with a
	// joining word, or doubles a function word (hasDanglingConnective).
	RefuseFragment = "residual-is-a-fragment"
	// RefuseIsSeriesName: the title IS the series name modulo articles, so the
	// whole title would go. Nothing is wrong with such a title - a one-book series
	// really is named after its book - there is simply nothing to propose.
	RefuseIsSeriesName = "title-is-the-series-name"
	// RefuseResultIsSeriesName: the STRIP turns the title into the series name
	// ("Scarlet and Ivy: Audio Collection Books 1-3" reduces to "Scarlet and
	// Ivy"), which would make a collection indistinguishable from its series.
	RefuseResultIsSeriesName = "result-is-the-series-name"
)

// RefusalCodes lists every code StripDecoration can refuse with.
//
// It exists so a consumer that BRANCHES on the reason can be checked for
// exhaustiveness against this package rather than against a hand-copied list:
// internal/issueform maps each code onto "proceed" or "a maintainer must title this
// book", and a code added here without a decision recorded there fails its test.
func RefusalCodes() []string {
	return []string{
		RefuseNothingToStrip, RefuseNoIdentity, RefuseFragment,
		RefuseIsSeriesName, RefuseResultIsSeriesName,
	}
}

// ProposeTitle is the title a retitle would WRITE, and whether proposing one is
// safe at all. series may be "". It is StripDecoration without the refusal code,
// for the callers that only need to know whether there is a title to write.
func ProposeTitle(title, series string) (string, bool) {
	proposed, _, ok := StripDecoration(title, series)
	return proposed, ok
}

// StripDecoration is ProposeTitle plus the REASON it refused, which is what a
// consumer that has to answer a contributor needs: "the title you submitted is
// decoration and I cannot mechanically derive the book's name from it" is a
// different message from "there was nothing to clean".
//
// refusal is one of the RefuseX codes above when ok is false, and "" when ok is
// true. It refuses rather than guessing in five cases, each measured - see the
// codes for what each one is and why.
func StripDecoration(title, series string) (proposed, refusal string, ok bool) {
	orig := strings.TrimSpace(title)
	s := dropWideGenreSubtitle(orig)
	if series != "" {
		if SameModuloArticles(orig, series) {
			return "", RefuseIsSeriesName, false
		}
		s = stripSeriesAtBoundary(s, SeriesForms(series))
	}
	s = dropDecorativeGroups(s, SeriesForms(series))
	s = tidyTitle(markerSeq.ReplaceAllString(s, " "))
	s = tidyTitle(wordVolumeMarker.ReplaceAllString(s, " "))
	s = tidyTitle(fluffWords.ReplaceAllString(s, " "))
	s = dropStrayBrackets(s)
	s = collapseSeparatorRuns(s)
	s = peel(s, dropDanglingHeadOnce)
	s = peel(s, dropDanglingTailOnce)
	s = trimStopwordTail(s)

	switch {
	case s == "" || s == orig || len(s) > len(orig):
		return "", RefuseNothingToStrip, false
	case !CarriesIdentity(s):
		return "", RefuseNoIdentity, false
	case hasDanglingConnective(s):
		return "", RefuseFragment, false
	case series != "" && SameModuloArticles(s, series):
		// The RESULT is the series' name. "Scarlet and Ivy: Audio Collection Books
		// 1-3" reduces to "Scarlet and Ivy", which is what the series is called, so
		// the collection would become indistinguishable from the series itself. The
		// same test on the original catches only the titles that were already the
		// series name; this one catches the ones the strip turns into it.
		return "", RefuseResultIsSeriesName, false
	}
	return s, "", true
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
		return bracketSuffixRE.MatchString(f.Title) && !HasEditionMarker(f.Title)
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
	{DecEdition, func(f TitleFacts) bool { return HasEditionMarker(f.Title) }},
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
	// A REST that carries a volume marker is the series reference, which makes the
	// HEAD the book's own title - the opposite assignment. "A Grim Awakening: The
	// Forest of Hollow, Book 1" would otherwise be proposed as "The Forest of
	// Hollow", dropping the book's title and keeping the series. Which of the two
	// segments is the series is then genuinely ambiguous, so the rule declines.
	if hasVolumeMarker(tail) {
		return "", "", "", false
	}
	return m[1], head, tail, true
}

// wordVolumeMarker matches a volume marker spelled with a word rather than a digit
// ("Book Two", "Part One"), which markerSeq cannot see because it requires \d.
var wordVolumeMarker = regexp.MustCompile(`(?i)\b(?:books?|bks?|vols?|volumes?|parts?|pts?|episodes?|eps?|b(?:a|ae|ä)nde?|teile?|tomes?|libros?)\s+` +
	`(?:one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|i{1,3}|iv|v|vi{1,3}|ix|x)\b`)

// hasVolumeMarker reports whether a text states a volume, in digits or in words.
func hasVolumeMarker(s string) bool {
	return markerSeq.MatchString(s) || wordVolumeMarker.MatchString(s)
}

// hasBareVolumeMarker reports a volume marker OUTSIDE any bracketed group. The
// distinction decides which side of a separator the catalogue is on: a bare ", Book
// 1" marks the tail as the series reference, while a bracketed "(Book 2)" is only a
// volume on a title that is otherwise the book's own.
func hasBareVolumeMarker(s string) bool {
	return hasVolumeMarker(parenGroup.ReplaceAllString(s, " "))
}

// HasVolumeMarker is hasVolumeMarker's exported door, for a caller judging whether a
// title states its own position.
func HasVolumeMarker(s string) bool { return hasVolumeMarker(s) }

// endsInBareNumber reports whether a text's last significant token is a plain
// number - the "(Mundodisco 9)", "(Discworld 9)" shape, where a bracketed group
// states a series and its volume with no keyword between them.
func endsInBareNumber(s string) bool {
	ws := identityWords(s)
	for i := len(ws) - 1; i >= 0; i-- {
		if len(ws[i]) == 0 {
			continue
		}
		return isAllDigits(ws[i]) && i > 0
	}
	return false
}

// StatesSeriesAndVolume reports whether a title states the series reference in the
// explicit boundary shape "<...>: <Series>, Book N" - a whole trailing segment made
// of the series name and a volume marker, and nothing else.
//
// That shape is CORROBORATION. A series name is often two ordinary words, and a
// title merely containing them is a coincidence as often as a fact; a title that
// hangs the name and its number off a separator is stating a membership. It is the
// same segment test the boundary strip uses, with the volume marker required rather
// than merely tolerated.
func StatesSeriesAndVolume(title, series string) bool {
	if series == "" {
		return false
	}
	forms := SeriesForms(series)
	for _, off := range sepOffsets(title) {
		if off <= 0 {
			continue
		}
		tail := title[off:]
		if hasVolumeMarker(tail) && segmentIsSeriesOnly(tail, forms) && tidyTitle(title[:off]) != "" {
			return true
		}
	}
	return false
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
//
// THE ORDER IS THE ARGUMENT. What survives a merge should be the most canonical
// RECORD, because everything else MOVES onto it - recordings, sidecars and series
// memberships are all re-pointed by the repair. So the rungs that describe the
// record itself come first (is it modeled, is its title clean) and the rungs that
// describe what hangs off it come last.
//
// The first draft had HasSidecar second, above Recordings, and it picked "The Secret
// Garden (Dramatized)" - one recording, one sidecar - over the clean "The Secret
// Garden" with forty-one. A sidecar is a REASON TO BE CAREFUL (REF-SIDECAR reports
// exactly that) and not a reason to survive.
type WorkRank struct {
	// InSeries: the work already has a series membership - it is the modeled one,
	// and its memberships are the ones that would not have to move.
	InSeries bool
	// Decorations is how many retailer decorations its title carries (fewer wins).
	// Above Recordings on purpose: the survivor's title is what every consumer
	// sees, and a clean title cannot be recovered by moving anything.
	Decorations int
	// TitleLen is its title's length in bytes (shorter wins) - the tiebreak between
	// two equally undecorated titles.
	TitleLen int
	// Recordings is how many recordings hang off the work. They move with the
	// merge, so this only breaks a tie between equally canonical records.
	Recordings int
	// HasSidecar: a works-community entry is keyed by this slug. It moves too, so
	// it ranks last but one.
	HasSidecar bool
	// ID breaks every remaining tie, so the choice never depends on input order.
	ID string
}

// Better reports whether r outranks o. Every rung is data, so the answer is the
// same on every run and in every consumer.
func (r WorkRank) Better(o WorkRank) bool {
	switch {
	case r.InSeries != o.InSeries:
		return r.InSeries
	case r.Decorations != o.Decorations:
		return r.Decorations < o.Decorations
	case r.TitleLen != o.TitleLen:
		return r.TitleLen < o.TitleLen
	case r.Recordings != o.Recordings:
		return r.Recordings > o.Recordings
	case r.HasSidecar != o.HasSidecar:
		return r.HasSidecar
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
