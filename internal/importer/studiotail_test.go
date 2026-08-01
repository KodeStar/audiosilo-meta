package importer

import (
	"strings"
	"testing"
)

// seenAs builds the creditSeenFunc shape the planner produces (a slug set), from
// the names a test wants to count as independently credited.
func seenAs(names ...string) creditSeenFunc {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[Slugify(n)] = true
	}
	return func(name string) bool { return set[Slugify(name)] }
}

// TestStudioTailVocabulary is the canonicalization self-test: a vocabulary entry
// that is not in its own comparison form can never match, and the two guards the
// measurement forced (multi-word role labels only, legal suffixes drawn from the
// studio list) are structural claims worth pinning rather than trusting.
func TestStudioTailVocabulary(t *testing.T) {
	for word := range studioTail {
		if got := foldCredit(word); got != word {
			t.Errorf("studioTail key %q is not canonical (foldCredit gives %q); it can never match", word, got)
		}
		if len(strings.Fields(word)) != 1 {
			t.Errorf("studioTail key %q is not a single token", word)
		}
	}
	for _, label := range roleLabelTails {
		if got := foldCredit(label); got != label {
			t.Errorf("roleLabelTails entry %q is not canonical (foldCredit gives %q)", label, got)
		}
		if len(strings.Fields(label)) < 2 {
			t.Fatalf("roleLabelTails entry %q is a SINGLE word; every single-word label is a real surname in the dump (John Voce, Brianna Vox, Sarah Sprecher) and must stay out", label)
		}
	}
	// The surname-shaped labels the measurement threw out, and "voice" (whose
	// 143k credits are the AI-voice family), must be in neither vocabulary.
	for _, word := range []string{"vox", "voz", "voce", "sprecher", "stimme", "narrator", "narratore", "voice"} {
		if studioTail[word] {
			t.Errorf("studioTail holds %q; it is a real surname or the AI-voice family, not an entity marker", word)
		}
	}
	for suffix := range corporateLegalSuffix {
		if !studioTail[suffix] {
			t.Errorf("corporateLegalSuffix %q is not in studioTail; the bare tier excludes from a list it is not on", suffix)
		}
		if bareStudioTail()[suffix] {
			t.Errorf("bareStudioTail still holds the legal suffix %q", suffix)
		}
	}
}

// TestStripStudioConcatTiers is the rule's passing table, one case per tier and
// per separator shape, each with the attestation its tier requires.
func TestStripStudioConcatTiers(t *testing.T) {
	cases := []struct {
		name   string
		credit string
		seen   creditSeenFunc
		want   string
	}{
		// Tier 1: a closed multi-word role label needs no attestation, and the
		// person half may be a single token.
		{"tier1 role label", "Aery Talento de Voz", nil, "Aery"},
		{"tier1 english label", "Marnye Young Voice Talent", nil, "Marnye Young"},
		{"tier1 walks down to an attested prefix", "Ken Clark Smooth Voice Over", seenAs("Ken Clark"), "Ken Clark"},
		{"tier1 keeps the whole prefix when nothing is attested", "Ken Clark Smooth Voice Over", nil, "Ken Clark Smooth"},

		// Tier 2: the "for" connector, no attestation.
		{"tier2 for", "Ann Russek for HotGhost Productions", nil, "Ann Russek"},
		{"tier2 for, singular", "Clancy Jones for HG Production", nil, "Clancy Jones"},
		{"tier2 refuses a one-token person", "Made for Success Publishing", nil, "Made for Success Publishing"},

		// Tier 3: attested splits.
		{"tier3 of", "Robert Wrenlock of Island Audio", seenAs("Robert Wrenlock"), "Robert Wrenlock"},
		{"tier3 paren", "Stefan Rudnicki (Skyboat Media)", seenAs("Stefan Rudnicki"), "Stefan Rudnicki"},
		{"tier3 bracket", "Duncan White [Scifi Publishing]", seenAs("Duncan White"), "Duncan White"},
		{"tier3 dash", "Alex Hyde-White - Punch Audio", seenAs("Alex Hyde-White"), "Alex Hyde-White"},
		{"tier3 slash", "Eileen Rizzo/Eye Hear Voices", seenAs("Eileen Rizzo"), "Eileen Rizzo"},
		{"tier3 pipe", "Eileen Rizzo|Eye Hear Voices", seenAs("Eileen Rizzo"), "Eileen Rizzo"},
		{"tier3 bare, both halves attested", "Alex Hyde-White Punch Audio", seenAs("Alex Hyde-White", "Punch Audio"), "Alex Hyde-White"},
		{"tier3 bare, longest attested person half wins", "Kelley Hazen Storyteller Productions", seenAs("Kelley Hazen", "Storyteller Productions"), "Kelley Hazen"},

		// Refusals inside tier 3.
		{"tier3 of refuses an unattested person", "Robert Wrenlock of Island Audio", nil, "Robert Wrenlock of Island Audio"},
		{"tier3 bare refuses an unattested tail", "Alex Hyde-White Punch Audio", seenAs("Alex Hyde-White"), "Alex Hyde-White Punch Audio"},
		{"tier3 bare refuses an unattested person", "Alex Hyde-White Punch Audio", seenAs("Punch Audio"), "Alex Hyde-White Punch Audio"},
		{"tier3 bare refuses a legal suffix", "Elle McNicoll Ltd", seenAs("Elle McNicoll", "Ltd"), "Elle McNicoll Ltd"},
		{"a tail that is not a studio word is not a tail", "Jasper Thorne Island Cottage", seenAs("Jasper Thorne", "Island Cottage"), "Jasper Thorne Island Cottage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripStudioConcat(tc.credit, tc.seen); got != tc.want {
				t.Errorf("stripStudioConcat(%q) = %q, want %q", tc.credit, got, tc.want)
			}
		})
	}
}

