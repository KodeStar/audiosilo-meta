package importer

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
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
	// Tier 2 cuts with no census behind it, so its tail vocabulary may only
	// claim the shape the dump measured: "<narrator> for <house> Production(s)".
	for word := range tier2HouseTail {
		if !studioTail[word] {
			t.Errorf("tier2HouseTail %q is not in studioTail; tier 2 narrows that list, it does not extend it", word)
		}
	}
	for _, word := range []string{"press", "publishing", "media", "audio", "llc"} {
		if tier2HouseTail[word] {
			t.Errorf("tier2HouseTail holds %q; a publisher-shaped tail is the corporate 'X for Y Press' shape, not a credit to a production house", word)
		}
	}
}

// TestStudioTailUnattestedTiersRefuseCorporateNames is the finding that tiers 1
// and 2 fire from the PUBLIC door (CleanCreditName / SplitNames) with no census
// at all, on constructions the dump never measured. Every name here is a whole
// corporate entity that the tiers truncated into a fabricated one, and every one
// must now come back unchanged - under the most generous census possible, since
// neither tier consults one.
func TestStudioTailUnattestedTiersRefuseCorporateNames(t *testing.T) {
	intact := []string{
		// Tier 2: the corporate name's OWN name spans the "for". None of these is
		// separable from "<narrator> for <house>" by anything in the string, so
		// the guard is the tail vocabulary: a publisher-shaped tail is refused.
		"The Foundation for Economic Education Press",
		"Christian Books for Children Publishing",
		"Mothers United for Literacy Media",
		// Tier 1: a ONE-token head carries no evidence either way, and the same
		// cut that reads "Aery Talento de Voz" as a narrator reads this studio's
		// name (Spanish for "voice talent productions") as one too.
		"Producciones Talento de Voz",
	}
	for _, name := range intact {
		t.Run(name, func(t *testing.T) {
			for _, seen := range []creditSeenFunc{nil, everyFragmentSeen(name)} {
				if got, _ := stripStudioConcat(name, seen); got != name {
					t.Errorf("stripStudioConcat(%q) = %q, want it untouched", name, got)
				}
			}
		})
	}
}

// TestStudioTailOutOfModel pins the construction honest tightening does NOT
// reach, so it is a deliberate, visible miss rather than a silent one.
//
// "Cool Beans Voice Over" is a studio; "Marnye Young Voice Talent" is a
// narrator. Both are a two-token head in front of a closed role label, and the
// only thing that tells them apart is knowing which head is a person's name.
// Narrowing the role-label vocabulary until this stops cleaning would take the
// measured targets with it, so the miss is accepted: the wrong result here is a
// truncated corporate name, and the alternative is losing the tier.
func TestStudioTailOutOfModel(t *testing.T) {
	got, _ := stripStudioConcat("Cool Beans Voice Over", nil)
	if want := "Cool Beans"; got != want {
		t.Fatalf("stripStudioConcat = %q, want %q - if a new guard changed this, say so here and in tier1RoleLabel's comment", got, want)
	}
}

// TestStudioTailDashNeedsWhitespace covers the separator half of the same
// hyphenated-surname guard the ASCII dash already had: an en dash inside a
// surname is not a boundary, and only surrounding whitespace makes any dash one.
func TestStudioTailDashNeedsWhitespace(t *testing.T) {
	const name = "Marie Curie–Sklodowska Media"
	if got, _ := stripStudioConcat(name, everyFragmentSeen(name)); got != name {
		t.Errorf("stripStudioConcat(%q) = %q; a bare en dash inside a surname is not a separator", name, got)
	}
	// The same dash WITH whitespace is a separator, and still splits.
	const spaced = "Marie Curie – Sklodowska Media"
	if got, _ := stripStudioConcat(spaced, seenAs("Marie Curie")); got != "Marie Curie" {
		t.Errorf("stripStudioConcat(%q) = %q, want %q", spaced, got, "Marie Curie")
	}
}

// TestStudioTailBareTailNeedsTwoTokens pins the bare tier's tail floor: a
// one-token tail is a surname that happens to be a studio word, not an entity.
func TestStudioTailBareTailNeedsTwoTokens(t *testing.T) {
	for _, name := range []string{"Katherine Anne Press", "Walt Disney Records"} {
		t.Run(name, func(t *testing.T) {
			if got, _ := stripStudioConcat(name, everyFragmentSeen(name)); got != name {
				t.Errorf("stripStudioConcat(%q) = %q, want it untouched", name, got)
			}
		})
	}
	// The two-token tail the measurement is built on still cuts.
	if got, _ := stripStudioConcat("Alex Hyde-White Punch Audio", seenAs("Alex Hyde-White", "Punch Audio")); got != "Alex Hyde-White" {
		t.Errorf("the measured bare target stopped cleaning: %q", got)
	}
}

