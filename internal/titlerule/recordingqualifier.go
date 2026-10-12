package titlerule

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// recordingqualifier.go reads a RECORDING QUALIFIER off a series name: a statement
// about one PRODUCTION of the books - who narrated it, that it is abridged, a
// full-cast or a dramatized recording - which a retailer folds into the series name
// when it lists each production as a series of its own ("Harry Potter (Narrated by
// Stephen Fry)", "Harry Potter (Full-Cast Editions)", "Discworld: Tiffany Aching
// (abridged)", "Harry Potter - Gesprochen von Rufus Beck"). A series is an ordered
// set of WORKS; narration, abridgement and production are facts about a recording
// (recording.narrators, recording.abridged), so a recording-qualified name names the
// SAME series as its base. Every reader of the question asks this one function:
// internal/importer's claim cleaning (cleanSeriesName) and its qualifier index, the
// intake form, and pkg/check's series-recording-qualifier advisory.
//
// What is NOT a recording qualifier, and is left exactly as written: a language
// EDITION ("[German Edition]", "(Deutsche Ausgabe)" - a translation, which is a
// different set of works, read by ReadSeriesQualifiers' language facet), a reading
// ORDER ("(Chronological)"), a collection or box set, a Hörspiel or radio production
// (a German Hörspiel series is very often its own numbered run of episodes - "Die drei
// ???", "Sherlock Holmes - Hörspiel" - rather than a recording of a text series), and
// a dialect note ("(Narración en Castellano)" - a Castilian and a Latin American
// Spanish edition are frequently two translations). Both vocabularies are CLOSED.
//
// Measured over the tree at origin/main (2026-10-11, 45,586 series): 767 series names
// match the broad pattern /narrated by|full[- ]cast|abridged|dramati[sz]|gesprochen|
// gelesen|read by|gekürzt|hörspiel|edition/i, most of them language editions or
// Hörspiel runs. This reader takes 189 of them, every one a production statement:
// 127 abridged, 23 gekürzt, 8 unabridged/ungekürzt, 25 dramatized, 1 full-cast and 5
// narrator credits (two bracketed - "(Narrated by Stephen Fry)", "(gelesen von Gerrit
// Petersen)" - the separator form "Harry Potter - Gesprochen von Rufus Beck" and two
// ", read by Timothy West"). The narrower pattern without "hörspiel|edition" matches
// 196; the seven it leaves are names whose words are not a qualifier ("Charles Paris
// Dramatisations", "Classic Stories and Tales Read by Celebrities", "Dramatis
// Personae", "Neil Gaiman Full-Cast Radio Adaptations", "Der König der purpurnen Stadt
// - Das ungekürzte Hörspiel").

// SeriesNarratorLeadIns are the lead-in phrases of a trailing BRACKETED group that
// qualifies a series name by its narrator ("Sherlock Holmes - Die galaktischen Fälle
// (gelesen von Peter Bocek)"). They MOVED here from internal/importer
// (seriesNarratorQualifiers), where they were measured over the full 1.13M-book libex
// dump, unchanged and in their order, so the importer, pkg/check and internal/audit
// read one list. Counts are distinct series titles and the books behind them. Every
// entry is lowercase ASCII and matched against the group's slug, so one entry covers
// "Hörspiele" and "Horspiele" alike.
//
// "read by" and "lu par" were deliberately left out of it by that measurement: neither
// occurs in the trailing-bracket position anywhere in the dump, and both occur
// elsewhere as false positives («"À la recherche du temps perdu" lu par de grands
// acteurs» credits "great actors"). A bracketed "(Read by X)" is still read, but only
// through the title rule's NAME bound (narratorCredit), which is what keeps it off
// prose.
var SeriesNarratorLeadIns = []string{
	"gelesen von",    // 15 series titles, 446 books - German "read by"
	"narrated by",    // 3 titles, 91 books
	"gesprochen von", // 1 title, 8 books - German "spoken by"
	"horspiele von",  // 2 titles, 6 books - German "audio dramas by"
}

// recordingFormatPhrases are the production statements a whole bracketed group may
// make, keyed by the group's slug (model.SlugifyWhole, so case, spacing, hyphens and
// diacritics are not identity): an abridgement either way, a full-cast recording and a
// dramatization. seriesNote reads every recording qualifier as a catalogue note too
// (recordingGroup), so the two vocabularies cannot disagree.
var recordingFormatPhrases = map[string]bool{
	"abridged": true, "unabridged": true, "gekurzt": true, "ungekurzt": true,
	"full-cast": true, "full-cast-edition": true, "full-cast-editions": true,
	"full-cast-production": true, "full-cast-recording": true,
	"dramatized": true, "dramatised": true,
	"dramatized-adaptation": true, "dramatised-adaptation": true,
	"dramatization": true, "dramatisation": true,
	"dramatized-version": true, "full-cast-dramatization": true,
}

// seriesNarratorLeadInSlugs are SeriesNarratorLeadIns in slug form, each with the
// hyphen that must follow it, taken once rather than per group - through the very
// slugger the group is read with (model.SlugifyWhole), so an entry and a group spelling
// it cannot disagree about case, spacing or a diacritic.
var seriesNarratorLeadInSlugs = func() []string {
	out := make([]string, len(SeriesNarratorLeadIns))
	for i, lead := range SeriesNarratorLeadIns {
		out[i] = model.SlugifyWhole(lead) + "-"
	}
	return out
}()

