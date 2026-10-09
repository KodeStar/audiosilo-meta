package query

import (
	"slices"
	"strings"
	"testing"
)

// TestCutNumbering pins the numbering and fluff a title sheds, and the number
// the numbering carried: the shapes a real library's folder names and tags
// carry, measured on the 463-book library the endpoint was built for.
func TestCutNumbering(t *testing.T) {
	for in, want := range map[string][2]string{
		"Sharpe - 08 - Sharpe's Eagle":                        {"Sharpe's Eagle", "08"},
		"SW06 - An Alliance Reformed":                         {"An Alliance Reformed", "06"},
		"BAE06 - Transcendence":                               {"Transcendence", "06"},
		"DD12 - Tales from a Not-So-Secret Crush Catastrophe": {"Tales from a Not-So-Secret Crush Catastrophe", "12"},
		"02. The Magic Faraway Tree":                          {"The Magic Faraway Tree", "02"},
		"02 - Sharpe's Triumph":                               {"Sharpe's Triumph", "02"},
		"01 Unsouled":                                         {"Unsouled", "01"},
		"[Outlaw  08] - The Death of Robin Hood":              {"The Death of Robin Hood", ""},
		"1984 (Unabridged)":                                   {"1984", ""},
		"A Broken Alliance: Sentenced to War, Book 5":         {"A Broken Alliance", ""},
		"Forge of Destiny - Volume 1":                         {"Forge of Destiny", ""},
		"HALO 25 - Renegades (MP3)":                           {"Renegades", "25"},
		"DF15.5 Brief Cases":                                  {"Brief Cases", "15.5"},
		"Book 3: The Hero of Ages":                            {"The Hero of Ages", "3"},
		"[Outlaw 08] - 08 - Title (Unabridged)":               {"Title", "08"},
	} {
		rest, number := cutNumbering(in)
		if rest != want[0] || number != want[1] {
			t.Errorf("cutNumbering(%q) = %q, %q; want %q, %q", in, rest, number, want[0], want[1])
		}
	}
	// Titles that only LOOK numbered are left whole: a year, a title that is a
	// number, a hyphenated word, an initialism.
	for _, in := range []string{"1984", "2001 A Space Odyssey", "Catch-22", "R2-D2 Rides Again", "Dune", "12", "Area 51: The Revelation", "Apollo 13: The Untold Story"} {
		if rest, number := cutNumbering(in); rest != in || number != "" {
			t.Errorf("cutNumbering(%q) = %q, %q; want it untouched", in, rest, number)
		}
	}
}

// TestTitleVariants: a guess is compared as given, without its numbering, and
// as titlerule.CleanTitle reads it beside the request's series.
func TestTitleVariants(t *testing.T) {
	got, number := titleVariants("SW05 - Sentenced to War: A Broken Alliance (Unabridged)", "Sentenced to War")
	want := []string{"SW05 - Sentenced to War: A Broken Alliance (Unabridged)", "Sentenced to War: A Broken Alliance", "A Broken Alliance"}
	if !slices.Equal(got, want) || number != "05" {
		t.Errorf("titleVariants = %q, %q; want %q, 05", got, number, want)
	}
	got, number = titleVariants("Vorkosigan Saga 03 - Barrayar", "Vorkosigan Saga")
	if want := []string{"Vorkosigan Saga 03 - Barrayar", "Barrayar"}; !slices.Equal(got, want) || number != "03" {
		t.Errorf("titleVariants = %q, %q; want %q, 03", got, number, want)
	}
	if got, number := titleVariants("Dune", ""); len(got) != 1 || number != "" {
		t.Errorf("titleVariants(Dune) = %q, %q; want it alone", got, number)
	}
}

