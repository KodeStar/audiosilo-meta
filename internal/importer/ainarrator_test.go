package importer

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"

	"github.com/kodestar/audiosilo-meta/pkg/model"
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

// ---------------------------------------------------------------------------
// The shared gate: every source, not just libex
//
// libex refuses an AI-credited row at its own parse layer. Every OTHER source
// goes through runBooks' gate (importer.go refuseAIBooks), which is what the
// tests below exercise - through the audiosilo-books envelope, the OpenAudible
// projection and the Libation export, because all three are user-library
// sources of Audible content and all three can carry a Virtual Voice title.
//
// For those three the gate's answer to a synthetic NARRATION is now "admit and
// fold" rather than "refuse": the book is in somebody's library, and the credit
// names the one canonical `virtual-voice` record instead of minting a person per
// TTS persona (synthetic.go). What still refuses, everywhere, is an AI credited
// as the AUTHOR and a generative system credited as the narrator.

// aiBooksExport is an audiosilo-books library whose entries credit an AI in
// every shape the vocabulary knows, beside one book that must still import.
const aiBooksExport = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {
      "title": "A Real Book",
      "authors": ["Solo Author"],
      "narrators": ["A Narrator"],
      "asin": "B0ABSAI001",
      "language": "en"
    },
    {
      "title": "Virtually Narrated",
      "authors": ["Solo Author"],
      "narrators": ["Virtual Voice"],
      "asin": "B0ABSAI002",
      "language": "en"
    },
    {
      "title": "Persona Narrated",
      "authors": ["Solo Author"],
      "narrators": ["AI Voice Nina"],
      "asin": "B0ABSAI003",
      "language": "en"
    },
    {
      "title": "Cloned Narration",
      "authors": ["Solo Author"],
      "narrators": ["Steve Stewart's Voice Replica"],
      "asin": "B0ABSAI004",
      "language": "en"
    },
    {
      "title": "Marked Narration",
      "authors": ["Solo Author"],
      "narrators": ["Santiago (Voz de IA)"],
      "asin": "B0ABSAI005",
      "language": "en"
    },
    {
      "title": "Written By A Model",
      "authors": ["ChatGPT ChatGPT"],
      "narrators": ["A Narrator"],
      "asin": "B0ABSAI006",
      "language": "en"
    }
  ]
}`

// narratorsOf returns a work's single recording's narrator slugs.
func narratorsOf(t *testing.T, dataDir, workSlug string) []string {
	t.Helper()
	recs := recSlugsOf(t, dataDir, workSlug)
	if len(recs) != 1 {
		t.Fatalf("work %q has recordings %v, want exactly one", workSlug, recs)
	}
	var rec recordingFile
	readEntity(t, dataDir, recAddr(workSlug, recs[0]), &rec)
	return rec.Narrators
}

// syntheticPerson decodes the canonical synthetic record, failing if it is
// absent.
func syntheticPerson(t *testing.T, dataDir string) OutPerson {
	t.Helper()
	var p OutPerson
	readEntity(t, dataDir, personAddr(syntheticVoiceSlug), &p)
	return p
}

// TestUserLibraryAdmitsSyntheticNarration is the acceptance test for the
// maintainer's decision: a book in a USER's own library is admitted even when
// its narrator is synthetic, and all four voice shapes resolve to ONE record
// rather than to four personas. The generative-SYSTEM author is still refused,
// because the decision is about narration.
func TestUserLibraryAdmitsSyntheticNarration(t *testing.T) {
	sum, dataDir := runAudiosiloBooks(t, aiBooksExport, false)

	// Five of the six books: the human one plus the four synthetic narrations.
	if sum.NewWorks != 5 || sum.NewRecordings != 5 {
		t.Errorf("NewWorks/NewRecordings = %d/%d, want 5/5", sum.NewWorks, sum.NewRecordings)
	}
	if sum.SkippedRows != 1 {
		t.Errorf("SkippedRows = %d, want 1 (the model-authored book)", sum.SkippedRows)
	}
	// Solo Author, A Narrator, and the ONE synthetic record. Not a persona more.
	if sum.NewPeople != 3 {
		t.Errorf("NewPeople = %d, want 3", sum.NewPeople)
	}
	for _, slug := range []string{
		"ai-voice-nina", "steve-stewarts-voice-replica", "santiago",
		"chatgpt-chatgpt", "chatgpt",
	} {
		if entryExists(t, dataDir, personAddr(slug)) {
			t.Errorf("an AI credit was minted as a person at %q", slug)
		}
	}
	// Every synthetic narration credits the canonical record, whatever the
	// source spelled.
	for _, work := range []string{
		"virtually-narrated", "persona-narrated", "cloned-narration", "marked-narration",
	} {
		if got := narratorsOf(t, dataDir, work); len(got) != 1 || got[0] != syntheticVoiceSlug {
			t.Errorf("work %q narrators = %v, want [%q]", work, got, syntheticVoiceSlug)
		}
	}
	// The record itself: minted once, named canonically, kind synthetic, and
	// carrying the run's provenance like any other minted person.
	person := syntheticPerson(t, dataDir)
	if person.Name != syntheticVoiceName || person.Kind != model.KindEntitySynthetic {
		t.Errorf("synthetic person = %q/%q, want %q/%q", person.Name, person.Kind, syntheticVoiceName, model.KindEntitySynthetic)
	}
	if len(person.Sources) != 1 || person.Sources[0].Type != sourceAudiosiloBooks {
		t.Errorf("synthetic person sources = %#v, want one %q entry", person.Sources, sourceAudiosiloBooks)
	}
	// The model-authored book is still a refusal, and it still reads as one.
	if len(sum.Warnings) == 0 || !strings.Contains(sum.Warnings[0], "1 books skipped") {
		t.Fatalf("warnings = %#v, want the author-side AI refusal first", sum.Warnings)
	}
	if !strings.Contains(sum.Warnings[0], "an AI system, not a person") {
		t.Errorf("warning %q does not name the author-side reason", sum.Warnings[0])
	}
	// The admission is REPORTED, as a note rather than a warning: nothing went
	// wrong, and the intake bot reads warnings as "entries fell out".
	if len(sum.Notes) != 1 {
		t.Fatalf("notes = %#v, want exactly one", sum.Notes)
	}
	for _, want := range []string{"4 recordings credited to Virtual Voice", "synthetic narration", "Virtually Narrated"} {
		if !strings.Contains(sum.Notes[0], want) {
			t.Errorf("note %q does not mention %q", sum.Notes[0], want)
		}
	}
}

// TestSyntheticNarrationFoldsInsideACommaJoinedCredit is the bypass a
// per-source gate over the source's own array shape could not see: the AI name
// is not an element of the credit array, it is INSIDE one. sourceNames splits
// that element on commas exactly as the credit pipeline does, so the gate judges
// the same names the import will credit - and the fold rewrites exactly the name
// the gate admitted, leaving the human beside it untouched.
//
// Both shapes a projection can hand credits over in are covered: an element
// holding two names, and the plain comma-joined string a non-array value falls
// back to.
func TestSyntheticNarrationFoldsInsideACommaJoinedCredit(t *testing.T) {
	const export = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {
      "title": "Joined Element",
      "authors": ["Solo Author"],
      "narrators": ["Jane Doe, Virtual Voice"],
      "asin": "B0ABSJN001",
      "language": "en"
    },
    {
      "title": "Joined String",
      "authors": ["Solo Author"],
      "narrators": "Jane Doe, AI Voice Nina",
      "asin": "B0ABSJN002",
      "language": "en"
    }
  ]
}`
	sum, dataDir := runAudiosiloBooks(t, export, false)

	if sum.NewWorks != 2 || sum.SkippedRows != 0 {
		t.Errorf("NewWorks/SkippedRows = %d/%d, want 2/0", sum.NewWorks, sum.SkippedRows)
	}
	if entryExists(t, dataDir, personAddr("ai-voice-nina")) {
		t.Error("a TTS persona was minted as a person at \"ai-voice-nina\"")
	}
	for _, work := range []string{"joined-element", "joined-string"} {
		got := narratorsOf(t, dataDir, work)
		if len(got) != 2 || got[0] != "jane-doe" || got[1] != syntheticVoiceSlug {
			t.Errorf("work %q narrators = %v, want [jane-doe %s]", work, got, syntheticVoiceSlug)
		}
	}
	if sum.Notes == nil || !strings.Contains(sum.Notes[0], "2 recordings credited to Virtual Voice") {
		t.Errorf("notes = %#v, want both rows counted", sum.Notes)
	}
}

