package importer

import (
	"reflect"
	"slices"
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

		// A credential title stacked onto the role is trimmed before lookup, and
		// the trimmed fragment goes back onto the NAME - it is part of what the
		// person is called, and a generational suffix is part of who they are.
		// See TestCredentialSuffixKeptOnName.
		{"credential suffix", "Dr Reed - Introduction M.D.", "Dr Reed M.D.", []string{model.RoleIntroduction}},
		{"repeated credential suffix", "Dr Reed - Introduction MD MD", "Dr Reed MD MD", []string{model.RoleIntroduction}},
		{"generational suffix", "Ed Ward - Editor Jr.", "Ed Ward Jr.", []string{model.RoleEditor}},
		{"credential on foreword", "Dr Reed - Foreword PhD", "Dr Reed PhD", []string{model.RoleForeword}},

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
// keeps it from ever returning an empty qualifier, and the dropped-word count
// the caller re-appends to the name with (see TestCredentialSuffixKeptOnName).
func TestTrimCredentialTitles(t *testing.T) {
	cases := map[string]struct {
		want    string
		dropped int
	}{
		"introduction m.d.":           {"introduction", 1},
		"introduction ph.d.":          {"introduction", 1},
		"introduction md md":          {"introduction", 2},
		"introduction jr. m.d. ph.d.": {"introduction", 3},
		"editor ph.d. ph.d.":          {"editor", 2},
		"foreword phd":                {"foreword", 1},
		"translator":                  {"translator", 0},
		"md":                          {"md", 0}, // nothing but a credential: left whole
		"jr.":                         {"jr.", 0},
		"smith jr.":                   {"smith", 1}, // trims, but "smith" is not a role
		"editor  and   translator":    {"editor and translator", 0},
	}
	for in, want := range cases {
		got, dropped := trimCredentialTitles(in)
		if got != want.want || dropped != want.dropped {
			t.Errorf("trimCredentialTitles(%q) = %q, %d; want %q, %d", in, got, dropped, want.want, want.dropped)
		}
	}
}

// TestCredentialSuffixKeptOnName pins the review finding: a credential trim is
// only allowed to enable the ROLE match, never to delete part of the name. "Jr."
// is a generational suffix, and dropping it merges a son's records into his
// father's.
func TestCredentialSuffixKeptOnName(t *testing.T) {
	cases := []struct {
		in    string
		name  string
		roles []string
	}{
		// The finding's exact case: the trim enabled "editor", so "Jr." goes back
		// on the name - in its source spelling, not the lowercased lookup form.
		{"Theodore C. Van Alst - editor Jr.", "Theodore C. Van Alst Jr.", []string{model.RoleEditor}},
		{"Ann Fisher - Introduction M.D.", "Ann Fisher M.D.", []string{model.RoleIntroduction}},
		{"Ann Fisher - introduction md md", "Ann Fisher md md", []string{model.RoleIntroduction}},
		// Negative controls: no role matched even after trimming, so nothing is
		// stripped and nothing is moved.
		{"Sam Jones - MD", "Sam Jones - MD", nil},
		{"Robert Downey - Jr.", "Robert Downey - Jr.", nil},
		{"Md. Rahman", "Md. Rahman", nil},
		{"Some Author - Smith Jr.", "Some Author - Smith Jr.", nil},
		// A role that needs no trimming keeps its behaviour exactly.
		{"Rosa Vidal - Translator", "Rosa Vidal", []string{model.RoleTranslator}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			gotName, gotRoles := CreditWithRoles(c.in)
			if gotName != c.name {
				t.Errorf("name = %q, want %q", gotName, c.name)
			}
			if !reflect.DeepEqual(gotRoles, c.roles) {
				t.Errorf("roles = %v, want %v", gotRoles, c.roles)
			}
		})
	}

	// The suffix must reach the person's IDENTITY too, not just the display
	// name: that is what keeps the two Van Alsts apart.
	if got, _ := personSlug("Theodore C. Van Alst Jr."); got == "theodore-c-van-alst" {
		t.Fatalf("suffix did not reach the slug: %q", got)
	}
	plain, _ := personSlug("Theodore C. Van Alst")
	suffixed, _ := personSlug(mustCleanName(t, "Theodore C. Van Alst - editor Jr."))
	if plain == suffixed {
		t.Errorf("the suffixed and plain names share one identity (%q)", plain)
	}
}

