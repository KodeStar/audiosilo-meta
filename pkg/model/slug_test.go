package model

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

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
	if len(got) > MaxSlugLen {
		t.Errorf("Slugify long slug len = %d, want <= %d", len(got), MaxSlugLen)
	}
	if got[len(got)-1] == '-' {
		t.Errorf("Slugify trimmed slug ends with a hyphen: %q", got)
	}
}

// TestSlugifyCollapsesSeparators pins the property the single-pass builder
// replaced a collapse-and-trim regexp with: a run of separators is ONE hyphen,
// and a slug never starts or ends on one however the input is padded.
func TestSlugifyCollapsesSeparators(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"---", ""},
		{"  --  a  --  b  --  ", "a-b"},
		{"a...b", "a-b"},
		{"(Bracketed) [Title]", "bracketed-title"},
		{"a/b|c", "a-b-c"},
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPersonSlug pins the one identity the catalogue's person ids are derived
// through, including the shared catch-all a fully-folding name falls back to -
// the single record whose id is legitimately not its name's slug.
func TestPersonSlug(t *testing.T) {
	cases := []struct {
		name         string
		wantSlug     string
		wantFellBack bool
	}{
		{"Madeleine L'Engle", "madeleine-lengle", false},
		{"Ramón De Ocampo", "ramon-de-ocampo", false},
		{"김영하", UnslugPersonID, true},
		{"", UnslugPersonID, true},
	}
	for _, c := range cases {
		slug, fellBack := PersonSlug(c.name)
		if slug != c.wantSlug || fellBack != c.wantFellBack {
			t.Errorf("PersonSlug(%q) = (%q, %v), want (%q, %v)", c.name, slug, fellBack, c.wantSlug, c.wantFellBack)
		}
	}
}

// TestSlugifyTransliteration pins the table of Latin letters NFD leaves whole,
// each with the romanization the dump corpus attested (see transliterations).
// Every case is a REAL name or title from that corpus, and the "was" column is
// what the letter-dropping fold produced before the table existed - the defect
// this table is here to make impossible.
func TestSlugifyTransliteration(t *testing.T) {
	cases := []struct {
		in   string
		want string
		was  string // the pre-table output, for the record
	}{
		// German eszett: 71 of wave 4's people carried one.
		{"Fabian Weiß", "fabian-weiss", "fabian-wei"},
		{"Katja Keßler", "katja-kessler", "katja-ke-ler"},
		{"Walter Schultheiß", "walter-schultheiss", "walter-schulthei"},
		// Nordic o-slash folds to the BASE letter, like ö and ó already do.
		{"Lars Møller", "lars-moller", "lars-m-ller"},
		{"Jørn Lier Horst", "jorn-lier-horst", "j-rn-lier-horst"},
		{"Ørjan Karlsson", "orjan-karlsson", "rjan-karlsson"},
		// Ligature letters take their standard two-letter romanization.
		{"Sara Blædel", "sara-blaedel", "sara-bl-del"},
		{"Eva Björg Ægisdóttir", "eva-bjorg-aegisdottir", "eva-bjorg-gisdottir"},
		{"Charlotte Cœur", "charlotte-coeur", "charlotte-c-ur"},
		// Icelandic eth and thorn.
		{"Yrsa Sigurðardóttir", "yrsa-sigurdardottir", "yrsa-sigur-ardottir"},
		{"Bragi Þór Valsson", "bragi-thor-valsson", "bragi-r-valsson"},
		// Stroked and dotless letters.
		{"Michał Ogórek", "michal-ogorek", "micha-ogorek"},
		{"Łukasz Radecki", "lukasz-radecki", "ukasz-radecki"},
		{"Başak Yılmaz", "basak-yilmaz", "ba-ak-y-lmaz"},
		{"Bóng Đá Live", "bong-da-live", "bong-live"},
		// Ordinal indicators.
		{"Ana Mª de la Fuente", "ana-ma-de-la-fuente", "ana-m-de-la-fuente"},
		// A typographic ligature.
		{"Marek Harloﬀ", "marek-harloff", "marek-harlo"},
		// An accented form of a transliterated letter: NFD sheds the mark down
		// to the bare letter first, so the table still sees it.
		{"Sønderjylland ǿ", "sonderjylland-o", "s-nderjylland"},
		// The umlaut rule is UNCHANGED - it folds to the base letter, and this
		// case is here so a future "oe" convention cannot be introduced for one
		// letter without failing on the other.
		{"Jörg Möller", "jorg-moller", "jorg-moller"},
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q (was %q before the table)", c.in, got, c.want, c.was)
		}
	}
}