func TestInformativeTitle(t *testing.T) {
	for in, want := range map[string]bool{
		"Dune": true, "1984": true, "Sharpe's Eagle": true, "It": true,
		"12": false, "CD1": false, "CD 1": false, "Track 01": false, "Book 3": false, "the": false, "": false, "a 1": false,
	} {
		if got := informativeTitle(matchTerms(in)); got != want {
			t.Errorf("informativeTitle(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestTitleSimilarity covers the bands the ranking depends on, with the
// examples the endpoint exists for.
func TestTitleSimilarity(t *testing.T) {
	sim := func(a, b string) float64 {
		x, y := newTermForm(matchTerms(a)), newTermForm(matchTerms(b))
		return titleSimilarity(&x, &y, nil)
	}
	for _, tc := range []struct {
		hyp, cand string
		min, max  float64
	}{
		// Folding: typographic apostrophe, possessive, diacritics, case.
		{"Sharpe's Eagle", "Sharpe’s Eagle", 1, 1},
		{"sharpes eagle", "Sharpe's Eagle", 1, 1},
		{"Rachel Renee Russell", "Rachel Renée Russell", 1, 1},
		// A typo in a folder name.
		{"An Alliance Reformed", "An Alliance Reforged", 0.9, 0.99},
		// A short stored title inside a long one.
		{"Tales from a Not-So-Secret Crush Catastrophe", "Crush Catastrophe", containedSimilarity, containedSimilarity},
		// One shared word is not containment.
		{"Dune Messiah", "Dune", 0, 0.7},
		// An article.
		{"Bonehunters", "The Bonehunters", articleSimilarity, articleSimilarity},
		// A volume number the candidate does not carry.
		{"Dork Diaries 12", "Dork Diaries", 0, numberConflictCap},
		{"Dork Diaries 12", "Dork Diaries 1", 0, numberConflictCap},
		// ...while a stored title carrying its own series tail is no conflict.
		{"Puppy Love", "Puppy Love: Dork Diaries, Book 10", containedSimilarity, 1},
		// Siblings sharing the naming pattern stay below the trust band.
		{"Sharpe's Siege", "Sharpe's Revenge", seriesDistrustLow, seriesDistrustHigh},
		{"Tales from a Not-So-Happy Heartbreaker", "Tales from a Not-So-Graceful Ice Princess", seriesDistrustLow, seriesDistrustHigh},
		{"Inferno", "Ember", 0, 0.3},
	} {
		got := sim(tc.hyp, tc.cand)
		if got < tc.min || got > tc.max {
			t.Errorf("titleSimilarity(%q, %q) = %.3f, want [%.2f, %.2f]", tc.hyp, tc.cand, got, tc.min, tc.max)
		}
	}
}

// TestCandidateTitleForms: a stored title is compared without its series tail
// and, discounted, by either side of a colon.
func TestCandidateTitleForms(t *testing.T) {
	best := func(hyp, title string, subtitle ...string) float64 {
		h := newTermForm(matchTerms(hyp))
		return bestTitle(&h, candidateTitleForms(title, matchTerms(title), strings.Join(subtitle, "")), nil)
	}
	if got := best("Hunted", "Hunted: The Iron Druid Chronicles, Book 6"); got != 1 {
		t.Errorf("series tail: %.2f, want 1", got)
	}
	if got := best("Inferno", "Awaken Online: Inferno"); got != colonPartWeight {
		t.Errorf("after the colon: %.2f, want %.2f", got, colonPartWeight)
	}
	if got := best("Homefront", "Homefront: An Expeditionary Force Audio Drama Special"); got != colonPartWeight {
		t.Errorf("before the colon: %.2f, want %.2f", got, colonPartWeight)
	}
	if got := best("Dune Messiah", "Dune", "Messiah"); got != 1 {
		t.Errorf("title + subtitle: %.2f, want 1", got)
	}
	// A stored title's own bracketed qualifier is kept, so the plain work wins.
	if got := best("Dune", "Dune (Dramatized Adaptation)"); got >= 1 {
		t.Errorf("bracketed qualifier: %.2f, want below 1", got)
	}
	if got := best("Hunted (Dramatized Adaptation)", "Hunted: The Iron Druid Chronicles, Book 6 (Dramatized Adaptation)"); got != 1 {
		t.Errorf("series tail before a qualifier: %.2f, want 1", got)
	}
}

func TestParseNameAndSplitAuthors(t *testing.T) {
	key := func(n personName) string { return strings.Join(n.terms(), " ") }
	for in, want := range map[string][]string{
		"Bernard Cornwell":                        {"bernard cornwell"},
		"Cornwell, Bernard":                       {"bernard cornwell"},
		"J.N. Chaney":                             {"j n chaney"},
		"Rachel Renée Russell":                    {"rachel renee russell"},
		"Martin Luther King Jr.":                  {"martin luther king"},
		"Smith, John, Jr.":                        {"john smith"},
		"Brandon Sanderson, Mary Robinette Kowal": {"brandon sanderson", "mary robinette kowal"},
		"Douglas Preston & Lincoln Child":         {"douglas preston", "lincoln child"},
		"Terry Pratchett and Neil Gaiman":         {"terry pratchett", "neil gaiman"},
		"TurtleMe":                                {"turtleme"},
		"Sharpe's Eagle (Sharpe 08)":              {"sharpes eagle"},
		"08":                                      nil,
		"":                                        nil,
	} {
		var got []string
		for _, n := range splitAuthors(in) {
			got = append(got, key(n))
		}
		if !slices.Equal(got, want) {
			t.Errorf("splitAuthors(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgreeAuthors(t *testing.T) {
	names := func(ss ...string) []personName {
		var out []personName
		for _, s := range ss {
			out = append(out, splitAuthors(s)...)
		}
		return out
	}
	for _, tc := range []struct {
		hyp, work string
		want      authorAgreement
	}{
		{"Bernard Cornwell", "Bernard Cornwell", authorFull},
		{"B. Cornwell", "Bernard Cornwell", authorFull},
		{"J.N. Chaney", "J. N. Chaney", authorFull},
		{"Cornwell", "Patricia Cornwell", authorFull},
		{"Bernard Cornwell", "Patricia Cornwell", authorSurname},
		{"Bernard Cornwell", "Dan Brown", authorNone},
	} {
		if got := agreeAuthors(names(tc.hyp), names(tc.work)); got != tc.want {
			t.Errorf("agreeAuthors(%q, %q) = %s, want %s", tc.hyp, tc.work, got, tc.want)
		}
	}
}

func TestSeriesNameFits(t *testing.T) {
	fits := func(a, b string) bool {
		x, y := newTermForm(matchTerms(a)), newTermForm(matchTerms(b))
		return seriesNameFits(&x, &y, nil)
	}
	for _, tc := range []struct {
		hyp, name string
		want      bool
	}{
		{"Richard Sharpe", "Richard Sharpe Novels", true},
		{"Sharpe", "Richard Sharpe Novels", true},
		{"Tarot", "Awaken Online: Tarot", true},
		{"Sentenced to War", "Sentenced to War", true},
		{"Dork Diaries", "Dork Diaries", true},
		{"Dresden Files", "The Dresden Files", true},
		// Either side may be the one-word or article-less one.
		{"Richard Sharpe", "Sharpe", true},
		{"The Expanse", "Expanse", true},
		{"Richard Sharpe", "Sharpe & Donovan", false},
		{"Dork Diaries", "The Misadventures of Max Crumbly", false},
	} {
		if got := fits(tc.hyp, tc.name); got != tc.want {
			t.Errorf("seriesNameFits(%q, %q) = %v, want %v", tc.hyp, tc.name, got, tc.want)
		}
	}
}

func TestRuntimeFit(t *testing.T) {
	for _, tc := range []struct {
		minutes, seconds int
		want             float64
	}{
		{600, 36000, 1},
		{600, 36900, 1},                // 2.4%
		{600, 39000, runtimeNearValue}, // 7.7%
		{600, 45000, 0},
	} {
		if got, _ := runtimeFit(tc.minutes, tc.seconds); got != tc.want {
			t.Errorf("runtimeFit(%d min, %d s) = %v, want %v", tc.minutes, tc.seconds, got, tc.want)
		}
	}
}

func TestTextTitleRuns(t *testing.T) {
	got := textTitleRuns("dune frank herbert")
	want := []string{"dune frank", "frank herbert", "dune", "herbert"}
	if !slices.Equal(got, want) {
		t.Errorf("textTitleRuns = %q, want %q", got, want)
	}
	// Bounded, and stopword runs are never probed.
	if got := textTitleRuns("the a of an x y z w"); len(got) > 2*maxTextRuns {
		t.Errorf("textTitleRuns returned %d runs, want at most %d", len(got), 2*maxTextRuns)
	}
	if got := textTitleRuns("the dune"); slices.Contains(got, "the") {
		t.Errorf("textTitleRuns probed a stopword: %q", got)
	}
}

func TestSplitVolumeAndVolumeOf(t *testing.T) {
	for in, want := range map[string]string{
		"dork diaries 12":          "dork diaries|12",
		"the primal hunter book 1": "the primal hunter|1",
		"book 3":                   "book|3",
		"1984":                     "",
		"catch 1984":               "",
		"dune":                     "",
	} {
		name, n, ok := splitVolume(matchTerms(in))
		got := ""
		if ok {
			got = strings.Join(name, " ") + "|" + n
		}
		if got != want {
			t.Errorf("splitVolume(%q) = %q, want %q", in, got, want)
		}
	}
	series := []termForm{newTermForm(matchTerms("The Primal Hunter"))}
	if n, ok := volumeOf(matchTerms("Primal Hunter 8"), series); !ok || n != "8" {
		t.Errorf("volumeOf = %q, %v; want 8", n, ok)
	}
	if _, ok := volumeOf(matchTerms("Catch 22"), series); ok {
		t.Error("volumeOf read another name's number as the series' volume")
	}
}

// TestLevenshteinReusesItsBuffer: one buffer serves comparisons of any size.
func TestLevenshteinReusesItsBuffer(t *testing.T) {
	buf := &editBuf{}
	for _, tc := range []struct {
		a, b string
		want int
	}{{"reformed", "reforged", 1}, {"a", "abcdefgh", 7}, {"kitten", "sitting", 3}, {"", "abc", 3}} {
		if got := levenshtein([]rune(tc.a), []rune(tc.b), buf); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
