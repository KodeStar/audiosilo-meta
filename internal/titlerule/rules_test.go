package titlerule

import (
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesIn is a stub SeriesResolver over a fixed list of series NAMES, for the rules
// that take one. It returns the matched form and the name it belongs to, exactly as
// the audit's index does.
func seriesIn(names ...string) SeriesResolver {
	return func(text string) (form, name string, ok bool) {
		for _, n := range names {
			if f, found := SeriesRefIn(lower(text), n); found {
				return f, n, true
			}
		}
		return "", "", false
	}
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func TestCleanRemovesRetailerDecoration(t *testing.T) {
	cases := []struct{ title, series, want string }{
		{"Hammered: The Iron Druid Chronicles, Book 3", "Iron Druid Chronicles", "Hammered"},
		{"Two Tales of the Iron Druid Chronicles", "Iron Druid Chronicles", "Two Tales"},
		{"Seeds of Chaos Omnibus: A GameLit Dark Adventure Series", "Seeds of Chaos", "Omnibus"},
		{"Mageling (Unabridged)", "", "Mageling"},
		{"Eric (Mundodisco 9) [Eric (Discworld)]", "", "Eric"},
		// Never over-cleans: a real subtitle, a stopword-only title, a leading number.
		{"Star Wars: A New Hope", "", "Star Wars: A New Hope"},
		{"The One and Only", "", "The One and Only"},
		{"1984: The Novel", "", "1984"},
		{"The Book Thief", "", "The Book Thief"},
		// A leading article is part of a title and must survive a clean.
		{"A Deadly Cliché:", "", "A Deadly Cliché"},
		// Removing a series name from BETWEEN two separators leaves an empty
		// segment, which neither dangling-segment peel can see.
		{"A Murder of Crows: Shadows and Ash, Book Two", "Shadows and Ash", "A Murder of Crows, Book Two"},
	}
	for _, c := range cases {
		if got := Clean(c.title, c.series); got != c.want {
			t.Errorf("Clean(%q, %q) = %q, want %q", c.title, c.series, got, c.want)
		}
	}
}

func TestCleanNeverEmptiesATitle(t *testing.T) {
	for _, title := range []string{"Unabridged", "(Unabridged)", "Omnibus", "The", "1"} {
		if got := Clean(title, ""); got == "" {
			t.Errorf("Clean(%q, \"\") emptied the title", title)
		}
	}
}

func TestCompareKeyIsArticleInsensitiveButCleanIsNot(t *testing.T) {
	if CompareKey("A Deadly Cliché") != CompareKey("Deadly Cliché") {
		t.Error("CompareKey should not treat a leading article as identity")
	}
	if Clean("A Deadly Cliché", "") == Clean("Deadly Cliché", "") {
		t.Error("Clean must keep a leading article: it is part of the title")
	}
}

func TestCarriesIdentity(t *testing.T) {
	for _, s := range []string{"Hammered", "Two Tales", "Level Nine Wizard"} {
		if !CarriesIdentity(s) {
			t.Errorf("CarriesIdentity(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"Omnibus", "Box Set", "The Complete Collection", "Level 2 Lessons 21-25"} {
		if CarriesIdentity(s) {
			t.Errorf("CarriesIdentity(%q) = true, want false", s)
		}
	}
}

func TestFoldKeyFoldsWhatASlugFolds(t *testing.T) {
	// A person's id IS model.PersonSlug(name) (pkg/check enforces it), so grouping
	// by the slug would find nothing. FoldKey drops the hyphens, which is what makes
	// the initials spellings meet.
	a, _ := model.PersonSlug("A.B. Kovacs")
	b, _ := model.PersonSlug("AB Kovacs")
	if a == b {
		t.Fatalf("the two spellings were supposed to slug differently, both = %q", a)
	}
	if FoldKey("A.B. Kovacs") != FoldKey("AB Kovacs") {
		t.Errorf("FoldKey did not fold the two spellings together: %q vs %q",
			FoldKey("A.B. Kovacs"), FoldKey("AB Kovacs"))
	}
	// And it folds diacritics, through the project's own Slugify - which the
	// server's Normalize (deliberately not copied) would have dropped.
	if FoldKey("Café Society") != FoldKey("Cafe Society") {
		t.Error("FoldKey did not fold a diacritic")
	}
}

func TestDecorationsAreReturnedInPriorityOrder(t *testing.T) {
	f := TitleFacts{Title: "Stacked, Book 3 (Unabridged)"}
	got := Decorations(f)
	if want := []string{DecVolume, DecEdition}; !reflect.DeepEqual(got, want) {
		t.Errorf("Decorations = %v, want %v (priority order)", got, want)
	}
	if p := PrimaryDecoration(got); p != DecVolume {
		t.Errorf("PrimaryDecoration = %q, want %q", p, DecVolume)
	}
}

func TestDecorationsAreSilentOnACleanTitle(t *testing.T) {
	for _, title := range []string{"The Book Thief", "Star Wars: A New Hope", "1984", "A Novel Idea"} {
		if got := Decorations(TitleFacts{Title: title}); len(got) != 0 {
			t.Errorf("%q was reported as decorated: %v", title, got)
		}
	}
}

// The edition-marker code must be the IMPORTER's rule, not a local re-spelling: a
// hand-written version drifted on bracket optionality and stacked markers.
func TestEditionMarkerIsTheImportersRule(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"Mageling (Unabridged)", true},
		{"Mageling [Abridged]", true},
		{"Mageling (Unabridged)(Abridged)", true}, // stacked
		{"Amelia Unabridged", false},              // no brackets: not the marker
		{"Unabridged Dictionary", false},
	}
	for _, c := range cases {
		got := len(Decorations(TitleFacts{Title: c.title})) > 0 &&
			PrimaryDecoration(Decorations(TitleFacts{Title: c.title})) == DecEdition
		if importer.HasEditionMarker(c.title) != c.want {
			t.Errorf("importer.HasEditionMarker(%q) = %v, want %v", c.title, !c.want, c.want)
		}
		if c.want && !got {
			t.Errorf("%q: the decoration table did not file it under %s", c.title, DecEdition)
		}
	}
}

func TestArticleSeriesPrefix(t *testing.T) {
	in := seriesIn("How to Train Your Dragon")
	art, series, rest, ok := ArticleSeriesPrefix("A How to Train Your Dragon: A Hero's Guide to Deadly Dragons", in)
	if !ok {
		t.Fatal("the article-plus-series prefix was not detected")
	}
	if art != "A" || series != "How to Train Your Dragon" || rest != "A Hero's Guide to Deadly Dragons" {
		t.Errorf("got (%q, %q, %q)", art, series, rest)
	}
	// An ordinary title whose head is not a series name is untouched.
	if _, _, _, ok := ArticleSeriesPrefix("A Study in Scarlet: The Novel", in); ok {
		t.Error("an ordinary article-led title was read as a series prefix")
	}
	// The series name must fill the WHOLE span before the separator.
	if _, _, _, ok := ArticleSeriesPrefix("A How to Train Your Dragon Handbook: Something", in); ok {
		t.Error("a partial series-name head was accepted")
	}
	// Nil resolver: the rule cannot fire.
	if _, _, _, ok := ArticleSeriesPrefix("A How to Train Your Dragon: A Hero's Guide", nil); ok {
		t.Error("the rule fired with no series resolver")
	}
}

// "The <series name>" is ordinary English and usually the series' own natural form.
// Accepting it made the rule report hundreds of perfectly good titles.
func TestArticleSeriesPrefixIgnoresTheDefiniteArticle(t *testing.T) {
	in := seriesIn("Legend of Dave the Villager", "Minder Project", "How to Train Your Dragon")
	for _, title := range []string{
		"The Legend of Dave the Villager, Books 16-20",
		"The Minder Project, Seasons 1-4",
	} {
		if _, _, _, ok := ArticleSeriesPrefix(title, in); ok {
			t.Errorf("%q was read as a spurious article prefix", title)
		}
	}
	// The indefinite article still fires - including across a comma, which is where
	// a retailer usually hangs the rest.
	for _, title := range []string{
		"A How to Train Your Dragon: A Hero's Guide to Deadly Dragons",
		"A How to Train Your Dragon, A Hero's Guide to Deadly Dragons",
	} {
		if _, _, _, ok := ArticleSeriesPrefix(title, in); !ok {
			t.Errorf("%q was not detected", title)
		}
	}
}

// A form is allowed to be the series name minus its leading article, so a series
// genuinely CALLED "A Thousand Li" resolves through the form "Thousand Li" - and its
// titles are written correctly.
func TestArticleSeriesPrefixIgnoresASeriesNamedWithTheArticle(t *testing.T) {
	named := seriesIn("A Thousand Li", "A Distant Eden")
	for _, title := range []string{
		"A Thousand Li: Descent from the Mountain",
		"A Distant Eden: Volume 1",
	} {
		if _, _, _, ok := ArticleSeriesPrefix(title, named); ok {
			t.Errorf("%q: the article is the series' own and must not be reported", title)
		}
	}
	// The same title shape against a series whose name does NOT carry the article
	// is the defect, and still fires.
	unnamed := seriesIn("Thousand Li")
	if _, _, _, ok := ArticleSeriesPrefix("A Thousand Li: Descent from the Mountain", unnamed); !ok {
		t.Error("a spurious article over an article-less series name was not detected")
	}
}

func TestSeriesKeyPeelsCatalogueDecoration(t *testing.T) {
	same := [][2]string{
		{"Dragon Heart", "Dragon Heart Series"},
		{"Richard Sharpe", "Richard Sharpe Novels"},
		{"The Primal Hunter", "Primal Hunter"},
		{"Cafe Society", "Café Society"},
		{"3 a.m.", "The 3 A.M."},
		{"Vorkosigan Saga", "Vorkosigan Saga (chronological)"},
	}
	for _, c := range same {
		if SeriesKey(c[0]) != SeriesKey(c[1]) {
			t.Errorf("SeriesKey(%q)=%q != SeriesKey(%q)=%q", c[0], SeriesKey(c[0]), c[1], SeriesKey(c[1]))
		}
	}
	// A trailing "Saga" is NOT peeled by the tight key - it is part of a real name
	// at least as often as it is decoration.
	if SeriesKey("Vorkosigan") == SeriesKey("Vorkosigan Saga") {
		t.Error("the tight key peeled a trailing Saga")
	}
	if SeriesSagaKey("Vorkosigan") != SeriesSagaKey("Vorkosigan Saga") {
		t.Error("the saga key did not peel a trailing Saga")
	}
	// A series genuinely named for its decoration survives.
	if SeriesKey("Series") == "" {
		t.Error(`a series named "Series" was peeled away entirely`)
	}
}

func TestSlugShapePredicates(t *testing.T) {
	if art, ok := LeadingSlugArticle("a-mageling"); !ok || art != "a" {
		t.Errorf("LeadingSlugArticle = (%q, %v)", art, ok)
	}
	if _, ok := LeadingSlugArticle("mageling"); ok {
		t.Error("a slug with no article reported one")
	}
	// The title's first SLUG word, so an initial is not read as an article.
	if !TitleStartsWith("A.A. Milne's Winnie-the-Pooh", "a") {
		t.Error(`"A.A. Milne's ..." should start with the slug word "a"`)
	}
	if !TitleStartsWith("A Deadly Cliché", "a") {
		t.Error("a real leading article was not seen")
	}
	if TitleStartsWith("Mageling", "a") {
		t.Error("a title with no article reported one")
	}
	// Doubled tokens: a real restatement fires, innocent numbers and initials do not.
	if frag, ok := DoubledSlugToken("simple-italian-italian-edition"); !ok || frag != "italian" {
		t.Errorf("DoubledSlugToken = (%q, %v), want italian", frag, ok)
	}
	if frag, ok := DoubledSlugToken("iron-druid-iron-druid-book-3"); !ok || frag != "iron-druid" {
		t.Errorf("DoubledSlugToken pair = (%q, %v)", frag, ok)
	}
	for _, slug := range []string{"10-000-000-marriage-proposal", "1-1", "a-a-milnes-winnie-the-pooh"} {
		if frag, ok := DoubledSlugToken(slug); ok {
			t.Errorf("DoubledSlugToken(%q) fired innocently: %q", slug, frag)
		}
	}
}

func TestOneEditApartIsExactlyOneEdit(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"rachelrenerussell", "rachelreneerussell", true}, // one insertion
		{"rachelreneerussell", "rachelrenerussell", true}, // ...and its mirror
		{"aattanasio", "aaattanasio", true},               // one insertion
		{"aghoward", "arhoward", true},                    // one substitution
		{"abcdefgh", "abcdfegh", true},                    // one transposition
		{"abcdefgh", "abcdefgh", false},                   // zero edits is not one
		{"abcdefgh", "abcdefij", false},                   // two substitutions
		{"abcdefgh", "abcdefghij", false},                 // two insertions
		{"", "a", true},                                   // degenerate but consistent
	}
	for _, c := range cases {
		if got := OneEditApart(c.a, c.b); got != c.want {
			t.Errorf("OneEditApart(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// The audit and the importer must never disagree about which two spellings are one
// person, which is why the key is the importer's own.
func TestMarkedNameKeyIsTheImportersRule(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"C. J. Critt", "CJ Critt", true},
		{"J.D. Franx", "JD Franx", true},
		{"E. M. Brown", "Em Brown", false},
		{"Mr. James", "M. R. James", false},
	} {
		if got := MarkedNameKey(c.a) == MarkedNameKey(c.b); got != c.same {
			t.Errorf("MarkedNameKey(%q) == MarkedNameKey(%q) is %v, want %v", c.a, c.b, got, c.same)
		}
		if MarkedNameKey(c.a) != importer.MarkedNameKey(c.a) {
			t.Errorf("MarkedNameKey(%q) is not the importer's answer", c.a)
		}
	}
}

func TestWorkRankLadder(t *testing.T) {
	base := WorkRank{ID: "b"}
	// Each rung beats everything below it. The order is the argument: what survives
	// a merge should be the most canonical RECORD, because everything else moves
	// onto it.
	if !(WorkRank{InSeries: true, Decorations: 9, ID: "z"}).Better(WorkRank{Recordings: 99, HasSidecar: true, ID: "a"}) {
		t.Error("a series membership must outrank everything below it")
	}
	if !(WorkRank{Decorations: 0, ID: "z"}).Better(WorkRank{Decorations: 1, Recordings: 99, ID: "a"}) {
		t.Error("fewer decorations must outrank more recordings: a clean title cannot be recovered by moving anything")
	}
	if !(WorkRank{TitleLen: 3, ID: "z"}).Better(WorkRank{TitleLen: 9, Recordings: 99, ID: "a"}) {
		t.Error("a shorter title must outrank more recordings")
	}
	if !(WorkRank{Recordings: 2, ID: "z"}).Better(WorkRank{Recordings: 1, HasSidecar: true, ID: "a"}) {
		t.Error("more recordings must outrank a sidecar")
	}
	// The regression this order exists for: one recording plus a sidecar must NOT
	// beat forty-one recordings with the same clean title.
	dramatized := WorkRank{Decorations: 0, TitleLen: len("The Secret Garden (Dramatized)"), Recordings: 1, HasSidecar: true, ID: "a"}
	clean := WorkRank{Decorations: 0, TitleLen: len("The Secret Garden"), Recordings: 41, ID: "z"}
	if !clean.Better(dramatized) || dramatized.Better(clean) {
		t.Error("a sidecar on a decorated one-recording record must not win the merge target")
	}
	// The id is the last resort, so the answer never depends on input order.
	if !(WorkRank{ID: "a"}).Better(base) || (base).Better(WorkRank{ID: "a"}) {
		t.Error("the id must break the final tie, one way only")
	}
}

func TestSeriesRankLadder(t *testing.T) {
	if !(SeriesRank{Works: 5, ID: "z"}).Better(SeriesRank{Works: 1, ID: "a"}) {
		t.Error("the series holding more works must win")
	}
	if !(SeriesRank{Works: 1, ID: "a"}).Better(SeriesRank{Works: 1, ID: "b"}) {
		t.Error("the id must break the tie")
	}
}

func TestSeriesFormsMemoReturnsTheSameForms(t *testing.T) {
	a := SeriesForms("The Dragon Heart Series")
	b := SeriesForms("The Dragon Heart Series")
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("memoized forms differ: %v vs %v", a, b)
	}
	if len(a) < 2 {
		t.Fatalf("expected several spellings, got %v", a)
	}
	// Every form a probe can LINK on must be a form stripSeries REMOVES, or a
	// linked pair would keep the series words in its residual.
	for _, form := range a {
		if _, ok := SeriesRefIn(lower("A "+form+" Story"), "The Dragon Heart Series"); !ok {
			t.Errorf("form %q does not link through SeriesRefIn", form)
		}
	}
}

func TestCountSignificantWords(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"Iron Druid Chronicles", 3},
		{"The Land", 1}, // "the" is a stopword
		{"A 1", 0},      // an article and a bare number
		{"", 0},
	}
	for _, c := range cases {
		if got := CountSignificantWords(c.in); got != c.want {
			t.Errorf("CountSignificantWords(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