// TestOpenAudibleAndLibationAdmitSyntheticNarration is the BREADTH half. All
// three user-library sources are ranked in one trust tier (pkg/model/trust.go)
// and all three read Audible content, so a rule that applied to one of them was
// arbitrary. Each source's own field shape reaches the same gate, because they
// all go through sourceNames.
func TestOpenAudibleAndLibationAdmitSyntheticNarration(t *testing.T) {
	t.Run("openaudible", func(t *testing.T) {
		books := `[
			{"asin":"B0OAAI0001","title_short":"Real One","author":"Mara Quill","narrated_by":"Priya Lund","language":"english","region":"US","seconds":1000},
			{"asin":"B0OAAI0002","title_short":"Synthetic One","author":"Mara Quill","narrated_by":"Virtual Voice","language":"english","region":"US","seconds":1000},
			{"asin":"B0OAAI0003","title_short":"Joined One","author":"Mara Quill","narrated_by":"Priya Lund, AI Voice Nina","language":"english","region":"US","seconds":1000}
		]`
		sum, dataDir := runImport(t, books, false)
		if sum.NewWorks != 3 || sum.SkippedRows != 0 {
			t.Errorf("NewWorks/SkippedRows = %d/%d, want 3/0", sum.NewWorks, sum.SkippedRows)
		}
		if entryExists(t, dataDir, personAddr("ai-voice-nina")) {
			t.Error("a TTS persona was minted as a person at \"ai-voice-nina\"")
		}
		if got := narratorsOf(t, dataDir, "synthetic-one"); len(got) != 1 || got[0] != syntheticVoiceSlug {
			t.Errorf("narrators = %v, want [%q]", got, syntheticVoiceSlug)
		}
		if p := syntheticPerson(t, dataDir); p.Kind != model.KindEntitySynthetic {
			t.Errorf("synthetic person kind = %q, want %q", p.Kind, model.KindEntitySynthetic)
		}
	})

	t.Run("libation", func(t *testing.T) {
		export := `[
			{"AudibleProductId":"B0LBAI0001","Locale":"us","Title":"Real One","AuthorNames":"Mara Quill","NarratorNames":"Priya Lund","LengthInMinutes":100,"Language":"English"},
			{"AudibleProductId":"B0LBAI0002","Locale":"us","Title":"Synthetic One","AuthorNames":"Mara Quill","NarratorNames":"Virtual Voice","LengthInMinutes":100,"Language":"English"}
		]`
		sum, dataDir := runLibation(t, export, false)
		if sum.NewWorks != 2 || sum.SkippedRows != 0 {
			t.Errorf("NewWorks/SkippedRows = %d/%d, want 2/0", sum.NewWorks, sum.SkippedRows)
		}
		if got := narratorsOf(t, dataDir, "synthetic-one"); len(got) != 1 || got[0] != syntheticVoiceSlug {
			t.Errorf("narrators = %v, want [%q]", got, syntheticVoiceSlug)
		}
	})
}

