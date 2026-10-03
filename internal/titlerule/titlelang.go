package titlerule

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// titlelang.go reads two weaker statements a TITLE makes about its language than the
// own-language edition decoration language.go reads: a BRACKET GLOSS ("La Odisea [The
// Odyssey]", the US marketplace's English translation printed after a foreign title)
// and the FUNCTION WORDS a title is written with ("Il Cuore Spezzato Di Arelium").
// Neither names a language on its own - a gloss only says the title it glosses is not
// English, and a closed function-word table is a guess - so internal/audit's L-MIX
// title-language subclass reads them only for an en-tagged member of a series in
// another language, as evidence for an ADVISORY proposal. No writer reads a language
// from either.

// glossBracket captures a title's trailing square-bracket group and what precedes it.
var glossBracket = regexp.MustCompile(`^(.*?)\s*\[([^\[\]]*)\]\s*$`)

// tieIn is the one format bracket the decoration readers do not already know: a
// "[Movie Tie-in]" edition is a product statement about an English book, not a gloss.
var tieIn = regexp.MustCompile(`(?i)\btie[\s-]*in\b`)

// GlossOf reads the BRACKET GLOSS "X [Y]" of a title: its trailing square-bracket group
// is the bracket-suffix decoration (the shape a retailer prints a second language's
// title in, never an edition marker) and is a TITLE rather than any other decoration -
// not a language edition, a dramatization, a collection, a tie-in, a number, one of
// seriesNames (the record's own series), one of publishers (its recordings' imprints,
// "[Naxos]" - read off the record rather than a list), or X itself. X must carry no
// English function word: a gloss is the English translation of a title that is not
// English. head is X and gloss is Y.
func GlossOf(title string, publishers, seriesNames []string) (head, gloss string, ok bool) {
	if !strings.HasSuffix(strings.TrimSpace(title), "]") {
		return "", "", false
	}
	if !slices.Contains(Decorations(TitleFacts{Title: title}), DecBracketSuffix) {
		return "", "", false
	}
	m := glossBracket.FindStringSubmatch(title)
	if m == nil {
		return "", "", false
	}
	head, gloss = strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	bracketed := "[" + gloss + "]"
	switch {
	case head == "" || gloss == "":
		return "", "", false
	case CompareKey(head) == CompareKey(gloss):
		return "", "", false
	case !CarriesIdentity(Clean(gloss, "")):
		return "", "", false
	case IsDramatization(gloss) || IsCollection(bracketed) || tieIn.MatchString(gloss):
		return "", "", false
	}
	if _, stated := EditionLanguage(bracketed); stated {
		return "", "", false
	}
	for _, name := range seriesNames {
		if SameSeriesName(name, gloss) {
			return "", "", false
		}
	}
	key := FoldKey(gloss)
	for _, pub := range publishers {
		if p := FoldKey(pub); p != "" && (strings.Contains(p, key) || strings.Contains(key, p)) {
			return "", "", false
		}
	}
	if hasEnglishFunctionWord(titleWords(head)) {
		return "", "", false
	}
	return head, gloss, true
}

// englishFunctionWords is titleStopwords (the copied matcher's English stopwords) plus
// the other English function words a title is written with. It is the ONE English set:
// a word in it is never evidence of another language, whatever list spells it too
// ("die", "den", "sin", "per", "con" are English words as well as German, Danish,
// Spanish and Italian ones).
var englishFunctionWords = func() map[string]bool {
	out := map[string]bool{}
	for w := range titleStopwords {
		out[w] = true
	}
	for _, w := range []string{
		"is", "are", "am", "was", "be", "my", "your", "his", "her", "its", "our", "their", "this", "that",
		"it", "i", "you", "we", "they", "he", "she", "not", "no", "how", "what", "when", "where", "who",
		"why", "into", "through", "about", "up", "out", "over", "after", "before", "under", "as", "if", "so",
		"do", "than", "then", "die", "den", "sin", "per", "con", "me", "us",
	} {
		out[w] = true
	}
	return out
}()

// functionWords are each non-English language's commonest FUNCTION words - articles,
// prepositions, conjunctions - the closed vocabulary TitleFunctionWords reads, spelled
// with their accents. Every list is used net of englishFunctionWords and of its
// one-letter ASCII words (an "a", an "o" and an "e" are initials as often as words), in
// functionWordSets. A language the edition vocabulary can state but that has no list
// here is in noFunctionWords, with the reason.
var functionWords = map[string][]string{
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

// noFunctionWords are the languages the edition vocabulary can state (EditionLanguage)
// that deliberately have no function-word list, so a title is never read as being in
// them by its words: the ones written in another script, whose titles the Latin-letter
// lists could never match anyway, and the Latin-script ones no mixed series of the tree
// has needed - each a list to measure before it is added.
var noFunctionWords = map[string]string{
	"ar": "another script", "el": "another script", "fa": "another script", "he": "another script",
	"hi": "another script", "ja": "another script", "ml": "another script", "mr": "another script",
	"ru": "another script", "zh": "another script",
	"cs": "unmeasured", "fi": "unmeasured", "hu": "unmeasured", "no": "unmeasured", "pl": "unmeasured",
	"ro": "unmeasured", "tr": "unmeasured",
}

// functionWordSets is functionWords as sets, each net of englishFunctionWords and of
// its one-letter ASCII words.
var functionWordSets = func() map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for lang, words := range functionWords {
		set := map[string]bool{}
		for _, w := range words {
			if englishFunctionWords[w] || len(w) == 1 { // one BYTE: an ASCII letter, never "à" or "é"
				continue
			}
			set[w] = true
		}
		out[lang] = set
	}
	return out
}()

// titleWords is a text's words for the function-word tests: maximal runs of letters,
// lower-cased, so an elision ("d'amour", "l'ombra") splits at its apostrophe.
// identityWords is not reused: it ASCII-folds, and these lists keep their accents
// (Italian "è" against "e", Portuguese "à" against "a").
func titleWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) })
}

func hasEnglishFunctionWord(words []string) bool {
	return slices.ContainsFunc(words, func(w string) bool { return englishFunctionWords[w] })
}

// TitleFunctionWords is the evidence that a record's texts (its title and subtitle) are
// in lang: the distinct function words of lang they carry outside their bracketed
// groups, sorted - at least two of them, and not one English function word - or nil
// (also for English, and for a language with no list).
func TitleFunctionWords(lang string, texts ...string) []string {
	set := functionWordSets[lang]
	if set == nil {
		return nil
	}
	words := titleWords(StripParenGroups(strings.Join(texts, " : ")))
	if hasEnglishFunctionWord(words) {
		return nil
	}
	var out []string
	for _, w := range words {
		if set[w] && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	if len(out) < 2 {
		return nil
	}
	slices.Sort(out)
	return out
}
