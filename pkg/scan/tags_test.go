package scan

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dhowden/tag"
)

// The raw-map fixtures below use ONLY shapes dhowden actually emits (verified
// against its source and against real ffmpeg-tagged files):
//   - ID3v2: user frames under raw keys "TXXX"/"TXXX_0"/... as *tag.Comm with
//     the user field name in Description.
//   - MP4: known atoms under their byte-encoded codes ("\xa9grp"); freeform
//     atoms (allow-listed means only) under their bare NAME sub-atom.

func TestFlatRaw(t *testing.T) {
	pairs := flatRaw(map[string]interface{}{
		"TALB":     "Unsouled",
		"TXXX":     &tag.Comm{Description: "ASIN", Text: "B076HYPQLK"},
		"TXXX_0":   &tag.Comm{Description: "SERIES", Text: "Cradle"},
		"\xa9grp":  "Grouped",
		"NARRATOR": "Travis Baldree", // MP4 freeform (com.apple.iTunes mean)
		"empty":    "",
	})
	// Sorted by raw key bytes: the 0xA9-prefixed atom code sorts after ASCII.
	want := []rawPair{
		{"asin", "B076HYPQLK"},
		{"narrator", "Travis Baldree"},
		{"series", "Cradle"},
		{"talb", "Unsouled"},
		{"\xa9grp", "Grouped"},
	}
	if !reflect.DeepEqual(pairs, want) {
		t.Errorf("flatRaw = %v, want %v", pairs, want)
	}
}

func TestASINFromPairs(t *testing.T) {
	tests := []struct {
		desc string
		raw  map[string]interface{}
		want string
	}{
		{"ID3v2 TXXX ASIN frame (the real MP3 shape)",
			map[string]interface{}{"TXXX": &tag.Comm{Description: "ASIN", Text: "B076HYPQLK"}}, "B076HYPQLK"},
		{"MP4 freeform ASIN name (upper-case, case-folded)",
			map[string]interface{}{"ASIN": "B0CGVKN3KL"}, "B0CGVKN3KL"},
		{"exact asin key accepts valid non-B0 ASINs whole",
			map[string]interface{}{"ASIN": "1774248182"}, "1774248182"},
		{"exact asin key normalizes case",
			map[string]interface{}{"ASIN": "b076hypqlk"}, "B076HYPQLK"},
		{"fuzzy key requires the strict B0 shape",
			map[string]interface{}{"AUDIBLE_ASIN": "B08G9PRS1K"}, "B08G9PRS1K"},
		{"fuzzy key rejects a 10-char non-B0 value (epoch seconds must not win)",
			map[string]interface{}{"audible_date": "1583020800"}, ""},
		{"unrelated key ignored even if the value looks like an ASIN",
			map[string]interface{}{"comment": "B076HYPQLK"}, ""},
		{"deterministic: exact asin key beats fuzzy keys regardless of map order",
			map[string]interface{}{
				"AUDIBLE_ASIN": "B000000000",
				"ASIN":         "B076HYPQLK",
			}, "B076HYPQLK"},
		{"no asin anywhere", map[string]interface{}{"title": "A Book"}, ""},
	}
	for _, tt := range tests {
		if got := asinFromPairs(flatRaw(tt.raw)); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.desc, got, tt.want)
		}
	}
}

