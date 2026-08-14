// match.go is COPIED, not imported.
//
// Source: github.com/kodestar/audiosilo-server, pkg/match/match.go, at commit
// 275e8cd5373d152e24d97488766c0c018c76aff2 (the sibling checkout's main).
//
// Why a copy: the behaviour this audit needs - seriesForms/seriesRefIn (the
// unified series spellings) and the bounded stripSeries they feed - landed AFTER
// the server's newest tag v1.9.0 (commits e337363 and 8005d23 touch pkg/match),
// so a `go get github.com/kodestar/audiosilo-server@v1.9.0` would pull the
// version that predates it, and a `replace` directive to the local checkout is
// not resolvable in CI. When the server cuts the tag that carries these commits,
// this file should be deleted and pkg/match imported instead; nothing outside it
// (and title.go, which layers the audit's own vocabulary on top) depends on the
// duplication.
//
// What was taken: the title/series NORMALIZATION half of the package. The
// scoring half (Book, Query, Best, tokenScore, titleCovers, personMatch and the
// score ladder) answers "are these two records the same book", which this package
// does not ask - its callers group by an exact key instead - and is deliberately
// left out rather than copied unused.
//
// THE DELTAS. This is not a byte-identical copy, and pretending otherwise would
// make the next re-diff untrustworthy. Every difference from the source is listed
// here, and there are no others:
//
//  1. removeFold/removeFoldWith collapsed into removeFoldBounded. Only the
//     BOUNDED form is reachable from what was taken (stripSeries), so the
//     unbounded entry point and the `bounded bool` parameter threaded through it
//     are gone; the body is otherwise the source's, including its rune-offset
//     mapping and the `prev` carry.
//  2. dropGenreSubtitle split into itself plus dropFluffSubtitle(s, fluff), which
//     takes the vocabulary as a parameter. The logic is unchanged and
//     dropGenreSubtitle still passes genreFluff; the seam exists so the wider
//     vocabulary in rules.go reuses the tail-splitting rather than re-spelling
//     where a subtitle starts.
//  3. Normalize and NormalizeSeries not taken at all - see the note at the tail
//     of this file for why.
//  4. SeqFromTitle, firstNumber, titleTokens, tokenScore, titleCovers and the
//     Book/Query/Best scoring surface not taken (nothing here calls them).
//  5. dropSeriesSuffix's body parameterized as dropOneSuffix(s, suffixes), the
//     same seam as (2) and for the same reason: rules.go's wider
//     catalogue-decoration peel (seriesKey) is that rule under a longer list, not
//     a second rule. dropSeriesSuffix still passes the source's single " series".
//  6. tokenize replaced by its own predicate, significantToken, plus the two
//     questions the retained callers actually ask (CountSignificantWords,
//     hasSignificantToken in rules.go). The token RULE is byte-for-byte the
//     source's; only the map it built - which nothing retained needs, since the
//     scoring half that consumed it was not taken - is gone.
//  7. seriesRefIn moved to rules.go as the exported SeriesRefIn, unchanged except
//     that it reads the MEMOIZED SeriesForms. Its callers ask tens of millions of
//     times over a full tree for tens of thousands of distinct names, and
//     seriesForms allocates.
//  8. Comment prose trimmed where it referred to the scoring half that was not
//     taken, and em dashes converted to hyphens (a workspace-wide rule). No
//     comment that describes retained BEHAVIOUR was changed.
//
// Deliberately UNCHANGED otherwise: every widening of the vocabulary lives in
// rules.go, so this file stays diffable against the server's copy.
package titlerule

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// bracketGroup matches one parenthetical/bracketed group. Defined once and shared
// by matchNoise and parenGroup so the bracket rule has a single definition.
const bracketGroup = `[\(\[][^\)\]]*[\)\]]`

// matchNoise strips parenthetical/bracketed groups and "book N"/"vol N" run tokens -
// the formatting that differs between sources' titles.
var matchNoise = regexp.MustCompile(`(?i)` + bracketGroup + `|\b(?:book|bk|vol|volume|part|pt|episode|ep|#)\s*\.?\s*\d+(?:\.\d+)?\b`)

// fluffWords are standalone words that are noise in a book title (not "book"/"novel",
// which appear in real titles like "The Book Thief").
var fluffWords = regexp.MustCompile(`(?i)\b(?:series|unabridged|abridged|audiobook|dramatized|adaptation)\b`)

