package model

// librivox.go holds LibriVox's two NAME tables - its language names and its
// genre names - in the one place both of their readers reach: internal/importer
// maps a project's names through them (LibriVox name -> tag or genre), and
// pkg/query's LibriVox-shaped feed reads them in reverse (a language tag back to
// LibriVox's name) and forward (a ?genre= name to the vocabulary it was mapped
// onto). One table, so the feed can never answer a name the importer would not
// have written.

import (
	"strings"
	"unicode"
)

// librivoxLanguages maps the language NAMES LibriVox's feed states ("the English
// display name for the language of the reading") that the shared word table
// (LanguageWords) does not hold onto a language tag the schema accepts.
// LibriVox reads in languages no retail mirror carries - Latin, Esperanto,
// Ancient Greek - so the shared table, which is deliberately limited to the
// ISO 639-1 codes the libex dump measured, is not enough on its own.
//
// Each entry is the language's ISO 639-1 code where it has one and its ISO 639-2
// code otherwise (Ancient Greek, Old English, Middle English and Old Norse have
// no 639-1 code; the schema's language pattern takes two or three letters), and
// "Multilingual" is 639-2's "mul", the code for a work in several languages.
// Nothing here is a guess between two codes: a name that is not in this table
// or the shared one is UNKNOWN, and internal/importer's planner skips the project with the
// `language` refusal, reported like any other source's (an unknown language is
// never mapped to "und" or a neighbour). A skipped language is one line here and
// a re-import of its id range.
var librivoxLanguages = map[string]string{
	"afrikaans":       "af",
	"albanian":        "sq",
	"ancient greek":   "grc",
	"armenian":        "hy",
	"basque":          "eu",
	"belarusian":      "be",
	"bengali":         "bn",
	"bosnian":         "bs",
	"breton":          "br",
	"bulgarian":       "bg",
	"catalan":         "ca",
	"church slavonic": "cu",
	"cornish":         "kw",
	"croatian":        "hr",
	"esperanto":       "eo",
	"estonian":        "et",
	"faroese":         "fo",
	"frisian":         "fy",
	"galician":        "gl",
	"georgian":        "ka",
	"gujarati":        "gu",
	"icelandic":       "is",
	"indonesian":      "id",
	"interlingua":     "ia",
	"irish":           "ga",
	"javanese":        "jv",
	"kannada":         "kn",
	"korean":          "ko",
	"latin":           "la",
	"latvian":         "lv",
	"lithuanian":      "lt",
	"luxembourgish":   "lb",
	"macedonian":      "mk",
	"maltese":         "mt",
	"manx":            "gv",
	"middle english":  "enm",
	"multilingual":    "mul",
	"nepali":          "ne",
	"occitan":         "oc",
	"old english":     "ang",
	"old norse":       "non",
	"persian":         "fa",
	"punjabi":         "pa",
	"sanskrit":        "sa",
	"scottish gaelic": "gd",
	"serbian":         "sr",
	"slovak":          "sk",
	"slovenian":       "sl",
	"swahili":         "sw",
	"tagalog":         "tl",
	"tamil":           "ta",
	"telugu":          "te",
	"thai":            "th",
	"ukrainian":       "uk",
	"urdu":            "ur",
	"vietnamese":      "vi",
	"welsh":           "cy",
	"yiddish":         "yi",
}

// librivoxGenres maps LibriVox's genre names (lowercased, as internal/importer's
// genre table keys them) onto this project's genre vocabulary - LICENSING.md's "never a
// retailer's taxonomy verbatim", which holds for a public-domain catalogue's
// taxonomy too. Only a genre with an evident vocabulary counterpart is mapped.
// Left out on purpose, and reported once per run as unmapped:
//
//   - LibriVox's PERIOD genres ("Published before 1800", "Published 1800 -1900",
//     "Published 1900 onward"): a publication date is not a genre;
//   - its umbrella and format genres ("General Fiction", "Fantastic Fiction",
//     "Dramatic Readings", "Anthologies", "Single Author Collections"), which
//     name no single vocabulary entry;
//   - anything not listed here, which a backfill tranche's unmapped line
//     surfaces and a later change adds deliberately.
var librivoxGenres = map[string]string{
	"action & adventure fiction":         "action-adventure",
	"biography & autobiography":          "biography-memoir",
	"children's fiction":                 "childrens",
	"children's non-fiction":             "childrens",
	"classics (greek & latin antiquity)": "classics",
	"cooking":                            "food-cooking",
	"crime & mystery fiction":            "mystery",
	"detective fiction":                  "mystery",
	"economics":                          "economics",
	"education":                          "education",
	"erotica":                            "erotica",
	"essays & short works":               "essays",
	"fairy tales":                        "fairy-tales-folklore",
	"fantasy fiction":                    "fantasy",
	"ghost stories":                      "ghost-stories",
	"gothic fiction":                     "gothic",
	"historical fiction":                 "historical-fiction",
	"history":                            "history",
	"horror & supernatural fiction":      "horror",
	"humorous fiction":                   "comedy-humor",
	"language learning":                  "language",
	"law":                                "law",
	"literary fiction":                   "literary-fiction",
	"medical":                            "medicine",
	"memoirs":                            "biography-memoir",
	"music":                              "music",
	"myths, legends & fairy tales":       "fairy-tales-folklore",
	"nature":                             "nature",
	"philosophy":                         "philosophy",
	"plays":                              "drama-plays",
	"poetry":                             "poetry",
	"political science":                  "politics",
	"psychology":                         "psychology",
	"religion":                           "religion-spirituality",
	"religious fiction":                  "religious-fiction",
	"romance":                            "romance",
	"satire":                             "comedy-humor",
	"science":                            "science",
	"science fiction":                    "science-fiction",
	"self-help":                          "self-help",
	"short stories":                      "short-stories",
	"travel & geography":                 "travel",
	"war & military fiction":             "war-military-fiction",
	"westerns":                           "westerns",
}

// LibrivoxLanguages returns a copy of the LibriVox language-name table
// (librivoxLanguages): lowercased names LibriVox states that the shared word
// table (LanguageWords) does not hold, each mapped to a schema language tag.
func LibrivoxLanguages() map[string]string { return copyTable(librivoxLanguages) }

// LibrivoxGenres returns a copy of the LibriVox genre-name table (librivoxGenres):
// lowercased LibriVox genre names mapped onto this project's genre vocabulary.
func LibrivoxGenres() map[string]string { return copyTable(librivoxGenres) }

func copyTable(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// librivoxLanguageNames is the reverse of the two tables a LibriVox language name
// is resolved through (librivoxLanguages, then languageWords): a tag back to the
// name LibriVox states, title-cased word by word ("ancient greek" -> "Ancient
// Greek"), which is how LibriVox spells every name in both tables. Both tables
// are injective and disjoint (TestLibrivoxLanguageNamesAreOneToOne), so every
// tag a LibriVox import can write has exactly one name.
var librivoxLanguageNames = func() map[string]string {
	out := map[string]string{}
	for _, m := range []map[string]string{languageWords, librivoxLanguages} {
		for name, tag := range m {
			out[tag] = titleWords(name)
		}
	}
	return out
}()

// LibrivoxLanguageName is the language name LibriVox states for a tag ("en" ->
// "English", "grc" -> "Ancient Greek"), matched on the primary subtag, or "" for
// a tag no LibriVox name maps to.
func LibrivoxLanguageName(tag string) string {
	return librivoxLanguageNames[PrimarySubtag(tag)]
}

// titleWords upper-cases the first letter of every space-separated word.
func titleWords(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}