// TestStudioTailFalsePositives is the load-bearing half. Every name here is a
// real credit in the dump that a lazier rule ate, and every one is tested with
// the MOST generous attestation possible (every proper prefix and suffix of the
// name counts as attested) - so a case that survives here survives whatever the
// catalogue happens to hold.
func TestStudioTailFalsePositives(t *testing.T) {
	intact := []string{
		// Real surnames that are studio words. The two-token floor is what saves
		// them: there is no person half of two tokens to cut.
		"John Voce",
		"Barry Press",
		"Noah Press",
		"Katherine Press",
		"Brianna Vox",
		"Steven Vox",
		"Sarah Sprecher",
		"Diverse Sprecher",
		// Whole corporate entities that merely END in a studio word.
		"Innovative Language Learning LLC",
		"Alcoholics Anonymous World Services Inc.",
		"Radio Spirits Inc.",
		"Random House Inc.",
		"Walt Disney Company Ltd.",
		"National Public Radio Inc.",
		"Noodle Juice Ltd",
		// "of" inside a corporate name, where the connector's own shape is the
		// guard: the person half is a single token, which no tier will cut on.
		"Way of Life Press",
		"Acts of The Word Productions",
		"Circle Of Insight Productions",
		// A trailing conjunction must never be left dangling, so the whole
		// string stays rather than becoming "Bob Carter &".
		"Bob Carter & the Neighborhood Studio",
		// Already-clean names, including the hyphenated surname a dash regexp
		// without a whitespace requirement cut in half.
		"Alex Hyde-White",
		"Punch Audio",
		"Skyboat Media",
	}
	for _, name := range intact {
		t.Run(name, func(t *testing.T) {
			if got := stripStudioConcat(name, everyFragmentSeen(name)); got != name {
				t.Errorf("stripStudioConcat(%q) = %q, want it untouched", name, got)
			}
		})
	}
}

// TestStudioTailCorporateOfNamesNeedAttestation covers the rest of the "of"
// family - the corporate names whose first half happens to be two tokens
// ("Square Root of Squid Publishing", "48 Laws of Power Studio"). Nothing
// STRUCTURAL saves these: the guard the measurement chose for them is the
// attestation itself, which is why "of" sits in tier 3 and "for" does not.
// They are intact against every attestation universe the importer can actually
// build, because "Square Root" and "48 Laws" are not credits - the dump has
// none, and neither does the catalogue.
func TestStudioTailCorporateOfNamesNeedAttestation(t *testing.T) {
	corporate := []string{
		"The Staff of Entrepreneur Media",
		"Square Root of Squid Publishing",
		"48 Laws of Power Studio",
		"International Service Organization of Sexual Compulsives Anonymous Inc.",
	}
	for _, name := range corporate {
		t.Run(name, func(t *testing.T) {
			// The realistic universe: the catalogue holds people, not the
			// fragments of a corporate name.
			if got := stripStudioConcat(name, seenAs("Alex Hyde-White", "Punch Audio")); got != name {
				t.Errorf("stripStudioConcat(%q) = %q, want it untouched", name, got)
			}
		})
	}
}

// everyFragmentSeen counts every contiguous fragment of a name as a credit -
// the worst case a real census could ever present.
func everyFragmentSeen(name string) creditSeenFunc {
	tokens := strings.Fields(name)
	var fragments []string
	for i := range tokens {
		for j := i + 1; j <= len(tokens); j++ {
			fragments = append(fragments, strings.Join(tokens[i:j], " "))
		}
	}
	return seenAs(fragments...)
}

