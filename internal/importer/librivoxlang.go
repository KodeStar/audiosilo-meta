package importer

import "strings"

// librivoxLanguages maps the language NAMES LibriVox's feed states ("the English
// display name for the language of the reading") that the shared word table
// (model.LanguageWords) does not hold onto a language tag the schema accepts.
// LibriVox reads in languages no retail mirror carries - Latin, Esperanto,
// Ancient Greek - so the shared table, which is deliberately limited to the
// ISO 639-1 codes the libex dump measured, is not enough on its own.
//
// Each entry is the language's ISO 639-1 code where it has one and its ISO 639-2
// code otherwise (Ancient Greek, Old English, Middle English and Old Norse have
// no 639-1 code; the schema's language pattern takes two or three letters), and
// "Multilingual" is 639-2's "mul", the code for a work in several languages.
// Nothing here is a guess between two codes: a name that is not in this table
// or the shared one is UNKNOWN, and the planner skips the project with the
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

// librivoxLanguage resolves a LibriVox language name (case-insensitive): this
// table first, then the shared word table. ok is false for a name neither holds.
func librivoxLanguage(name string) (string, bool) {
	if code, ok := librivoxLanguages[strings.ToLower(strings.TrimSpace(name))]; ok {
		return code, true
	}
	return mapLanguage(name)
}
