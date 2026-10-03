package audit

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// titlelang.go is L-MIX's TITLE-LANGUAGE subclass: the set-work-language proposals a
// work's own TITLE supports, the second source beside narrator evidence
// (narration-contradicts). Without it a mis-tag no narrator had recorded twice
// elsewhere could not be proposed at all, and a language is only corrected by a
// reviewer accepting a proposal. Every proposal is ADVISORY: a title is a statement
// about a book's NAME, and a name is not its language often enough ("Dave Pelzer
// [Spanish Edition]" holds an English book, "Steelheart [German Edition]" keeps its
// English title in German). Two signals, each named in the reason with its evidence:
//
//   - EDITION STATEMENT, over the whole catalogue: the title or subtitle states an
//     own-language edition (titlerule.EditionLanguage) of a language other than the
//     work's tag - "The Gambler [Persian Edition]" tagged en. Measured over the tree
//     at landing, the same shape is otherwise a language COURSE ("Learn German: By
//     Reading Fantasy (German Edition)", "101 Conversations in Simple Spanish (Spanish
//     Edition)"), whose decoration names the language taught; a title naming a
//     language outside its decoration (titlerule.NamesALanguage) is withheld.
//   - SERIES MINORITY TITLE: an en-tagged member of a mixed-language series whose
//     language L is not English - the series' derived language, or for a tie the one
//     language every OTHER member states - and whose title reads as not English in
//     either of two narrow ways: a BRACKET GLOSS (glossOf: "La Odisea [The Odyssey]",
//     the US marketplace's English translation printed after a foreign title), or
//     FUNCTION WORDS of L (titleFunctionWords: at least two distinct ones, and not one
//     English function word - "Il Cuore Spezzato Di Arelium" in an Italian series).
//
// The series signal reads an en tag only. The two shapes run one way: an English
// gloss says the title it glosses is NOT English, and an English-language edition
// rarely keeps a foreign title - while a translation INTO another language keeps its
// original's English title all the time ("A Dance of Lies", French, translator
// credited, in a tied English/French series), so English words in a non-English
// record's title are no evidence against its tag. The series context is the
// load-bearing half: a gloss alone is not (M. Robinson's English "El Diablo [The
// Devil]"), so neither signal of the second kind is read outside a mixed series.
//
// Narrator evidence (check.NarrationProfile) only WITHHOLDS here as everywhere: a
// contradiction is named in the reason, and the proposal is advisory either way. A
// work the narration-contradicts subclass already proposes keeps that one finding,
// with the title evidence folded into its notes.

// glossBracket captures a title's trailing square-bracket group and what precedes it.
var glossBracket = regexp.MustCompile(`^(.*?)\s*\[([^\[\]]*)\]\s*$`)

// tieIn is the one format bracket the decoration readers do not already know: a
// "[Movie Tie-in]" edition is a product statement about an English book, not a gloss.
var tieIn = regexp.MustCompile(`(?i)\btie[\s-]*in\b`)

// glossOf reads the BRACKET GLOSS "X [Y]": a title whose trailing square-bracket group
// is titlerule's bracket-suffix decoration (the shape a retailer prints a second
// language's title in, never an edition marker) and is a TITLE rather than any other
// decoration - not a language edition, a dramatization, a collection, a tie-in, a
// number, the work's own series name, the name of a recording's publisher ("[Naxos]",
// read off the record rather than a list), or X itself. (A statement naming a language,
// "[UK English]", never gets here: a title naming a language is read as a course.) X must carry no English function word: a gloss is the
// English translation of a title that is not English.
func glossOf(w *model.Work, seriesNames []string) (head, gloss string, ok bool) {
	if !slices.Contains(titlerule.Decorations(titlerule.TitleFacts{Title: w.Title}), titlerule.DecBracketSuffix) {
		return "", "", false
	}
	m := glossBracket.FindStringSubmatch(w.Title)
	if m == nil {
		return "", "", false
	}
	head, gloss = strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	bracketed := "[" + gloss + "]"
	switch {
	case head == "" || gloss == "":
		return "", "", false
	case titlerule.CompareKey(head) == titlerule.CompareKey(gloss):
		return "", "", false
	case !titlerule.CarriesIdentity(titlerule.Clean(gloss, "")):
		return "", "", false
	case titlerule.IsDramatization(gloss) || titlerule.IsCollection(bracketed) || tieIn.MatchString(gloss):
		return "", "", false
	}
	if _, stated := titlerule.EditionLanguage(bracketed); stated {
		return "", "", false
	}
	for _, name := range seriesNames {
		if titlerule.SameSeriesName(name, gloss) {
			return "", "", false
		}
	}
	key := titlerule.FoldKey(gloss)
	for _, r := range w.Recordings {
		if p := titlerule.FoldKey(r.Publisher); p != "" && (strings.Contains(p, key) || strings.Contains(key, p)) {
			return "", "", false
		}
	}
	if len(functionWordsIn(head, exclusiveFunctionWords["en"])) > 0 {
		return "", "", false
	}
	return head, gloss, true
}

