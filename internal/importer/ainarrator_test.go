package importer

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// aiNarratorFixture is six rows lifted from the real libex dump: four
// AI-narrated (one per matching shape, in three languages) and two human. Every
// one of the six carries is_vvab = FALSE upstream, which is the whole point -
// the flag the export SQL filters on says "not a virtual voice" for all of them,
// so only the narrator credit tells them apart.
//
// The last row is the negative control the rule has to survive: a book ABOUT
// synthetic voices ("Fighting Deepfakes Across Time") read by a real narrator.
// The subject of a book is not evidence about who read it, and only the credit
// is ever judged.
func aiNarratorFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/libex_ai_narrators.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestLibexRefusesAINarratedRows is the rule's acceptance test: an AI-narrated
// row must be refused by the PARSE layer, so nothing downstream - no work, no
// recording, and above all no person record for a text-to-speech engine - is
// ever planned from it, while the human row in the same file imports normally.
func TestLibexRefusesAINarratedRows(t *testing.T) {
	sum, dataDir := runLibex(t, aiNarratorFixture(t), false)

	if sum.SkippedRows != 4 {
		t.Errorf("SkippedRows = %d, want 4 (the AI-narrated rows)", sum.SkippedRows)
	}
	// Only the two human rows survive. "Just So" states exactly two credits and
	// Alan Watts is both its author and its narrator - the slug is the identity,
	// so that is ONE person - and the deepfakes book adds its author and its
	// narrator, so three people in all.
	if sum.NewWorks != 2 || sum.NewRecordings != 2 || sum.NewPeople != 3 {
		t.Errorf("summary = %d works / %d recordings / %d people, want 2/2/3", sum.NewWorks, sum.NewRecordings, sum.NewPeople)
	}
	if !entryExists(t, dataDir, "works/ju/just-so/work.json") {
		t.Error("the human-narrated row was not imported")
	}
	if !entryExists(t, dataDir, "people/al/alan-watts.json") {
		t.Error("the human narrator got no person record")
	}
	// The negative control: a book about synthetic voices, read by a person.
	if !entryExists(t, dataDir, "works/fi/fighting-deepfakes-across-time/work.json") {
		t.Error("a book ABOUT voice cloning was refused; only the credit is judged")
	}
	if !entryExists(t, dataDir, "people/we/wendy-baran.json") {
		t.Error("the deepfakes book's real narrator got no person record")
	}
	// No AI voice became a person, and no AI-narrated book became a work.
	for _, rel := range []string{
		"people/vi/virtual-voice.json",
		"people/ai/ai-voice-arthur-lane.json",
		"people/sa/santiago-voz-de-ia.json",
		"people/si/silvia-a-voz-de-ia.json",
		"works/de/democracy-in-the-middle-east/work.json",
		"works/in/in-the-abyss/work.json",
		"works/ma/magia-de-los-celtas/work.json",
		// The voice-replica program: the cloned narrator is a real person, but
		// this credit is a model of their voice, not them.
		"people/ad/adrian-bissons-voice-replica.json",
		"people/ad/adrian-bisson.json",
		"works/th/the-bridge-child/work.json",
	} {
		if entryExists(t, dataDir, rel) {
			t.Errorf("an AI-narrated row created %s", rel)
		}
	}
}