// CleanTitle returns a display title with the series name and edition/"Book N" fluff
// removed: "Diary of a Wimpy Kid: The Ugly Truth (Book 5)" + series "Diary of a
// Wimpy Kid" -> "The Ugly Truth". Useful both to name a file and (tokenized) to
// match. Falls back to the original (minus parentheticals) if cleaning empties it.
func CleanTitle(title, series string) string {
	t := residualTitle(title, series)
	if t == "" {
		t = tidyTitle(matchNoise.ReplaceAllString(title, " "))
	}
	if t == "" {
		t = strings.TrimSpace(title)
	}
	return t
}

// residualTitle is the title with the series name and numbering/edition fluff
// removed and NO fallback - empty when the title carries nothing of its own
// ("Unintended Cultivator: Volume 9" with that series is pure series + number).
func residualTitle(title, series string) string {
	t := stripSeries(title, series)
	t = dropGenreSubtitle(t)
	t = matchNoise.ReplaceAllString(t, " ")
	t = fluffWords.ReplaceAllString(t, " ")
	return tidyTitle(t)
}

// seriesForms enumerates the spellings one series name appears in, most specific
// first: as given, without a parenthetical ordering note ("Vorkosigan Saga
// (chronological)"), without the catalog's trailing " Series" decoration
// (Audible's "Dragon Heart Series" vs a title that says "Dragon Heart"), without a
// leading article, and the combinations of those. ONE list, used both to FIND a
// series name in a title (seriesRefIn) and to REMOVE it (stripSeries), so the two
// can never disagree about what the series is called - a form that links a pair
// is by construction a form that strips.
func seriesForms(series string) []string {
	base := strings.TrimSpace(series)
	if base == "" {
		return nil
	}
	var out []string
	add := func(s string) {
		if s = tidyTitle(s); s == "" {
			return
		}
		for _, prev := range out {
			if strings.EqualFold(prev, s) {
				return
			}
		}
		out = append(out, s)
	}
	for _, s := range [2]string{base, stripParenGroups(base)} {
		noSuffix := dropSeriesSuffix(s)
		add(s)
		add(noSuffix)
		add(dropLeadingArticle(s))
		add(dropLeadingArticle(noSuffix))
	}
	return out
}

// seriesRefIn's job is done by rules.go's exported SeriesRefIn, which is the same
// loop over the MEMOIZED forms - see delta (8).

// parenGroup matches a parenthetical/bracketed decoration on a series name:
// "Vorkosigan Saga (chronological)" is the catalog's ordering note, not part of
// the name a library title carries.
var parenGroup = regexp.MustCompile(bracketGroup)

func stripParenGroups(s string) string {
	if !strings.ContainsAny(s, "([") {
		return s // the common case, kept off the regexp
	}
	return parenGroup.ReplaceAllString(s, " ")
}

// dropLeadingArticle removes a leading "the "/"a "/"an ", never emptying the
// string - an article-only name keeps its one word.
func dropLeadingArticle(s string) string {
	t := strings.TrimSpace(s)
	for _, art := range []string{"the ", "a ", "an "} {
		if len(t) > len(art) && strings.EqualFold(t[:len(art)], art) {
			return t[len(art):]
		}
	}
	return t
}

// dropSeriesSuffix removes a trailing " Series", never emptying the string, so a
// series genuinely named "Series" survives.
func dropSeriesSuffix(s string) string { return dropOneSuffix(s, seriesSuffixOnly) }

// seriesSuffixOnly is dropSeriesSuffix's vocabulary: the source's single suffix.
var seriesSuffixOnly = []string{" series"}

// dropOneSuffix removes the FIRST matching trailing suffix from s, never emptying
// the string. It is dropSeriesSuffix's body with the vocabulary as a parameter -
// the same seam dropFluffSubtitle has - so the wider catalogue-decoration list in
// seriesdup.go peels through this rule rather than spelling a second one. The
// suffixes are compared case-insensitively and are expected lowercase.
func dropOneSuffix(s string, suffixes []string) string {
	t := strings.TrimSpace(s)
	for _, suf := range suffixes {
		if len(t) > len(suf) && strings.EqualFold(t[len(t)-len(suf):], suf) {
			return strings.TrimSpace(t[:len(t)-len(suf)])
		}
	}
	return t
}