// recordingGroup reports whether one bracketed group, given as its contents and their
// slug (model.SlugifyWhole), is a recording qualifier and nothing else: a production
// phrase, a series narrator lead-in followed by anything at all (the posture the
// importer measured the bracketed lead-ins under, so "(Narrated by the author)" is read
// too), or a narrator credit under the title rule's name bound. A group holding a
// further bracket ("((gelesen von Peter)", which the group pattern matches from its
// first opener) is never one - stripping it would leave a dangling bracket.
func recordingGroup(inner, slug string) bool {
	if inner == "" || strings.ContainsAny(inner, "()[]") {
		return false
	}
	if recordingFormatPhrases[slug] {
		return true
	}
	for _, lead := range seriesNarratorLeadInSlugs {
		if rest, ok := strings.CutPrefix(slug, lead); ok && rest != "" {
			return true
		}
	}
	return narratorCredit(inner)
}

// StripSeriesRecordingQualifiers returns a series name without the recording
// qualifiers it ends in, and whether it carried any. Two shapes are read:
//
//   - trailing BRACKETED groups that are recording qualifiers (recordingGroup), any
//     number of them; a language edition or ordering group among them is KEPT, in
//     place, so "Horatio Hornblower (publication order) (abridged)" is "Horatio
//     Hornblower (publication order)" and a language edition stays the edition it is;
//   - a trailing narrator SEGMENT after a separator ("Harry Potter - Gesprochen von
//     Rufus Beck", "..., read by Timothy West"), held to exactly the bound the title
//     rule strips a narrator credit under (trailingNarratorCut: a closed lead-in
//     opening the last segment, then a NAME of two to six capitalized words) - an
//     unbracketed tail is otherwise indistinguishable from a name containing the
//     words, which is why "Classic Stories Read by Celebrities" stays.
//
// The two interleave: what a narrator segment leaves behind is read again, so "Foo
// (abridged) - Narrated by Ann Reader" is "Foo" in one call rather than in two.
//
// The first group that is neither a recording qualifier nor a kept edition/ordering
// group stops the reading. Nothing is cut that would leave no letter or digit behind
// (peelTrailingGroup and the segment cut both refuse it), so a name that is nothing
// but its qualifier comes back unchanged. Whenever ok is false the name is returned
// exactly as given. It is idempotent.
func StripSeriesRecordingQualifiers(name string) (base string, ok bool) {
	return stripSeriesRecording(name, nil)
}

// abridgementPhrases are the recording format groups that state an abridgement either
// way, keyed as recordingFormatPhrases is: true for an abridged recording.
var abridgementPhrases = map[string]bool{
	"abridged": true, "gekurzt": true,
	"unabridged": false, "ungekurzt": false,
}

// SeriesAbridgement is the abridgement a series name's recording qualifiers state -
// "(abridged)", "[gekürzt]", "(Unabridged)" - read by the very reading
// StripSeriesRecordingQualifiers strips them under, so a writer cleaning the qualifier
// off a series claim can carry the fact onto the RECORDING it is about instead of
// losing it. stated is false when no qualifier states one, or when two contradict.
func SeriesAbridgement(name string) (abridged, stated bool) {
	conflict := false
	stripSeriesRecording(name, func(slug string) {
		a, ok := abridgementPhrases[slug]
		if !ok {
			return
		}
		if stated && a != abridged {
			conflict = true
		}
		abridged, stated = a, true
	})
	if conflict {
		return false, false
	}
	return abridged, stated
}

// stripSeriesRecording is the one reading behind StripSeriesRecordingQualifiers and
// SeriesAbridgement; onDrop, when not nil, is handed the slug of every bracketed group
// dropped.
func stripSeriesRecording(name string, onDrop func(slug string)) (string, bool) {
	if !strings.ContainsAny(name, ")]") && !mentionsNarratorLeadInFold(name) {
		return name, false // the common case: no group, no lead-in
	}
	s := strings.TrimSpace(name)
	var kept []string // the edition/ordering groups peeled on the way, last first
	dropped := false
read:
	for {
		rest, group, peeled := peelTrailingGroup(s)
		if !peeled {
			// No group left: a narrator credit may be the last segment, and what it
			// leaves may end in a further recording group.
			cut, found := trailingNarratorSegment(s)
			if !found {
				break
			}
			s, dropped = cut, true
			continue
		}
		inner := groupContents(group)
		slug := model.SlugifyWhole(inner)
		switch {
		case recordingGroup(inner, slug):
			dropped = true
			if onDrop != nil {
				onDrop(slug)
			}
		case editionLanguagePhrases[slug] != "" || OrderingOfDecoration(slug) != "":
			kept = append(kept, group)
		default:
			// Neither: the reading stops and the group stays where it is - which
			// also means no narrator segment can be trailing.
			break read
		}
		s = rest
	}
	if !dropped {
		return name, false
	}
	for i := len(kept) - 1; i >= 0; i-- {
		s += " " + kept[i]
	}
	return s, true
}

// trailingNarratorSegment cuts a narrator credit that is the name's LAST segment
// (trailingNarratorCut) and returns what precedes it with the separator trimmed
// (trailingNarratorHead, then any separator run left before it), refusing a cut that
// would leave no letter or digit.
func trailingNarratorSegment(s string) (string, bool) {
	if !mentionsNarratorLeadInFold(s) {
		return s, false
	}
	i, found := trailingNarratorCut(s)
	if !found {
		return s, false
	}
	head := strings.TrimSpace(trailingSeparatorRE.ReplaceAllString(trailingNarratorHead(s, i), ""))
	if !hasAlnum(head) {
		return s, false
	}
	return head, true
}