// functionWords are each language's commonest FUNCTION words - articles, prepositions,
// conjunctions - the closed vocabulary titleFunctionWords reads. Every non-English list
// is used net of the English one (exclusiveFunctionWords), so a word English spells too
// ("in", "an", "do", "die", "den", "sin", "per") is never evidence of another language,
// and one-letter ASCII words are left out of every non-English list (an "a", an "o" and
// an "e" are initials as often as words). It is evidence for an advisory proposal and
// nothing else: no writer reads a language from it.
var functionWords = map[string][]string{
	"en": {"the", "a", "an", "of", "and", "to", "in", "on", "for", "with", "at", "by", "or", "nor", "from",
		"is", "are", "am", "was", "be", "my", "your", "his", "her", "its", "our", "their", "this", "that",
		"it", "i", "you", "we", "they", "he", "she", "not", "no", "how", "what", "when", "where", "who",
		"why", "into", "through", "about", "up", "out", "over", "after", "before", "under", "as", "if", "so",
		"do", "than", "then", "die", "den", "sin", "per", "con", "me", "us"},
	"de": {"der", "das", "des", "dem", "ein", "eine", "einer", "eines", "einem", "einen", "und", "mit", "von",
		"vom", "zum", "zur", "im", "aus", "auf", "bei", "nach", "für", "über", "unter", "durch", "ist",
		"nicht", "oder", "zu", "wie", "wenn", "sich", "auch"},
	"es": {"el", "los", "las", "del", "la", "de", "y", "en", "para", "por", "una", "un", "sobre", "que",
		"al", "su", "sus", "mi"},
	"fr": {"le", "les", "la", "des", "du", "de", "un", "une", "et", "au", "aux", "en", "dans", "pour", "par",
		"sur", "avec", "sans", "qui", "que", "est", "ne", "pas", "mon", "ma", "mes", "son", "sa", "ses"},
	"it": {"il", "lo", "gli", "la", "le", "della", "delle", "degli", "dello", "dei", "del", "di", "nel",
		"nella", "nei", "negli", "una", "uno", "un", "è", "che", "alla", "allo", "alle", "sul", "sulla",
		"tra", "fra", "da", "dal", "dalla"},
	"pt": {"os", "do", "da", "dos", "das", "um", "uma", "de", "em", "na", "nas", "nos", "para", "por", "com",
		"sem", "que", "ao", "à", "é"},
	"nl": {"de", "het", "een", "van", "en", "met", "voor", "naar", "uit", "bij", "niet", "zijn", "op", "te"},
	"da": {"det", "en", "et", "og", "af", "med", "til", "på", "fra", "som", "er"},
	"sv": {"det", "en", "ett", "och", "av", "med", "till", "på", "från", "som", "är", "för", "att"},
}

// exclusiveFunctionWords is functionWords as sets, every non-English list net of the
// English one and of its one-letter ASCII words.
var exclusiveFunctionWords = func() map[string]map[string]bool {
	en := map[string]bool{}
	for _, w := range functionWords["en"] {
		en[w] = true
	}
	out := map[string]map[string]bool{"en": en}
	for lang, words := range functionWords {
		if lang == "en" {
			continue
		}
		set := map[string]bool{}
		for _, w := range words {
			if en[w] || len(w) == 1 { // one BYTE: an ASCII letter, never "à" or "é"
				continue
			}
			set[w] = true
		}
		out[lang] = set
	}
	return out
}()

// functionWordsIn is the distinct words of text in set, sorted. Words are maximal runs
// of letters, lower-cased, so an elision ("d'amour", "l'ombra") splits at its apostrophe.
func functionWordsIn(text string, set map[string]bool) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if set[w] {
			out = append(out, w)
		}
	}
	return sortedUnique(out)
}

// titleFunctionWords is the evidence that a title is in lang: the distinct function
// words of lang (net of English) its title and subtitle carry outside their bracketed
// groups - at least two of them, and not one English function word - or nil.
func titleFunctionWords(w *model.Work, lang string) []string {
	set := exclusiveFunctionWords[lang]
	if set == nil || lang == "en" {
		return nil
	}
	text := titlerule.StripParenGroups(w.Title + " : " + w.Subtitle)
	if len(functionWordsIn(text, exclusiveFunctionWords["en"])) > 0 {
		return nil
	}
	if words := functionWordsIn(text, set); len(words) >= 2 {
		return words
	}
	return nil
}

// noteTitleLanguage records title evidence that w is in lang.
func (m *langMix) noteTitleLanguage(w *model.Work, lang, why string) {
	c := m.languageCandidate(w)
	if c.titled == nil {
		c.titled = map[string][]string{}
	}
	if !slices.Contains(c.titled[lang], why) {
		c.titled[lang] = append(c.titled[lang], why)
	}
}