// containsPhraseLower reports whether the already-lowercased ls contains sub
// case-insensitively at alphanumeric boundaries. The boundaries matter: a short
// series name ("Land") must not link itself to an unrelated title that merely
// embeds it ("The Landlord's Daughter").
func containsPhraseLower(ls, sub string) bool {
	// Offsets index the lowered copies only, and lowering preserves whether a rune
	// is alphanumeric, so the boundary test needs no mapping back onto the original.
	lsub := strings.ToLower(sub)
	if lsub == "" {
		return false
	}
	for from := 0; ; {
		i := strings.Index(ls[from:], lsub)
		if i < 0 {
			return false
		}
		i += from
		if boundedAt(ls, i, i+len(lsub)) {
			return true
		}
		from = i + 1
	}
}

// boundedAt reports whether s[start:end] sits on alphanumeric boundaries.
// Decoding an empty edge slice yields RuneError, which is neither letter nor
// digit, so the ends of the string need no special case.
func boundedAt(s string, start, end int) bool {
	before, _ := utf8.DecodeLastRuneInString(s[:start])
	after, _ := utf8.DecodeRuneInString(s[end:])
	return !isAlnumRune(before) && !isAlnumRune(after)
}

// isAlnumRune is deliberately Unicode-aware, unlike the ASCII-only notAlnum: it
// classifies the raw runes of real titles and series names, where notAlnum is the
// splitter that reduces a title to ASCII word tokens. Do not merge them.
func isAlnumRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// markerSeq matches an explicit sequence marker ("Volume 9", "Book 3", "(Book 5)" -
// the marker half of matchNoise) and captures its number.
var markerSeq = regexp.MustCompile(`(?i)\b(?:book|bk|vol|volume|part|pt|episode|ep|#)\s*\.?\s*(\d+(?:\.\d+)?)\b`)

// bareSeq derives the volume number of a bare "series + number" title from its
// explicit marker ("... Volume 9") or, failing that, from a residual that is nothing
// but the number ("Series Name 3"). It states a number the title itself spells
// out, so it never grabs an incidental one (a "(2015)" year, a digit-named series
// like "86").
func bareSeq(title, series string) (float64, bool) {
	t := stripSeries(title, series)
	if m := markerSeq.FindStringSubmatch(t); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v, true
		}
	}
	if s := tidyTitle(t); isAllDigits(s) {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v, true
		}
	}
	return 0, false
}

// stripSeries removes every spelling of the series name (seriesForms) from a
// title. Removal is WORD-BOUNDARY-aware: series "Land Series" contributes the form
// "Land", and an unbounded removal turned the unrelated title "The Landlord" into
// "The lord", destroying its exact-title match.
func stripSeries(title, series string) string {
	t := title
	for _, form := range seriesForms(series) {
		t = removeFoldBounded(t, form)
	}
	return t
}

// tidyTitle collapses whitespace and trims stray leading/trailing separators left by
// removing the series/fluff (": ", " - ", ", ").
func tidyTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.Trim(s, " -:,;|.")
}

// removeFoldBounded removes every case-insensitive occurrence of sub from s that
// sits on alphanumeric boundaries, replacing each with a space, so a series form
// can never be cut out of the middle of a word.
func removeFoldBounded(s, sub string) string {
	lsub := strings.ToLower(sub)
	if lsub == "" {
		return s
	}
	var b strings.Builder
	// The rune already written, so a boundary test at the start of the REMAINING
	// text still sees what preceded it in the original ("aLandLand" must reject
	// both occurrences of "Land", not just the first).
	prev := utf8.RuneError
	for s != "" {
		ls := strings.ToLower(s)
		i := strings.Index(ls, lsub)
		if i < 0 {
			b.WriteString(s)
			break
		}
		// Map the lowercased match span [i, i+len(lsub)) onto byte offsets in s by
		// lowering s one rune at a time until the lowered length reaches each bound.
		start, end, lowered := -1, -1, 0
		for off, r := range s {
			if lowered == i {
				start = off
			}
			lowered += len(strings.ToLower(string(r)))
			if lowered == i+len(lsub) {
				end = off + len(string(r))
				break
			}
		}
		if start < 0 || end < 0 {
			// Match straddles a rune whose case expansion has no clean byte boundary;
			// leave the remainder untouched rather than corrupt it.
			b.WriteString(s)
			break
		}
		before := prev
		if i > 0 {
			before, _ = utf8.DecodeLastRuneInString(ls[:i])
		}
		after, _ := utf8.DecodeRuneInString(ls[i+len(lsub):])
		if isAlnumRune(before) || isAlnumRune(after) {
			b.WriteString(s[:end])
			prev, _ = utf8.DecodeLastRuneInString(s[:end])
		} else {
			b.WriteString(s[:start])
			b.WriteByte(' ')
			prev = ' '
		}
		s = s[end:]
	}
	return b.String()
}

