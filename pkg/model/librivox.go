package model

// librivox.go holds LibriVox's two NAME tables - its language names and its
// genre names - in the one place both of their readers reach: internal/importer
// maps a project's names through them (LibriVox name -> tag or genre), and
// pkg/query's LibriVox-shaped feed reads them in reverse (a language tag back to
// LibriVox's name) and forward (a ?genre= name to the vocabulary it was mapped
// onto). One table, so the feed can never answer a name the importer would not
// have written.

import (
	"maps"
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
// taxonomy too. Only a genre with an evident vocabulary counterpart is mapped,
// and no vocabulary term exists for LibriVox's sake (issue #2761: clear fits
// onto existing terms, nothing new). The rules, each read off what the genre
// holds in the 22,449-project snapshot rather than off its name alone:
//
//   - a sub-genre maps where its own name has one (a poetry form to poetry, a
//     Bible translation to religion-spirituality, a science branch to science,
//     Mathematics to science as the Audible table files it);
//   - the PLAY genres Comedy, Drama and Tragedy are all drama-plays: LibriVox
//     files plays under them (Twelfth Night, Uncle Vanya and Pirates of Penzance
//     are its Comedy; only 13 of its 226 Comedy projects also carry Plays), so a
//     comic play is a play, never comedy-humor, which is the humour shelf;
//   - a name that exists ONLY in LibriVox's two children's branches (genre ids
//     37-51 under Children's Fiction and Children's Non-fiction) and names no
//     vocabulary entry of its own - Animals & Nature, Family, General,
//     Historical, School, Short works - is childrens: Wind in the Willows is
//     Animals & Nature, the Bobbsey Twins are Family, Tom Brown's School Days is
//     School, Struwwelpeter is Short works. Animals & Nature is NOT nature,
//     whose vocabulary entry is non-fiction (the Audible table's genre kinds).
//     A children's name with an entry of its own keeps it (Action & Adventure,
//     Arts, Biography, Language learning), and a name a children's branch
//     SHARES with another branch (Religion, Poetry, Science, Reference, "Myths,
//     Legends & Fairy Tales") maps by name, since the table cannot tell the two
//     ids apart;
//   - the non-fiction sub-genres with a precedent in the Audible table
//     (audiblegenres.json by_name) map as it does: Literary Criticism to
//     writing-publishing (its "Literary History & Criticism"; criticism is a
//     subject, essays a form LibriVox states separately), Atheism & Agnosticism
//     to religion-spirituality (its "Atheism" and "Agnosticism"; 4 of 31 carry
//     Religion, 1 Philosophy), Crafts & Hobbies to home-garden, Animals to
//     nature; and Exploration, a sub-genre of Travel & Geography holding the
//     expedition narratives (Farthest North, South!, Lewis and Clark's
//     journals), to travel as its parent does.
//
// Left out on purpose, and reported once per run as unmapped:
//
//   - LibriVox's PERIOD genres ("Published before 1800", "Published 1800 -1900",
//     "Published 1900 onward") and the period sub-genres of its History and
//     Poetry branches ("Antiquity", "Ancient", "Asian Antiquity", "Medieval",
//     "Middle Ages/Middle History", "Early Modern", "Modern", "Modern (19th C)",
//     "Modern (20th C)", "Contemporary"): a period is not a genre;
//   - its umbrella and format genres ("General Fiction", "*Non-fiction",
//     "Fantastic Fiction", "Dramatic Readings", "Anthologies", "Single Author
//     Collections", "Single author", "Short non-fiction", "Literary
//     Collections", "Periodicals & Magazines", "Narratives", "Letters",
//     "Epistolary Fiction", "Reference"), which name no single vocabulary entry;
//   - a genre whose nearest vocabulary entry would be a guess. A FICTION genre
//     whose nearest entry is non-fiction ("Nature & Animal Fiction" -> nature,
//     "Travel Fiction" -> travel, "Family Life" -> parenting-relationships), and
//     one whose projects split across entries ("Writing & Linguistics" is
//     punctuation guides, novel-writing manuals and literary histories;
//     "Transportation" is flying handbooks beside the Pony Express; "Games" is
//     euchre rules, riddles and Baseball Ballads; "Sagas" is the Forsyte Saga
//     beside the Nibelungenlied; "Fictional Biographies & Memoirs" is fictional
//     autobiography - David Copperfield's kind - not historical-fiction's
//     biographical novel), plus "Culture & Heritage Fiction" and "Nautical &
//     Marine Fiction";
//   - anything not listed here, which a backfill tranche's unmapped line
//     surfaces and a later change adds deliberately.
//
// A change here reaches the works already imported through
// `metaimport librivox import --regenerate-genres` (internal/importer's
// regenerate.go), not by itself.
var librivoxGenres = map[string]string{
	"action & adventure":                 "action-adventure",
	"action & adventure fiction":         "action-adventure",
	"american standard version":          "religion-spirituality",
	"animals":                            "nature",
	"animals & nature":                   "childrens",
	"art, design & architecture":         "arts-entertainment",
	"arts":                               "arts-entertainment",
	"astronomy, physics & mechanics":     "science",
	"atheism & agnosticism":              "religion-spirituality",
	"ballads":                            "poetry",
	"bibles":                             "religion-spirituality",
	"biography":                          "biography-memoir",
	"biography & autobiography":          "biography-memoir",
	"business & economics":               "business",
	"chemistry":                          "science",
	"children's fiction":                 "childrens",
	"children's non-fiction":             "childrens",
	"christian fiction":                  "religious-fiction",
	"christianity - biographies":         "religion-spirituality",
	"christianity - commentary":          "religion-spirituality",
	"christianity - other":               "religion-spirituality",
	"classics (greek & latin antiquity)": "classics",
	"comedy":                             "drama-plays",
	"cooking":                            "food-cooking",
	"crafts & hobbies":                   "home-garden",
	"crime & mystery fiction":            "mystery",
	"detective fiction":                  "mystery",
	"douay-rheims version":               "religion-spirituality",
	"drama":                              "drama-plays",
	"earth sciences":                     "science",
	"economics":                          "economics",
	"education":                          "education",
	"elegies & odes":                     "poetry",
	"epics":                              "poetry",
	"erotica":                            "erotica",
	"essays":                             "essays",
	"essays & short works":               "essays",
	"exploration":                        "travel",
	"fairy tales":                        "fairy-tales-folklore",
	"family":                             "childrens",
	"family & relationships":             "parenting-relationships",
	"fantasy fiction":                    "fantasy",
	"free verse":                         "poetry",
	"gardening":                          "home-garden",
	"general":                            "childrens",
	"ghost stories":                      "ghost-stories",
	"gothic fiction":                     "gothic",
	"health & fitness":                   "health-wellness",
	"historical":                         "childrens",
	"historical fiction":                 "historical-fiction",
	"history":                            "history",
	"horror & supernatural fiction":      "horror",
	"house & home":                       "home-garden",
	"humor":                              "comedy-humor",
	"humorous fiction":                   "comedy-humor",
	"king james version":                 "religion-spirituality",
	"language learning":                  "language",
	"law":                                "law",
	"life sciences":                      "science",
	"literary criticism":                 "writing-publishing",
	"literary fiction":                   "literary-fiction",
	"lyric":                              "poetry",
	"mathematics":                        "science",
	"medical":                            "medicine",
	"memoirs":                            "biography-memoir",
	"multi-version (weekly and fortnightly poetry)": "poetry",
	"music":                        "music",
	"myths, legends & fairy tales": "fairy-tales-folklore",
	"nature":                       "nature",
	"other religions":              "religion-spirituality",
	"performing arts":              "arts-entertainment",
	"philosophy":                   "philosophy",
	"plays":                        "drama-plays",
	"poetry":                       "poetry",
	"political science":            "politics",
	"psychology":                   "psychology",
	"religion":                     "religion-spirituality",
	"religious fiction":            "religious-fiction",
	"romance":                      "romance",
	"satire":                       "comedy-humor",
	"school":                       "childrens",
	"science":                      "science",
	"science fiction":              "science-fiction",
	"self-help":                    "self-help",
	"short stories":                "short-stories",
	"short works":                  "childrens",
	"social science (culture & anthropology)": "social-sciences",
	"sonnets":             "poetry",
	"sports & recreation": "sports",
	"sports fiction":      "sports",
	"suspense, espionage, political & thrillers": "thriller-suspense",
	"technology & engineering":                   "technology",
	"tragedy":                                    "drama-plays",
	"travel & geography":                         "travel",
	"true crime":                                 "true-crime",
	"war & military":                             "military-history",
	"war & military fiction":                     "war-military-fiction",
	"westerns":                                   "westerns",
	"weymouth new testament":                     "religion-spirituality",
	"world english bible":                        "religion-spirituality",
	"young's literal translation":                "religion-spirituality",
}

// LibrivoxLanguages returns a copy of the LibriVox language-name table
// (librivoxLanguages): lowercased names LibriVox states that the shared word
// table (LanguageWords) does not hold, each mapped to a schema language tag.
func LibrivoxLanguages() map[string]string { return maps.Clone(librivoxLanguages) }

// LibrivoxGenres returns a copy of the LibriVox genre-name table (librivoxGenres):
// lowercased LibriVox genre names mapped onto this project's genre vocabulary.
func LibrivoxGenres() map[string]string { return maps.Clone(librivoxGenres) }

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
