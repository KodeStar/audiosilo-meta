package audit

import (
	"regexp"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// title.go is the audit's own title layer, LAYERED over the copied match.go
// rather than edited into it, so that file stays diffable against the server's
// pkg/match (see its header).
//
// Two things live here. First, a WIDER genre-subtitle vocabulary: the server's
// list is tuned for matching a user's library against a catalogue, where dropping
// a real subtitle costs a missed match, while the audit only ever REPORTS, so it
// can afford the retailer marketing words the narrow list leaves in ("Seeds of
// Chaos Omnibus: A GameLit Dark Adventure Series" turns on "dark", which is not
// in the server's list, and the pair it duplicates is otherwise invisible).
// Second, the decoration DETECTORS: what a title carries that a title should not.

// wideGenreFluff extends genreFluff with the marketing vocabulary a retailer
// genre subtitle is written in. Every entry is a word that describes a book's
// CATEGORY rather than naming the book, and the rule that reads it is unchanged:
// the tail is dropped only when EVERY significant word in it is fluff, so ": A
// Novel of the Roman Empire" survives ("roman", "empire") while ": A Dark LitRPG
// Adventure" does not.
//
// Kept out deliberately: "book", "chronicle(s)", "legend(s)", "world" and
// "academy", each of which routinely names a real subtitle or sub-series.
var wideGenreFluff = func() map[string]bool {
	m := make(map[string]bool, len(genreFluff)+64)
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
		// Packaging / format words. "series" and "novels" are here as well as in
		// the copied fluffWords regexp, which removes them as standalone tokens
		// but is applied AFTER the subtitle rule - so without them a tail reading
		// ": A GameLit Dark Adventure Series" was not all-fluff and survived whole.
		"series", "novels",
		// Structural words a residual is left with once the series name comes
		// off. Measured need: the eight Pimsleur language courses all reduce to
		// "Level 2 Lessons 21-25", which titleCarriesIdentity must call
		// identity-less so W-DUP keys them by their series and keeps them apart.
		"level", "levels", "lesson", "lessons", "unit", "units", "course",
		"season", "episode", "episodes", "volume", "volumes", "part", "parts",
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

// dropWideGenreSubtitle strips trailing all-fluff subtitles under
// wideGenreFluff, repeatedly: a retailer really does stack two of them ("...: A
// LitRPG Adventure: Book One of a New Series"). The loop is bounded so a
// pathological title cannot spin.
func dropWideGenreSubtitle(s string) string {
	for i := 0; i < 4; i++ {
		next := dropFluffSubtitle(s, wideGenreFluff)
		if next == s {
			return s
		}
		s = next
	}
	return s
}

// auditCleanTitle is the audit's cleaned title: the wide genre-subtitle drop,
// then the server's CleanTitle against series (which strips the series name,
// volume markers, edition markers and the narrow fluff, and falls back rather
// than emptying the title), then the dangling-tail sweep below.
//
// series may be "" - a work with no membership and no series name embedded in its
// title still gets its markers and fluff removed.
func auditCleanTitle(title, series string) string {
	s := CleanTitle(dropWideGenreSubtitle(title), series)
	s = dropStrayBrackets(s)
	s = dropDanglingHead(s)
	s = dropDanglingTail(s)
	return trimStopwordTail(s)
}

// dropStrayBrackets removes bracket characters left UNBALANCED by the bracket-group
// removal, which cannot see a nested group: bracketGroup's character class stops at
// the first closer, so "Eric (Mundodisco 9) [Eric (Discworld)]" loses
// "[Eric (Discworld)" and keeps the orphan "]".
//
// It only fires when the brackets do not balance, so a title that legitimately
// carries a matched pair is untouched.
func dropStrayBrackets(s string) string {
	if bracketsBalance(s, '(', ')') && bracketsBalance(s, '[', ']') {
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

func bracketsBalance(s string, open, close rune) bool {
	return strings.Count(s, string(open)) == strings.Count(s, string(close))
}

// dropDanglingHead removes a LEADING segment left holding nothing but stopwords -
// the mirror of dropDanglingTail, and the shape a retailer's article-plus-series
// prefix leaves once the series name comes off ("A How to Train Your Dragon: A
// Hero's Guide..." against that series reduces to "A : A Hero's Guide...").
//
// The head test is stopwords-ONLY rather than dropDanglingTail's "no significant
// tokens", because tokenize also discards pure numbers and a leading number is
// routinely the whole identity of a title ("1984: The Novel" must keep its 1984).
func dropDanglingHead(s string) string {
	for i := 0; i < 4; i++ {
		sep := firstSeparator(s)
		if sep <= 0 || !stopwordsOnly(s[:sep]) {
			return s
		}
		next := tidyTitle(s[sep:])
		if next == "" {
			return s
		}
		s = next
	}
	return s
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

// firstSeparator is the offset of the first subtitle separator in s, or -1.
func firstSeparator(s string) int {
	sep := -1
	for _, alt := range []string{": ", " - ", ", "} {
		if i := strings.Index(s, alt); i >= 0 && (sep < 0 || i < sep) {
			sep = i
		}
	}
	return sep
}

// dropDanglingTail removes a trailing segment left holding nothing significant -
// only articles, prepositions, single letters and numbers.
//
// It exists because removing a series name from the MIDDLE of a title leaves the
// article that introduced it behind: "Hammered: The Iron Druid Chronicles, Book 3"
// against that series reduces to "Hammered: The", which is not the same key as the
// plain "Hammered" it duplicates. CleanTitle's own tidyTitle only trims separator
// characters, not the words stranded between them.
//
// A tail is only dropped when tokenize finds nothing in it, so a real subtitle
// ("Star Wars: A New Hope" -> "new", "hope") is never touched. Bounded, because a
// title can strand more than one ("...: The, Book 3" -> "...: The" -> "...").
func dropDanglingTail(s string) string {
	for i := 0; i < 4; i++ {
		sep := lastSeparator(s)
		if sep <= 0 || len(tokenize(s[sep:])) > 0 {
			return s
		}
		next := tidyTitle(s[:sep])
		if next == "" {
			return s // never empty a title: the whole of it would be "dangling"
		}
		s = next
	}
	return s
}

// trimStopwordTail drops the stopwords stranded at the END of a cleaned title.
// Removing a series name from the middle of a title leaves the preposition that
// led into it behind with no separator to hang off ("Two Tales of the Iron Druid
// Chronicles" against that series reduces to "Two Tales of the"), which
// dropDanglingTail cannot see because there is no segment boundary there.
//
// Deliberately TAIL-ONLY. A leading article is part of a title ("A Deadly Cliché"
// is not "Deadly Cliché"), so trimming it here would make W-TITLE propose a wrong
// retitle. Where article-insensitivity IS wanted - comparing two titles for
// identity - the caller drops it on the KEY instead (see index.titleCompareKey),
// which is the same split the server's own NormalizeSeries makes one level up.
//
// It never empties the title: a title genuinely made of nothing but stopwords
// ("The One and Only") keeps its words rather than becoming "".
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

// titleCompareKey is a cleaned title's comparison identity: foldKey with a leading
// article dropped first, so "A Deadly Cliché" and "Deadly Cliché" are one key
// while the titles themselves are left as their authors wrote them.
//
// The split is deliberate and is the same one the server's NormalizeSeries makes for series
// names: article-insensitivity belongs to the COMPARISON, never to the value a
// repair pass would write back.
func titleCompareKey(cleaned string) string { return foldKey(dropLeadingArticle(cleaned)) }

// titleCarriesIdentity reports whether a cleaned title holds at least one word
// that is not packaging vocabulary. A title that does not ("Omnibus", "Box Set",
// "Complete Collection" - all that is left once the series name comes off) names
// no book of its own, so W-DUP will not group two of them on the title alone.
func titleCarriesIdentity(cleaned string) bool {
	for w := range tokenize(cleaned) {
		if !wideGenreFluff[w] {
			return true
		}
	}
	return false
}

// lastSeparator is the offset of the last subtitle separator in s, or -1. It reads
// the same two separators dropFluffSubtitle does, plus the comma a volume marker
// is usually hung off.
func lastSeparator(s string) int {
	sep := strings.LastIndex(s, ": ")
	for _, alt := range []string{" - ", ", "} {
		if i := strings.LastIndex(s, alt); i > sep {
			sep = i
		}
	}
	return sep
}

// foldKey is the audit's identity key for a free-text name: the project's own
// diacritic folding and punctuation rules (model.Slugify, the ONE definition of
// what text becomes a slug) with the hyphens removed, so a difference that is
// only spacing or punctuation is not an identity.
//
// Removing the hyphens is what makes it COARSER than a slug, which matters: a
// person's id already IS model.PersonSlug(name) (pkg/check enforces it), so
// grouping people by their slug would find nothing by construction, while
// "A.B. Kovacs" (a-b-kovacs) and "AB Kovacs" (ab-kovacs) meet here.
//
// It is also the reason the server's Normalize is not among the copied functions
// (see match.go's tail): that one drops every non-ASCII rune, where this folds
// them, so "Café Society" and "Cafe Society" are one key.
func foldKey(s string) string { return strings.ReplaceAll(model.Slugify(s), "-", "") }

// Decoration subclass codes. They name what the title carries, and they are the
// subclass values of W-TITLE findings and part of F-HYGIENE's, so they are
// written once here.
const (
	decEdition       = "edition-marker"
	decVolume        = "volume-marker"
	decBracketSuffix = "bracket-suffix"
	decGenreSubtitle = "genre-subtitle"
	decSeriesName    = "series-name"
	decArticleSeries = "article-series-prefix"
	decTrailingPunct = "trailing-separator"
)

// editionMarker matches a trailing (Unabridged)/(Abridged) edition marker, in
// brackets or bare. The importer strips this shape before work identity
// (cleanWorkTitle), so a title still carrying it was written by something that
// did not.
var editionMarker = regexp.MustCompile(`(?i)[\(\[]?\s*(?:un)?abridged\s*[\)\]]?\s*$`)

// bracketSuffix matches a trailing bracketed group, the shape a retailer uses to
// print a second language's title after the local one ("Eric (Mundodisco 9)
// [Eric (Discworld)]"). Parentheses are NOT this rule - a parenthetical is
// usually the series or the volume, which have their own subclasses.
var bracketSuffix = regexp.MustCompile(`\[[^\]]*\]\s*$`)

// volumeMarkerAnywhere is markerSeq without the capture: "does this title spell a
// volume number out at all".
var volumeMarkerAnywhere = markerSeq

// trailingSeparator matches a title left ending in a separator, which is what a
// half-cleaned title looks like.
var trailingSeparator = regexp.MustCompile(`[\s\-:;,|]+$`)

// leadingArticleRE captures a leading article and the text after it.
var leadingArticleRE = regexp.MustCompile(`(?i)^(a|an|the)\s+(.+)$`)

// subtitleSeparator finds where a title's subtitle starts, using the same two
// separators dropFluffSubtitle reads.
func subtitleSeparator(s string) int {
	sep := strings.Index(s, ": ")
	if d := strings.Index(s, " - "); d >= 0 && (sep < 0 || d < sep) {
		sep = d
	}
	return sep
}

// articleSeriesPrefix reports whether title is "<article> <series name><sep>
// <rest>" - a retailer prefixing a book with an article and its SERIES name, so
// the article belongs to nothing ("A How to Train Your Dragon: A Hero's Guide to
// Deadly Dragons", whose slug then leads with a dangling `a-`). seriesFormIn
// resolves a series name occurring in the given text, returning the matched
// spelling.
//
// It requires the series name to fill the whole span between the article and the
// separator, so an ordinary "A Study in Scarlet: ..." is untouched unless a
// series is really called "Study in Scarlet".
func articleSeriesPrefix(title string, seriesFormIn func(string) (string, bool)) (article, series, rest string, ok bool) {
	m := leadingArticleRE.FindStringSubmatch(title)
	if m == nil {
		return "", "", "", false
	}
	sep := subtitleSeparator(m[2])
	if sep <= 0 {
		return "", "", "", false
	}
	head, tail := strings.TrimSpace(m[2][:sep]), strings.TrimSpace(tidyTitle(m[2][sep:]))
	if head == "" || tail == "" {
		return "", "", "", false
	}
	form, found := seriesFormIn(head)
	if !found || foldKey(form) != foldKey(head) {
		return "", "", "", false
	}
	return m[1], head, tail, true
}
