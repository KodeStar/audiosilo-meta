package importer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestCreditWithRoles is the qualifier -> role table's behaviour test: the name
// is cleaned exactly as it always was, and the qualifier that was stripped now
// comes back as schema roles.
func TestCreditWithRoles(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantName  string
		wantRoles []string
	}{
		// The ordinary case: no qualifier at all, which is ~95% of credits.
		{"plain name", "Ada Mapmaker", "Ada Mapmaker", nil},

		// One qualifier, one role.
		{"english translator", "J. Kharkova - translator", "J. Kharkova", []string{model.RoleTranslator}},
		{"english editor", "Ann Editor - Editor", "Ann Editor", []string{model.RoleEditor}},
		{"english introduction", "Val Kornosenko - introduction", "Val Kornosenko", []string{model.RoleIntroduction}},
		{"english contributor", "Cal Helper - Contributor", "Cal Helper", []string{model.RoleContributor}},
		{"misspelled foreword", "Fay Writer - Foreward", "Fay Writer", []string{model.RoleForeword}},

		// Every language variant resolves to the SAME role slug - that is the
		// whole point of a controlled vocabulary over a scraped string.
		{"german translator", "Uwe Wort - Übersetzer", "Uwe Wort", []string{model.RoleTranslator}},
		// Spelled decomposed ("U" + U+0308), which is what a Mac filesystem or an
		// older exporter emits; the lookup re-composes to NFC before matching.
		{"german translator, decomposed", "Uwe Wort - U\u0308bersetzer", "Uwe Wort", []string{model.RoleTranslator}},
		{"german translator, ascii-folded", "Uwe Wort - Ubersetzer", "Uwe Wort", []string{model.RoleTranslator}},
		{"italian translator", "Gio Parola - Traduttore", "Gio Parola", []string{model.RoleTranslator}},
		{"french translator", "Luc Mot - Traducteur", "Luc Mot", []string{model.RoleTranslator}},
		{"portuguese translator", "Rui Letra - Tradução", "Rui Letra", []string{model.RoleTranslator}},
		{"romanian translator", "Ion Cuvant - Traducător", "Ion Cuvant", []string{model.RoleTranslator}},
		{"german editor", "Kai Buch - Herausgeber", "Kai Buch", []string{model.RoleEditor}},
		{"italian editor", "Rita Libro - Curatore", "Rita Libro", []string{model.RoleEditor}},
		{"french illustrator", "Zoe Trait - Illustratrice", "Zoe Trait", []string{model.RoleIllustrator}},
		{"italian afterword", "Nia Fine - Postfazione", "Nia Fine", []string{model.RoleAfterword}},
		{"french preface", "Eve Debut - Préface", "Eve Debut", []string{model.RolePreface}},
		{"german adaptation", "Jan Fassung - Bearbeitung", "Jan Fassung", []string{model.RoleAdaptation}},

		// Combined qualifiers: one string, two roles, two credit entries.
		{"combined with and", "Sam Both - Editor and Translator", "Sam Both", []string{model.RoleEditor, model.RoleTranslator}},
		{"combined with slash", "Sam Both - translator/editor", "Sam Both", []string{model.RoleEditor, model.RoleTranslator}},
		{"combined bare", "Sam Both - editor translator", "Sam Both", []string{model.RoleEditor, model.RoleTranslator}},
		{"combined editor introduction", "Sam Both - Editor Introduction", "Sam Both", []string{model.RoleEditor, model.RoleIntroduction}},
		// "author" is not a credit role - the work's authors list already says
		// it - so only the editor half becomes a credit.
		{"author/editor keeps only the modeled half", "Sam Both - author/editor", "Sam Both", []string{model.RoleEditor}},

		// Stacked qualifiers converge through the fixpoint, and the repeated role
		// is stated once.
		{"doubled qualifier", "Dan Veksler - Translator - translator", "Dan Veksler", []string{model.RoleTranslator}},
		{"two different qualifiers", "Pat Two - Editor - translator", "Pat Two", []string{model.RoleEditor, model.RoleTranslator}},

		// A credential title stacked onto the role is trimmed before lookup.
		{"credential suffix", "Dr Reed - Introduction M.D.", "Dr Reed", []string{model.RoleIntroduction}},
		{"repeated credential suffix", "Dr Reed - Introduction MD MD", "Dr Reed", []string{model.RoleIntroduction}},
		{"generational suffix", "Ed Ward - Editor Jr.", "Ed Ward", []string{model.RoleEditor}},
		{"credential on foreword", "Dr Reed - Foreword PhD", "Dr Reed", []string{model.RoleForeword}},

		// Stripped, but NOT a modeled role: the name is cleaned exactly as before
		// and nothing is stated. This is the facts-only boundary.
		{"narrator qualifier states nothing", "Bea Reader - Narrator", "Bea Reader", nil},
		{"compilation qualifier states nothing", "Cal Gather - compilation", "Cal Gather", nil},
		{"creator qualifier states nothing", "Stan Lee - creator", "Stan Lee", nil},
		{"producer qualifier states nothing", "Pia Maker - producer", "Pia Maker", nil},

		// The prefix credit is not a role either, and never was.
		{"prefix credit", "Created by Stan Lee", "Stan Lee", nil},

		// Degenerate input: stripping would leave nothing, so nothing is stripped
		// and nothing is stated.
		{"qualifier only", "- translator", "- translator", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotName, gotRoles := CreditWithRoles(c.in)
			if gotName != c.wantName {
				t.Errorf("name = %q, want %q", gotName, c.wantName)
			}
			if !reflect.DeepEqual(gotRoles, c.wantRoles) {
				t.Errorf("roles = %#v, want %#v", gotRoles, c.wantRoles)
			}
			// CleanCreditName is the name-only view of the same pass, so the two
			// can never disagree about what a person is called.
			if plain := CleanCreditName(c.in); plain != gotName {
				t.Errorf("CleanCreditName = %q but CreditWithRoles said %q", plain, gotName)
			}
		})
	}
}

