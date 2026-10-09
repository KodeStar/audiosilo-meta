package query

import (
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The SCORING half of the structured match (works/match, match.go): pure
// functions over the facts a request states and the facts a candidate work
// carries, kept apart from the SQL so every rule here is a table test.
//
// THE MODEL. A request carries two independent sets of evidence, and a
// candidate's score is the BETTER of the two:
//
//   - the STRUCTURED facts: title hypotheses, author hypotheses, a series name
//     and position, a runtime. These are what a client derives from a file's
//     tags and its folder path, so each field may hold several guesses and any
//     of them may be garbage (a title tag reading "Bernard Cornwell", an author
//     tag reading "Sharpe's Eagle (Sharpe 08)"). Every field is judged by its
//     BEST hypothesis, so a wrong guess beside a right one costs nothing.
//   - the FREE TEXT (`q`): what a person typed, which may mix title, author and
//     series words in any order. It is read per candidate as the facts it
//     states about that work (textFacts) - the work's author words are its
//     author, its series' words and a volume number are that volume, the rest
//     is a title - and judged by the same rules, so "dune herbert" is "Dune" by
//     Herbert and "sharpe 8" is volume 8 of the Sharpe novels.
//
// Within a set the score is a weighted average over the facts that were
// actually judged: title 60, author 20, series 10, runtime 10. A fact the
// request does not state is left out of the denominator rather than counted as
// a mismatch, so a book with no author tag is not penalized for it - with one
// exception: a structured request whose titles say nothing (no hypothesis, or
// only "CD1"/"12"-shaped ones) keeps the title's weight in the denominator
// unless the series AND its position agree, so "every book by this author" can
// never read as a 100% match.
const (
	weightTitle   = 60.0
	weightAuthor  = 20.0
	weightSeries  = 10.0
	weightRuntime = 10.0
)

// Runtime agreement: within 3% is the same edition, within 10% plausibly so.
const (
	runtimeSame      = 0.03
	runtimeNear      = 0.10
	runtimeNearValue = 0.6
)

// The title-similarity bands. containedSimilarity is what a title CONTAINED in
// the other one at a word boundary scores (a decorated title, or a work stored
// under the short form of a long title: "Tales from a Not-So-Secret Crush
// Catastrophe" against "Crush Catastrophe"). numberConflictCap is the most a
// hypothesis naming a volume number the candidate does not carry can score:
// "Dork Diaries 12" is not "Dork Diaries".
const (
	articleSimilarity   = 0.95
	containedSimilarity = 0.88
	numberConflictCap   = 0.5
)

// colonPartWeight discounts a comparison against ONE SIDE of a candidate's
// "X: Y" title. The two halves are compared because the catalogue stores both
// shapes - "Awaken Online: Inferno" (series, then title) and "Homefront: An
// Expeditionary Force Audio Drama Special" (title, then description) - while a
// file usually names the book by one half. The discount keeps a work titled
// exactly what the file says ahead of one that only contains it.
const colonPartWeight = 0.9

// genericTitleValue is what a GENERIC candidate title ("Dork Diaries 12": the
// series' name and the requested number) counts as title agreement: the
// numbering says it is the volume, but a work stored under the volume's real
// title ("Crush Catastrophe", the same volume 12) says more.
const genericTitleValue = 0.85

// authorMismatchFactor scales a structured score when the request named
// authors and none of them wrote the candidate.
const authorMismatchFactor = 0.75

// seriesTrust is the band in which a title DISAGREES with a candidate strongly
// enough that a series-number agreement must not be believed. A title that
// shares the naming pattern but not the name ("Sharpe's Siege" against
// "Sharpe's Revenge", ~0.6) names a different volume of the same series, and
// the numbering in a folder name is exactly what goes wrong when a volume is
// missing or renumbered - so the named title wins. Below the band the two
// titles share nothing at all, which reads as a different naming of one book (a
// marketing title, a translation), and the numbering is the better evidence.
const (
	seriesDistrustLow  = 0.3
	seriesDistrustHigh = 0.75
)

// maxEditRunes bounds the strings the edit distance compares. Titles are short
// (the longest real one is 252 bytes) and the distance is quadratic, so the
// comparison reads a prefix of a longer hypothesis rather than letting a
// request price itself.
const maxEditRunes = 96

// ---- normalization -----------------------------------------------------------

// matchTerms folds s into the terms the match compares: lowercased, diacritics
// stripped (unicode61 strips them on the index side, so "Renee" already FINDS
// "Renée" and the comparison has to agree), "&" read as "and", and a possessive
// folded into one word exactly as nameKey folds it ("Sharpe's" and "Sharpe’s"
// are both "sharpes").
func matchTerms(s string) []string {
	s = strings.ReplaceAll(s, "&", " and ")
	return strings.Fields(nameKey(stripMarks(s)))
}

// stripMarks removes combining marks after canonical decomposition, so a
// precomposed "é" and a decomposed one both become "e".
func stripMarks(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if !model.IsCombiningMark(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// termForm is one string folded for comparison (matchTerms) with everything a
// comparison reads computed once: a request's guesses and a candidate's title
// forms are each compared against many others.
type termForm struct {
	terms       []string
	joined      string          // the terms, space-joined
	runes       []rune          // joined, cut to maxEditRunes (editRatio)
	set         map[string]bool // the distinct terms (dice)
	ident       string          // the identifying terms, stopwords left out (sameIdentifyingTerms)
	identifying int             // how many terms identify (identifyingTerms)
	numbers     []string        // the volume-sized numbers, leading zeros off (numberConflict)
}

func newTermForm(terms []string) termForm {
	f := termForm{terms: terms, joined: strings.Join(terms, " "), set: make(map[string]bool, len(terms))}
	f.runes = []rune(f.joined)
	if len(f.runes) > maxEditRunes {
		f.runes = f.runes[:maxEditRunes]
	}
	f.ident = identOf(terms)
	for _, t := range terms {
		f.set[t] = true
		if utf8.RuneCountInString(t) >= 2 && !probeStopwords[t] {
			f.identifying++
		}
		if isSmallNumber(t) {
			f.numbers = append(f.numbers, strings.TrimLeft(t, "0"))
		}
	}
	return f
}

// identOf is terms without their stopwords, space-joined: what two names that
// differ only by an article share ("The Dresden Files", "Dresden Files").
func identOf(terms []string) string {
	return strings.Join(filter(terms, func(t string) bool { return probeStopwords[t] }), " ")
}

// ---- title hypotheses ----------------------------------------------------------

// The shapes a file's numbering takes in front of its title, and the edition
// fluff behind it. They are applied here, not by the client, so a client may
// send a raw tag or folder name ("02 - Sharpe's Triumph", "[Outlaw  08] - The
// Death of Robin Hood", "A Broken Alliance: Sentenced to War, Book 5") and the
// API is the one place that reads numbering. Each lead shape captures its
// number, which becomes the request's position when it states none.
var (
	// "02 - ", "7. ", "Book 3: ", "#4 ", "Vol. 2 - ", and the scanner's "01 Title".
	leadNumberRE = regexp.MustCompile(`(?i)^\s*(?:(?:book|vol(?:ume)?\.?|part|band|teil|tome)\s*)?#?(\d{1,3}(?:\.\d+)?)(?:\s*[-.:_)\]]\s*|\s+)`)
	// "SW06 - ", "BAE06: ", "C02 ": a short series code glued to its number.
	// The separator must be followed by a space, so "R2-D2" and "Catch-22" are
	// titles, not codes.
	leadCodeRE = regexp.MustCompile(`(?i)^\s*[a-z]{1,6}(\d{1,3}(?:\.\d+)?)\s*[-.:_)\]]\s+`)
	// "HALO 28 - ": the same code SPACED from its number. A colon does not
	// separate it, since a word, a number and a colon is a title's own shape
	// ("Area 51: The Revelation", "Apollo 13: The Untold Story").
	leadSpacedCodeRE = regexp.MustCompile(`(?i)^\s*[a-z]{1,6}\s(\d{1,3}(?:\.\d+)?)\s*[-._)\]]\s+`)
	// "Sharpe - 08 - ": a series name, then the number, each spaced-hyphenated.
	leadSeriesNumberRE = regexp.MustCompile(`(?i)^\s*[^-]{1,60}?\s+-\s+(\d{1,3}(?:\.\d+)?)\s+-\s+`)
	// "[Outlaw 08] - ": a bracketed series label.
	leadBracketRE = regexp.MustCompile(`^\s*\[[^\]]{0,60}\]\s*[-.:]?\s*`)
	// "DF15.5 Brief Cases": an UPPER-CASE code glued to its number, with only a
	// space after it. Case-sensitive, so an ordinary capitalized word followed
	// by a number is left alone.
	leadGluedCodeRE = regexp.MustCompile(`^\s*[A-Z]{1,6}(\d{1,3}(?:\.\d+)?)\s+`)
	// "(Unabridged)", "[MP3]", "(Sharpe 08)": any trailing bracketed group.
	trailBracketRE = regexp.MustCompile(`\s*[(\[][^()\[\]]{0,80}[)\]]\s*$`)
	// ": Sentenced to War, Book 5" / ", Book 5": a series-and-volume tail.
	trailColonVolumeRE = regexp.MustCompile(`(?i)\s*:\s*[^:]*\b(?:book|vol(?:ume)?\.?|part|band|teil|tome)\s*#?\d+(?:\.\d+)?\s*$`)
	trailCommaVolumeRE = regexp.MustCompile(`(?i)\s*[,-]\s*(?:book|vol(?:ume)?\.?|part|band|teil|tome)\s*#?\d+(?:\.\d+)?\s*$`)

	// The cuts in the order cutNumbering applies them.
	trailCuts = []*regexp.Regexp{trailBracketRE, trailColonVolumeRE, trailCommaVolumeRE}
	leadCuts  = []*regexp.Regexp{leadBracketRE, leadSeriesNumberRE, leadCodeRE, leadSpacedCodeRE, leadGluedCodeRE, leadNumberRE}
)

// cutNumbering takes a title's numbering prefix and edition fluff off,
// returning what is left and the number the first numbering prefix carried (""
// when none did). A cut that would leave nothing is not made. Every shape needs
// a digit or a bracket, so a title holding neither is returned as it is
// without running a regex.
func cutNumbering(h string) (rest, number string) {
	if !strings.ContainsAny(h, "0123456789([") {
		return h, ""
	}
	rest = h
	for range 3 { // the shapes can stack: "[Outlaw 08] - 08 - Title (Unabridged)"
		before := rest
		for _, re := range trailCuts {
			rest, _ = cutIfLeft(rest, re)
		}
		for _, re := range leadCuts {
			var n string
			if rest, n = cutIfLeft(rest, re); number == "" {
				number = n
			}
		}
		if rest == before {
			break
		}
	}
	return strings.TrimSpace(rest), number
}

// cutIfLeft removes re's match from s unless that would leave no term,
// returning the number re's first group captured.
func cutIfLeft(s string, re *regexp.Regexp) (string, string) {
	m := re.FindStringSubmatchIndex(s)
	if m == nil {
		return s, ""
	}
	cut := s[:m[0]] + s[m[1]:]
	if len(ftsTerms(cut)) == 0 {
		return s, ""
	}
	if len(m) >= 4 && m[2] >= 0 {
		return cut, s[m[2]:m[3]]
	}
	return cut, ""
}

// titleVariants returns the forms of one title hypothesis worth comparing, and
// the volume number its numbering prefix carried: the hypothesis as given, the
// hypothesis without its numbering and edition fluff (cutNumbering), and that
// as titlerule.CleanTitle reads it beside the request's series - without the
// series' name and "Book N" fluff ("Dork Diaries: Puppy Love" in the Dork
// Diaries is "Puppy Love") - cut again. The caller takes the BEST comparison over
// the variants, so a variant can only ever help the hypothesis it came from.
func titleVariants(h, series string) ([]string, string) {
	out := []string{h}
	cut, number := cutNumbering(h)
	// Taking the series' name off can bare a number: "Vorkosigan Saga 03 -
	// Barrayar" in the Vorkosigan Saga is volume 3, "Barrayar".
	clean, n := cutNumbering(titlerule.CleanTitle(cut, series))
	if number == "" {
		number = n
	}
	for _, v := range []string{cut, clean} {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out, number
}

// discTermRE is a disc/track label glued to its number ("cd1", "disc02").
var discTermRE = regexp.MustCompile(`^(?:cd|disc|disk|track|part)\d+$`)

// discWords are the words that, with a number, make a part label rather than a
// title ("Track 01", "Disc 2").
var discWords = map[string]bool{"cd": true, "disc": true, "disk": true, "track": true, "chapter": true}

// informativeTitle reports whether terms can identify a title: they must hold
// one term that is not a stopword, a volume or disc word, a lone rune or a
// short number. "12", "CD1", "Book 3" and "Track 01" say nothing; "1984" and
// "Dune" do. An uninformative hypothesis is not judged at all (see the scoring
// model above) rather than being compared and scoring near zero.
func informativeTitle(terms []string) bool {
	for _, t := range terms {
		switch {
		case utf8.RuneCountInString(t) < 2,
			probeStopwords[t], volumeKeywords[t], discWords[t],
			discTermRE.MatchString(t),
			isSmallNumber(t):
			continue
		}
		return true
	}
	return false
}

// isSmallNumber reports whether t is a volume-sized number: one to three
// digits. A four-digit run is a year or a title ("1984"), never a volume.
func isSmallNumber(t string) bool { return len(t) <= 3 && isAllDigits(t) }

// splitVolume reads terms as "<name> [volume word] <number>" ("dork diaries
// 6", "sharpe book 8"), returning the name and the number; ok is false unless
// the last term is a volume-sized number with a name before it.
func splitVolume(terms []string) (name []string, number string, ok bool) {
	n := len(terms)
	if n < 2 || !isSmallNumber(terms[n-1]) {
		return nil, "", false
	}
	name = terms[:n-1]
	if len(name) > 1 && volumeKeywords[name[len(name)-1]] {
		name = name[:len(name)-1]
	}
	return name, terms[n-1], true
}

// volumeOf reads terms as the numbering of one of the named series: the
// series' name, an optional volume word and a number ("dork diaries 12",
// "the primal hunter book 1"), returning the number.
func volumeOf(terms []string, series []termForm) (string, bool) {
	name, n, ok := splitVolume(terms)
	if !ok {
		return "", false
	}
	ident := identOf(name)
	if ident != "" && slices.ContainsFunc(series, func(s termForm) bool { return s.ident == ident }) {
		return n, true
	}
	return "", false
}

// filter returns the terms drop does not reject.
func filter(terms []string, drop func(string) bool) []string {
	var out []string
	for _, t := range terms {
		if !drop(t) {
			out = append(out, t)
		}
	}
	return out
}

// maxTextRuns bounds how many leading and trailing word runs of the typed text
// are probed as exact titles (textTitleRuns).
const maxTextRuns = 3

// textTitleRuns returns the runs of typed text that may be a title once a name
// before or after it is dropped: the text without its last one to maxTextRuns
// words, and without its first one to maxTextRuns words, longest first. Each
// run must pass the exact-title probe's own cost gate (worthTitleProbing), so
// a run of stopwords is never probed.
func textTitleRuns(text string) []string {
	words := strings.Fields(text)
	var out []string
	for drop := 1; drop <= maxTextRuns && drop < len(words); drop++ {
		for _, run := range []string{
			strings.Join(words[:len(words)-drop], " "),
			strings.Join(words[drop:], " "),
		} {
			if worthTitleProbing(run) && !slices.Contains(out, run) {
				out = append(out, run)
			}
		}
	}
	return out
}

// ---- similarity ------------------------------------------------------------------

// editBuf is the edit distance's scratch row pair, reused across the
// comparisons of one request (one goroutine); a nil one allocates.
type editBuf struct{ row []int }

// titleSimilarity compares a hypothesis with a candidate title, in [0,1]:
//
//   - 1 when they are the same words;
//   - capped at numberConflictCap when the hypothesis names a volume number the
//     candidate does not carry ("Dork Diaries 12" against "Dork Diaries"); the
//     reverse is NOT a conflict, since a stored title often carries its series
//     tail ("Puppy Love: Dork Diaries, Book 10") where a file says "Puppy Love";
//   - containedSimilarity when one is a run of the other's words and the shorter
//     holds two identifying words ("Crush Catastrophe" inside "Tales from a
//     Not-So-Secret Crush Catastrophe") - one word ("Dune" in "Dune Messiah") is
//     not enough;
//   - otherwise the better of the word overlap (Dice) and the character edit
//     ratio, which is what forgives a typo in a folder name ("An Alliance
//     Reformed" for "An Alliance Reforged").
func titleSimilarity(hyp, cand *termForm, buf *editBuf) float64 {
	if len(hyp.terms) == 0 || len(cand.terms) == 0 {
		return 0
	}
	if hyp.joined == cand.joined {
		return 1
	}
	best := max(dice(hyp, cand), editRatio(hyp.runes, cand.runes, buf))
	if sameIdentifyingTerms(hyp, cand) {
		// The same words but for an article ("Bonehunters", "The Bonehunters").
		best = max(best, articleSimilarity)
	}
	if containsRun(hyp, cand) || containsRun(cand, hyp) {
		best = max(best, containedSimilarity)
	}
	if numberConflict(hyp, cand) {
		best = min(best, numberConflictCap)
	}
	return best
}

// titleForm is one form a candidate's title is compared in, with the weight a
// match against it carries.
type titleForm struct {
	termForm
	weight float64
}

// candidateTitleForms is every form a candidate work's title is compared in,
// the title itself first (whose terms are passed in, already folded): the
// title, the title with its subtitle, the title with its own series tail or
// edition fluff taken off ("Hunted: The Iron Druid Chronicles, Book 6" is
// "Hunted"), and - discounted by colonPartWeight - either side of an "X: Y"
// title. Only the hypotheses are read through titlerule.CleanTitle: a work's
// own "(Dramatized Adaptation)" or genre subtitle still tells it apart.
func candidateTitleForms(title string, terms []string, subtitle string) []titleForm {
	var forms []titleForm
	addTerms := func(terms []string, w float64) {
		if len(terms) > 0 {
			forms = append(forms, titleForm{termForm: newTermForm(terms), weight: w})
		}
	}
	add := func(s string, w float64) { addTerms(matchTerms(s), w) }
	addTerms(terms, 1)
	if subtitle != "" {
		add(title+" "+subtitle, 1)
	}
	// The cut keeps a trailing bracketed qualifier: a stored title is already
	// clean of edition fluff, so what is left in brackets is the work's own
	// ("Dune (Dramatized Adaptation)" is not "Dune").
	body, qualifier := title, ""
	if loc := trailBracketRE.FindStringIndex(title); loc != nil && len(ftsTerms(title[:loc[0]])) > 0 {
		body, qualifier = title[:loc[0]], title[loc[0]:]
	}
	if cut, _ := cutNumbering(body); cut != body {
		add(cut+qualifier, 1)
	}
	if before, after, ok := strings.Cut(title, ":"); ok {
		add(before, colonPartWeight)
		add(after, colonPartWeight)
	}
	return forms
}

// bestTitle is a hypothesis's best weighted similarity over a candidate's
// title forms.
func bestTitle(hyp *termForm, forms []titleForm, buf *editBuf) float64 {
	best := 0.0
	for i := range forms {
		f := &forms[i]
		if f.weight <= best {
			continue
		}
		best = max(best, f.weight*titleSimilarity(hyp, &f.termForm, buf))
	}
	return best
}

// sameIdentifyingTerms reports whether two term lists hold the same words once
// stopwords are left out ("The Dresden Files" and "Dresden Files").
func sameIdentifyingTerms(a, b *termForm) bool { return a.ident != "" && a.ident == b.ident }

// containsRun reports whether short appears in long as a contiguous run of
// whole words and holds at least two identifying ones.
func containsRun(long, short *termForm) bool {
	if len(short.terms) >= len(long.terms) || short.identifying < 2 {
		return false
	}
	for i := 0; i+len(short.terms) <= len(long.terms); i++ {
		if slices.Equal(long.terms[i:i+len(short.terms)], short.terms) {
			return true
		}
	}
	return false
}

// numberConflict reports whether hyp names a volume-sized number cand lacks.
func numberConflict(hyp, cand *termForm) bool {
	for _, n := range hyp.numbers {
		if !slices.Contains(cand.numbers, n) {
			return true
		}
	}
	return false
}

// dice is the Sørensen-Dice overlap of two term sets.
func dice(a, b *termForm) float64 {
	if len(a.set)+len(b.set) == 0 {
		return 0
	}
	common := 0
	for t := range a.set {
		if b.set[t] {
			common++
		}
	}
	return 2 * float64(common) / float64(len(a.set)+len(b.set))
}

// editRatio is 1 - levenshtein(a, b) / max(len): the character-level
// similarity of two folded titles, each already cut to maxEditRunes. A pair
// whose lengths alone rule out a useful ratio is not compared at all.
func editRatio(a, b []rune, buf *editBuf) float64 {
	longest := max(len(a), len(b))
	if longest == 0 {
		return 0
	}
	diff := len(a) - len(b)
	if diff < 0 {
		diff = -diff
	}
	// The distance is at least the length difference, so this is an upper
	// bound on the ratio; below a half the edit ratio cannot decide anything
	// the word overlap does not already say.
	if 1-float64(diff)/float64(longest) < 0.5 {
		return 0
	}
	return 1 - float64(levenshtein(a, b, buf))/float64(longest)
}

// levenshtein is the classic two-row edit distance over runes, in buf's rows.
func levenshtein(a, b []rune, buf *editBuf) int {
	if buf == nil {
		buf = &editBuf{}
	}
	n := len(b) + 1
	if cap(buf.row) < 2*n {
		buf.row = make([]int, 2*n)
	}
	prev, cur := buf.row[:n], buf.row[n:2*n]
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// wavg is a weighted average under construction: each judged fact adds its
// weight and its weighted value.
type wavg struct{ num, den float64 }

func (w *wavg) add(weight, value float64) {
	w.num += weight * value
	w.den += weight
}

func (w wavg) value() float64 {
	if w.den == 0 {
		return 0
	}
	return w.num / w.den
}

// round2 rounds a similarity to the two places the reasons report.
func round2(x float64) *float64 {
	r := math.Round(x*100) / 100
	return &r
}

// ---- authors -------------------------------------------------------------------

// personName is a name read for matching: its surname (the last identifying
// term) and the given names before it, all folded.
type personName struct {
	surname string
	given   []string
}

// terms is the whole name in order, given names first.
func (n personName) terms() []string { return append(slices.Clone(n.given), n.surname) }

// key is the name's terms sorted and joined, so "bernard cornwell" and
// "cornwell bernard" share it (candidate.isAuthorName).
func (n personName) key() string { return sortedKey(n.terms()) }

// sortedKey joins a copy of terms in sorted order.
func sortedKey(terms []string) string {
	x := slices.Clone(terms)
	slices.Sort(x)
	return strings.Join(x, " ")
}

// nameSuffixes are the trailing credentials and generational marks that are not
// a surname ("Martin Luther King Jr.", "John Smith PhD").
var nameSuffixes = map[string]bool{
	"jr": true, "sr": true, "ii": true, "iii": true, "iv": true,
	"phd": true, "md": true, "dr": true, "mr": true, "mrs": true, "ms": true, "prof": true,
}

// parseName reads one person's name. "Cornwell, Bernard" (one word, a comma,
// the rest) is the inverted form and reads as "Bernard Cornwell". It reports
// false when no usable surname remains - a number or a lone rune, which is what
// a garbage author tag ("Sharpe 08") leaves.
func parseName(s string) (personName, bool) {
	s = trailBracketRE.ReplaceAllString(s, "")
	// A trailing ", Jr." is a suffix, not a name: "Smith, John, Jr." is still
	// the inverted form of John Smith.
	if i := strings.LastIndex(s, ","); i >= 0 {
		if t := matchTerms(s[i+1:]); len(t) == 1 && nameSuffixes[t[0]] {
			s = s[:i]
		}
	}
	if before, after, ok := strings.Cut(s, ","); ok && len(ftsTerms(before)) == 1 && strings.TrimSpace(after) != "" && !strings.Contains(after, ",") {
		if aft := matchTerms(after); len(aft) > 0 && !nameSuffixes[aft[0]] {
			s = after + " " + before
		}
	}
	terms := matchTerms(s)
	for len(terms) > 0 && nameSuffixes[terms[len(terms)-1]] {
		terms = terms[:len(terms)-1]
	}
	if len(terms) == 0 {
		return personName{}, false
	}
	sur := terms[len(terms)-1]
	if utf8.RuneCountInString(sur) < 2 || isAllDigits(sur) {
		return personName{}, false
	}
	return personName{surname: sur, given: terms[:len(terms)-1]}, true
}

// isAllDigits reports whether t is a non-empty run of ASCII digits.
func isAllDigits(t string) bool {
	if t == "" {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] < '0' || t[i] > '9' {
			return false
		}
	}
	return true
}

// authorSplitRE separates the names of a multi-author credit: ";", "&", "/" and
// a spaced "and".
var authorSplitRE = regexp.MustCompile(`(?i)\s*(?:;|&|/|\band\b)\s*`)

// splitAuthors reads one author hypothesis as the names it credits. A comma
// separates names too, except in the inverted single-name form parseName reads
// ("Cornwell, Bernard"), which is one name.
func splitAuthors(s string) []personName {
	var out []personName
	for _, part := range authorSplitRE.Split(s, -1) {
		pieces := []string{part}
		if before, _, ok := strings.Cut(part, ","); ok && len(ftsTerms(before)) != 1 {
			pieces = strings.Split(part, ",")
		}
		for _, p := range pieces {
			if n, ok := parseName(p); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

// givenCompatible reports whether two sets of given names can belong to one
// person: either side has none, or their FIRST given names agree, a lone
// initial agreeing with any name it begins ("J.N. Chaney" and "Jason
// Chaney"... and "J. N. Chaney").
func givenCompatible(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	x, y := a[0], b[0]
	switch {
	case x == y:
		return true
	case utf8.RuneCountInString(x) == 1:
		return strings.HasPrefix(y, x)
	case utf8.RuneCountInString(y) == 1:
		return strings.HasPrefix(x, y)
	}
	return false
}

// authorAgreement is how well a candidate's credited authors fit the request's
// author hypotheses: "full" (surname and first given name agree), "surname"
// (the surname alone does), "none".
type authorAgreement string

const (
	authorFull    authorAgreement = "full"
	authorSurname authorAgreement = "surname"
	authorNone    authorAgreement = "none"
)

// value is the agreement's weight in the score: a surname alone is strong but
// not proof (Bernard and Patricia Cornwell).
func (a authorAgreement) value() float64 {
	switch a {
	case authorFull:
		return 1
	case authorSurname:
		return 0.8
	}
	return 0
}

// agreeAuthors judges candidate authors against the hypotheses: the best pair
// decides.
func agreeAuthors(hyps []personName, authors []personName) authorAgreement {
	best := authorNone
	for _, h := range hyps {
		for _, a := range authors {
			if h.surname != a.surname {
				continue
			}
			if givenCompatible(h.given, a.given) {
				return authorFull
			}
			best = authorSurname
		}
	}
	return best
}

// ---- series ----------------------------------------------------------------------

// seriesNameFits reports whether a candidate's series name is the series the
// request names: the same words, one contained in the other ("Richard Sharpe"
// and "Richard Sharpe Novels", "Sentenced to War" and "Sentenced to War
// (abridged)"), or a close spelling.
func seriesNameFits(hyp, name *termForm, buf *editBuf) bool {
	if len(hyp.terms) == 0 || len(name.terms) == 0 {
		return false
	}
	if hyp.joined == name.joined || sameIdentifyingTerms(hyp, name) || containsRun(hyp, name) || containsRun(name, hyp) {
		return true
	}
	// One identifying word on either side ("Sharpe" for "Richard Sharpe
	// Novels", or a stored "Sharpe" for a folder's "Richard Sharpe") is
	// contained but too short for containsRun's two-word floor; it fits when it
	// is the whole of one side and a word of the other.
	if oneWordOf(hyp, name) || oneWordOf(name, hyp) {
		return true
	}
	return max(dice(hyp, name), editRatio(hyp.runes, name.runes, buf)) >= 0.8
}

// oneWordOf reports whether a is a single identifying word that is a word of b.
func oneWordOf(a, b *termForm) bool {
	return a.identifying == 1 && len(a.terms) == 1 && b.set[a.terms[0]]
}

// seriesAgreement is how a candidate's series memberships fit the request's
// series hypothesis.
type seriesAgreement string

const (
	// seriesPosition: the series AND the requested position agree.
	seriesPosition seriesAgreement = "position"
	// seriesName: the series agrees and no position was requested.
	seriesName seriesAgreement = "name"
	// seriesConflict: the series agrees but the position does not, or the
	// candidate's title disagrees with the request's (see seriesTrust).
	seriesConflict seriesAgreement = "conflict"
	seriesNone     seriesAgreement = "none"
)

func (s seriesAgreement) value() float64 {
	if s == seriesPosition || s == seriesName {
		return 1
	}
	return 0
}

// ---- runtime -----------------------------------------------------------------------

// runtimeFit scores a recording runtime (minutes) against the book's length
// (seconds): 1 within runtimeSame, runtimeNearValue within runtimeNear, else 0,
// with the relative difference beside it.
func runtimeFit(runtimeMin int, seconds int) (fit, diff float64) {
	diff = math.Abs(float64(runtimeMin)*60-float64(seconds)) / float64(seconds)
	switch {
	case diff <= runtimeSame:
		return 1, diff
	case diff <= runtimeNear:
		return runtimeNearValue, diff
	}
	return 0, diff
}
