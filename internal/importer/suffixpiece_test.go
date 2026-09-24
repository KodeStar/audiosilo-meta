package importer

import (
	"reflect"
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

// TestSplitNamesRejoinsASuffixPiece is issue #2320: a library export's comma
// list split "David Posen, MD" and "Anthony Rao, Ph.D." each into two credits.
func TestSplitNamesRejoinsASuffixPiece(t *testing.T) {
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
		// never a person.
		{"PhD, Jane Roe", []string{"Jane Roe"}},
		{"MD", nil},
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
			if got := SplitNames(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("SplitNames(%q) = %q, want %q", c.in, got, c.want)
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
	if want := []string{"Anthony Rao", "Martin Luther King, Jr."}; !reflect.DeepEqual(got, want) {
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