// TestSyntheticRecordIsMintedOnceAndReused pins the whole point of the fold: a
// second AI-narrated book does not create a second record, whether it spells the
// credit the same way or not. NewPeople is what proves it - the record already
// exists by the time the second row is planned.
func TestSyntheticRecordIsMintedOnceAndReused(t *testing.T) {
	const export = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {
      "title": "First Synthetic",
      "authors": ["Solo Author"],
      "narrators": ["Virtual Voice"],
      "asin": "B0ABSRU001",
      "language": "en"
    },
    {
      "title": "Second Synthetic",
      "authors": ["Solo Author"],
      "narrators": ["Voix Virtuelle"],
      "asin": "B0ABSRU002",
      "language": "en"
    }
  ]
}`
	sum, dataDir := runAudiosiloBooks(t, export, false)

	if sum.NewWorks != 2 {
		t.Fatalf("NewWorks = %d, want 2", sum.NewWorks)
	}
	// Solo Author and the synthetic record - the second book adds nobody.
	if sum.NewPeople != 2 {
		t.Errorf("NewPeople = %d, want 2 (the synthetic record is minted once)", sum.NewPeople)
	}
	if entryExists(t, dataDir, personAddr("voix-virtuelle")) {
		t.Error("the French spelling minted a second record at \"voix-virtuelle\"")
	}
	for _, work := range []string{"first-synthetic", "second-synthetic"} {
		if got := narratorsOf(t, dataDir, work); len(got) != 1 || got[0] != syntheticVoiceSlug {
			t.Errorf("work %q narrators = %v, want [%q]", work, got, syntheticVoiceSlug)
		}
	}
}

// TestSyntheticFoldIsNarratorSideOnly pins the two halves of the scope the
// decision did NOT widen: an AI credited as the AUTHOR still refuses the whole
// row, and so does a generative SYSTEM credited as the narrator, which is the
// dump's untidiness about columns rather than a production fact.
func TestSyntheticFoldIsNarratorSideOnly(t *testing.T) {
	const export = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {
      "title": "Voice As Author",
      "authors": ["Virtual Voice"],
      "narrators": ["A Narrator"],
      "asin": "B0ABSNS001",
      "language": "en"
    },
    {
      "title": "System As Narrator",
      "authors": ["Solo Author"],
      "narrators": ["ChatGPT"],
      "asin": "B0ABSNS002",
      "language": "en"
    },
    {
      "title": "System Beside A Voice",
      "authors": ["Solo Author"],
      "narrators": ["Virtual Voice", "Sparky From ChatGPT"],
      "asin": "B0ABSNS003",
      "language": "en"
    }
  ]
}`
	sum, dataDir := runAudiosiloBooks(t, export, false)

	if sum.NewWorks != 0 || sum.SkippedRows != 3 {
		t.Errorf("NewWorks/SkippedRows = %d/%d, want 0/3", sum.NewWorks, sum.SkippedRows)
	}
	if len(sum.Notes) != 0 {
		t.Errorf("notes = %#v, want none: no row was admitted", sum.Notes)
	}
	// Above all: a REFUSED row never mints the canonical record, so a book the
	// catalogue declined cannot leave a synthetic narrator behind it.
	for _, slug := range []string{syntheticVoiceSlug, "chatgpt", "sparky-from-chatgpt"} {
		if entryExists(t, dataDir, personAddr(slug)) {
			t.Errorf("a credit from a refused row was minted as a person at %q", slug)
		}
	}
}

