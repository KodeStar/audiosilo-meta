package titlerule

import (
	"regexp"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// language.go reads the one LANGUAGE statement a title or a series name makes about
// itself: a bracketed OWN-LANGUAGE EDITION decoration - "(German Edition)",
// "[Spanish Edition]", "(Deutsche Ausgabe)" - which is the US Audible marketplace's
// convention for a book or series it sells in a language other than English. It is
// the evidence internal/audit's T-LINK class proposes translation links from: on a
// series name it says the series is that language's edition of the series its BASE
// names, and on a work's title (in the work's own language) it says the work is a
// translation. Stated evidence only - nothing here guesses a language from words.
//
// The vocabulary is CLOSED and measured over the tree's bracketed series names and
// work titles (2026-09-30): the "<Language> Edition" spellings of fourteen languages
// plus Japanese, the German "Deutsche Ausgabe", and the two own-language phrases the
// work titles carry ("edición en español" 12, "edizione italiana" 1). "Castilian
// Spanish" is one series name and six titles; "Fench" is a real misspelling, carried
// by ONE series name in the tree ("[Fench Edition]"), and read as French because the
// decoration states nothing else. A bilingual statement ("English and Spanish
// Edition", seven titles) names no one language and is deliberately absent, as is
// every non-language edition ("AmazonClassics Edition", "Second Edition").

// editionLanguageWords are the language words a "<word> Edition" decoration may
// carry, in slug form, mapped to a primary subtag.
var editionLanguageWords = map[string]string{
	"german":            "de",
	"spanish":           "es",
	"castilian-spanish": "es",
	"french":            "fr",
	"fench":             "fr", // one series name in the tree; see the file comment
	"italian":           "it",
	"portuguese":        "pt",
	"russian":           "ru",
	"danish":            "da",
	"dutch":             "nl",
	"polish":            "pl",
	"swedish":           "sv",
	"turkish":           "tr",
	"hindi":             "hi",
	"english":           "en",
	"japanese":          "ja",
}

// editionLanguagePhrases is every decoration the rule reads, keyed by the slug of the
// group's contents (model.SlugifyWhole, so case, spacing and diacritics are not
// identity), mapped to the primary subtag it states.
var editionLanguagePhrases = func() map[string]string {
	out := map[string]string{
		"deutsche-ausgabe":   "de",
		"edicion-en-espanol": "es",
		"edizione-italiana":  "it",
	}
	for word, tag := range editionLanguageWords {
		out[word+"-edition"] = tag
	}
	return out
}()

// groupLanguage is the language a bracketed group's contents state, or "".
func groupLanguage(group string) string {
	return editionLanguagePhrases[model.SlugifyWhole(group[1:len(group)-1])]
}

// EditionLanguage is the primary language subtag a text's own-language edition
// decoration states, and whether it states exactly one. Every bracketed group is read;
// two groups naming DIFFERENT languages state nothing (a text cannot be two languages'
// edition at once, so neither reading is safe).
func EditionLanguage(text string) (string, bool) {
	if !strings.ContainsAny(text, "([") {
		return "", false
	}
	lang := ""
	for _, g := range parenGroup.FindAllString(text, -1) {
		l := groupLanguage(g)
		switch {
		case l == "":
			continue
		case lang != "" && l != lang:
			return "", false
		}
		lang = l
	}
	return lang, lang != ""
}

// StripEditionLanguage removes every own-language edition decoration EditionLanguage
// reads from a text and tidies what is left: "Families First, Volume 2 (German
// Edition)" is "Families First, Volume 2". Every other group is kept.
func StripEditionLanguage(text string) string {
	if !strings.ContainsAny(text, "([") {
		return tidyTitle(text)
	}
	return tidyTitle(parenGroup.ReplaceAllStringFunc(text, func(g string) string {
		if groupLanguage(g) != "" {
			return " "
		}
		return g
	}))
}

// SameUntranslatedTitle reports whether a translation's title is its original's title
// left UNTRANSLATED - the original's title plus an own-language edition decoration,
// which is the retailer convention the decoration comes from ("A Game of Fate (French
// Edition)" beside "A Game of Fate"). Both sides lose their edition decorations and any
// (Unabridged)/(Abridged) marker and are then compared by CompareKey, so case,
// punctuation, diacritics and a leading article are not a difference; a title the key
// folds to nothing (a script Slugify cannot write) is compared whole, case-folded.
// Everything else IS a difference: a volume ("Families First, Volume 2"), a collection
// ("Mia & Korum (Die komplette Krinar Chroniken Trilogie)"), a production ("City of
// Thorns (Dramatized Adaptation)") or a series name the other title does not carry.
func SameUntranslatedTitle(translation, original string) bool {
	a := StripEditionMarkers(StripEditionLanguage(translation))
	b := StripEditionMarkers(tidyTitle(original))
	ka, kb := CompareKey(a), CompareKey(b)
	if ka == "" || kb == "" {
		return a != "" && strings.EqualFold(a, b)
	}
	return ka == kb
}

// trailingGroup is a series name's LAST bracketed group, anchored at the end of the
// name (trailing whitespace allowed), with what precedes it captured.
var trailingGroup = regexp.MustCompile(`^(.*?)\s*(` + bracketGroup + `)\s*$`)

// SplitEditionName reads a series name of the shape "<base> <edition decoration>":
// the name ENDS in one bracketed own-language edition group ("Throne of
// Glass[French Edition]", "NOMADS Legacy (German Edition)"), and base is everything
// before it, with a trailing separator trimmed. base must carry a letter or digit - a
// name that is nothing but its decoration names no series to be the edition of.
func SplitEditionName(name string) (base, lang string, ok bool) {
	m := trailingGroup.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	if lang = groupLanguage(m[2]); lang == "" {
		return "", "", false
	}
	base = strings.TrimRight(strings.TrimSpace(m[1]), " -:,;|")
	if !hasAlnum(base) {
		return "", "", false
	}
	return base, lang, true
}

// dramatization matches a title announcing a DRAMATIZED production ("[Dramatized
// Adaptation]", "(Dramatised)", "A Full-Cast Dramatization"): a production written
// for voices, which a translation link must not treat as the plain text it may have
// been translated from. The duplicate decisions deliberately do not read it (a
// dramatization of one work is still that work - see internal/audit's
// vetoAdaptedEditionOneSide); only the translation-link direction does.
var dramatization = regexp.MustCompile(`(?i)\bdramati[sz](?:ed|ations?)\b`)

// IsDramatization reports whether a title announces a dramatized production.
func IsDramatization(title string) bool { return dramatization.MatchString(title) }