// mustCleanName is CleanCreditName with the test's assertion that it changed
// something, so a case that silently stops matching cannot pass by comparing
// two unchanged names.
func mustCleanName(t *testing.T, in string) string {
	t.Helper()
	out := CleanCreditName(in)
	if out == in {
		t.Fatalf("CleanCreditName(%q) stripped nothing", in)
	}
	return out
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

// TestCreditsNeverNameTheCatchAllPerson is the HIGH finding's regression test.
// personSlug substitutes the shared "person" record for a name Slugify folds to
// nothing - every Korean, Cyrillic and CJK credit in the seed wave - and the
// first cut of workCredits wrote {person: "person", role: ...} for those, a
// metacheck-green statement that one shared record translated every such book.
//
// An unidentifiable person is not credited at all, and the drop is reported.
//
// The row runs through a USER-LIBRARY importer (OpenAudible), which is now the
// only way to reach this guard: libex refuses such a row outright at its parse
// layer (firstUnnamedCredit), and that exclusion is deliberately libex-only. The
// guard therefore still protects exactly the paths the exclusion does not cover.
func TestCreditsNeverNameTheCatchAllPerson(t *testing.T) {
	export := `[{
		"asin":"B0CRED0010","title_short":"A Korean Translation","region":"US","language":"english","seconds":18000,
		"author":"Original Author, 김영하 - Translator, Александр Пушкин - Editor",
		"narrated_by":"Bea Reader"
	}]`
	sum, dataDir := runImport(t, export, false)

	var work struct {
		Authors []string       `json:"authors"`
		Credits []model.Credit `json:"credits"`
	}
	readEntity(t, dataDir, "works/ak/a-korean-translation/work.json", &work)

	if len(work.Credits) != 0 {
		t.Errorf("credits = %+v, want none: neither credited name resolves to a person", work.Credits)
	}
	if sum.Credits != 0 {
		t.Errorf("Summary.Credits = %d, want 0", sum.Credits)
	}
	// The pre-existing conflation on the AUTHORS side is deliberately unchanged:
	// this branch fixes the false ROLE claim, not Slugify (see the PR notes).
	if !slices.Contains(work.Authors, "person") {
		t.Errorf("authors = %v, want the pre-existing catch-all author entry", work.Authors)
	}

	// The drop is visible, once, with the count and an example - not silent, and
	// not one line per occurrence.
	var lines []string
	for _, w := range sum.Warnings {
		if strings.Contains(w, "credits dropped") {
			lines = append(lines, w)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("want exactly one aggregated drop warning, got %v", lines)
	}
	if !strings.Contains(lines[0], "2 role-qualified credits dropped") || !strings.Contains(lines[0], "김영하") {
		t.Errorf("warning does not report the count and an example: %q", lines[0])
	}

	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}

// TestBareStringAuthorsStateTheSameCredits is the MEDIUM finding's regression
// test: libexNames used to CLEAN a bare-string author list before sourceCredits
// could read its qualifiers, so the same facts spelled as a joined string
// silently lost every role. No row in the received dump uses that shape, so
// this pins a latent difference, not an observed loss.
func TestBareStringAuthorsStateTheSameCredits(t *testing.T) {
	const arrayForm = `{
		"asin":"B0CRED0011","title":"Array Form","region":"us","language":"english",
		"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
		"authors":[{"name":"Original Author"},{"name":"Tessa Word - Translator"}],
		"narrators":[{"name":"Bea Reader"}]
	}`
	const stringForm = `{
		"asin":"B0CRED0012","title":"String Form","region":"us","language":"english",
		"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
		"authors":"Original Author, Tessa Word - Translator",
		"narrators":"Bea Reader"
	}`
	sum, dataDir := runLibex(t, "["+arrayForm+","+stringForm+"]", false)

	var fromArray, fromString struct {
		Authors []string       `json:"authors"`
		Credits []model.Credit `json:"credits"`
	}
	readEntity(t, dataDir, "works/ar/array-form/work.json", &fromArray)
	readEntity(t, dataDir, "works/st/string-form/work.json", &fromString)

	want := []model.Credit{{Person: "tessa-word", Role: model.RoleTranslator}}
	if !reflect.DeepEqual(fromArray.Credits, want) {
		t.Errorf("array-form credits = %+v, want %+v", fromArray.Credits, want)
	}
	if !reflect.DeepEqual(fromString.Credits, fromArray.Credits) {
		t.Errorf("the two shapes disagree: string form %+v, array form %+v",
			fromString.Credits, fromArray.Credits)
	}
	if !reflect.DeepEqual(fromString.Authors, fromArray.Authors) {
		t.Errorf("the two shapes disagree on authors: %v vs %v", fromString.Authors, fromArray.Authors)
	}
	if sum.Credits != 2 {
		t.Errorf("Summary.Credits = %d, want 2 (one per work)", sum.Credits)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}

// TestUserAttestationWritesCredits is the test the trust-tier comment was
// missing. A user-library export states no genres, which is why applyToWork's
// attest-scope branch was documented as inert - but it DOES state a credit
// whenever a credit name carries a role qualifier, so the branch is live.
//
// The tier rule then applies unchanged: a bulk-mirror-only work takes the
// credit, an already-attested work does not.
func TestUserAttestationWritesCredits(t *testing.T) {
	const rowWithRole = `[{
	  "asin": "B0LIBEX001",
	  "title_short": "The Lost Cartographer",
	  "author": "Ada Mapmaker - Editor",
	  "narrated_by": "Bea Reader",
	  "language": "english",
	  "region": "us"
	}]`

	t.Run("a mirror-seeded work takes the credit", func(t *testing.T) {
		dataDir := seedTierTree(t, nil)
		sum := runUserImport(t, dataDir, rowWithRole)
		if sum.AttestedWorks != 1 || sum.Credits != 1 {
			t.Errorf("AttestedWorks = %d, Credits = %d; want 1 and 1", sum.AttestedWorks, sum.Credits)
		}
		var work struct {
			Authors []string       `json:"authors"`
			Credits []model.Credit `json:"credits"`
		}
		readEntity(t, dataDir, tierWorkRel, &work)
		want := []model.Credit{{Person: "ada-mapmaker", Role: model.RoleEditor}}
		if !reflect.DeepEqual(work.Credits, want) {
			t.Errorf("credits = %+v, want %+v", work.Credits, want)
		}
		// The role ADDS to the person's standing; it never removes them from the
		// authors list the identity model is built on.
		if !reflect.DeepEqual(work.Authors, []string{"ada-mapmaker"}) {
			t.Errorf("authors = %v, want the recorded author list", work.Authors)
		}
		if res := check.Load(dataDir); !res.OK() {
			t.Fatalf("attested tree failed validation:\n%v", res.Problems)
		}
	})

	t.Run("an attested work is left alone", func(t *testing.T) {
		dataDir := seedTierTree(t, map[string]string{
			tierWorkRel: tierWorkAttested,
			tierRecRel:  tierRecordingAttested,
		})
		before := string(rawEntity(t, dataDir, tierWorkRel))
		sum := runUserImport(t, dataDir, rowWithRole)
		if sum.Credits != 0 {
			t.Errorf("Summary.Credits = %d, want 0: first writer wins", sum.Credits)
		}
		if after := string(rawEntity(t, dataDir, tierWorkRel)); after != before {
			t.Errorf("an attested work was rewritten:\n%s\n%s", before, after)
		}
	})
}

// TestSecondRowOfARunAddsMissingCredits is the in-run drop finding's regression
// test, on both planning paths. Within one run a work's credits accrete; across
// runs the documented fill-absent rule is unchanged, which the last subtest
// pins from the other side.
func TestSecondRowOfARunAddsMissingCredits(t *testing.T) {
	t.Run("create path: two rows merging into one work", func(t *testing.T) {
		// Same title, same author SET (so the second row merges into the work the
		// first created), different role qualifier on the shared person.
		const rows = `[{
			"asin":"B0CRED0020","title":"Merged Work","region":"us","language":"english",
			"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
			"authors":[{"name":"Original Author"},{"name":"Tessa Word - Translator"}],
			"narrators":[{"name":"Bea Reader"}]
		},{
			"asin":"B0CRED0021","title":"Merged Work","region":"uk","language":"english",
			"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
			"authors":[{"name":"Original Author"},{"name":"Tessa Word - Illustrator"}],
			"narrators":[{"name":"Cal Voice"}]
		}]`
		sum, dataDir := runLibex(t, rows, false)

		var work struct {
			Credits []model.Credit `json:"credits"`
			Sources []struct {
				Ref string `json:"ref"`
			} `json:"sources"`
		}
		readEntity(t, dataDir, "works/me/merged-work/work.json", &work)
		want := []model.Credit{
			{Person: "tessa-word", Role: model.RoleIllustrator},
			{Person: "tessa-word", Role: model.RoleTranslator},
		}
		if !reflect.DeepEqual(work.Credits, want) {
			t.Errorf("credits = %+v, want %+v", work.Credits, want)
		}
		if sum.Credits != 2 {
			t.Errorf("Summary.Credits = %d, want 2", sum.Credits)
		}
		// The second row contributed a fact to the work, so its provenance is on
		// the work too.
		var refs []string
		for _, s := range work.Sources {
			refs = append(refs, s.Ref)
		}
		if !slices.Contains(refs, "B0CRED0021") {
			t.Errorf("work sources = %v, want the contributing row's ref", refs)
		}
		if res := check.Load(dataDir); !res.OK() {
			t.Fatalf("imported tree failed validation:\n%v", res.Problems)
		}
	})

	t.Run("enrich path: two ASINs of one work in one run", func(t *testing.T) {
		dataDir := t.TempDir()
		seed := enrichSeed("")
		// A second recording of the seeded work, with an ASIN of its own, so one
		// run matches the same work twice.
		seed["works/en/enrich-me/recordings/enrich-me-cal-voice.json"] =
			`{"asin":[{"asin":"B0CRED0004","region":"uk"}],"id":"enrich-me-cal-voice","language":"en",` +
				`"license":"CC0-1.0","narrators":["cal-voice"],"sources":[{"type":"user"}],"work":"enrich-me"}`
		seed["people/ca/cal-voice.json"] =
			`{"id":"cal-voice","license":"CC0-1.0","name":"Cal Voice","sources":[{"type":"user"}]}`
		seedTree(t, dataDir, seed)

		const rows = `[{
			"asin":"B0CRED0003","title":"Enrich Me","region":"us","language":"english",
			"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
			"authors":[{"name":"Original Author"},{"name":"Tessa Word - Translator"}],
			"narrators":[{"name":"Bea Reader"}]
		},{
			"asin":"B0CRED0004","title":"Enrich Me","region":"uk","language":"english",
			"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
			"authors":[{"name":"Original Author - Editor"},{"name":"Tessa Word - Translator"}],
			"narrators":[{"name":"Cal Voice"}]
		}]`
		sum := runEnrich(t, dataDir, rows, false)

		var work struct {
			Credits []model.Credit `json:"credits"`
		}
		readEntity(t, dataDir, "works/en/enrich-me/work.json", &work)
		want := []model.Credit{
			{Person: "original-author", Role: model.RoleEditor},
			{Person: "tessa-word", Role: model.RoleTranslator},
		}
		if !reflect.DeepEqual(work.Credits, want) {
			t.Errorf("credits = %+v, want %+v", work.Credits, want)
		}
		// Two written, and the pair the second row REPEATED is not counted twice.
		if sum.Credits != 2 {
			t.Errorf("Summary.Credits = %d, want 2", sum.Credits)
		}
		if res := check.Load(dataDir); !res.OK() {
			t.Fatalf("enriched tree failed validation:\n%v", res.Problems)
		}
	})

	t.Run("a LATER run never adds to a recorded credits list", func(t *testing.T) {
		// The cross-run rule, from the other side of the same code: run one fills
		// the list, run two states a role the work does not carry, and it is
		// dropped - the recorded description of who did what belongs to whoever
		// wrote it.
		dataDir := t.TempDir()
		seedTree(t, dataDir, enrichSeed(""))
		first := runEnrich(t, dataDir, `[{
			"asin":"B0CRED0003","title":"Enrich Me","region":"us","language":"english",
			"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
			"authors":[{"name":"Original Author"},{"name":"Tessa Word - Translator"}],
			"narrators":[{"name":"Bea Reader"}]
		}]`, false)
		if first.Credits != 1 {
			t.Fatalf("first run wrote %d credits, want 1", first.Credits)
		}
		before := string(rawEntity(t, dataDir, "works/en/enrich-me/work.json"))

		second := runEnrich(t, dataDir, `[{
			"asin":"B0CRED0003","title":"Enrich Me","region":"us","language":"english",
			"bookFormat":"unabridged","releaseDate":"2024-05-01T00:00:00Z","lengthMinutes":300,
			"authors":[{"name":"Original Author - Editor"},{"name":"Tessa Word - Translator"}],
			"narrators":[{"name":"Bea Reader"}]
		}]`, false)
		if second.Credits != 0 {
			t.Errorf("second run wrote %d credits, want 0", second.Credits)
		}
		if after := string(rawEntity(t, dataDir, "works/en/enrich-me/work.json")); after != before {
			t.Errorf("a later run changed a recorded credits list:\n%s\n%s", before, after)
		}
	})
}
