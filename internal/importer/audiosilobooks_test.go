package importer

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// runAudiosiloBooks runs the audiosilo-books importer against a fresh empty data
// dir (reusing the shared runWith harness).
func runAudiosiloBooks(t *testing.T, envelope string, dryRun bool) (Summary, string) {
	t.Helper()
	return runWith(t, RunAudiosiloBooks, envelope, dryRun)
}

const audiosiloBooksExport = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {
      "title": "The Way of Kings",
      "authors": ["Brandon Sanderson"],
      "narrators": ["Kate Reading", "Michael Kramer"],
      "series": "The Stormlight Archive",
      "series_position": "1",
      "asin": "B0ABS00001",
      "language": "en",
      "release_date": "2010-08-31",
      "publisher": "Macmillan Audio",
      "runtime_min": 500,
      "chapters": 45,
      "abridged": false,
      "cover_url": "https://m.media-amazon.com/images/I/way-of-kings._SL500_.jpg"
    },
    {
      "title": "Unknown Abridgement",
      "authors": ["Solo Author"],
      "narrators": ["A Narrator"],
      "asin": "B0ABS00002",
      "language": "en",
      "cover_url": "http://insecure.example/cover.jpg"
    }
  ]
}`

func TestRunAudiosiloBooks(t *testing.T) {
	sum, dataDir := runAudiosiloBooks(t, audiosiloBooksExport, false)

	if sum.NewWorks != 2 {
		t.Errorf("NewWorks = %d, want 2", sum.NewWorks)
	}
	if sum.NewRecordings != 2 {
		t.Errorf("NewRecordings = %d, want 2", sum.NewRecordings)
	}
	// Brandon Sanderson + Kate Reading + Michael Kramer + Solo Author + A Narrator.
	if sum.NewPeople != 5 {
		t.Errorf("NewPeople = %d, want 5", sum.NewPeople)
	}
	if sum.NewSeries != 1 {
		t.Errorf("NewSeries = %d, want 1", sum.NewSeries)
	}

	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}

	// Work: language ISO code accepted verbatim, audiosilo-books provenance.
	var work struct {
		Title    string   `json:"title"`
		Authors  []string `json:"authors"`
		Language string   `json:"language"`
		Sources  []struct {
			Type string `json:"type"`
			Ref  string `json:"ref"`
		} `json:"sources"`
	}
	readEntity(t, dataDir, "works/th/the-way-of-kings/work.json", &work)
	if work.Title != "The Way of Kings" {
		t.Errorf("work title = %q", work.Title)
	}
	if work.Language != "en" {
		t.Errorf("work language = %q, want en (ISO code passed through)", work.Language)
	}
	if len(work.Sources) != 1 || work.Sources[0].Type != "audiosilo-books-import" || work.Sources[0].Ref != "B0ABS00001" {
		t.Errorf("work sources = %+v", work.Sources)
	}

	// Recording: two narrators, region defaulted to us, abridged emitted, and the
	// projection's https cover_url carried through to cover_url.
	var rec struct {
		Narrators []string `json:"narrators"`
		Abridged  *bool    `json:"abridged"`
		CoverURL  string   `json:"cover_url"`
		ASIN      []struct {
			Region string `json:"region"`
			ASIN   string `json:"asin"`
		} `json:"asin"`
	}
	readEntity(t, dataDir, "works/th/the-way-of-kings/recordings/kate-reading-2010.json", &rec)
	if len(rec.Narrators) != 2 {
		t.Errorf("narrators = %v, want 2", rec.Narrators)
	}
	if rec.Abridged == nil || *rec.Abridged != false {
		t.Errorf("abridged = %v, want explicit false", rec.Abridged)
	}
	if rec.CoverURL != "https://m.media-amazon.com/images/I/way-of-kings._SL500_.jpg" {
		t.Errorf("cover_url = %q, want the https projection cover", rec.CoverURL)
	}
	if len(rec.ASIN) != 1 || rec.ASIN[0].Region != "us" || rec.ASIN[0].ASIN != "B0ABS00001" {
		t.Errorf("asin = %+v, want one us B0ABS00001 (region defaulted)", rec.ASIN)
	}

	// The second book stated no abridged flag: the field must be OMITTED, never
	// fabricated to false (omit-never-guess). Its cover_url is non-https, so it is
	// dropped rather than recorded as an invalid cover.
	var rec2 struct {
		Abridged *bool  `json:"abridged"`
		CoverURL string `json:"cover_url"`
	}
	readEntity(t, dataDir, "works/un/unknown-abridgement/recordings/a-narrator.json", &rec2)
	if rec2.Abridged != nil {
		t.Errorf("abridged = %v, want absent (unstated)", rec2.Abridged)
	}
	if rec2.CoverURL != "" {
		t.Errorf("cover_url = %q, want empty (non-https dropped)", rec2.CoverURL)
	}
}

func TestParseAudiosiloBooksRejectsForeignEnvelope(t *testing.T) {
	// A bare array (no format marker) is not an audiosilo-books envelope and must
	// fail loud rather than misparse.
	if _, _, err := parseAudiosiloBooks([]byte(`[{"title":"x"}]`)); err == nil {
		t.Error("expected an error for a non-envelope payload")
	}
	// A future version must not be silently accepted.
	if _, _, err := parseAudiosiloBooks([]byte(`{"format":"audiosilo-books","version":2,"books":[]}`)); err == nil {
		t.Error("expected an error for an unsupported version")
	}
}

func TestMapLanguageAcceptsISOCode(t *testing.T) {
	if code, ok := mapLanguage("en"); !ok || code != "en" {
		t.Errorf("mapLanguage(en) = %q,%v; want en,true", code, ok)
	}
	if code, ok := mapLanguage("English"); !ok || code != "en" {
		t.Errorf("mapLanguage(English) = %q,%v; want en,true", code, ok)
	}
	if _, ok := mapLanguage("xx"); ok {
		t.Error("mapLanguage(xx) must be rejected (not an accepted code)")
	}
}

// aiBooksExport is a library whose entries credit an AI in every shape the
// vocabulary knows, beside one book that must still import.
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

// TestAudiosiloBooksRefusesAICredits closes the gap that put four virtual-voice
// works in the catalogue: this envelope is how an Audiobookshelf library
// reaches the intake bot, and it bypassed the AI vocabulary entirely. All four
// VOICE shapes and the generative-SYSTEM tokens are refused, on both credit
// lists, and the one real book still imports.
func TestAudiosiloBooksRefusesAICredits(t *testing.T) {
	sum, dataDir := runAudiosiloBooks(t, aiBooksExport, false)

	if sum.NewWorks != 1 || sum.NewRecordings != 1 {
		t.Errorf("NewWorks/NewRecordings = %d/%d, want 1/1", sum.NewWorks, sum.NewRecordings)
	}
	if sum.SkippedRows != 5 {
		t.Errorf("SkippedRows = %d, want 5", sum.SkippedRows)
	}
	// Solo Author + A Narrator, and nobody else: not one AI credit may become a
	// person record.
	if sum.NewPeople != 2 {
		t.Errorf("NewPeople = %d, want 2", sum.NewPeople)
	}
	for _, slug := range []string{
		"virtual-voice", "ai-voice-nina", "steve-stewarts-voice-replica",
		"santiago", "chatgpt-chatgpt", "chatgpt",
	} {
		if entryExists(t, dataDir, personAddr(slug)) {
			t.Errorf("an AI credit was minted as a person at %q", slug)
		}
	}
	// One aggregated line, in the form every aggregated importer warning takes,
	// naming the books an operator would go and look at.
	if len(sum.Warnings) == 0 || !strings.Contains(sum.Warnings[0], "5 books skipped") {
		t.Fatalf("warnings = %#v, want an aggregated AI-refusal line first", sum.Warnings)
	}
	for _, want := range []string{"audiosilo-books:", "an AI voice", "Virtually Narrated"} {
		if !strings.Contains(sum.Warnings[0], want) {
			t.Errorf("warning %q does not mention %q", sum.Warnings[0], want)
		}
	}
}

// TestAudiosiloBooksKeepsTheNonAIRefusalsLibexOnly pins the deliberate scope of
// the change: an audiosilo-books entry is a USER's own library, so a credit that
// slugs away to nothing keeps today's behaviour (the catch-all conflation, which
// is visible to the user whose book it is) rather than losing the user their
// book. Only the AI vocabulary applies here - see the note in audiosilobooks.go.
func TestAudiosiloBooksKeepsTheNonAIRefusalsLibexOnly(t *testing.T) {
	const export = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {
      "title": "A Korean Book",
      "authors": ["\uae40\uc601\ud558"],
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
