package scan

import "testing"

func TestSplitVerdict(t *testing.T) {
	tests := []struct {
		desc  string
		files []string
		tags  []tagInfo
		want  verdict
	}{
		{
			desc:  "distinct albums split (the Cradle case)",
			files: []string{"01 - Unsouled.m4b", "02 - Soulsmith.m4b"},
			tags: []tagInfo{
				{album: "Unsouled"},
				{album: "Soulsmith"},
			},
			want: verdictSplit,
		},
		{
			desc:  "distinct albums split regardless of case/whitespace",
			files: []string{"a.mp3", "b.mp3"},
			tags: []tagInfo{
				{album: " Unsouled "},
				{album: "SOULSMITH"},
			},
			want: verdictSplit,
		},
		{
			desc:  "shared album keeps (chapter parts of one book)",
			files: []string{"01 - Chapter 01.mp3", "02 - Chapter 02.mp3"},
			tags: []tagInfo{
				{album: "Unsouled", trackTitle: "Chapter 01"},
				{album: "Unsouled", trackTitle: "Chapter 02"},
			},
			want: verdictKeep,
		},
		{
			desc:  "shared album normalized: case difference is still the same album",
			files: []string{"part1.mp3", "part2.mp3"},
			tags: []tagInfo{
				{album: "unsouled"},
				{album: "Unsouled"},
			},
			want: verdictKeep,
		},
		{
			desc:  "no tags at all keeps, flagged ambiguous",
			files: []string{"part1.mp3", "part2.mp3"},
			tags:  []tagInfo{{}, {}},
			want:  verdictKeepAmbiguous,
		},
		{
			desc:  "partial albums keep, flagged ambiguous (never split on partial evidence)",
			files: []string{"01 - A.mp3", "02 - B.mp3"},
			tags: []tagInfo{
				{album: "Book A"},
				{},
			},
			want: verdictKeepAmbiguous,
		},
		{
			desc:  "mixed albums (some shared, some distinct) keep, flagged ambiguous",
			files: []string{"a.mp3", "b.mp3", "c.mp3"},
			tags: []tagInfo{
				{album: "X"},
				{album: "X"},
				{album: "Y"},
			},
			want: verdictKeepAmbiguous,
		},
		{
			desc:  "distinct titles matching their filenames split (no albums)",
			files: []string{"01 - Unsouled.m4b", "02 - Soulsmith.m4b"},
			tags: []tagInfo{
				{trackTitle: "Unsouled"},
				{trackTitle: "Soulsmith"},
			},
			want: verdictSplit,
		},
		{
			desc:  "generic chapter titles never split even when they match filenames",
			files: []string{"Chapter 07.mp3", "Chapter 08.mp3"},
			tags: []tagInfo{
				{trackTitle: "Chapter 07"},
				{trackTitle: "Chapter 08"},
			},
			want: verdictKeepAmbiguous,
		},
		{
			desc:  "distinct titles NOT matching their filenames keep (tag/name disagree)",
			files: []string{"part1.mp3", "part2.mp3"},
			tags: []tagInfo{
				{trackTitle: "Unsouled"},
				{trackTitle: "Soulsmith"},
			},
			want: verdictKeepAmbiguous,
		},
		{
			desc:  "identical titles keep (one book, title repeated per part)",
			files: []string{"Unsouled.mp3", "Unsouled (1).mp3"},
			tags: []tagInfo{
				{trackTitle: "Unsouled"},
				{trackTitle: "Unsouled"},
			},
			want: verdictKeepAmbiguous,
		},
		{
			desc:  "single file has nothing to split",
			files: []string{"book.m4b"},
			tags:  []tagInfo{{album: "Anything"}},
			want:  verdictKeep,
		},
	}
	for _, tt := range tests {
		if got := splitVerdict(tt.files, tt.tags); got != tt.want {
			t.Errorf("%s: splitVerdict(%v) = %v, want %v", tt.desc, tt.files, got, tt.want)
		}
	}
}

func TestIsGenericTitle(t *testing.T) {
	generic := []string{"Chapter 07", "Track 01", "Part 2", "Disc 1", "CD1", "07", "chapter-3", ""}
	real := []string{"Unsouled", "Part of Your World", "The Final Empire", "Book One"}
	for _, s := range generic {
		if !isGenericTitle(s) {
			t.Errorf("isGenericTitle(%q) = false, want true", s)
		}
	}
	for _, s := range real {
		if isGenericTitle(s) {
			t.Errorf("isGenericTitle(%q) = true, want false", s)
		}
	}
}