// genreFluff marks a trailing ": ..."/" - ..." subtitle made up entirely of these
// words as boilerplate to drop - "Dungeon Crawler Carl: A LitRPG/Gamelit
// Adventure" -> "Dungeon Crawler Carl".
//
// The audit LAYERS a wider vocabulary over this one rather than editing it; see
// wideGenreFluff in title.go.
var genreFluff = map[string]bool{
	"litrpg": true, "gamelit": true, "gamlit": true, "progression": true,
	"fantasy": true, "epic": true, "novel": true, "novella": true, "saga": true,
	"adventure": true, "story": true, "tale": true, "tales": true, "cozy": true,
	"mystery": true, "romance": true, "thriller": true, "audible": true,
	"original": true, "dramatized": true, "adaptation": true, "omnibus": true,
	"complete": true, "collection": true, "edition": true, "anthology": true,
}

// dropGenreSubtitle removes a trailing ": ..." or " - ..." segment whose
// significant words are all genre/format fluff. It only fires when the whole tail
// is fluff, so a real subtitle ("An Unexpected Journey") is kept.
func dropGenreSubtitle(s string) string { return dropFluffSubtitle(s, genreFluff) }

// dropFluffSubtitle is dropGenreSubtitle over a caller-supplied vocabulary, which
// is the one edit to the copied function: the audit needs the same rule under a
// wider word list (title.go) and re-spelling the tail-splitting logic beside it
// would be a second definition of where a subtitle starts.
func dropFluffSubtitle(s string, fluff map[string]bool) string {
	sep := strings.LastIndex(s, ": ")
	if d := strings.LastIndex(s, " - "); d > sep {
		sep = d
	}
	if sep < 0 {
		return s
	}
	any, allFluff := false, true
	for _, w := range strings.FieldsFunc(strings.ToLower(s[sep:]), notAlnum) {
		if len(w) < 2 || titleStopwords[w] {
			continue
		}
		any = true
		if !fluff[w] {
			allFluff = false
			break
		}
	}
	if any && allFluff {
		return strings.TrimSpace(s[:sep])
	}
	return s
}

var titleStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "and": true, "to": true,
	"in": true, "on": true, "for": true, "with": true, "at": true, "by": true,
	"or": true, "nor": true, "from": true,
}

// significantToken reports whether w is a token tokenize would keep: not a
// stopword, not a single character, not a pure number - so the distinctive book
// words drive the comparison.
//
// The source spelled this inline inside tokenize, which built a SET. Nothing taken
// here needs the set (the scoring half that did was not copied); the three retained
// callers ask "how many" and "is there one", so the predicate is the shared piece
// and the set is not built at all. See CountSignificantWords and
// hasSignificantToken - delta (7) in the header.
func significantToken(w string) bool {
	return len(w) >= 2 && !titleStopwords[w] && !isAllDigits(w)
}

// CountSignificantWords counts the tokens of s that carry meaning. It is the floor
// test a caller applies before treating a series name as distinctive enough to
// search titles for.
func CountSignificantWords(s string) int {
	n := 0
	for _, w := range strings.FieldsFunc(strings.ToLower(s), notAlnum) {
		if significantToken(w) {
			n++
		}
	}
	return n
}

func notAlnum(r rune) bool {
	return (r < 'a' || r > 'z') && (r < '0' || r > '9')
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// Normalize and NormalizeSeries are deliberately NOT copied. The audit's
// comparison keys go through foldKey/seriesKey (title.go, seriesdup.go), which fold
// diacritics and transliterate through model.Slugify - the project's own ONE
// definition of what text becomes an identity - where the server's Normalize
// simply drops every non-ASCII rune. Two series named "Café Society" and "Cafe
// Society" are one series here, and copying a second, weaker normalizer beside the
// one this repo already owns would be a rule with two definitions.
