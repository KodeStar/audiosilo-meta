package scan

import (
	"reflect"
	"testing"
)

func TestParseName(t *testing.T) {
	tests := []struct {
		name              string
		wantSeries, wantP string
		wantTitle         string
		wantConfident     bool
	}{
		{"01 - Killing Floor", "", "1", "Killing Floor", true},
		{"03. Tripwire", "", "3", "Tripwire", true},
		{"2.5 - The Novella", "", "2.5", "The Novella", true},
		{"Book 3 - The Well of Ascension", "", "3", "The Well of Ascension", true},
		{"Volume 2: Soulsmith", "", "2", "Soulsmith", true},
		{"#4 The Reckoning", "", "4", "The Reckoning", true},
		{"The Final Empire, Book 1", "", "1", "The Final Empire", true},
		{"Ship of Magic - Book 1", "", "1", "Ship of Magic", true},
		// Zero-padded position: intentional naming, confident.
		{"Jack Reacher 03 - Tripwire", "Jack Reacher", "3", "Tripwire", true},
		// Unpadded seriesNum reading: plausible but accidental-shape-prone -
		// returned as a claim needing corroboration.
		{"The Stormlight Archive 2 - Words of Radiance", "The Stormlight Archive", "2", "Words of Radiance", false},
		{"Fahrenheit 451 - Ray Bradbury", "Fahrenheit", "451", "Ray Bradbury", false},
		{"Catch 22 - HarperAudio", "Catch", "22", "HarperAudio", false},
		// No position -> whole string is the title, no series.
		{"Good Omens", "", "", "Good Omens", true},
		{"1984", "", "", "1984", true},                                     // 4-digit year guard
		{"2001 - A Space Odyssey", "", "", "2001 - A Space Odyssey", true}, // 4-digit bare number rejected
		{"", "", "", "", true},
	}
	for _, tt := range tests {
		gotS, gotP, gotT, gotC := parseName(tt.name)
		if gotS != tt.wantSeries || gotP != tt.wantP || gotT != tt.wantTitle || gotC != tt.wantConfident {
			t.Errorf("parseName(%q) = (series=%q pos=%q title=%q confident=%v), want (series=%q pos=%q title=%q confident=%v)",
				tt.name, gotS, gotP, gotT, gotC, tt.wantSeries, tt.wantP, tt.wantTitle, tt.wantConfident)
		}
	}
}

func TestNormalizePosition(t *testing.T) {
	tests := []struct {
		raw      string
		explicit bool
		want     string
	}{
		{"03", false, "3"},
		{"1", false, "1"},
		{"2.50", false, "2.5"},
		{"0", false, "0"},
		{"1984", false, ""},    // 4-digit bare number rejected
		{"1984", true, "1984"}, // explicit volume marker accepts it
		{"2015.5", false, ""},  // year guard applies to the INTEGER part
		{"", false, ""},
		{"abc", false, ""},
		{"-1", false, ""},
		// Omnibus ranges (importer.NormalizeSequence acceptance; tag-carried).
		{"1-3.5", true, "1-3.5"},
		{"01-03", true, "1-3"}, // zero-collapse applies per component
		{"1-", true, ""},       // malformed range rejected
	}
	for _, tt := range tests {
		if got := normalizePosition(tt.raw, tt.explicit); got != tt.want {
			t.Errorf("normalizePosition(%q, %v) = %q, want %q", tt.raw, tt.explicit, got, tt.want)
		}
	}
}

