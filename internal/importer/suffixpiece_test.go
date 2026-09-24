package importer

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestSuffixPieceVocabulary is the canonicalization self-test: a key that is not
// in its own comparison form, or longer than the two tokens isSuffixPiece
// probes, can never match. It also pins the measured refusals - tokens that are
// a name or a pair of initials as readily as a post-nominal.
func TestSuffixPieceVocabulary(t *testing.T) {
	for s := range suffixPieceSpellings {
		if got := foldCredit(s); got != s {
			t.Errorf("suffix spelling %q is not canonical (foldCredit gives %q); it can never match", s, got)
		}
		if n := len(strings.Fields(s)); n < 1 || n > maxCredentialWords {
			t.Errorf("suffix spelling %q has %d words; tailVocab.cut probes at most %d", s, n, maxCredentialWords)
		}
	}
	if !suffixPieceSpellings["gmbh"] || !suffixPieceSpellings["ph.d."] {
		t.Error("suffixPieceSpellings does not carry the vocabularies it reuses (corporateLegalSuffix, academicCredentials)")
	}
	for _, declined := range []string{"ed", "dc", "d.c.", "ma", "m.a.", "jd", "j.d.", "rn", "ms", "do", "do.", "nd", "rd", "sj", "op"} {
		if suffixPieceSpellings[declined] {
			t.Errorf("suffix vocabulary holds %q, which is a name or initials as readily as a post-nominal", declined)
		}
	}
}