func TestISBNFromPairs(t *testing.T) {
	tests := []struct {
		raw  map[string]interface{}
		want string
	}{
		{map[string]interface{}{"ISBN": "978-0-399-59050-4"}, "9780399590504"},
		{map[string]interface{}{"isbn13": "9781401238964"}, "9781401238964"},
		{map[string]interface{}{"comment": "9780399590504"}, ""}, // key not isbn-ish
		{map[string]interface{}{"title": "x"}, ""},
	}
	for _, tt := range tests {
		if got := isbnFromPairs(flatRaw(tt.raw)); got != tt.want {
			t.Errorf("isbnFromPairs(%v) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestNormalizeReleaseDate(t *testing.T) {
	tests := []struct{ in, want string }{
		{"2017", "2017"},
		{"2017-11-02", "2017-11-02"},
		{"2017-11-02T09:00:00Z", "2017-11-02"}, // ISO timestamp truncated
		{"0000-00-00", ""},                     // junk year rejected
		{"3000", ""},                           // out of sane range
		{"1399", ""},
		{"20171102", ""}, // not a supported shape
		{"next year", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizeReleaseDate(tt.in); got != tt.want {
			t.Errorf("normalizeReleaseDate(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitPeople(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"Lee Child", []string{"Lee Child"}},
		{"Terry Pratchett & Neil Gaiman", []string{"Terry Pratchett", "Neil Gaiman"}},
		{"Author One, Author Two", []string{"Author One", "Author Two"}},
		{"A; B; C", []string{"A", "B", "C"}},
		{"Jane Doe and John Roe", []string{"Jane Doe", "John Roe"}},
		{"  Spaced  ", []string{"Spaced"}},
		// Audible role qualifiers are stripped via importer.SplitNames.
		{"Kirill Klevanski, J. Kharkova - Translator", []string{"Kirill Klevanski", "J. Kharkova"}},
		{"Author One & Jane Doe - narrator", []string{"Author One", "Jane Doe"}},
	}
	for _, tt := range tests {
		if got := splitPeople(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitPeople(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestMergeTags(t *testing.T) {
	d := tagInfo{
		album:       "Dhowden Album",
		authors:     []string{"Dhowden Author"},
		asin:        "B076HYPQLK",
		releaseDate: "2017",
	}
	p := tagInfo{
		album:       "Probe Album",
		narrators:   []string{"Probe Narrator"},
		publisher:   "Probe Publisher",
		asin:        "B000000000",
		releaseDate: "2017-11-02",
	}
	got := mergeTags(d, p)
	if got.album != "Dhowden Album" {
		t.Errorf("album: dhowden must win, got %q", got.album)
	}
	if len(got.authors) != 1 || got.authors[0] != "Dhowden Author" {
		t.Errorf("authors: dhowden must win, got %v", got.authors)
	}
	if len(got.narrators) != 1 || got.narrators[0] != "Probe Narrator" {
		t.Errorf("narrators: probe must fill the gap, got %v", got.narrators)
	}
	if got.publisher != "Probe Publisher" {
		t.Errorf("publisher: probe must fill the gap, got %q", got.publisher)
	}
	if got.releaseDate != "2017-11-02" {
		t.Errorf("releaseDate: the fuller date must win, got %q", got.releaseDate)
	}
	// Ties keep the dhowden value (documented tie-break).
	if got := mergeTags(tagInfo{releaseDate: "2017"}, tagInfo{releaseDate: "2018"}); got.releaseDate != "2017" {
		t.Errorf("releaseDate tie: dhowden must win, got %q", got.releaseDate)
	}
	// ASIN is deliberately NOT merged - the probe copy rides separately at the
	// lowest precedence (see mergeTags doc).
	if got.asin != "B076HYPQLK" {
		t.Errorf("asin: dhowden copy expected, got %q", got.asin)
	}
	if got := mergeTags(tagInfo{}, p); got.asin != "" {
		t.Errorf("asin must not be merged from probe tags, got %q", got.asin)
	}
}

func TestProbeTagInfo(t *testing.T) {
	got := probeTagInfo(map[string]string{
		"album":     " The Book ",
		"artist":    "Some Author",
		"series":    "The Series",
		"asin":      "B076HYPQLK",
		"publisher": "Pub House",
		"date":      "2020-01-05",
		"language":  "eng",
	})
	if got.album != "The Book" || got.authors[0] != "Some Author" || got.series != "The Series" {
		t.Errorf("basic mapping wrong: %+v", got)
	}
	if got.asin != "B076HYPQLK" || got.publisher != "Pub House" || got.releaseDate != "2020-01-05" || got.language != "eng" {
		t.Errorf("identifier/extra mapping wrong: %+v", got)
	}
	if empty := probeTagInfo(nil); !reflect.DeepEqual(empty, tagInfo{}) {
		t.Errorf("nil map must yield a zero tagInfo, got %+v", empty)
	}
}

// TestReadTagsRealFiles pins what dhowden ACTUALLY emits for real tagged files
// (ffmpeg-generated; skips without ffmpeg): ID3v2 TXXX frames arrive as
// *tag.Comm and the extraction must read ASIN/series/series-part/narrator out
// of their Descriptions; MP4 grouping arrives under the byte-encoded "\xa9grp".
func TestReadTagsRealFiles(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()

	mp3 := filepath.Join(dir, "tagged.mp3")
	cmd := exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", "1", "-c:a", "libmp3lame",
		"-metadata", "album=Unsouled",
		"-metadata", "artist=Will Wight",
		"-metadata", "composer=Travis Baldree",
		"-metadata", "ASIN=B076HYPQLK",
		"-metadata", "SERIES=Cradle",
		"-metadata", "SERIES-PART=1",
		mp3)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate the mp3 fixture: %v\n%s", err, out)
	}

	got, hasTags, failed := readTags(mp3)
	if !hasTags || failed {
		t.Fatalf("readTags(mp3): hasTags=%v failed=%v", hasTags, failed)
	}
	if got.album != "Unsouled" || got.asin != "B076HYPQLK" || got.series != "Cradle" || got.position != "1" {
		t.Errorf("mp3 TXXX extraction wrong: %+v", got)
	}
	if len(got.authors) != 1 || got.authors[0] != "Will Wight" {
		t.Errorf("mp3 authors wrong: %v", got.authors)
	}
	if len(got.narrators) != 1 || got.narrators[0] != "Travis Baldree" {
		t.Errorf("mp3 narrator (composer) wrong: %v", got.narrators)
	}

	m4a := filepath.Join(dir, "tagged.m4a")
	cmd = exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", "1", "-c:a", "aac",
		"-metadata", "album=Unsouled",
		"-metadata", "grouping=Cradle",
		m4a)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate the m4a fixture: %v\n%s", err, out)
	}
	got, hasTags, failed = readTags(m4a)
	if !hasTags || failed {
		t.Fatalf("readTags(m4a): hasTags=%v failed=%v", hasTags, failed)
	}
	if got.album != "Unsouled" || got.series != "Cradle" {
		t.Errorf("m4a \\xa9grp extraction wrong: %+v", got)
	}

	// An untagged-but-valid file is NOT a failure; a truncated/empty file is.
	plain := filepath.Join(dir, "plain.mp3")
	cmd = exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", "1", "-c:a", "libmp3lame", "-map_metadata", "-1", "-write_id3v2", "0", "-id3v2_version", "0", plain)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate the plain fixture: %v\n%s", err, out)
	}
	if _, _, failed := readTags(plain); failed {
		t.Errorf("an untagged mp3 must not count as a tag-read failure")
	}
}