// TestDoNotMapJunkIsNeitherStrippedNorARole is the violating half of the
// vocabulary: every string below matched the trailing-qualifier regex when the
// dump was measured, and NONE of them is a credit role. Each must leave the name
// exactly as it arrived and state nothing - eating one would silently mangle a
// real person's name across a 142k-record seed.
func TestDoNotMapJunkIsNeitherStrippedNorARole(t *testing.T) {
	junk := []string{
		// Promotional text the scrape captured after a hyphen.
		"Some Author - Extra 20% off (save up to 40%) all orders",
		"Some Author - save up to 80% off",
		// An academic affiliation, not a role.
		"Some Author - Universidad de Navarra",
		// A collective marker, not a person's role.
		"Various - Diverse Forfattere",
		// A co-author list that uses " - " as its separator: the tail is a NAME.
		"Some Author - Feng Menglong",
		"Some Author - M. C. Beaton",
		"Some Author - Smith",
		// A production company appended to a narrator credit.
		"Some Narrator - Bella Vox Productions",
		"Some Narrator - Second Chapter Creative LLC",
		// A self-promotional bio fragment.
		"Some Author - The Anxiety Therapist for Eldest Daughters",
		"Some Author - Welt der Meditation",
		// A military-retired suffix.
		"Some Author - Ret.",
		// The ambiguous connector the vocabulary has always excluded.
		"Some Author - with",
		// A credential with no role in front of it stays whole: trimming it would
		// leave nothing to match anyway.
		"Some Author - M.D.",
		// A surname that happens to end in a credential-shaped token.
		"Some Author - Smith Jr.",
	}
	for _, in := range junk {
		t.Run(in, func(t *testing.T) {
			gotName, gotRoles := CreditWithRoles(in)
			if gotName != in {
				t.Errorf("name was altered: %q -> %q", in, gotName)
			}
			if gotRoles != nil {
				t.Errorf("junk qualifier stated roles %v", gotRoles)
			}
		})
	}
}

