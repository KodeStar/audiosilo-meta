package model

import (
	"strings"
	"unicode"

	"github.com/kodestar/audiosilo-meta/internal/creditvocab"
)

// fileTagCredits are the labels a file's author and narrator tags carry for no
// particular contributor that neither of the importer's tables lists: a tagger's
// stand-ins ("Unknown Artist", "Multiple Narrators", "VA") and a production's
// label in the reader's place ("GraphicAudio [Dramatized Adaptation]").
var fileTagCredits = []string{
	"", "Various Readers", "Various Voices", "Multiple Narrators", "Multiple Readers",
	"Multiple Voices", "Unknown Narrator", "Unknown Artist", "Unknown Author", "Anon",
	"Narrator", "Narrators", "None", "VA", "GraphicAudio", "GraphicAudio LLC",
	"GraphicAudio Dramatized Adaptation", "GraphicAudio Dramatised Adaptation",
	"Dramatized Adaptation", "Dramatised Adaptation", "Dramatization", "Dramatisation",
	"Dramatized", "Dramatised",
}

// genericCredits holds the creditKey of every collective statement, booking
// placeholder and file-tag label.
var genericCredits = func() map[string]bool {
	names := map[string]bool{}
	for name := range creditvocab.Collective {
		names[creditKey(name)] = true
	}
	for name := range creditvocab.Placeholders {
		names[creditKey(name)] = true
	}
	for _, name := range fileTagCredits {
		names[creditKey(name)] = true
	}
	return names
}()

// IsGenericCredit reports whether a file credit names no particular contributor:
// a collective statement ("Full Cast", "Various", "N.N.", "autori vari"), a
// booking placeholder ("TBA") or a file-tag label ("Unknown Artist",
// "GraphicAudio"). It compares the WHOLE credit, case, accents, spaces and
// punctuation aside, so a named ensemble ("the Full Cast Family") or a surname
// ("P.C. Cast") keeps its identity. It is deliberately narrower than the
// importer's placeholder refusal: no phrase inside a longer name ("Author to be
// Announced") and no doubled name ("TBD TBD").
func IsGenericCredit(name string) bool { return genericCredits[creditKey(name)] }

// creditKey is a credit's lowercase letters and digits, accents removed.
func creditKey(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, StripMarks(name))
}