// TestLibexAINarratorWarningLines pins how the refusal reads in both reporting
// shapes. A curated create-mode tranche gets a line per row (the contributor
// acts on each); a catalogue-bounded run over a large export gets one aggregate
// line, because on the full dump this class alone would otherwise print ~145,000
// of them.
func TestLibexAINarratorWarningLines(t *testing.T) {
	parsed, err := parseLibex([]byte(aiNarratorFixture(t)))
	if err != nil {
		t.Fatal(err)
	}

	perRow := parsed.warningLines(false)
	if len(perRow) != 4 {
		t.Fatalf("create-mode lines = %v, want one per refused row", perRow)
	}
	for _, want := range []string{
		`B0F5RH86TX: narrator "Virtual Voice" is an AI voice; row skipped`,
		`B0H6LM9KCD: narrator "AI Voice Arthur Lane" is an AI voice; row skipped`,
		`B0F1TG8K35: narrator "Santiago (Voz de IA)" is an AI voice; row skipped`,
		`B0FCZXJRH1: narrator "Adrian Bisson's voice replica" is an AI voice; row skipped`,
	} {
		if !strings.Contains(strings.Join(perRow, "\n"), want) {
			t.Errorf("per-row lines missing %q:\n%s", want, strings.Join(perRow, "\n"))
		}
	}

	aggregate := parsed.warningLines(true)
	if len(aggregate) != 1 {
		t.Fatalf("aggregate lines = %v, want exactly one", aggregate)
	}
	if got, want := aggregate[0], "libex: 4 rows skipped: a credited name is an AI voice or system"; !strings.HasPrefix(got, want) {
		t.Errorf("aggregate line = %q, want it to start with %q", got, want)
	}
	// The examples ride along so an operator can go and look at the data.
	if !strings.Contains(aggregate[0], "B0F5RH86TX") {
		t.Errorf("aggregate line names no example: %q", aggregate[0])
	}
}

// TestIsAINarratorName is the rule's passing/violating table. The REFUSALS in
// the second half are the load-bearing ones: every name there is a real narrator
// in the dump that a lazier rule (a substring search for "tts", or a bare
// "ai"/"ki"/"ia" token) would wrongly exclude.
func TestIsAINarratorName(t *testing.T) {
	aiVoices := []string{
		// Wholly-AI credits, one per localization the dump carries.
		"Virtual Voice",
		"virtual voice",
		"Voz Virtual",
		"Voz virtual",
		"Voix virtuelle",
		"Voce Virtuale",
		"Virtuelle Stimme",
		"Voz sintética",
		"Voce Artificiale",
		"Voz virual",
		"Digital Voices",
		// Diacritics may arrive decomposed (NFD) from a Mac-side exporter.
		norm.NFD.String("Voz sintética"),
		// The "AI Voice <persona>" prefix family, including the bare form.
		"AI Voice",
		"AI Voice Arthur Lane",
		"ai voice nina",
		"AI Voice AI Narrator - Synthesized Voice (Google WaveNet de-DE)",
		// The "<narrator>'s voice replica" suffix family, in the spellings the
		// dump actually carries (including a stray space before the possessive
		// and a company rather than a person as the cloned party).
		"Steve Stewart's voice replica",
		"Adrian Bisson's voice replica",
		"Alex Gage's Voice Replica",
		"Linda Kutzer 's voice replica",
		"Steve Bramham LLC's voice replica",
		"Aldus H Chapin II's voice replica",
		"N. Holt's voice replica",
		"voice replica",
		// Trailing markers that declare the credit synthetic.
		"Santiago (Voz de IA)",
		"Elise (AI)",
		"Amir [AI]",
		"Mar Cabra (Réplica de voz autorizada)",
		"Jaime Mayor O. (Réplica de voz Autorizada)",
		"Garry Dippel (KI Sprecher)",
	}
	for _, name := range aiVoices {
		if !isAINarratorName(name) {
			t.Errorf("isAINarratorName(%q) = false, want true", name)
		}
	}

	humans := []string{
		// Real narrators a "tts" substring rule would eat.
		"Alan Watts", "Wade Stotts", "Madison Mitts", "Patricia Ricketts",
		"Pauline Letts", "Faith Potts", "Niecy Nash-Betts", "Lisa Reneé Pitts",
		// Real narrators a bare "ai"/"ki"/"ia" token rule would eat.
		"Ki Hong Lee", "Ai-jen Poo", "Ka Ki Lee", "Sage Ia", "Man-ki Kim",
		// A human name that merely STARTS like the prefix; the word boundary
		// after "ai voice" is what saves it.
		"Ai Voicu",
		// Human qualifiers in the same trailing-marker position.
		"Stefan Rudnicki (Skyboat Media)", "Someone (TheVoiceOgre)",
		"Someone (The Captain's Voice)", "Someone (Vitruvian Sound)",
		// Real credits a looser "'s voice" rule would eat, all from the dump.
		"The Captain's Voice", "April's Voice", "Debra Shieber's Voice Talent",
		"Michael LaVoice", "Grace Voice", "Zia Islam - Your Digital Voice",
		// A name that merely ENDS like the suffix; the word boundary before
		// "voice replica" is what saves it.
		"Ivoice Replica", "Replica",
		// Ordinary credits, including a corporate one.
		"Stephen Fry", "Full Cast", "Audible Studios", "",
	}
	for _, name := range humans {
		if isAINarratorName(name) {
			t.Errorf("isAINarratorName(%q) = true, want false", name)
		}
	}
}