func TestDerivePath(t *testing.T) {
	tests := []struct {
		desc      string
		name      string
		nameSrc   source
		ancestors []string
		want      derived
	}{
		{
			desc:      "Author/Series/NN - Title",
			name:      "01 - Killing Floor",
			nameSrc:   "path",
			ancestors: []string{"Lee Child", "Jack Reacher"},
			want: derived{
				title: "Killing Floor", titleSrc: "path",
				author: "Lee Child", authorSrc: "path",
				series: "Jack Reacher", seriesSrc: "path",
				position: "1", posSrc: "path",
			},
		},
		{
			desc:      "Author/Book (one ancestor, no position) -> author only",
			name:      "Good Omens",
			nameSrc:   "path",
			ancestors: []string{"Neil Gaiman"},
			want: derived{
				title: "Good Omens", titleSrc: "path",
				author: "Neil Gaiman", authorSrc: "path",
			},
		},
		{
			desc:      "Series/NN - Title (one ancestor, name has a position) -> series",
			name:      "03 - Tripwire",
			nameSrc:   "path",
			ancestors: []string{"Jack Reacher"},
			want: derived{
				title: "Tripwire", titleSrc: "path",
				series: "Jack Reacher", seriesSrc: "path",
				position: "3", posSrc: "path",
			},
		},
		{
			desc:      "2+ ancestors WITHOUT position evidence -> nearest ancestor is the author, no series guess",
			name:      "Good Omens",
			nameSrc:   "path",
			ancestors: []string{"Audiobooks", "Neil Gaiman"},
			want: derived{
				title: "Good Omens", titleSrc: "path",
				author: "Neil Gaiman", authorSrc: "path",
			},
		},
		{
			desc:      "series embedded in the name itself (zero-padded), no ancestors",
			name:      "Jack Reacher 03 - Tripwire",
			nameSrc:   "filename",
			ancestors: nil,
			want: derived{
				title: "Tripwire", titleSrc: "filename",
				series: "Jack Reacher", seriesSrc: "filename",
				position: "3", posSrc: "filename",
			},
		},
		{
			desc:      "unpadded seriesNum shape -> safe parse + pending claim (Fahrenheit guard)",
			name:      "Fahrenheit 451 - Ray Bradbury",
			nameSrc:   "filename",
			ancestors: nil,
			want: derived{
				title: "Fahrenheit 451 - Ray Bradbury", titleSrc: "filename",
				pending: &nameClaim{series: "Fahrenheit", position: "451", title: "Ray Bradbury", src: "filename"},
			},
		},
		{
			desc:      "loose file, no ancestors, no position",
			name:      "Some Standalone",
			nameSrc:   "filename",
			ancestors: nil,
			want: derived{
				title: "Some Standalone", titleSrc: "filename",
			},
		},
	}
	for _, tt := range tests {
		got := derivePath(tt.name, tt.nameSrc, tt.ancestors)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: derivePath(%q, %q, %v)\n got  %+v (pending %+v)\n want %+v (pending %+v)",
				tt.desc, tt.name, tt.nameSrc, tt.ancestors, got, got.pending, tt.want, tt.want.pending)
		}
	}
}

func TestFindASIN(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Killing Floor [B076HYPQLK].m4b", "B076HYPQLK"},
		{"B0CGVKN3KL", "B0CGVKN3KL"},
		{"asin=B08G9PRS1K end", "B08G9PRS1K"},
		{"no asin here", ""},
		{"B076HYPQ", ""},    // too short
		{"XB076HYPQLK", ""}, // no word boundary
		{"B176HYPQLK", ""},  // must start B0
		{"b076hypqlk", ""},  // lowercase junk is NOT an ASIN (case guard)
	}
	for _, tt := range tests {
		if got := findASIN(tt.in); got != tt.want {
			t.Errorf("findASIN(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFindISBN(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"978-0-399-59050-4", "9780399590504"},
		{"isbn 9781401238964", "9781401238964"},
		{"9770399590504", ""}, // 977 prefix is not an ISBN-13
		{"9781401238965", ""}, // bad check digit rejected
		{"nothing", ""},
		// First candidate fails the check digit, the next valid one wins.
		{"9781401238965 then 9781401238964", "9781401238964"},
	}
	for _, tt := range tests {
		if got := findISBN(tt.in); got != tt.want {
			t.Errorf("findISBN(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
