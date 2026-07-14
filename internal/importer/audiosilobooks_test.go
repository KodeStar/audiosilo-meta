package importer

import (
	"path/filepath"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/check"
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
      "abridged": false
    },
    {
      "title": "Unknown Abridgement",
      "authors": ["Solo Author"],
      "narrators": ["A Narrator"],
      "asin": "B0ABS00002",
      "language": "en"
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
	readJSON(t, filepath.Join(dataDir, "works/th/the-way-of-kings/work.json"), &work)
	if work.Title != "The Way of Kings" {
		t.Errorf("work title = %q", work.Title)
	}
	if work.Language != "en" {
		t.Errorf("work language = %q, want en (ISO code passed through)", work.Language)
	}
	if len(work.Sources) != 1 || work.Sources[0].Type != "audiosilo-books-import" || work.Sources[0].Ref != "B0ABS00001" {
		t.Errorf("work sources = %+v", work.Sources)
	}

	// Recording: two narrators, region defaulted to us, abridged emitted.
	var rec struct {
		Narrators []string `json:"narrators"`
		Abridged  *bool    `json:"abridged"`
		ASIN      []struct {
			Region string `json:"region"`
			ASIN   string `json:"asin"`
		} `json:"asin"`
	}
	readJSON(t, filepath.Join(dataDir, "works/th/the-way-of-kings/recordings/kate-reading-2010.json"), &rec)
	if len(rec.Narrators) != 2 {
		t.Errorf("narrators = %v, want 2", rec.Narrators)
	}
	if rec.Abridged == nil || *rec.Abridged != false {
		t.Errorf("abridged = %v, want explicit false", rec.Abridged)
	}
	if len(rec.ASIN) != 1 || rec.ASIN[0].Region != "us" || rec.ASIN[0].ASIN != "B0ABS00001" {
		t.Errorf("asin = %+v, want one us B0ABS00001 (region defaulted)", rec.ASIN)
	}

	// The second book stated no abridged flag: the field must be OMITTED, never
	// fabricated to false (omit-never-guess).
	var rec2 struct {
		Abridged *bool `json:"abridged"`
	}
	recPath := filepath.Join(dataDir, "works/un/unknown-abridgement/recordings/a-narrator.json")
	readJSON(t, recPath, &rec2)
	if rec2.Abridged != nil {
		t.Errorf("abridged = %v, want absent (unstated)", rec2.Abridged)
	}
}

func TestParseAudiosiloBooksRejectsForeignEnvelope(t *testing.T) {
	// A bare array (no format marker) is not an audiosilo-books envelope and must
	// fail loud rather than misparse.
	if _, err := parseAudiosiloBooks([]byte(`[{"title":"x"}]`)); err == nil {
		t.Error("expected an error for a non-envelope payload")
	}
	// A future version must not be silently accepted.
	if _, err := parseAudiosiloBooks([]byte(`{"format":"audiosilo-books","version":2,"books":[]}`)); err == nil {
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