// editionStatements is the EDITION-STATEMENT signal over the whole catalogue.
func (m *langMix) editionStatements() {
	for _, w := range m.ix.cat.Works {
		lang, ok := titlerule.EditionLanguage(w.Title, w.Subtitle)
		if !ok || lang == model.PrimarySubtag(w.Language) {
			continue
		}
		if titlerule.NamesALanguage(w.Title) || titlerule.NamesALanguage(w.Subtitle) {
			m.st.TitleCourse++
			continue
		}
		m.st.TitleEdition++
		m.noteTitleLanguage(w, lang, fmt.Sprintf("the title %q states the %s edition", titleOf(w), lang))
	}
}

// seriesTitles is the SERIES-MINORITY-TITLE signal over one mixed series: each en-tagged
// member read against the series' language - the derived one where the series has a
// majority, else (a tie) the one language every other member states.
func (m *langMix) seriesTitles(s *model.Series, byLang map[string][]model.SeriesWork, keeper, how string) {
	members := byLang["en"]
	if len(members) == 0 {
		return
	}
	for _, sw := range sortedMembers(members) {
		w := m.ix.workByID[sw.Work]
		lang := keeper
		if how != keepMajority {
			lang = otherMembersLanguage(byLang, w.ID)
		}
		if lang == "" || lang == "en" || titlerule.NamesALanguage(w.Title) || titlerule.NamesALanguage(w.Subtitle) {
			continue
		}
		var evidence []string
		if head, gloss, ok := glossOf(w, m.seriesNames(w)); ok {
			evidence = append(evidence, fmt.Sprintf("its title %q is glossed [%s], the English translation of a title that is not English", head, gloss))
		}
		if words := titleFunctionWords(w, lang); len(words) > 0 {
			evidence = append(evidence, fmt.Sprintf("its title %q carries %s function words (%s) and no English ones", titleOf(w), lang, strings.Join(words, ", ")))
		}
		if len(evidence) == 0 {
			continue
		}
		m.st.TitleSeries++
		m.noteTitleLanguage(w, lang, fmt.Sprintf("a member of %s, whose other members are %s: %s", s.ID, lang, strings.Join(evidence, "; ")))
	}
}

// otherMembersLanguage is the one primary language every member of the series other
// than work states, or "" when they state several.
func otherMembersLanguage(byLang map[string][]model.SeriesWork, work string) string {
	found := ""
	for _, lang := range sortedKeys(byLang) {
		if slices.ContainsFunc(byLang[lang], func(sw model.SeriesWork) bool { return sw.Work != work }) {
			if found != "" {
				return ""
			}
			found = lang
		}
	}
	return found
}

// seriesNames is the names of every series a work is a member of.
func (m *langMix) seriesNames(w *model.Work) []string {
	var out []string
	for _, ms := range m.ix.memberships[w.ID] {
		if s := m.ix.seriesByID[ms.series]; s != nil {
			out = append(out, s.Name)
		}
	}
	return out
}

// titleOf is a work's title and subtitle as one string, for a reason.
func titleOf(w *model.Work) string {
	if w.Subtitle == "" {
		return w.Title
	}
	return w.Title + ": " + w.Subtitle
}

// titleLanguageFinding is the title-language record of a work no narration finding
// names: one set-work-language proposal when the evidence names one language, a review
// when it names several. Advisory either way, with the narrators' verdict in the reason.
func (m *langMix) titleLanguageFinding(w *model.Work, c *languageCandidate) Finding {
	tos := sortedKeys(c.titled)
	fd := Finding{Subclass: lMixTitle, Key: w.ID, Works: []WorkRef{m.ix.workRef(w, "")}}
	for _, to := range tos {
		fd.Notes = append(fd.Notes, "-> "+to+" (title): "+strings.Join(c.titled[to], "; "))
	}
	if c.evidence.Total > 0 {
		fd.Notes = append(fd.Notes, "the narrators' recordings of other works: "+evidenceText(c.evidence))
	}
	if cs := c.contested; len(cs) > 0 {
		fd.Notes = append(fd.Notes, "a minority member of a series whose keeper language is contested: "+strings.Join(sortedUnique(cs), "; "))
	}
	if len(tos) != 1 {
		fd.Propose = Proposal{Op: OpReview, Target: w.ID, Field: fieldLanguage, From: w.Language, Advisory: true,
			Reason: "the title evidence names several languages this work could be (" + strings.Join(tos, ", ") + "); a human decides"}
		return fd
	}
	to := tos[0]
	verdict := "the narrators' other recordings settle nothing"
	switch d := c.evidence.Dominant(); {
	case c.evidence.Contradicts(to):
		verdict = fmt.Sprintf("its narrators record in %s (%s), which contradicts %s", d, evidenceText(c.evidence), to)
	case d != "":
		verdict = fmt.Sprintf("its narrators record in %s too (%s)", d, evidenceText(c.evidence))
	}
	fd.Propose = Proposal{Op: OpSetWorkLanguage, Target: w.ID, Field: fieldLanguage, From: w.Language, To: to, Advisory: true,
		Reason: fmt.Sprintf("the title says %s: %s; %s. A title is not a language, so a reviewed decision is what applies this",
			to, strings.Join(c.titled[to], "; "), verdict)}
	return fd
}
