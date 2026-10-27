package importer

import (
	"encoding/json"
	"testing"
)

// The L2 facts a LibriVox import keeps beside the L1 identity: the recording's
// copyright_year and links.text, and an author's or translator's life years.

type lvFacts struct {
	CopyrightYear int `json:"copyright_year"`
	Links         *struct {
		Text string `json:"text"`
	} `json:"links"`
}

type lvLife struct {
	BirthYear *int `json:"birth_year"`
	DeathYear *int `json:"death_year"`
}

func TestLibrivoxImportKeepsYearsAndTextSource(t *testing.T) {
	dataDir := t.TempDir()
	runLibrivox(t, librivoxFixture, dataDir, allLibrivox)

	var r lvFacts
	readEntity(t, dataDir, recAddr("metamorphosis", "david-barnes"), &r)
	if r.CopyrightYear != 1915 || r.Links == nil || r.Links.Text != "https://www.kafka.org/index.php?id=162,170,0,0,1,0" {
		t.Errorf("527 copyright_year/text = %d %+v", r.CopyrightYear, r.Links)
	}
	// 296 states copyright_year "0" (LibriVox's "not stated") and an empty
	// url_text_source: neither is written.
	recs := recSlugsOf(t, dataDir, "short-story-collection-vol-001")
	r = lvFacts{}
	readEntity(t, dataDir, recAddr("short-story-collection-vol-001", recs[0]), &r)
	if r.CopyrightYear != 0 || (r.Links != nil && r.Links.Text != "") {
		t.Errorf("296 copyright_year/text = %d %+v, want neither", r.CopyrightYear, r.Links)
	}

	year := func(n int) *int { return &n }
	for addr, want := range map[string]lvLife{
		personAddr("franz-kafka"):  {year(1883), year(1924)},
		personAddr("voltaire"):     {year(1694), year(1778)},
		personAddr("ian-johnston"): {year(1938), nil}, // dod "alive" is not a year
		personAddr("various"):      {nil, nil},        // dob and dod ""
		personAddr("david-barnes"): {nil, nil},        // a reader states none
	} {
		var p lvLife
		readEntity(t, dataDir, addr, &p)
		if !sameYear(p.BirthYear, want.BirthYear) || !sameYear(p.DeathYear, want.DeathYear) {
			t.Errorf("%s life = %v/%v, want %v/%v", addr, deref(p.BirthYear), deref(p.DeathYear), deref(want.BirthYear), deref(want.DeathYear))
		}
	}
}

func sameYear(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// TestLibrivoxLifeYearsNeverOverwrite: a person who already states a year keeps
// it, and gets neither LibriVox year (half a pair could invert the two).
func TestLibrivoxLifeYearsNeverOverwrite(t *testing.T) {
	raw := map[string]any{"birth_year": 1880}
	stampLifeYears(raw, lifeYears{birth: 1883, death: 1924})
	if raw["birth_year"] != 1880 || raw["death_year"] != nil {
		t.Errorf("stamp over a stated birth_year = %v", raw)
	}
	raw = map[string]any{}
	stampLifeYears(raw, lifeYears{birth: 1883, death: 1924})
	if raw["birth_year"] != 1883 || raw["death_year"] != 1924 {
		t.Errorf("stamp onto an unstated record = %v", raw)
	}
}

func TestLibrivoxYear(t *testing.T) {
	for in, want := range map[string]int{
		"1883":     1883,
		" 1915 ":   1915,
		"-428":     -428,
		"428 BC":   -428,
		"428 BCE":  -428,
		"428 b.c.": -428,
		"800":      800,
	} {
		if got, ok := librivoxYear(in); !ok || got != want {
			t.Errorf("librivoxYear(%q) = %d %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "0", "alive", "c. 1500", "1500?", "12345", "-428 BC", "1883-1924", "unknown"} {
		if got, ok := librivoxYear(in); ok {
			t.Errorf("librivoxYear(%q) = %d, want not a year", in, got)
		}
	}
}

// TestLibrivoxYearFieldsDecodeEitherType: the feed states some numbers bare and
// some quoted (lvInt's reason), so a year field arriving as a JSON number - or
// null - reads as text rather than failing the decode of its whole page.
func TestLibrivoxYearFieldsDecodeEitherType(t *testing.T) {
	var pr LibrivoxProject
	raw := `{"id":"527","copyright_year":1915,"authors":[{"id":"100","dob":1883,"dod":null}]}`
	if err := json.Unmarshal([]byte(raw), &pr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pr.CopyrightYear != "1915" || len(pr.Authors) != 1 || pr.Authors[0].DOB != "1883" || pr.Authors[0].DOD != "" {
		t.Errorf("decoded = %q %+v", pr.CopyrightYear, pr.Authors)
	}
	if got := lifeYearsOf(pr.Authors[0]); got != (lifeYears{birth: 1883}) {
		t.Errorf("life = %+v", got)
	}
}

func TestLifeYearsOfDropsAnInvertedPair(t *testing.T) {
	if got := lifeYearsOf(LibrivoxPerson{DOB: "1924", DOD: "1883"}); got != (lifeYears{}) {
		t.Errorf("inverted pair = %+v, want neither", got)
	}
	if got := lifeYearsOf(LibrivoxPerson{DOB: "-428", DOD: "-348"}); got != (lifeYears{birth: -428, death: -348}) {
		t.Errorf("BC pair = %+v", got)
	}
}