// TestFirstAINarratorIsAnyNotAll pins the ANY rule and the one case that makes
// it visible. No book in the dump credits a human alongside a synthetic voice,
// so this decides nothing today - it pins which way the code fails when one
// appears, and refusing is the direction that cannot write bad data.
func TestFirstAICreditIsAnyNotAll(t *testing.T) {
	_, name, _, isAI := firstAICredit(nil, []string{"Stephen Fry", "Virtual Voice"})
	if !isAI {
		t.Error("a mixed human/AI credit list was admitted; the rule is ANY, not ALL")
	}
	if name != "Virtual Voice" {
		t.Errorf("named %q, want the offending credit", name)
	}
	if _, _, _, isAI := firstAICredit(nil, []string{"Stephen Fry", "Jim Dale"}); isAI {
		t.Error("an all-human credit list was refused")
	}
	if _, _, _, isAI := firstAICredit(nil, nil); isAI {
		t.Error("an empty credit list was refused")
	}
}

// TestAINarratorRoleQualifiedCredit pins the defensive second look. A trailing
// role qualifier hides the marker from the parenthetical test, and the CLEANED
// name is the one that would become the person record, so it is the one that has
// to be judged. No such form is in the dump today.
func TestAINarratorRoleQualifiedCredit(t *testing.T) {
	const credit = "Elise (AI) - narrator"
	if isAINarratorName(credit) {
		t.Fatalf("precondition: %q should not match on its raw form", credit)
	}
	if _, _, _, isAI := firstAICredit(nil, []string{credit}); !isAI {
		t.Errorf("firstAICredit missed %q; the cleaned form %q is an AI voice", credit, CleanCreditName(credit))
	}
}

// TestAINarratorVocabularyIsCanonical pins the shape of both tables. Lookups go
// through foldCredit (lowercased, diacritics folded, single-spaced), so a
// key that is not already in that form is unreachable by construction - it would
// silently never match the release it was added for. Mirrors
// TestRoleQualifiersVocabulary.
func TestAINarratorVocabularyIsCanonical(t *testing.T) {
	tables := map[string]map[string]bool{
		"aiNarratorNames":   aiNarratorNames,
		"aiNarratorMarkers": aiNarratorMarkers,
	}
	for table, entries := range tables {
		for entry := range entries {
			if entry == "" {
				t.Errorf("%s contains an empty key", table)
				continue
			}
			if got := foldCredit(entry); got != entry {
				t.Errorf("%s key %q is not canonical (foldCredit gives %q); it can never match", table, entry, got)
			}
		}
	}
	for name, affix := range map[string]string{
		"aiNarratorPrefix": aiNarratorPrefix,
		"aiNarratorSuffix": aiNarratorSuffix,
	} {
		if got := foldCredit(affix); got != affix {
			t.Errorf("%s %q is not canonical (want %q)", name, affix, got)
		}
	}
}