// TestTrimCredentialTitles covers the helper directly, including the guard that
// keeps it from ever returning an empty qualifier.
func TestTrimCredentialTitles(t *testing.T) {
	cases := map[string]string{
		"introduction m.d.":           "introduction",
		"introduction ph.d.":          "introduction",
		"introduction md md":          "introduction",
		"introduction jr. m.d. ph.d.": "introduction",
		"editor ph.d. ph.d.":          "editor",
		"foreword phd":                "foreword",
		"translator":                  "translator",
		"md":                          "md", // nothing but a credential: left whole
		"jr.":                         "jr.",
		"smith jr.":                   "smith", // trims, but "smith" is not a role
		"editor  and   translator":    "editor and translator",
	}
	for in, want := range cases {
		if got := trimCredentialTitles(in); got != want {
			t.Errorf("trimCredentialTitles(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRoleQualifiersMapIntoSchemaEnum is the drift guard between the importer's
// qualifier table and the contract: every role the table can emit must be a
// value schema/common.schema.json's credit_role enum allows, or the seed would
// write records that fail metacheck.
func TestRoleQualifiersMapIntoSchemaEnum(t *testing.T) {
	allowed := schemaDefEnum(t, "credit_role")
	for qualifier, roles := range roleQualifiers {
		for _, role := range roles {
			if !allowed[role] {
				t.Errorf("qualifier %q maps to %q, which is not in the credit_role enum", qualifier, role)
			}
		}
		// A mapped qualifier's roles must be sorted and distinct: they land in a
		// credits list whose ordering is part of a deterministic import.
		for i := 1; i < len(roles); i++ {
			if roles[i] <= roles[i-1] {
				t.Errorf("qualifier %q roles are not sorted/distinct: %v", qualifier, roles)
			}
		}
	}
}

// TestLibexCreditsLandEndToEnd is the whole point of the change, proved through
// a real import: a libex row whose author credit carries a role qualifier lands
// as a work credit, the person stays an ordinary author, and the tree the run
// wrote validates.
//
// The combined qualifier is what makes credits a LIST of pairs: one person, two
// roles, two entries.
func TestLibexCreditsLandEndToEnd(t *testing.T) {
	export := `[{
		"asin":"B0CRED0001","title":"A Translated Book","region":"us","language":"english",
		"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
		"authors":[
			{"name":"Original Author"},
			{"name":"Tessa Word - Translator"},
			{"name":"Ed Both - Editor and Introduction"},
			{"name":"Plain Contributor - Diverse Forfattere"}
		],
		"narrators":[{"name":"Bea Reader - Introduction"}]
	}]`
	sum, dataDir := runLibex(t, export, false)

	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}

	var work struct {
		Authors []string       `json:"authors"`
		Credits []model.Credit `json:"credits"`
	}
	readEntity(t, dataDir, "works/at/a-translated-book/work.json", &work)

	// The junk-qualified credit keeps its whole name (and so its whole slug):
	// "Diverse Forfattere" is not a role and must not be eaten.
	wantAuthors := []string{"original-author", "tessa-word", "ed-both", "plain-contributor-diverse-forfattere"}
	if !reflect.DeepEqual(work.Authors, wantAuthors) {
		t.Errorf("authors = %v, want %v", work.Authors, wantAuthors)
	}

	// Sorted by (person, role), which is what makes a re-import byte-identical.
	wantCredits := []model.Credit{
		{Person: "ed-both", Role: model.RoleEditor},
		{Person: "ed-both", Role: model.RoleIntroduction},
		{Person: "tessa-word", Role: model.RoleTranslator},
	}
	if !reflect.DeepEqual(work.Credits, wantCredits) {
		t.Errorf("credits = %+v, want %+v", work.Credits, wantCredits)
	}
	if sum.Credits != 3 {
		t.Errorf("Summary.Credits = %d, want 3", sum.Credits)
	}

	// The narrator's "- Introduction" qualifier is stripped for the name (so the
	// person is "Bea Reader") but states NOTHING: a recording-level qualifier is
	// not evidence about the work. See rowAuthorCredits.
	for _, c := range work.Credits {
		if c.Person == "bea-reader" {
			t.Errorf("a narrator-side qualifier became a work credit: %+v", c)
		}
	}
	if !entryExists(t, dataDir, "people/be/bea-reader.json") {
		t.Error("narrator person was not created under its cleaned name")
	}
}

// TestLibexCreditsOmittedWhenNoRoleStated is the negative fixture: an import
// whose credits carry no role qualifier writes no credits member at all, so the
// field stays absent rather than present-and-empty.
func TestLibexCreditsOmittedWhenNoRoleStated(t *testing.T) {
	export := `[{
		"asin":"B0CRED0002","title":"A Plain Book","region":"us","language":"english",
		"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
		"authors":[{"name":"Original Author"}],
		"narrators":[{"name":"Bea Reader"}]
	}]`
	sum, dataDir := runLibex(t, export, false)
	if sum.Credits != 0 {
		t.Errorf("Summary.Credits = %d, want 0", sum.Credits)
	}
	if raw := rawEntity(t, dataDir, "works/ap/a-plain-book/work.json"); strings.Contains(string(raw), "credits") {
		t.Errorf("credits member emitted for a work with no stated role: %s", raw)
	}
}

// TestLibexEnrichFillsCreditsOnlyWhenAbsent covers the enrichment posture: a
// matched row fills credits onto a work that has none, never over a work that
// already lists them, and a second identical run changes nothing.
func TestLibexEnrichFillsCreditsOnlyWhenAbsent(t *testing.T) {
	row := `[{
		"asin":"B0CRED0003","title":"Enrich Me","region":"us","language":"english",
		"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
		"authors":[{"name":"Original Author"},{"name":"Tessa Word - Translator"}],
		"narrators":[{"name":"Bea Reader"}]
	}]`

	t.Run("fills an absent credits member", func(t *testing.T) {
		dataDir := t.TempDir()
		seedTree(t, dataDir, enrichSeed(""))
		sum := runEnrich(t, dataDir, row, false)
		if sum.Credits != 1 {
			t.Fatalf("Summary.Credits = %d, want 1", sum.Credits)
		}
		var work struct {
			Credits []model.Credit `json:"credits"`
		}
		readEntity(t, dataDir, "works/en/enrich-me/work.json", &work)
		want := []model.Credit{{Person: "tessa-word", Role: model.RoleTranslator}}
		if !reflect.DeepEqual(work.Credits, want) {
			t.Errorf("credits = %+v, want %+v", work.Credits, want)
		}
		if res := check.Load(dataDir); !res.OK() {
			t.Fatalf("enriched tree failed validation:\n%v", res.Problems)
		}

		// A second identical run must be a no-op: the credits are no longer
		// absent, so there is nothing to fill.
		before := string(rawEntity(t, dataDir, "works/en/enrich-me/work.json"))
		sum2 := runEnrich(t, dataDir, row, false)
		if sum2.Credits != 0 {
			t.Errorf("second run wrote %d credits, want 0", sum2.Credits)
		}
		if after := string(rawEntity(t, dataDir, "works/en/enrich-me/work.json")); after != before {
			t.Errorf("second identical enrich run changed the record:\n%s\n%s", before, after)
		}
	})

	t.Run("never overwrites an existing credits member", func(t *testing.T) {
		dataDir := t.TempDir()
		seedTree(t, dataDir, enrichSeed(`"credits":[{"person":"original-author","role":"editor"}],`))
		sum := runEnrich(t, dataDir, row, false)
		if sum.Credits != 0 {
			t.Errorf("Summary.Credits = %d, want 0 (the work already had credits)", sum.Credits)
		}
		var work struct {
			Credits []model.Credit `json:"credits"`
		}
		readEntity(t, dataDir, "works/en/enrich-me/work.json", &work)
		want := []model.Credit{{Person: "original-author", Role: model.RoleEditor}}
		if !reflect.DeepEqual(work.Credits, want) {
			t.Errorf("credits = %+v, want the recorded %+v", work.Credits, want)
		}
	})
}

// TestEnrichSkipsCreditsForUnknownPeople pins the rule that keeps enrichment's
// output referentially sound: the pass creates nothing, so a role qualifier
// naming a person the catalogue does not hold states a credit that cannot be
// written, and it is dropped rather than emitted as a dangling reference.
func TestEnrichSkipsCreditsForUnknownPeople(t *testing.T) {
	// "Nora Newcomer" is not in the seed, and enrichment will not create her.
	row := `[{
		"asin":"B0CRED0003","title":"Enrich Me","region":"us","language":"english",
		"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
		"authors":[{"name":"Original Author"},{"name":"Nora Newcomer - Translator"}],
		"narrators":[{"name":"Bea Reader"}]
	}]`
	dataDir := t.TempDir()
	seedTree(t, dataDir, enrichSeed(""))
	sum := runEnrich(t, dataDir, row, false)
	if sum.Credits != 0 {
		t.Errorf("Summary.Credits = %d, want 0 (the credited person is not in the catalogue)", sum.Credits)
	}
	if entryExists(t, dataDir, "people/no/nora-newcomer.json") {
		t.Error("enrichment created a person; it must never create anything")
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("enriched tree failed validation:\n%v", res.Problems)
	}
}

// enrichSeed is a minimal catalogue holding the work and recording the
// enrichment rows above match by ASIN. extra is spliced into the work record so
// a case can seed it with or without a credits member.
func enrichSeed(extra string) map[string]string {
	return map[string]string{
		"people/or/original-author.json": `{"id":"original-author","license":"CC0-1.0","name":"Original Author","sources":[{"type":"user"}]}`,
		"people/te/tessa-word.json":      `{"id":"tessa-word","license":"CC0-1.0","name":"Tessa Word","sources":[{"type":"user"}]}`,
		"people/be/bea-reader.json":      `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"user"}]}`,
		"works/en/enrich-me/work.json": `{"authors":["original-author"],` + extra +
			`"id":"enrich-me","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Enrich Me"}`,
		"works/en/enrich-me/recordings/enrich-me-bea-reader.json": `{"asin":[{"asin":"B0CRED0003","region":"us"}],"id":"enrich-me-bea-reader","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"enrich-me"}`,
	}
}
