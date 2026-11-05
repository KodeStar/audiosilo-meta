package titlerule

import (
	"regexp"
	"slices"
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
// The vocabulary is CLOSED. Its language words are the project's one word-to-code
// table (model.LanguageWords, the importer's language mapping), so a language the
// catalogue can hold is a language whose "<Language> Edition" is read. Layered on
// top are the spellings measured over the tree's bracketed series names and work
// titles (2026-09-30) that the importer's table has no business accepting: the
// German "Deutsche Ausgabe", the two own-language phrases the work titles carry
// ("edición en español" 12, "edizione italiana" 1), "Castilian Spanish" (one series
// name and six titles) and "Fench" - a real misspelling, carried by ONE series name
// in the tree ("[Fench Edition]"), and read as French because the decoration states
// nothing else. "Persian Edition" (two work titles, 2026-10-03, both tagged en and
// read by Persian narrators) is here rather than in the importer's table for the
// same reason: no libex wave has asked for a Persian mapping, and an edition word
// is a statement about one title, not a language the importer accepts. A bilingual statement ("English and Spanish Edition", seven titles)
// names no one language and is deliberately absent, as is every non-language
// edition ("AmazonClassics Edition", "Second Edition").

// editionOnlyWords are the "<word> Edition" language words, in slug form, that are
// not in model.LanguageWords: the edition reader accepts them, the importer does not.
var editionOnlyWords = map[string]string{
	"castilian-spanish": "es",
	"fench":             "fr", // one series name in the tree; see the file comment
	"persian":           "fa", // two work titles in the tree, read by L-MIX title-language and the W-DUP edition-language veto; see the file comment
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
	for _, words := range []map[string]string{model.LanguageWords(), editionOnlyWords} {
		for word, tag := range words {
			out[model.SlugifyWhole(word)+"-edition"] = tag
		}
	}
	return out
}()

// groupLanguage is the language a bracketed group's contents state, or "".
func groupLanguage(group string) string {
	return editionLanguagePhrases[model.SlugifyWhole(groupContents(group))]
}

// EditionLanguage is the primary language subtag the own-language edition
// decorations of texts state - a record's title and subtitle, each read on its own -
// and whether they state exactly one. Every bracketed group is read; two groups naming
// DIFFERENT languages state nothing (a record cannot be two languages' edition at
// once, so neither reading is safe).
func EditionLanguage(texts ...string) (string, bool) {
	lang := ""
	for _, text := range texts {
		if !strings.ContainsAny(text, "([") {
			continue
		}
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
	}
	return lang, lang != ""
}

// EditionLanguageOfDecoration is the language a series name's LAST bracketed group
// states, read off its DecorationKey (the slugs of every group, joined by "+") - the
// cheap gate a caller already holding that key asks before SplitEditionName.
func EditionLanguageOfDecoration(decor string) string {
	if i := strings.LastIndexByte(decor, '+'); i >= 0 {
		decor = decor[i+1:]
	}
	return editionLanguagePhrases[decor]
}

// NamesALanguage reports whether a text names a language the edition vocabulary knows
// (model.LanguageWords plus the edition-only words) as a whole word, once its own
// edition decorations are gone: "Learn German: By Reading Fantasy (German Edition)"
// does, "Steelheart [German Edition]" does not. A title that names a language is very
// often a language COURSE ("101 Conversations in Simple Spanish"), whose edition
// decoration names the language taught rather than the language read, which is why
// internal/audit reads no title-language evidence from one.
func NamesALanguage(text string) bool {
	slug := "-" + model.SlugifyWhole(StripEditionLanguage(text)) + "-"
	if slug == "--" {
		return false
	}
	for _, needle := range languageNeedles {
		if strings.Contains(slug, needle) {
			return true
		}
	}
	return false
}

// languageNeedles are NamesALanguage's language words, slugified and hyphen-bounded
// once ("-german-", "-castilian-spanish-").
var languageNeedles = func() []string {
	var out []string
	for _, words := range []map[string]string{model.LanguageWords(), editionOnlyWords} {
		for word := range words {
			out = append(out, "-"+model.SlugifyWhole(word)+"-")
		}
	}
	slices.Sort(out)
	return out
}()

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

// SameUntranslatedSubtitle is SameUntranslatedTitle for the SUBTITLES of the same two
// records, where both sides may be empty: two subtitles that are nothing once the
// edition decorations and markers are gone ("" beside "(French Edition)") agree, and
// otherwise the subtitles must be one text by SameUntranslatedTitle's rule. A subtitle
// only one side carries is a difference - "Volume 2 (French Edition)" beside no
// subtitle is a volume the title comparison never saw.
func SameUntranslatedSubtitle(translation, original string) bool {
	a := StripEditionMarkers(StripEditionLanguage(translation))
	b := StripEditionMarkers(tidyTitle(original))
	if a == "" || b == "" {
		return a == b
	}
	return SameUntranslatedTitle(translation, original)
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
	base, group, ok := peelTrailingGroup(name)
	if !ok {
		return "", "", false
	}
	if lang = groupLanguage(group); lang == "" {
		return "", "", false
	}
	return base, lang, true
}

// dramatization matches a title announcing a DRAMATIZED production ("[Dramatized
// Adaptation]", "(Dramatised)", "A Full-Cast Dramatization"). The duplicate decisions
// deliberately do not read it (a dramatization of one work is still that work - see
// internal/audit's vetoAdaptedEditionOneSide). What says a recording is dramatized is
// the recording itself (model.Recording.IsDramatization); a stored title is read only
// through TitleDramatization, since the label comes off a title as a qualifier.
var dramatization = regexp.MustCompile(`(?i)\bdramati[sz](?:ed|ations?)\b`)

// IsDramatization reports whether a title announces a dramatized production. A candidate
// pool asks it of thousands of titles a request, so the common case stays off the regexp.
func IsDramatization(title string) bool {
	return containsFoldASCII(title, "dramati") && dramatization.MatchString(title)
}

// namedProduction matches the other ways a text names a production for voices: a full
// cast, and GraphicAudio, whose whole list is dramatized (model.IsDramatizationPublisher).
var namedProduction = regexp.MustCompile(`(?i)\b(?:full[- ]?cast|graphic\s?audio)\b`)

// StatesDramatization reports whether a text - a library book's tagged title, folder or
// narrator credit, or a work's stored title - says it is a dramatized production.
func StatesDramatization(text string) bool {
	return IsDramatization(text) ||
		((containsFoldASCII(text, "cast") || containsFoldASCII(text, "graphic")) && namedProduction.MatchString(text))
}

// TitleDramatization is what a work's own title and subtitle say about its recordings.
type TitleDramatization struct {
	// Stated: they say the work is a dramatized production, in any words.
	Stated bool
	// Kept: they say it in words no title qualifier takes off ("(Dramatized)", "A Radio
	// Dramatization", a split-release part's label). Such a title stays as it is, so it
	// goes on naming the production its recordings are, whoever published them. A title
	// holding only the adaptation label does not: a plain reading can sit under the label
	// until the work is retitled.
	Kept bool
}

// DramatizationOf reads a work's title and subtitle.
func DramatizationOf(title, subtitle string) TitleDramatization {
	var d TitleDramatization
	for _, t := range []string{title, subtitle} {
		if StatesDramatization(t) {
			d.Stated = true
			d.Kept = d.Kept || StatesDramatization(StripTitleQualifiers(t))
		}
	}
	return d
}

// Covers reports whether the title speaks for a recording of the given publisher that
// says nothing of itself: always when the statement stays on the title, and otherwise
// only for a recording stating no publisher. A work where no recording says it of itself
// is the label's alone (the importer keeps it for such a row), so readers take every
// recording of a Stated title as covered then: see model.Recording.IsDramatization's
// callers.
func (d TitleDramatization) Covers(publisher string) bool {
	return d.Kept || (d.Stated && publisher == "")
}