// TestStudioTailAttestationGate pins that the SAME string is decided by the
// attestation universe alone, which is the property the tier-3 design rests on.
func TestStudioTailAttestationGate(t *testing.T) {
	const name = "Zachary Webber Punch Audio"
	if got := stripStudioConcat(name, nil); got != name {
		t.Errorf("with nothing attested: got %q, want the name untouched", got)
	}
	if got, want := stripStudioConcat(name, seenAs("Zachary Webber", "Punch Audio")), "Zachary Webber"; got != want {
		t.Errorf("with both halves attested: got %q, want %q", got, want)
	}
}

// TestStudioTailFlaggedSpecimens covers the three specimens the credit-quality
// report singled out, including the one it deliberately leaves alone.
func TestStudioTailFlaggedSpecimens(t *testing.T) {
	if got, want := stripStudioConcat("Alex Hyde-White Punch Audio", seenAs("Alex Hyde-White", "Punch Audio")), "Alex Hyde-White"; got != want {
		t.Errorf("bare specimen = %q, want %q", got, want)
	}
	if got, want := stripStudioConcat("Aery Talento de Voz", nil), "Aery"; got != want {
		t.Errorf("role-label specimen = %q, want %q", got, want)
	}
	// "A.J Watkins" has zero credits anywhere in the 1.13M-book dump and
	// "Books.Audio" has zero as a standalone credit, so no attestation-based
	// rule may cut this string. It imports as the source spelled it and is a
	// hand-remediation item - pinned here so a future widening of the rule has
	// to change this line deliberately.
	const watkins = "A.J Watkins of Books.Audio"
	if got := stripStudioConcat(watkins, everyFragmentSeen(watkins)); got != watkins {
		t.Errorf("A.J Watkins specimen = %q, want it imported as-is", got)
	}
}

// TestCreditWithRolesAppliesTheStudioTail checks the rule really is a step of
// the shared fixpoint - it composes with the role-qualifier strip in one pass,
// and the public entry point (no attestation universe) still gets the two
// self-evidencing tiers.
func TestCreditWithRolesAppliesTheStudioTail(t *testing.T) {
	got, roles := creditWithRoles("Ann Russek for HotGhost Productions - translator", nil)
	if want := "Ann Russek"; got != want {
		t.Errorf("cleaned = %q, want %q", got, want)
	}
	if len(roles) != 1 || roles[0] != "translator" {
		t.Errorf("roles = %v, want [translator]", roles)
	}
	if got := CleanCreditName("Alex Hyde-White Punch Audio"); got != "Alex Hyde-White Punch Audio" {
		t.Errorf("CleanCreditName applied an attestation-gated tier with no universe: %q", got)
	}
}

// TestLibexImportSplitsStudioConcatenation is the end-to-end proof, over the
// two rows the concatenation actually appears as: one row credits the studio
// alone (which is what attests it) and one credits the narrator alone, and a
// third spells them concatenated. The concatenated row must resolve onto the
// narrator, and the STUDIO must keep the person record of its own.
func TestLibexImportSplitsStudioConcatenation(t *testing.T) {
	const rows = `{"asin":"B0TEST0001","title":"Alpha","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-01 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Alex Hyde-White"}],"genres":[],"series":[]}
{"asin":"B0TEST0002","title":"Beta","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-02 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Punch Audio"}],"genres":[],"series":[]}
{"asin":"B0TEST0003","title":"Gamma","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-03 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Alex Hyde-White Punch Audio"}],"genres":[],"series":[]}`

	_, dataDir := runLibex(t, rows, false)

	if !entryExists(t, dataDir, "people/al/alex-hyde-white.json") {
		t.Error("the narrator has no person record")
	}
	// The studio is a legitimate credit of its own and keeps its record; the
	// concatenation must not have become a third identity.
	if !entryExists(t, dataDir, "people/pu/punch-audio.json") {
		t.Error("the studio's own person record was not kept")
	}
	if entryExists(t, dataDir, "people/al/alex-hyde-white-punch-audio.json") {
		t.Error("the concatenation minted a person record of its own")
	}

	recs := recSlugsOf(t, dataDir, "gamma")
	if len(recs) != 1 {
		t.Fatalf("work gamma has %d recordings, want 1", len(recs))
	}
	var rec outRecording
	readEntity(t, dataDir, recAddr("gamma", recs[0]), &rec)
	if len(rec.Narrators) != 1 || rec.Narrators[0] != "alex-hyde-white" {
		t.Errorf("narrators = %v, want [alex-hyde-white]", rec.Narrators)
	}
}
