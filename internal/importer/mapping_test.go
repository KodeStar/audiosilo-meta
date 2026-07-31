package importer

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"golang.org/x/text/unicode/norm"
)

func TestMapLanguage(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"english", "en", true},
		{"English", "en", true},
		{"  TURKISH ", "tr", true},
		{"german", "de", true},
		{"french", "fr", true},
		{"spanish", "es", true},
		{"italian", "it", true},
		{"japanese", "ja", true},
		{"portuguese", "pt", true},
		{"dutch", "nl", true},
		{"polish", "pl", true},
		{"russian", "ru", true},
		{"chinese", "zh", true},
		{"klingon", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := mapLanguage(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("mapLanguage(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestMapRegion(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"UK", "uk", true},
		{"US", "us", true},
		{" jp ", "jp", true},
		{"br", "br", true},
		// ISO-3166 spells the UK marketplace "gb"; the alias resolves here so
		// every source gets it, not just the one that first hit it.
		{"gb", "uk", true},
		{"GB", "uk", true},
		{" gb ", "uk", true},
		{"XX", "", false},
		{"", "", false},
		{"europe", "", false},
	}
	for _, c := range cases {
		got, ok := mapRegion(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("mapRegion(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestNormalizeSequence(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"3", "3", true},
		{"0.5", "0.5", true},
		{"1-3.5", "1-3.5", true},
		{" 2 ", "2", true},
		{"12-13", "12-13", true},
		// A position is a STRING, so two spellings of one number would be two
		// different positions: trailing fractional zeros are canonicalized away
		// (a Postgres numeric renders book 1 as "1.0"). "10" keeps its zero.
		{"1.0", "1", true},
		{"2.50", "2.5", true},
		{"1.00", "1", true},
		{"10", "10", true},
		{"10.0", "10", true},
		{"1.0-3.50", "1-3.5", true},
		{"", "", false},
		{"one", "", false},
		{"1.2.3", "", false},
		{"1-", "", false},
		{"-2", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeSequence(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("NormalizeSequence(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestSplitNames(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Dennis Vanderkerken, Dakota Krout", []string{"Dennis Vanderkerken", "Dakota Krout"}},
		{"  Solo Author  ", []string{"Solo Author"}},
		{"A,,B, ,C", []string{"A", "B", "C"}},
		{"", nil},
		{" , ", nil},
	}
	for _, c := range cases {
		got := SplitNames(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitNames(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Harry Potter and the Philosopher's Stone", "harry-potter-and-the-philosophers-stone"},
		{"Café Society", "cafe-society"},
		{"Motörhead: Overkill", "motorhead-overkill"},
		{"  Spaced   Out  ", "spaced-out"},
		{"J.R.R. Tolkien", "j-r-r-tolkien"},
		{"It's a Test — Really", "its-a-test-really"},
		{"ALL CAPS", "all-caps"},
		{"日本語", ""}, // non-Latin folds away; caller supplies a fallback
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugifyMaxLen(t *testing.T) {
	long := ""
	for i := 0; i < 60; i++ {
		long += "ab "
	}
	got := Slugify(long)
	if len(got) > 100 {
		t.Errorf("Slugify long slug len = %d, want <= 100", len(got))
	}
	if got[len(got)-1] == '-' {
		t.Errorf("Slugify trimmed slug ends with a hyphen: %q", got)
	}
}

// TestBoundedSlugTail pins the three regimes: the join fits, the base is cut at
// a hyphen boundary, the base is hard-cut because no boundary leaves room. The
// tail must come through intact in all of them.
func TestBoundedSlugTail(t *testing.T) {
	hyphenated := strings.Repeat("word-", 19) + "last" // 99 chars
	cases := []struct {
		name, base, tail, want string
	}{
		{"fits", "short-title", "-2020", "short-title-2020"},
		{"word boundary cut", hyphenated, "-2020", strings.Repeat("word-", 18) + "word-2020"},
		{"hard cut, no boundary", strings.Repeat("a", model.MaxSlugLen), "-2020", strings.Repeat("a", 95) + "-2020"},
		// Defensive regime: no caller composes a tail this long (the longest is an
		// author credit plus a numeric suffix), but the END of the tail - where the
		// distinguishing suffix lives - must still survive it.
		{"tail alone fills the cap", "a-title", "-" + strings.Repeat("bb-", 40) + "cast-7", "bb-" + strings.Repeat("bb-", 30) + "cast-7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BoundedSlugTail(c.base, c.tail)
			if got != c.want {
				t.Errorf("BoundedSlugTail(%q, %q) = %q, want %q", c.base, c.tail, got, c.want)
			}
			if !model.ValidSlug(got) {
				t.Errorf("result %q (%d chars) is not a valid slug", got, len(got))
			}
			if !strings.HasSuffix(got, "7") {
				return // only the long-tail case carries a distinguishing suffix
			}
			if !strings.HasSuffix(got, "cast-7") {
				t.Errorf("result %q dropped the tail's distinguishing suffix", got)
			}
		})
	}
}

// TestNumberedSlugAtBounded pins the shared numeric-suffix chain (recordings and
// series): candidate 0 is the bare base and every later candidate keeps its
// distinguishing number inside the cap, which makes the numbered candidates
// pairwise distinct. Candidate 0 is excluded from that claim on purpose - a base
// ending in "-<n>" equals the n'th candidate, which a walk absorbs as one wasted
// probe.
func TestNumberedSlugAtBounded(t *testing.T) {
	base := strings.Repeat("narrator-", 11) + "voice" // 104 chars, cut by Slugify in real use
	base = base[:model.MaxSlugLen]
	seen := map[string]bool{}
	for i := 0; i < 12; i++ {
		got := NumberedSlugAt(base, i)
		if !model.ValidSlug(got) {
			t.Errorf("candidate %d = %q (%d chars) is not a valid slug", i, got, len(got))
		}
		if i == 0 {
			if got != base {
				t.Fatalf("candidate 0 = %q, want the bare base", got)
			}
			continue
		}
		if !strings.HasSuffix(got, fmt.Sprintf("-%d", i+1)) {
			t.Errorf("candidate %d = %q lost its number", i, got)
		}
		if seen[got] {
			t.Errorf("numbered candidate %d = %q duplicates an earlier one", i, got)
		}
		seen[got] = true
	}
}

func TestYearOf(t *testing.T) {
	cases := map[string]string{
		"2025-11-20": "2025",
		"2001":       "2001",
		"":           "",
		"garbage":    "",
		"20-01-2020": "",
	}
	for in, want := range cases {
		if got := YearOf(in); got != want {
			t.Errorf("YearOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCoercion(t *testing.T) {
	book := rawBook{}
	if err := jsonInto(`{"a":"3","b":3,"c":3.9,"d":true,"e":null,"f":"x"}`, &book); err != nil {
		t.Fatal(err)
	}
	if got := book.str("a"); got != "3" {
		t.Errorf("str(a)=%q", got)
	}
	if got := book.str("b"); got != "3" {
		t.Errorf("str(number b)=%q", got)
	}
	if n, ok := book.intVal("b"); !ok || n != 3 {
		t.Errorf("intVal(b)=(%d,%v)", n, ok)
	}
	if n, ok := book.intVal("c"); !ok || n != 3 { // 3.9 truncates
		t.Errorf("intVal(c)=(%d,%v)", n, ok)
	}
	if n, ok := book.intVal("a"); !ok || n != 3 { // "3" as string
		t.Errorf("intVal(string a)=(%d,%v)", n, ok)
	}
	if _, ok := book.intVal("f"); ok {
		t.Errorf("intVal(non-numeric f) should fail")
	}
	if _, ok := book.intVal("missing"); ok {
		t.Errorf("intVal(missing) should fail")
	}
}

func TestBoolPtrTriState(t *testing.T) {
	book := rawBook{}
	if err := jsonInto(`{"t":true,"f":false,"n":null,"s":"true","x":"maybe"}`, &book); err != nil {
		t.Fatal(err)
	}
	if p := book.boolPtr("t"); p == nil || *p != true {
		t.Errorf("boolPtr(true) = %v", p)
	}
	if p := book.boolPtr("f"); p == nil || *p != false {
		t.Errorf("boolPtr(false) = %v", p)
	}
	if p := book.boolPtr("n"); p != nil {
		t.Errorf("boolPtr(null) = %v, want nil (unknown)", *p)
	}
	if p := book.boolPtr("missing"); p != nil {
		t.Errorf("boolPtr(absent) should be nil")
	}
	if p := book.boolPtr("s"); p == nil || *p != true {
		t.Errorf(`boolPtr("true" string) = %v`, p)
	}
	if p := book.boolPtr("x"); p != nil {
		t.Errorf("boolPtr(non-bool string) should be nil")
	}
}

func TestCleanCreditName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Every known role strips, case-insensitively.
		{"translator", "J. Kharkova - translator", "J. Kharkova"},
		{"introduction", "Valeria Kornosenko - introduction", "Valeria Kornosenko"},
		{"mixed case", "Graham Hancock - Introduction", "Graham Hancock"},
		{"upper case", "Charles S. Terry - TRANSLATOR", "Charles S. Terry"},
		{"intro", "Someone - intro", "Someone"},
		{"foreword", "Someone - foreword", "Someone"},
		{"afterword", "Someone - afterword", "Someone"},
		{"preface", "Someone - preface", "Someone"},
		{"editor", "Someone - editor", "Someone"},
		{"illustrator", "Someone - illustrator", "Someone"},
		{"adaptation", "Someone - adaptation", "Someone"},
		{"contributor", "Someone - contributor", "Someone"},
		{"narrator", "Someone - narrator", "Someone"},
		{"ghostwriter", "Someone - ghostwriter", "Someone"},
		{"compilation", "Someone - compilation", "Someone"},

		// The libex tranche's real shapes: a DOUBLED qualifier (the source
		// string was "Dan Veksler - Translator - translator") and a separator
		// with no space after the hyphen in front of a multi-word role.
		{"doubled qualifier", "Dan Veksler - Translator - translator", "Dan Veksler"},
		{"no space after hyphen", "Dan Veksler -translated by", "Dan Veksler"},
		{"repeated hyphens", "Dan Veksler -- translator", "Dan Veksler"},
		{"multi-word role", "Someone - edited by", "Someone"},
		{"foreword by", "Someone - foreword by", "Someone"},
		{"translation", "Someone - translation", "Someone"},
		{"adaptor", "Someone - adaptor", "Someone"},
		{"producer", "Someone - producer", "Someone"},
		{"instrumental soloist", "Yo Player - instrumental soloist", "Yo Player"},
		{"illustration", "Someone - illustration", "Someone"},

		// The role list is not English-only, and the German role appears both
		// accented and ASCII-folded.
		{"creator", "Gertrude Chandler Warner - creator", "Gertrude Chandler Warner"},
		{"ubersetzer accented", "Claudia Kern - Übersetzer", "Claudia Kern"},
		{"ubersetzer folded", "Claudia Kern - Ubersetzer", "Claudia Kern"},
		{"ubersetzerin", "Claudia Kern - Übersetzerin", "Claudia Kern"},
		{"herausgeber", "Hans Held - Herausgeber", "Hans Held"},
		{"traducteur", "Claude Gilbert - traducteur", "Claude Gilbert"},
		{"traductrice", "Claire Gilbert - traductrice", "Claire Gilbert"},
		{"illustrateur", "Claude Gilbert - illustrateur", "Claude Gilbert"},
		{"illustratrice", "Claire Gilbert - illustratrice", "Claire Gilbert"},
		{"traduttore", "Simona Brogli - traduttore", "Simona Brogli"},
		{"traduttrice", "Simona Brogli - traduttrice", "Simona Brogli"},
		{"curatore", "Simona Brogli - curatore", "Simona Brogli"},
		{"traductor", "Juan Perez - traductor", "Juan Perez"},
		{"traductora", "Ana Perez - traductora", "Ana Perez"},
		{"tradutor", "Joao Silva - tradutor", "Joao Silva"},
		{"traducao", "Joao Silva - tradução", "Joao Silva"},
		{"traducator", "Ion Popa - traducător", "Ion Popa"},

		// An accented role must match however the source ENCODES it: precomposed
		// (NFC, the map's own spelling), decomposed (NFD - "U" + U+0308, what a
		// Mac filesystem or an older exporter emits), and upper-cased.
		{"NFD ubersetzer", norm.NFD.String("Claudia Kern - Übersetzer"), "Claudia Kern"},
		{"NFD traducao", norm.NFD.String("Joao Silva - tradução"), "Joao Silva"},
		{"upper case accented role", "Claudia Kern - ÜBERSETZER", "Claudia Kern"},

		// The role capture is whitespace-collapsed before the lookup, so a
		// double-spaced multi-word role still matches its single-spaced key.
		{"double-spaced role", "Someone - translated  by", "Someone"},

		// Prefix credits, both observed languages.
		{"created by", "Created by Stan Lee", "Stan Lee"},
		{"creato da", "Creato da Stan Lee", "Stan Lee"},
		{"created by lowercase", "created by Stan Lee", "Stan Lee"},
		// A prefix and a suffix on the same name: the fixpoint loop applies both.
		{"prefix and suffix", "Created by Stan Lee - editor", "Stan Lee"},

		// Exact-half doubled names collapse.
		{"doubled multi-word name", "Melissa K. Roehrich Melissa K. Roehrich", "Melissa K. Roehrich"},
		{"doubled two-word name", "Full Cast Full Cast", "Full Cast"},
		{"doubled with differing case", "full cast Full Cast", "full cast"},

		// Negative cases. Never strip an arbitrary " - X" suffix, never collapse
		// a repeated single word, never collapse an odd word count.
		{"pen name with spaced hyphen", "The All - Stars", "The All - Stars"},
		{"hyphenated given name", "Jean - Luc Picard", "Jean - Luc Picard"},
		{"non-role suffix", "Anna - Maria Sound", "Anna - Maria Sound"},
		{"ampersand sound", "Ampersand - Sound", "Ampersand - Sound"},
		{"repeated single word", "Duran Duran", "Duran Duran"},
		{"odd word count", "Mitz Mitz Vah", "Mitz Mitz Vah"},
		{"prefix word is not a prefix credit", "Created Beings", "Created Beings"},
		// A multibyte LEADING rune must not be sliced mid-rune by the prefix
		// compare: the first rune simply is not "c", so nothing strips and the
		// name comes back byte-identical (never mangled into invalid UTF-8).
		{"multibyte leading name", "Ćreated by Someone", "Ćreated by Someone"},
		{"accented plain name", "Émile Zola", "Émile Zola"},
		// A name whose rune boundaries straddle len("created by ") - slicing it
		// at that byte offset would hand a half-rune to the comparison.
		{"multibyte name straddling the prefix length", "Created b文 Someone", "Created b文 Someone"},
		// Ambiguous connectors are deliberately absent from the role list.
		{"bare with is not a role", "Someone - with", "Someone - with"},
		// Empty-after-strip falls back to the unstripped name.
		{"strip would empty", "- translator", "- translator"},
		{"strip would empty with leading space", " - translator", " - translator"},
		{"prefix strip would empty", "Created by ", "Created by "},
		// No qualifier at all.
		{"plain", "Plain Name", "Plain Name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CleanCreditName(c.in); got != c.want {
				t.Errorf("CleanCreditName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// Cleaning is a FIXPOINT: the function loops until the name stops changing,
	// so re-cleaning an already-cleaned name must be a no-op. A rule that kept
	// nibbling (or that only converged for the shapes above) would show up here
	// across the whole table, including the negative cases.
	t.Run("idempotent", func(t *testing.T) {
		for _, c := range cases {
			once := CleanCreditName(c.in)
			if twice := CleanCreditName(once); twice != once {
				t.Errorf("%s: CleanCreditName is not idempotent: %q -> %q -> %q", c.name, c.in, once, twice)
			}
		}
	})
}

// TestCutPrefixFold pins the two properties that made the prefix compare
// rune-aware rather than a byte slice at len(prefix):
//   - a multibyte leading rune is never cut in half (the old name[:len(prefix)]
//     produced invalid UTF-8, which no comparison can reason about);
//   - a case fold that CHANGES the byte length still matches - "İ" (U+0130, two
//     bytes) folds to the one-byte "i", so a byte-length-equal compare could
//     never have matched it.
func TestCutPrefixFold(t *testing.T) {
	cases := []struct {
		name     string
		s        string
		prefix   string
		wantRest string
		wantOK   bool
	}{
		{"exact", "created by Stan Lee", "created by ", "Stan Lee", true},
		{"case-insensitive", "CREATED BY Stan Lee", "created by ", "Stan Lee", true},
		{"whole string is the prefix", "created by ", "created by ", "", true},
		{"shorter than the prefix", "created", "created by ", "", false},
		{"multibyte leading rune", "Ćreated by Someone", "created by ", "", false},
		{"multibyte straddling the prefix length", "Created b文 Someone", "created by ", "", false},
		{"fold changes byte length", "İstanbul Press", "i", "stanbul Press", true},
		{"remainder keeps its multibyte runes", "Created by Émile Zola", "created by ", "Émile Zola", true},
		{"invalid UTF-8 never matches", "\xff\xfeated by X", "created by ", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rest, ok := cutPrefixFold(c.s, c.prefix)
			if ok != c.wantOK || rest != c.wantRest {
				t.Errorf("cutPrefixFold(%q, %q) = (%q, %v), want (%q, %v)", c.s, c.prefix, rest, ok, c.wantRest, c.wantOK)
			}
		})
	}
}

// TestRoleQualifiersVocabulary pins the shape of the credit vocabularies. Each
// property is load-bearing for the lookup, not cosmetic:
//   - lowercase + NFC, because stripRoleQualifier lowercases and NFC-normalizes
//     the captured role before the map lookup, so a key that is neither could
//     never be matched by anything.
//   - single-spaced and untrimmed-free, because the capture is collapsed with
//     strings.Fields.
//   - HYPHEN-FREE, because roleSuffixRE's capture is `[^-]+`: a key containing a
//     hyphen is unreachable by construction.
//
// prefixCredits are matched rune-by-rune under case folding by cutPrefixFold and
// each must keep its trailing space, so a name merely STARTING with the word
// ("Created Beings") is left alone.
func TestRoleQualifiersVocabulary(t *testing.T) {
	for role := range roleQualifiers {
		if role == "" {
			t.Error("roleQualifiers contains an empty key")
			continue
		}
		if lower := strings.ToLower(role); role != lower {
			t.Errorf("role %q is not lowercase (want %q)", role, lower)
		}
		if nfc := norm.NFC.String(role); role != nfc {
			t.Errorf("role %q is not NFC-normalized (want %q)", role, nfc)
		}
		if collapsed := strings.Join(strings.Fields(role), " "); role != collapsed {
			t.Errorf("role %q is not trimmed/single-spaced (want %q)", role, collapsed)
		}
		if strings.Contains(role, "-") {
			t.Errorf("role %q contains a hyphen; roleSuffixRE captures [^-]+ so it can never match", role)
		}
	}
	for _, prefix := range prefixCredits {
		if lower := strings.ToLower(prefix); prefix != lower {
			t.Errorf("prefix credit %q is not lowercase (want %q)", prefix, lower)
		}
		if !strings.HasSuffix(prefix, " ") {
			t.Errorf("prefix credit %q must end with a space so a name merely starting with the word is untouched", prefix)
		}
	}
}

func TestSplitNamesCleansCredits(t *testing.T) {
	got := SplitNames("Kirill Klevanski, Valeria Kornosenko - introduction, J. Kharkova - Translator, " +
		"Dan Veksler - Translator - translator, Created by Stan Lee, Full Cast Full Cast, The All - Stars")
	want := []string{
		"Kirill Klevanski", "Valeria Kornosenko", "J. Kharkova",
		"Dan Veksler", "Stan Lee", "Full Cast", "The All - Stars",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SplitNames credit cleaning = %#v, want %#v", got, want)
	}
}

func TestISODatePart(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2024-12-06T03:00:00", "2024-12-06"},
		{"2024-12-06T03:00:00Z", "2024-12-06"},
		// A SQL timestamp renders with a SPACE, not a "T" - reading only the "T"
		// left the whole string in place, which then failed the date pattern and
		// dropped the release date silently.
		{"2019-05-01 00:00:00", "2019-05-01"},
		{"2019-05-01 00:00:00+00", "2019-05-01"},
		{"2024-12-06", "2024-12-06"},
		{"  2024-12-06  ", "2024-12-06"},
		{"", ""},
	}
	for _, c := range cases {
		if got := isoDatePart(c.in); got != c.want {
			t.Errorf("isoDatePart(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeISBN(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"9781234567897", "9781234567897", true},
		// Printed with hyphens (the form field has always accepted this shape;
		// a bulk import used to reject it).
		{"978-1-234-56789-7", "9781234567897", true},
		{" 978 1 234 56789 7 ", "9781234567897", true},
		{"030640615X", "030640615X", true},
		{"123", "", false},
		{"", "", false},
		{"97812345678979", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeISBN(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("NormalizeISBN(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}