// TestStudioTailWalkDownKeepsTheFullPrefix pins tier 1's walk-down floor at the
// top rather than one token short of it: the full attested name has to be a
// candidate, or an attested shorter prefix truncates it.
func TestStudioTailWalkDownKeepsTheFullPrefix(t *testing.T) {
	const name = "Mary Jane Watson Voice Talent"
	seen := seenAs("Mary Jane Watson", "Mary Jane")
	if got, _ := stripStudioConcat(name, seen); got != "Mary Jane Watson" {
		t.Errorf("stripStudioConcat(%q) = %q, want %q", name, got, "Mary Jane Watson")
	}
}

// TestStudioTailSeparatorKeepsTheRole is the role the dash tier used to eat: the
// source stacked a qualifier and a studio behind one separator, so the studio
// strip removed both. The identity was right either way; the credit was lost.
func TestStudioTailSeparatorKeepsTheRole(t *testing.T) {
	cases := []struct {
		credit string
		want   string
		roles  []string
	}{
		{"Jane Doe - translator Punch Audio", "Jane Doe", []string{model.RoleTranslator}},
		{"Jane Doe (translator Punch Audio)", "Jane Doe", []string{model.RoleTranslator}},
		{"Jane Doe/editor and translator Punch Audio", "Jane Doe", []string{model.RoleEditor, model.RoleTranslator}},
		// A tail that states no listed role states none: the name is cleaned
		// exactly as before and nothing is invented.
		{"Jane Doe - Punch Audio", "Jane Doe", nil},
	}
	for _, tc := range cases {
		t.Run(tc.credit, func(t *testing.T) {
			got, roles := creditWithRoles(tc.credit, seenAs("Jane Doe"))
			if got != tc.want {
				t.Errorf("cleaned = %q, want %q", got, tc.want)
			}
			if !slices.Equal(roles, tc.roles) {
				t.Errorf("roles = %v, want %v", roles, tc.roles)
			}
		})
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
		// Tier 1: a closed multi-word role label needs no attestation, but the
		// person half is floored at two tokens like every other tier's - see
		// minPersonTokens and TestStudioTailUnattestedTiersRefuseCorporateNames.
		{"tier1 refuses a one-token person", "Aery Talento de Voz", nil, "Aery Talento de Voz"},
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
			if got, _ := stripStudioConcat(tc.credit, tc.seen); got != tc.want {
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
			if got, _ := stripStudioConcat(name, everyFragmentSeen(name)); got != name {
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
			if got, _ := stripStudioConcat(name, seenAs("Alex Hyde-White", "Punch Audio")); got != name {
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
	if got, _ := stripStudioConcat(name, nil); got != name {
		t.Errorf("with nothing attested: got %q, want the name untouched", got)
	}
	if got, _ := stripStudioConcat(name, seenAs("Zachary Webber", "Punch Audio")); got != "Zachary Webber" {
		t.Errorf("with both halves attested: got %q, want %q", got, "Zachary Webber")
	}
}

// TestStudioTailFlaggedSpecimens covers the three specimens the credit-quality
// report singled out, including the one it deliberately leaves alone.
func TestStudioTailFlaggedSpecimens(t *testing.T) {
	if got, _ := stripStudioConcat("Alex Hyde-White Punch Audio", seenAs("Alex Hyde-White", "Punch Audio")); got != "Alex Hyde-White" {
		t.Errorf("bare specimen = %q, want %q", got, "Alex Hyde-White")
	}
	// The role-label specimen is a ONE-token head, so it is now a no-op and a
	// hand-remediation item - which is what the credit-quality report's
	// sequencing already listed "confirm aery-talento-de-voz -> aery" as.
	if got, _ := stripStudioConcat("Aery Talento de Voz", nil); got != "Aery Talento de Voz" {
		t.Errorf("role-label specimen = %q, want it imported as-is", got)
	}
	// "A.J Watkins" has zero credits anywhere in the 1.13M-book dump and
	// "Books.Audio" has zero as a standalone credit, so no attestation-based
	// rule may cut this string. It imports as the source spelled it and is a
	// hand-remediation item - pinned here so a future widening of the rule has
	// to change this line deliberately.
	const watkins = "A.J Watkins of Books.Audio"
	if got, _ := stripStudioConcat(watkins, everyFragmentSeen(watkins)); got != watkins {
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

// TestSingleWordStudioBrands is the measured exception to minTailTokens: a
// coined brand concatenated onto a person's name with no boundary marker and no
// second tail token. Nuanxed is the whole list today - 33 distinct dump credit
// names, 94 author-side books and 1 narrator credit, every one of them the
// brand.
//
// The negative controls are the load-bearing half. The floor the exception lifts
// exists to protect real people whose SURNAME is a studio word, so the tests
// that matter are the ones proving those people are still protected: the
// exception must reach exactly the listed brand and nothing that merely looks
// like one.
func TestSingleWordStudioBrands(t *testing.T) {
	// The whole list must be canonical, or an entry can never match.
	for word := range singleWordStudioBrands {
		if got := foldCredit(word); got != word {
			t.Errorf("singleWordStudioBrands key %q is not canonical (foldCredit gives %q)", word, got)
		}
		if len(strings.Fields(word)) != 1 {
			t.Errorf("singleWordStudioBrands key %q is not a single token", word)
		}
	}
	// A brand is not a studio-tail word: the bare tier reaches it through its
	// own branch, and putting it in studioTail would widen the separator and
	// connector tiers with no measured target behind it.
	for word := range singleWordStudioBrands {
		if studioTail[word] {
			t.Errorf("singleWordStudioBrands %q is also in studioTail; the exception is meant to be the ONLY way a one-token tail is reached", word)
		}
	}

	cases := []struct {
		name   string
		credit string
		seen   creditSeenFunc
		want   string
	}{
		// The measured target. The person half is attested; the tail is not,
		// and does not need to be - the list is its evidence.
		{"bare brand tail, person attested", "Adriana Uribe Nuanxed", seenAs("Adriana Uribe"), "Adriana Uribe"},
		{"the tail needs no attestation of its own", "Peter Breum Nuanxed", seenAs("Peter Breum"), "Peter Breum"},
		{"a longer person half is kept whole", "Trine V. Ipsen Nuanxed", seenAs("Trine V. Ipsen"), "Trine V. Ipsen"},

		// The person half is still census-gated: the list says what the TAIL is,
		// not that the head is a person.
		{"refuses an unattested person half", "Adriana Uribe Nuanxed", nil, "Adriana Uribe Nuanxed"},
		{"refuses a one-token person half", "Nuanxed Nuanxed", seenAs("Nuanxed"), "Nuanxed Nuanxed"},

		// NEGATIVE CONTROLS. A person legitimately surnamed with a brand-shaped
		// word must not split, and the two-token floor is what protects them.
		// "Press", "Audio" and "Records" are all real dump surnames, and all
		// three stay whole however generous the census is.
		{"a real surname that is a studio word", "Katherine Anne Press", everyFragmentSeen("Katherine Anne Press"), "Katherine Anne Press"},
		{"a real surname that is a studio word (audio)", "Marcus Lee Audio", everyFragmentSeen("Marcus Lee Audio"), "Marcus Lee Audio"},
		{"a real corporate name ending in a studio word", "Walt Disney Records", everyFragmentSeen("Walt Disney Records"), "Walt Disney Records"},
		// The constructed control the exception is really aimed at: a person
		// whose surname IS a listed brand word. Nuanxed is on the list precisely
		// because the dump has no such person - if one ever appears, this is the
		// case that has to change, deliberately, together with the measurement.
		{"a brand word is not reached as a person's own surname", "Nuanxed", everyFragmentSeen("Nuanxed"), "Nuanxed"},
		// An unlisted coined brand gets no exception: it is one token, so the
		// floor still refuses it.
		{"an unlisted brand keeps the two-token floor", "Adriana Uribe Voxtura", everyFragmentSeen("Adriana Uribe Voxtura"), "Adriana Uribe Voxtura"},
		// The exception is BARE-tier only. A boundary marker sends the string to
		// the separator and connector tiers, whose vocabularies are unchanged.
		{"the exception does not reach tier 2", "Anders Larsen for Nuanxed", everyFragmentSeen("Anders Larsen for Nuanxed"), "Anders Larsen for Nuanxed"},
		{"the exception does not reach a separator tier", "Nuanxed - Karine Marques", everyFragmentSeen("Nuanxed - Karine Marques"), "Nuanxed - Karine Marques"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := stripStudioConcat(tc.credit, tc.seen); got != tc.want {
				t.Errorf("stripStudioConcat(%q) = %q, want %q", tc.credit, got, tc.want)
			}
		})
	}
}

// TestDirectorQualifierStrips pins the two spellings of the director qualifier
// as STRIP-ONLY: the name loses the qualifier and no credit is emitted, because
// direction is a production role the credit vocabulary excludes on purpose.
func TestDirectorQualifierStrips(t *testing.T) {
	cases := []struct {
		credit string
		want   string
	}{
		{"Alison Belle Bews - director", "Alison Belle Bews"},
		{"Sir Peter Hall - Director", "Sir Peter Hall"},
		{"Cassandra de Cuir – director", "Cassandra de Cuir"}, // en dash separator
		{"Gabrielle de Cuir -director", "Gabrielle de Cuir"},  // missing space
		{"Jean Dupont - directeur", "Jean Dupont"},
	}
	for _, tc := range cases {
		name, roles := CreditWithRoles(tc.credit)
		if name != tc.want {
			t.Errorf("CreditWithRoles(%q) name = %q, want %q", tc.credit, name, tc.want)
		}
		if len(roles) != 0 {
			t.Errorf("CreditWithRoles(%q) roles = %v, want none: direction has no home in the credit vocabulary", tc.credit, roles)
		}
	}
	// The word inside a NAME is untouched - only the trailing qualifier form
	// strips, which is the whole reason roleQualifiers is a closed list.
	if name, _ := CreditWithRoles("Ann Director"); name != "Ann Director" {
		t.Errorf("CreditWithRoles(%q) = %q, want it untouched", "Ann Director", name)
	}
}