// TestLibexStillRefusesSyntheticNarration is the OTHER side of the tier split,
// stated as its own test rather than left implicit in the parse-layer suite
// above: the same row that a user's library gets admitted for is refused from
// the bulk mirror, because 145,558 dump rows credit an AI voice and seeding them
// would make one synthetic record the catalogue's most prolific narrator off
// nobody's attestation.
func TestLibexStillRefusesSyntheticNarration(t *testing.T) {
	sum, dataDir := runLibex(t, aiNarratorFixture(t), false)

	if sum.SkippedRows != 4 {
		t.Errorf("SkippedRows = %d, want 4", sum.SkippedRows)
	}
	if len(sum.Notes) != 0 {
		t.Errorf("notes = %#v, want none: the mirror admits no synthetic narration", sum.Notes)
	}
	if entryExists(t, dataDir, personAddr(syntheticVoiceSlug)) {
		t.Errorf("a libex row minted the canonical record at %q", syntheticVoiceSlug)
	}
	// Create mode keeps a line per row (a curated tranche is small, and the
	// detail is what a contributor acts on); either way every refusal is named.
	if len(sum.Warnings) != 4 {
		t.Errorf("warnings = %#v, want one per refused row", sum.Warnings)
	}
}

// TestRealCreditCannotMintTheSyntheticSlug is the RESERVATION guard. The
// canonical record is only a canonical if nothing else can ever write to its
// address, and what reserves it is the vocabulary itself rather than a second
// rule: every spelling that slugs to `virtual-voice` is in aiNarratorNames, so
// it either folds (narrator side) or refuses its row (author side).
func TestRealCreditCannotMintTheSyntheticSlug(t *testing.T) {
	for _, name := range []string{
		"Virtual Voice", "virtual voice", "VIRTUAL VOICE", "Virtual  Voice",
	} {
		if slug, _ := personSlug(name); slug != syntheticVoiceSlug {
			continue // not an address this guard is about
		}
		if !namesSyntheticVoice(name) {
			t.Errorf("%q slugs to %q but the vocabulary does not know it, so a real credit could mint the canonical",
				name, syntheticVoiceSlug)
		}
		if _, isAI := AICreditReason(name); !isAI {
			t.Errorf("%q slugs to %q but would not refuse an author-side row", name, syntheticVoiceSlug)
		}
	}
}

