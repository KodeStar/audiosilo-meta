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
	}{
		{"01 - Killing Floor", "", "1", "Killing Floor"},
		{"03. Tripwire", "", "3", "Tripwire"},
		{"2.5 - The Novella", "", "2.5", "The Novella"},
		{"Book 3 - The Well of Ascension", "", "3", "The Well of Ascension"},
		{"Volume 2: Soulsmith", "", "2", "Soulsmith"},
		{"#4 The Reckoning", "", "4", "The Reckoning"},
		{"The Final Empire, Book 1", "", "1", "The Final Empire"},
		{"Ship of Magic - Book 1", "", "1", "Ship of Magic"},
		{"Jack Reacher 03 - Tripwire", "Jack Reacher", "3", "Tripwire"},
		{"The Stormlight Archive 2 - Words of Radiance", "The Stormlight Archive", "2", "Words of Radiance"},
		// No position -> whole string is the title, no series.
		{"Good Omens", "", "", "Good Omens"},
		{"1984", "", "", "1984"},                                     // 4-digit year guard
		{"2001 - A Space Odyssey", "", "", "2001 - A Space Odyssey"}, // 4-digit bare number rejected
		{"", "", "", ""},
	}
	for _, tt := range tests {
		gotS, gotP, gotT := parseName(tt.name)
		if gotS != tt.wantSeries || gotP != tt.wantP || gotT != tt.wantTitle {
			t.Errorf("parseName(%q) = (series=%q pos=%q title=%q), want (series=%q pos=%q title=%q)",
				tt.name, gotS, gotP, gotT, tt.wantSeries, tt.wantP, tt.wantTitle)
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
			desc:      "series embedded in the name itself, no ancestors",
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
			t.Errorf("%s: derivePath(%q, %q, %v)\n got  %+v\n want %+v", tt.desc, tt.name, tt.nameSrc, tt.ancestors, got, tt.want)
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
		{"nothing", ""},
	}
	for _, tt := range tests {
		if got := findISBN(tt.in); got != tt.want {
			t.Errorf("findISBN(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