// TestSlugifyApostropheGlyphs pins that every glyph a source spells the
// apostrophe with collapses the word rather than splitting it. The left single
// quote was the one missing from the set, so "Cheng‘en Wu" minted a different
// person from "Cheng’en Wu".
func TestSlugifyApostropheGlyphs(t *testing.T) {
	const want = "chengen-wu"
	for _, glyph := range []string{"'", "’", "‘", "´", "`", "ʼ", "ʻ"} {
		in := "Cheng" + glyph + "en Wu"
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// slugifyBeforeTransliteration is the EXACT implementation Slugify had before
// the transliteration table, kept here as the reference the compatibility proof
// below compares against. It is not dead code: it is the definition of "what
// the catalogue's existing slugs were minted by", and without it the claim that
// this change is byte-compatible outside the table would be an assertion rather
// than a test.
func slugifyBeforeTransliteration(s string) string {
	old := strings.NewReplacer("'", "", "’", "", "ʼ", "", "`", "")
	s = old.Replace(s)
	decomposed := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(decomposed))
	pending := false
	for _, r := range decomposed {
		var keep byte
		switch {
		case r >= 'A' && r <= 'Z':
			keep = byte(r) + ('a' - 'A')
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			keep = byte(r)
		case IsCombiningMark(r):
			continue
		default:
			pending = true
			continue
		}
		if pending && b.Len() > 0 {
			b.WriteByte('-')
		}
		pending = false
		b.WriteByte(keep)
	}
	slug := b.String()
	if len(slug) > MaxSlugLen {
		slug = strings.Trim(slug[:MaxSlugLen], "-")
	}
	return slug
}

// TestSlugifyByteCompatibility is the migration's safety proof: EVERY slug that
// does not contain an affected character is byte-identical to what the previous
// implementation produced, so the only records this change can move are the ones
// the migration in this same commit renames.
//
// It is exhaustive rather than sampled. Sweeping every assigned rune in the
// Basic Multilingual Plane plus the supplementary planes Latin can reach, each
// placed in a name context, proves the property for every possible input
// character - which a corpus, however large, only ever proves for the characters
// it happens to contain. (Measured separately over the libex dump's 1,446,523
// distinct name strings, the two implementations also agree on all but the 6,957
// that carry an affected letter.)
func TestSlugifyByteCompatibility(t *testing.T) {
	// affected reports whether a rune is one this change is ALLOWED to move: a
	// character in the transliteration table or the apostrophe glyphs the set
	// gained (the ones it already had are not a change, so they are not listed).
	//
	// The test is against the rune's NFD form, not the rune itself, because
	// Slugify transliterates after decomposing: "ǿ" is not a table entry, but it
	// decomposes to "ø" plus a combining acute, so it is legitimately affected.
	affected := func(r rune) bool {
		for _, d := range norm.NFD.String(string(r)) {
			if _, ok := transliterations[d]; ok {
				return true
			}
			if d == '‘' || d == '´' || d == 'ʻ' {
				return true
			}
		}
		return false
	}

	changed := map[rune]bool{}
	for r := rune(0); r <= 0x2ffff; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		// The rune is placed between two ASCII letters so a separator it
		// introduces (or stops introducing) is visible in the output.
		in := "a" + string(r) + "b"
		got, want := Slugify(in), slugifyBeforeTransliteration(in)
		if got == want {
			continue
		}
		changed[r] = true
		if !affected(r) {
			t.Errorf("Slugify(%q) = %q but the previous implementation gave %q: "+
				"U+%04X is not in the transliteration table or the apostrophe set, "+
				"so this change must not have moved it", in, got, want, r)
		}
	}

	// The converse: every entry in the table must actually CHANGE something,
	// or it is an entry the fold already handled and nobody would notice going
	// stale (İ is the letter that failed this - NFD decomposes it already).
	for r := range transliterations {
		if !changed[r] {
			t.Errorf("transliterations has U+%04X %q, but Slugify's output for it is "+
				"unchanged from the previous implementation - the entry is unreachable", r, r)
		}
	}
}