// TestSyntheticCanonicalSlugsToItsID is the collective fold's guard, applied to
// this canonical: the fold is only a fold onto ONE address if the name it mints
// under slugs to the id the rest of the code spells.
func TestSyntheticCanonicalSlugsToItsID(t *testing.T) {
	slug, fellBack := model.PersonSlug(syntheticVoiceName)
	if fellBack || slug != syntheticVoiceSlug {
		t.Errorf("PersonSlug(%q) = %q (fellBack=%v), want %q", syntheticVoiceName, slug, fellBack, syntheticVoiceSlug)
	}
	if got := PersonKindFor(syntheticVoiceName); got != model.KindEntitySynthetic {
		t.Errorf("PersonKindFor(%q) = %q, want %q", syntheticVoiceName, got, model.KindEntitySynthetic)
	}
	if got := PersonKindFor("Stephen Fry"); got != "" {
		t.Errorf("PersonKindFor(%q) = %q, want an empty kind", "Stephen Fry", got)
	}
}

// TestSyntheticNarratorNameFoldsEveryShape pins the exported door the intake
// forms use against the same four shapes the bulk fold covers, and against the
// role-qualified spelling only the cleaned name reveals.
func TestSyntheticNarratorNameFoldsEveryShape(t *testing.T) {
	for _, name := range []string{
		"Virtual Voice", "Voz Virtual", "Voce Virtuale",
		"AI Voice Nina", "AI Voice",
		"Steve Stewart's Voice Replica",
		"Santiago (Voz de IA)", "Elise (AI)",
		"Virtual Voice - narrator",
	} {
		got, folded := SyntheticNarratorName(name)
		if !folded || got != syntheticVoiceName {
			t.Errorf("SyntheticNarratorName(%q) = %q/%v, want %q/true", name, got, folded, syntheticVoiceName)
		}
	}
	for _, name := range []string{
		// Real narrators the vocabulary must leave alone, and a generative
		// system, which is not a voice credit at all.
		"Ai Voicu", "April's Voice", "Debra Shieber's Voice Talent",
		"Stephen Fry", "Full Cast", "ChatGPT", "",
	} {
		got, folded := SyntheticNarratorName(name)
		if folded || got != name {
			t.Errorf("SyntheticNarratorName(%q) = %q/%v, want it untouched", name, got, folded)
		}
	}
}

// TestAudiosiloBooksKeepsTheNonAIRefusalsLibexOnly pins the deliberate scope of
// the gate: an audiosilo-books entry is a USER's own library, so a credit that
// slugs away to nothing keeps today's behaviour (the catch-all conflation, which
// is visible to the user whose book it is) rather than losing them their book.
// Only the AI vocabulary crosses over - see the note above refuseAIBooks.
func TestAudiosiloBooksKeepsTheNonAIRefusalsLibexOnly(t *testing.T) {
	const export = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {
      "title": "A Korean Book",
      "authors": ["김영하"],
      "narrators": ["A Narrator"],
      "asin": "B0ABSKO001",
      "language": "en"
    }
  ]
}`
	sum, _ := runAudiosiloBooks(t, export, false)
	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1: a user's own library is not refused for an unslugabble name", sum.NewWorks)
	}
	if sum.SkippedRows != 0 {
		t.Errorf("SkippedRows = %d, want 0", sum.SkippedRows)
	}
}