// TestSplitRawNamesRejoinsASuffixPiece is issue #2320: a library export's comma
// list split "David Posen, MD" and "Anthony Rao, Ph.D." each into two credits.
// This is the RAW split, so the source's spelling survives; what the cleaning
// then makes of the name is TestCleanedCreditCarriesItsSuffixOnTheName's.
// site/src/lib/import-parse.test.ts pins its twin against these same cases.
func TestSplitRawNamesRejoinsASuffixPiece(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"David Posen, MD", []string{"David Posen, MD"}},
		{"Anthony Rao, Ph.D.", []string{"Anthony Rao, Ph.D."}},
		// A suffix in the MIDDLE of a list belongs to the name before it and
		// leaves the name after it alone.
		{"Jane Roe, MD, John Doe", []string{"Jane Roe, MD", "John Doe"}},
		// Stacked, each its own comma piece, and stacked inside one piece.
		{"Frank Lipman, MD, PhD", []string{"Frank Lipman, MD, PhD"}},
		{"Frank Lipman, MD PhD, Ann Reader", []string{"Frank Lipman, MD PhD", "Ann Reader"}},
		// The spaced spellings are one piece.
		{"Jane Roe, Ph. D.", []string{"Jane Roe, Ph. D."}},
		{"Jane Roe, D. Min", []string{"Jane Roe, D. Min"}},
		// A list that OPENS with a suffix has nobody to attach it to: dropped,
		// never a person - while a real credit remains on the side.
		{"PhD, Jane Roe", []string{"Jane Roe"}},
		// A side made of nothing BUT suffix-shaped pieces is kept exactly as it
		// was split before this rule: "Ii" is a Japanese surname, and dropping
		// the lone credit would leave the book with nobody on that side.
		{"MD", []string{"MD"}},
		{"Ii", []string{"Ii"}},
		{"MD, PhD", []string{"MD", "PhD"}},
		// Generational and legal-entity suffixes are the same mechanism.
		{"Martin Luther King, Jr., Coretta Scott King", []string{"Martin Luther King, Jr.", "Coretta Scott King"}},
		{"David W. Dietz, III", []string{"David W. Dietz, III"}},
		{"Listen & Live Audio, Inc.", []string{"Listen & Live Audio, Inc."}},
		{"Der Audio Verlag, GmbH", []string{"Der Audio Verlag, GmbH"}},
		// Declined tokens keep splitting exactly as before: "Ed" is a real
		// credit, and "DC" is DC Comics as often as a chiropractor.
		{"Jane Roe, Ed", []string{"Jane Roe", "Ed"}},
		{"Tom King, DC", []string{"Tom King", "DC"}},
		{"Ed", []string{"Ed"}},
		// A credential at the end of a WORD is not a piece.
		{"Jane Roe, Mdina Smith", []string{"Jane Roe", "Mdina Smith"}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := SplitRawNames(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("SplitRawNames(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSuffixPieceRejectsANameWithoutAllocating pins the cost: every credit of
// every import is asked, and an ordinary name must be rejected by its last
// token alone, with nothing folded or split on the heap.
func TestSuffixPieceRejectsANameWithoutAllocating(t *testing.T) {
	for _, name := range []string{"Philip Zimbardo", "Jane Roe, Mdina Smith", "Ann Reader Ph."} {
		if got := testing.AllocsPerRun(100, func() { isSuffixPiece(name) }); got != 0 {
			t.Errorf("isSuffixPiece(%q) allocates %v times per call, want 0", name, got)
		}
	}
}

// TestRejoinedCredentialFoldsOntoItsTwin is the point of rejoining rather than
// dropping: the ordinary cleaning then decides the name, so a doctorate folds
// onto the bare twin the same-side census holds, and a generational suffix
// stays part of the name however permissive the census is.
func TestRejoinedCredentialFoldsOntoItsTwin(t *testing.T) {
	seen := seenAs("David Posen", "Anthony Rao", "Martin Luther King")
	census := creditCensus{anySide: seen, sameSide: seen}
	if got := creditNamesOf(sourceCredits(nil, "David Posen, MD", census)); !reflect.DeepEqual(got, []string{"David Posen"}) {
		t.Errorf("author credits = %q, want the bare twin", got)
	}
	got := creditNamesOf(sourceCredits(nil, "Anthony Rao, Ph.D., Martin Luther King, Jr.", census))
	if want := []string{"Anthony Rao", "Martin Luther King Jr."}; !reflect.DeepEqual(got, want) {
		t.Errorf("narrator credits = %q, want %q", got, want)
	}
}

// TestLibexDropsAStrandedSuffix is the typed path: libex split its own credits
// upstream and its list order states nothing, so the stranded piece is dropped
// rather than attached to a neighbour. The row is the dump's B002V1OQ7O, which
// seeded `ph-d` as the first author of the-new-york-times-pocket-mba-ph-d.
func TestLibexDropsAStrandedSuffix(t *testing.T) {
	got := libexNames([]any{
		map[string]any{"name": "Ph.D"},
		map[string]any{"name": "Dileep Rao Ph.D."},
		map[string]any{"name": "Richard Cardozo"},
		map[string]any{"name": "Brion McClanahan, Ph.D."}, // a typed credit is never split
	})
	want := []string{"Dileep Rao Ph.D.", "Richard Cardozo", "Brion McClanahan, Ph.D."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("libexNames = %q, want %q", got, want)
	}
	// A side whose ONLY credit is suffix-shaped keeps it, exactly as before.
	if got := libexNames([]any{map[string]any{"name": "IV"}}); !reflect.DeepEqual(got, []string{"IV"}) {
		t.Errorf("libexNames(lone IV) = %q, want it kept", got)
	}

	sum, dataDir := runLibex(t, rows(libexRow{
		asin: "B002V1OQ7O", title: "The New York Times Pocket MBA",
		authors:   `{"name":"Ph.D"},{"name":"Dileep Rao Ph.D."},{"name":"Richard Cardozo"}`,
		narrators: `{"name":"Eric Conger"},{"name":"III"}`,
	}), false)
	if sum.NewPeople != 3 {
		t.Errorf("NewPeople = %d, want 3 (two authors, one narrator)", sum.NewPeople)
	}
	for _, junk := range []string{"ph-d", "iii"} {
		if entryExists(t, dataDir, personAddr(junk)) {
			t.Errorf("person %q was minted from a stranded suffix", junk)
		}
	}
}

// TestUserImportRejoinsASuffixPiece is issue #2320 end to end through a
// library export's comma-joined credit fields.
func TestUserImportRejoinsASuffixPiece(t *testing.T) {
	sum, dataDir := runImport(t, `[{"asin":"B0SUFFIX01","title_short":"The Joy of Tension","author":"David Posen, MD","narrated_by":"Anthony Rao, Ph.D.","language":"english","region":"US","seconds":1000}]`, false)
	if sum.NewPeople != 2 {
		t.Errorf("NewPeople = %d, want 2", sum.NewPeople)
	}
	for _, junk := range []string{"md", "ph-d"} {
		if entryExists(t, dataDir, personAddr(junk)) {
			t.Errorf("person %q was minted from a comma-split credential", junk)
		}
	}
	for _, slug := range []string{"david-posen-md", "anthony-rao-ph-d"} {
		if !entryExists(t, dataDir, personAddr(slug)) {
			t.Errorf("person %q is missing", slug)
		}
	}
}

// TestRejoinedSuffixKeepsTheRoleQualifier: the rejoin puts a comma between a
// role qualifier and the post-nominal after it ("Jane Doe - translator, PhD"),
// and the qualifier must still be read - the split this replaced credited Jane
// Doe as a translator, so rejoining may not turn her into a person named
// "Jane Doe - translator, PhD" with no role.
func TestRejoinedSuffixKeepsTheRoleQualifier(t *testing.T) {
	for in, want := range map[string]credit{
		"Jane Doe - translator, PhD": {name: "Jane Doe PhD", roles: []string{"translator"}},
		"Jane Doe - editor, Jr.":     {name: "Jane Doe Jr.", roles: []string{"editor"}},
	} {
		got := sourceCredits(nil, in, creditCensus{})
		if len(got) != 1 || got[0].name != want.name || !reflect.DeepEqual(got[0].roles, want.roles) {
			t.Errorf("sourceCredits(%q) = %+v, want [%+v]", in, got, want)
		}
	}
}

// TestTailVocabReadsAMultiByteSeparator: strings.Fields, which the tail matcher
// replaced, splits on every Unicode space. A no-break space is two bytes, and
// the matcher must step over all of it rather than start the token mid-rune.
func TestTailVocabReadsAMultiByteSeparator(t *testing.T) {
	if bare, ok := deCredentialed("Anthony Rao\u00a0PhD"); !ok || bare != "Anthony Rao" {
		t.Errorf("deCredentialed(no-break space) = %q, %v; want \"Anthony Rao\", true", bare, ok)
	}
	if bare, ok := deCredentialed("Anthony\u3000Rao\u3000Ph.\u00a0D."); !ok || bare != "Anthony Rao" {
		t.Errorf("deCredentialed(wide spaces, spaced credential) = %q, %v; want \"Anthony Rao\", true", bare, ok)
	}
	if !isSuffixPiece("Ph.\u00a0D.") {
		t.Error("isSuffixPiece does not read a spaced credential joined by a no-break space")
	}
}

// TestLibexKeepsALoneSuffixShapedNarrator: dropping a stranded suffix is only
// safe beside a real credit. A row whose ONLY narrator is "IV" keeps that credit
// as a name, exactly as it imported before the rule, rather than a recording
// with no narrator at all.
func TestLibexKeepsALoneSuffixShapedNarrator(t *testing.T) {
	_, dataDir := runLibex(t, rows(libexRow{
		asin: "B0LONEIV01", title: "A Lone Voice", authors: `{"name":"Ada Author"}`,
		narrators: `{"name":"IV"}`,
	}), false)
	if !entryExists(t, dataDir, personAddr("iv")) {
		t.Error("the row's only narrator was dropped")
	}
}

// TestCleanedCreditCarriesItsSuffixOnTheName: a rejoined ", <suffix>" chunk is
// peeled off BEFORE the role-qualifier rules run and re-appended to the cleaned
// NAME, the mechanism "X - editor Jr." already uses - so a qualifier the source
// wrote in front of the post-nominal is still read, in either spelling.
func TestCleanedCreditCarriesItsSuffixOnTheName(t *testing.T) {
	for in, want := range map[string]credit{
		"David Posen, MD":            {name: "David Posen MD"},
		"Frank Lipman, MD, PhD":      {name: "Frank Lipman MD PhD"},
		"Martin Luther King, Jr.":    {name: "Martin Luther King Jr."},
		"Listen & Live Audio, Inc.":  {name: "Listen & Live Audio Inc."},
		"Jane Doe (translator), PhD": {name: "Jane Doe PhD", roles: []string{"translator"}},
		"Jane Doe [editor], Jr.":     {name: "Jane Doe Jr.", roles: []string{"editor"}},
		"Jane Doe - translator, PhD": {name: "Jane Doe PhD", roles: []string{"translator"}},
		// Not a suffix: the comma stays part of the name, as it always did.
		"Alexandre Dumas, pere": {name: "Alexandre Dumas, pere"},
	} {
		got := sourceCredits([]string{in}, "", creditCensus{})
		if len(got) != 1 || got[0].name != want.name || !reflect.DeepEqual(got[0].roles, want.roles) {
			t.Errorf("sourceCredits(%q) = %+v, want [%+v]", in, got, want)
		}
	}
}

// TestLicensureFoldsOntoTheBareTwin keeps the rejoin from FORKING an identity:
// the split used to credit "Jane Doe, LCSW" to an existing `jane-doe` (beside a
// junk "Lcsw"), and the rejoined name must reach the same record through the
// census-backed fold - while a generational suffix never folds (a father and
// son are two people) and a legal-entity suffix is left as it is.
func TestLicensureFoldsOntoTheBareTwin(t *testing.T) {
	seen := seenAs("Jane Doe", "Acme Audio")
	census := creditCensus{anySide: seen, sameSide: seen}
	for in, want := range map[string]string{
		"Jane Doe, LCSW":   "Jane Doe",
		"Jane Doe, MBA":    "Jane Doe",
		"Jane Doe MPH":     "Jane Doe",
		"Jane Doe, Esq.":   "Jane Doe",
		"Jane Doe, Jr.":    "Jane Doe Jr.",
		"Jane Doe III":     "Jane Doe III",
		"Acme Audio, Inc.": "Acme Audio Inc.",
	} {
		got := creditNamesOf(sourceCredits([]string{in}, "", census))
		if len(got) != 1 || got[0] != want {
			t.Errorf("sourceCredits(%q) = %q, want %q", in, got, want)
		}
	}

	dataDir := t.TempDir()
	runUserImport(t, dataDir, `[{"asin":"B0LICENSE1","title_short":"First Book","author":"Jane Doe","narrated_by":"Nat Reader","language":"english","region":"US","seconds":1000}]`)
	sum := runUserImport(t, dataDir, `[{"asin":"B0LICENSE2","title_short":"Second Book","author":"Jane Doe, LCSW","narrated_by":"Nat Reader","language":"english","region":"US","seconds":1000}]`)
	if sum.NewPeople != 0 {
		t.Errorf("NewPeople = %d, want 0: the credentialed spelling forked jane-doe", sum.NewPeople)
	}
	for _, fork := range []string{"jane-doe-lcsw", "lcsw"} {
		if entryExists(t, dataDir, personAddr(fork)) {
			t.Errorf("person %q was minted beside jane-doe", fork)
		}
	}
}

// TestSiteSuffixVocabularyMatches is the drift guard on the HAND-MIRRORED twin:
// site/src/lib/import-parse.ts keeps its own SUFFIX_SPELLINGS so the /import
// preview rejoins the same pieces this package does, and a spelling added on
// one side only would make the preview name somebody the importer does not
// record. The site's twin tests pin the same cases as
// TestSplitRawNamesRejoinsASuffixPiece.
func TestSiteSuffixVocabularyMatches(t *testing.T) {
	src, err := os.ReadFile("../../site/src/lib/import-parse.ts")
	if err != nil {
		t.Fatal(err)
	}
	const begin, end = "// suffix-spellings:begin", "// suffix-spellings:end"
	text := string(src)
	i, j := strings.Index(text, begin), strings.Index(text, end)
	if i < 0 || j < i {
		t.Fatalf("site/src/lib/import-parse.ts has no %q ... %q block", begin, end)
	}
	site := map[string]bool{}
	for _, m := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(text[i:j], -1) {
		site[m[1]] = true
	}
	for s := range suffixPieceSpellings {
		if !site[s] {
			t.Errorf("suffix spelling %q is missing from the site's SUFFIX_SPELLINGS", s)
		}
	}
	for s := range site {
		if !suffixPieceSpellings[s] {
			t.Errorf("the site's SUFFIX_SPELLINGS holds %q, which suffixPieceSpellings does not", s)
		}
	}
}
