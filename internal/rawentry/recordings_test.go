package rawentry

import (
	"strings"
	"testing"
)

func mustObj(t *testing.T, s string) Obj {
	t.Helper()
	o, err := Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// A merged recording keeps every LibriVox fact either side states: the keeper's
// win, the mover fills what the keeper does not state, and a value both state
// differently is reported. A chapter's audio_url travels with the list kept.
func TestMergeRecordingsKeepsTheLibrivoxFacts(t *testing.T) {
	keep := mustObj(t, `{"id":"r","work":"w","narrators":["n"],"librivox_id":527,"sources":[{"type":"librivox","ref":"527"}]}`)
	mover := mustObj(t, `{"id":"r","work":"w","narrators":["n"],"librivox_id":527,"archive_id":"metamorphosis_librivox",`+
		`"links":{"librivox":"https://librivox.org/the-metamorphosis/"},"copyright_year":1915,`+
		`"chapters":[{"title":"Part 1","start_ms":0,"audio_url":"https://archive.org/download/metamorphosis_librivox/1.mp3"}],`+
		`"sources":[{"type":"librivox","ref":"527"}]}`)
	out, lost := MergeRecordings(keep, mover)
	if len(lost) != 0 {
		t.Errorf("lost = %+v, want nothing chosen away", lost)
	}
	for field, want := range map[string]string{
		"librivox_id":    `527`,
		"archive_id":     `"metamorphosis_librivox"`,
		"links":          `{"librivox":"https://librivox.org/the-metamorphosis/"}`,
		"copyright_year": `1915`,
	} {
		if got := string(out[field]); got != want {
			t.Errorf("%s = %s, want %s", field, got, want)
		}
	}
	if !strings.Contains(string(out["chapters"]), `"audio_url":"https://archive.org/download/metamorphosis_librivox/1.mp3"`) {
		t.Errorf("chapters = %s, want the mover's list with its audio_url", out["chapters"])
	}

	// Both state a value differently: the keeper's stays and the mover's is reported.
	keep = mustObj(t, `{"id":"r","work":"w","narrators":["n"],"librivox_id":527,"archive_id":"a_librivox","copyright_year":1915}`)
	mover = mustObj(t, `{"id":"r","work":"w","narrators":["n"],"librivox_id":527,"archive_id":"b_librivox","copyright_year":1916}`)
	out, lost = MergeRecordings(keep, mover)
	if string(out["archive_id"]) != `"a_librivox"` || string(out["copyright_year"]) != `1915` {
		t.Errorf("merged = %s / %s, want the keeper's", out["archive_id"], out["copyright_year"])
	}
	fields := map[string]bool{}
	for _, l := range lost {
		fields[l.Field] = true
	}
	if !fields["archive_id"] || !fields["copyright_year"] {
		t.Errorf("lost = %+v, want archive_id and copyright_year reported", lost)
	}
}

// Only the same LibriVox project is the same production.
func TestSameProductionReadsTheLibrivoxProject(t *testing.T) {
	rec := func(id string) Obj {
		s := `{"narrators":["n"],"runtime_min":600`
		if id != "" {
			s += `,"librivox_id":` + id
		}
		return mustObj(t, s+`}`)
	}
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"527", "527", true},
		{"527", "801", false},
		{"527", "", false},
		{"", "801", false},
		{"", "", true},
	} {
		if why, same := SameProduction(rec(tc.a), rec(tc.b)); same != tc.same {
			t.Errorf("SameProduction(%q, %q) = %v (%s), want %v", tc.a, tc.b, same, why, tc.same)
		}
	}
}

// Links merge per key: a store page on each side keeps both, and a key both
// state differently keeps the keeper's and reports the mover's.
func TestMergeRecordingsMergesLinksPerKey(t *testing.T) {
	keep := mustObj(t, `{"id":"r","work":"w","narrators":["n"],"links":{"audiobookguild":"https://audiobookguild.com/products/a"}}`)
	mover := mustObj(t, `{"id":"r","work":"w","narrators":["n"],"links":{"graphicaudio":"https://www.graphicaudio.net/a.html"}}`)
	out, lost := MergeRecordings(keep, mover)
	if len(lost) != 0 {
		t.Errorf("lost = %+v, want nothing chosen away", lost)
	}
	if got, want := string(out["links"]), `{"audiobookguild":"https://audiobookguild.com/products/a","graphicaudio":"https://www.graphicaudio.net/a.html"}`; got != want {
		t.Errorf("links = %s, want %s", got, want)
	}

	mover = mustObj(t, `{"id":"r","work":"w","narrators":["n"],"links":{"audiobookguild":"https://audiobookguild.com/products/b"}}`)
	out, lost = MergeRecordings(keep, mover)
	if string(out["links"]) != `{"audiobookguild":"https://audiobookguild.com/products/a"}` {
		t.Errorf("links = %s, want the keeper's", out["links"])
	}
	if len(lost) != 1 || lost[0].Field != "links.audiobookguild" || lost[0].Dropped != "https://audiobookguild.com/products/b" {
		t.Errorf("lost = %+v, want links.audiobookguild reported", lost)
	}
}
