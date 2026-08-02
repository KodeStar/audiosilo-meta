package importer

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// collectiveCase is one measured variant and the canonical record it must fold
// onto. variant is spelled the way a SOURCE spells it (title case, accents and
// all), because that is the string the importer is handed; the folded key the
// table is written in is derived from it here rather than restated.
type collectiveCase struct {
	variant string
	name    string // the canonical credit name
	slug    string // the person id that name slugs to
}

// collectiveCases pins every entry of collectiveCredits. The completeness check
// in TestEveryCollectiveVariantIsPinned fails if the table gains an entry this
// list does not cover, so a new fold can never land untested.
//
// One folded key carries two spellings: the Spanish "Anónimo" and the Italian
// "Anonimo" are the same string once foldCredit removes the accent, and both are
// listed so the fold is proven on the accented form as well as the bare one.
func collectiveCases() []collectiveCase {
	return []collectiveCase{
		// Bucket 1 -> Full Cast.
		{"Full Cast", "Full Cast", "full-cast"},
		{"A Full Cast", "Full Cast", "full-cast"},
		{"Ensemble Cast", "Full Cast", "full-cast"},
		{"An Ensemble Cast", "Full Cast", "full-cast"},
		{"Cast", "Full Cast", "full-cast"},
		{"Full Cast Dramatization", "Full Cast", "full-cast"},
		{"Elenco", "Full Cast", "full-cast"},
		{"Distribution Complète", "Full Cast", "full-cast"},
		{"Full Supporting Cast", "Full Cast", "full-cast"},
		{"Full Cast Ensemble", "Full Cast", "full-cast"},
		{"Additional Cast", "Full Cast", "full-cast"},
		{"An All-Star Cast", "Full Cast", "full-cast"},
		{"Full Cast Recording", "Full Cast", "full-cast"},
		{"Assorted Cast", "Full Cast", "full-cast"},

		// Bucket 1 -> Various.
		{"Various", "Various", "various"},
		{"Various Authors", "Various", "various"},
		{"Autori Vari", "Various", "various"},
		{"Diverse", "Various", "various"},
		{"Divers Auteurs", "Various", "various"},
		{"Various Narrators", "Various", "various"},
		{"Varios Narradores", "Various", "various"},
		{"Divers Narrateurs", "Various", "various"},
		{"Narratori Vari", "Various", "various"},
		// The German twin of the four above. Which canonical a variant lands on
		// is the statement's business, never the language's.
		{"Diverse Sprecher", "Various", "various"},
		{"Various Artists", "Various", "various"},
		{"Varios Autores", "Various", "various"},
		{"Diversos Narradores", "Various", "various"},
		{"Various Performers", "Various", "various"},
		{"Diverse Diverse", "Various", "various"},

		// Bucket 1 -> Anonymous.
		{"Anonymous", "Anonymous", "anonymous"},
		{"Anónimo", "Anonymous", "anonymous"},
		{"Anonimo", "Anonymous", "anonymous"},
		{"Anonyme", "Anonymous", "anonymous"},
		{"I Anonymous", "Anonymous", "anonymous"},

		// Bucket 1 -> Uncredited.
		{"Uncredited", "Uncredited", "uncredited"},

		// Bucket 2 -> Unknown.
		{"Unknown", "Unknown", "unknown"},
		{"N.N.", "Unknown", "unknown"},
		{"N. N.", "Unknown", "unknown"},
		{"N.N", "Unknown", "unknown"},
		{"Auteur Inconnu", "Unknown", "unknown"},
		{"Narrateur Inconnu", "Unknown", "unknown"},
		{"Autore Sconosciuto", "Unknown", "unknown"},
		{"Narratore Sconosciuto", "Unknown", "unknown"},
		{"Autor Desconocido", "Unknown", "unknown"},
	}
}

// TestEveryCollectiveVariantIsPinned is the completeness guard: every key in the
// table is exercised by a case above, and every case names a key that exists.
// Adding a fold without a test fails here rather than shipping unproven.
func TestEveryCollectiveVariantIsPinned(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range collectiveCases() {
		key := foldCredit(c.variant)
		if _, ok := collectiveCredits[key]; !ok {
			t.Errorf("case %q folds to %q, which is not in collectiveCredits", c.variant, key)
		}
		covered[key] = true
	}
	var missing []string
	for key := range collectiveCredits {
		if !covered[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("collectiveCredits entries with no pinned case: %v", missing)
	}
}

// TestCollectiveVariantsFoldOntoTheirCanonical is the rule itself, at both doors
// a variant can arrive through: the fold helper and the public credit cleaner.
func TestCollectiveVariantsFoldOntoTheirCanonical(t *testing.T) {
	for _, c := range collectiveCases() {
		t.Run(c.variant, func(t *testing.T) {
			if got := canonicalCreditName(c.variant); got != c.name {
				t.Errorf("canonicalCreditName(%q) = %q, want %q", c.variant, got, c.name)
			}
			if got := CleanCreditName(c.variant); got != c.name {
				t.Errorf("CleanCreditName(%q) = %q, want %q", c.variant, got, c.name)
			}
			// Case is not identity: the same statement shouted or whispered is
			// the same statement.
			for _, spelling := range []string{strings.ToUpper(c.variant), strings.ToLower(c.variant)} {
				if got := canonicalCreditName(spelling); got != c.name {
					t.Errorf("canonicalCreditName(%q) = %q, want %q", spelling, got, c.name)
				}
			}
		})
	}
}

// TestCollectiveCanonicalsSlugToTheirIDs is what makes this a FOLD rather than a
// rename: each canonical name must slug to the id the catalogue already holds
// (or would mint), through the very call the importer mints ids with. A
// canonical whose name slugged elsewhere would create a second record instead of
// collapsing onto one.
func TestCollectiveCanonicalsSlugToTheirIDs(t *testing.T) {
	for _, c := range collectiveCases() {
		slug, fellBack := model.PersonSlug(c.name)
		if fellBack {
			t.Errorf("canonical %q slugs away to the catch-all record", c.name)
		}
		if slug != c.slug {
			t.Errorf("model.PersonSlug(%q) = %q, want %q", c.name, slug, c.slug)
		}
		// The canonical is a key of its own table, so a source spelling it in
		// another case still mints the record under the canonical spelling.
		if got, ok := collectiveCredits[foldCredit(c.name)]; !ok || got != c.name {
			t.Errorf("canonical %q is not its own table entry (got %q, present=%v)", c.name, got, ok)
		}
	}
}

// TestBrandedEnsemblesAreNotFolded is the load-bearing half. Every name here is
// a real credit - a branded troupe with a record of its own, or a person whose
// name merely contains the words a pattern rule would fire on. The fold matches
// WHOLE names, exactly, which is what keeps all of them intact.
func TestBrandedEnsemblesAreNotFolded(t *testing.T) {
	intact := []string{
		// Branded ensembles: a real group with a real name (kind: group, in the
		// census's terms), never a bare description.
		"The Colonial Radio Players",
		"Museum Audiobooks cast",
		"the Full Cast Family",
		"Das Schauspiel-Ensemble vom Landestheater Detmold",
		"Linguistics Team",
		"Cast of Critical Role",
		"Soundbooth Theater",
		"The Faen Ensemble",
		"Peter Pan Ensemble",
		"The Cast of StudioBos",
		"Viseu-Multicast",
		// People whose names contain a folded word.
		"P.C. Cast",
		"Kristin Cast",
		"Jayne Castle",
		"Rainer Castor",
		"Devon Sorvari",
		"N. N. Britt",
		"Anonymous Guest",
		"Sarah Sprecher",
		"Various Smith",
		// The documented exclusions: forms the dump does not carry (the German
		// "anonym", 0 credits) or that a maintainer decision left out
		// ("anonymus", "unbekannt", "desconocido", "Div."). Every entry in the
		// table is a measured form, so these stay ordinary credits until
		// somebody measures and decides them.
		"Div.",
		"Anonym",
		"Anonymus",
		"Unbekannt",
		"Desconocido",
	}
	for _, name := range intact {
		t.Run(name, func(t *testing.T) {
			if got := canonicalCreditName(name); got != name {
				t.Errorf("canonicalCreditName(%q) = %q, want it untouched", name, got)
			}
		})
	}
}

// TestCollectiveFoldRunsAfterCleaning pins the ORDER: the cleaning rules run
// first and the fold reads what they produced, so a variant wearing a role
// qualifier or doubled by the source still lands on its canonical - and the
// roles the qualifier stated survive the fold, because what the fold changes is
// who is credited, not what the source said they did.
func TestCollectiveFoldRunsAfterCleaning(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		roles []string
	}{
		{"Autori Vari - traduttore", "Various", []string{model.RoleTranslator}},
		{"Various Authors (introduction)", "Various", []string{model.RoleIntroduction}},
		{"Full Cast Full Cast", "Full Cast", nil},
		{"Created by Various Authors", "Various", nil},
		{"  Narratori   Vari  ", "Various", nil},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, roles := CreditWithRoles(c.in)
			if got != c.want {
				t.Errorf("CreditWithRoles(%q) name = %q, want %q", c.in, got, c.want)
			}
			if len(roles) != len(c.roles) {
				t.Fatalf("CreditWithRoles(%q) roles = %v, want %v", c.in, roles, c.roles)
			}
			for i := range roles {
				if roles[i] != c.roles[i] {
					t.Errorf("CreditWithRoles(%q) roles = %v, want %v", c.in, roles, c.roles)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// End to end

// TestLibexImportsCollectiveCreditsAsCanonicals is the acceptance test on the
// NARRATOR side: one row per measured variant, each importing as a book
// narrated by the canonical record - and not one variant record among them.
func TestLibexImportsCollectiveCreditsAsCanonicals(t *testing.T) {
	cases := collectiveCases()
	var rs []libexRow
	for i, c := range cases {
		rs = append(rs, libexRow{
			asin:      fmt.Sprintf("B0FOLD%04d", i),
			title:     fmt.Sprintf("Folded Book %d", i),
			authors:   `{"name":"Ada One"}`,
			narrators: `{"name":"` + c.variant + `"}`,
		})
	}
	sum, dataDir := runLibex(t, rows(rs...), false)

	if sum.NewWorks != len(cases) {
		t.Errorf("NewWorks = %d, want %d (every row imports)", sum.NewWorks, len(cases))
	}
	// The one author plus exactly the five canonical records: no variant of any
	// of them was minted.
	if sum.NewPeople != 6 {
		t.Errorf("NewPeople = %d, want 6 (Ada One + the five canonicals): the fold missed a variant", sum.NewPeople)
	}
	for _, slug := range []string{"full-cast", "various", "anonymous", "uncredited", "unknown"} {
		if !entryExists(t, dataDir, personAddr(slug)) {
			t.Errorf("canonical person %s was not created", slug)
		}
	}
	for i, c := range cases {
		variantSlug, _ := model.PersonSlug(c.variant)
		if variantSlug != c.slug && entryExists(t, dataDir, personAddr(variantSlug)) {
			t.Errorf("variant %q minted its own record %s", c.variant, variantSlug)
		}
		var rec struct {
			Narrators []string `json:"narrators"`
		}
		work := fmt.Sprintf("folded-book-%d", i)
		readEntity(t, dataDir, recAddr(work, c.slug+"-2020"), &rec)
		if len(rec.Narrators) != 1 || rec.Narrators[0] != c.slug {
			t.Errorf("%q: recording narrators = %v, want [%s]", c.variant, rec.Narrators, c.slug)
		}
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}

// TestLibexImportsCollectiveAuthorsAsCanonicals is the same acceptance test on
// the AUTHOR side, which is the side that matters most: authors is the identity
// list, and a work needs at least one, so a fold that only covered narrators
// would leave the anthologies forked by language.
func TestLibexImportsCollectiveAuthorsAsCanonicals(t *testing.T) {
	cases := collectiveCases()
	var rs []libexRow
	for i, c := range cases {
		rs = append(rs, libexRow{
			asin:      fmt.Sprintf("B0AUTH%04d", i),
			title:     fmt.Sprintf("Authored Book %d", i),
			authors:   `{"name":"` + c.variant + `"}`,
			narrators: `{"name":"Bea Reader"}`,
		})
	}
	_, dataDir := runLibex(t, rows(rs...), false)

	for i, c := range cases {
		var work struct {
			Authors []string `json:"authors"`
		}
		readEntity(t, dataDir, workAddr(fmt.Sprintf("authored-book-%d", i)), &work)
		if len(work.Authors) != 1 || work.Authors[0] != c.slug {
			t.Errorf("%q: work authors = %v, want [%s]", c.variant, work.Authors, c.slug)
		}
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}

// TestTwoCreditsFoldingOntoOneCanonicalDedupe is the collision the fold makes
// possible: one row can state the SAME statement twice, in two spellings, on one
// credit list. The dump really does - 13 books credit both "A Full Cast" and
// "full cast" as narrators, 5 credit both "Diverse" and "Various authors" as
// authors - and before the fold those were two records, so nothing deduplicated
// them.
//
// Slug-level deduplication absorbs it (creditSlugs dedupes by slug, and the work
// author set is a set), which is what keeps the emitted arrays schema-valid:
// narrators and authors carry uniqueItems, so a doubled canonical would be a
// rejected record rather than a cosmetic wart.
func TestTwoCreditsFoldingOntoOneCanonicalDedupe(t *testing.T) {
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0DEDUPE01", title: "Dual Cast Book", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"A Full Cast"},{"name":"full cast"}`},
		libexRow{asin: "B0DEDUPE02", title: "Dual Author Book",
			authors:   `{"name":"Diverse"},{"name":"Various authors"}`,
			narrators: `{"name":"Bea Reader"}`},
	), false)

	if sum.NewWorks != 2 {
		t.Fatalf("NewWorks = %d, want 2", sum.NewWorks)
	}
	// ada-one + full-cast + various + bea-reader: each collision is ONE record.
	if sum.NewPeople != 4 {
		t.Errorf("NewPeople = %d, want 4 (each doubled statement is one record)", sum.NewPeople)
	}

	var rec struct {
		Narrators []string `json:"narrators"`
	}
	readEntity(t, dataDir, recAddr("dual-cast-book", "full-cast-2020"), &rec)
	if len(rec.Narrators) != 1 || rec.Narrators[0] != "full-cast" {
		t.Errorf("recording narrators = %v, want [full-cast] exactly once", rec.Narrators)
	}

	var work struct {
		Authors []string `json:"authors"`
	}
	readEntity(t, dataDir, workAddr("dual-author-book"), &work)
	if len(work.Authors) != 1 || work.Authors[0] != "various" {
		t.Errorf("work authors = %v, want [various] exactly once", work.Authors)
	}

	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}

// TestFoldedCollisionKeepsBothRoles is the same collision carrying ROLES. Two
// spellings of Various state two different jobs; the fold collapses the person
// and the credit list keeps both pairs, because a credit is (person, role) and
// only an exact repeat of the pair is a duplicate. The result is sorted by
// (person, role), which is what makes one set of credits one byte-form.
func TestFoldedCollisionKeepsBothRoles(t *testing.T) {
	_, dataDir := runLibex(t, rows(
		libexRow{asin: "B0ROLEFLD1", title: "Collected Essays",
			authors:   `{"name":"Autori Vari - traduttore"},{"name":"Various Authors - editor"}`,
			narrators: `{"name":"Bea Reader"}`},
	), false)

	var work struct {
		Authors []string       `json:"authors"`
		Credits []model.Credit `json:"credits"`
	}
	readEntity(t, dataDir, workAddr("collected-essays"), &work)
	if len(work.Authors) != 1 || work.Authors[0] != "various" {
		t.Errorf("work authors = %v, want [various]", work.Authors)
	}
	want := []model.Credit{
		{Person: "various", Role: model.RoleEditor},
		{Person: "various", Role: model.RoleTranslator},
	}
	if len(work.Credits) != len(want) {
		t.Fatalf("work credits = %+v, want %+v", work.Credits, want)
	}
	for i := range want {
		if work.Credits[i] != want[i] {
			t.Fatalf("work credits = %+v, want %+v", work.Credits, want)
		}
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}

// TestBrandedEnsembleImportsAsItself is the other side of the same run: a
// branded troupe keeps its own person record, which is what the exact-name
// matching exists to protect.
func TestBrandedEnsembleImportsAsItself(t *testing.T) {
	_, dataDir := runLibex(t, rows(
		libexRow{asin: "B0BRANDED1", title: "A Radio Play", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"The Colonial Radio Players"}`},
		libexRow{asin: "B0BRANDED2", title: "Another Radio Play", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Museum Audiobooks cast"}`},
	), false)

	for _, slug := range []string{"the-colonial-radio-players", "museum-audiobooks-cast"} {
		if !entryExists(t, dataDir, personAddr(slug)) {
			t.Errorf("branded ensemble %s was folded away", slug)
		}
	}
	if entryExists(t, dataDir, personAddr("full-cast")) {
		t.Error("a branded ensemble was folded onto full-cast")
	}
}

// TestBookingPlaceholdersAreStillRefused is the boundary between the buckets,
// asserted from the other direction: the forms that name nobody at all are
// refused as before, in the same run as the forms that now fold. A row is
// refused WHOLE, so nothing of it reaches the tree.
func TestBookingPlaceholdersAreStillRefused(t *testing.T) {
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0TBAROW01", title: "Unbooked Book", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"To Be Announced"}`},
		libexRow{asin: "B0FOLDROW1", title: "Folded Book", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Narratori Vari"}`},
	), false)

	if sum.SkippedRows != 1 {
		t.Errorf("SkippedRows = %d, want 1 (the placeholder row only)", sum.SkippedRows)
	}
	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1 (the folded row imported)", sum.NewWorks)
	}
	if entryExists(t, dataDir, personAddr("to-be-announced")) {
		t.Error("a booking placeholder became a person of record")
	}
	if !entryExists(t, dataDir, personAddr("various")) {
		t.Error("the collective row did not import onto its canonical")
	}
}

// TestCollectiveFoldIsNotEvidenceOfItsOwnSpelling covers the interaction with
// the batch pre-passes. Both dotted spellings of the bibliographic N.N. fold
// inside the shared credit cleaning, so the credit census and the initials
// pre-pass see "Unknown" and never the variant: neither spelling contributes
// evidence for an identity of its own.
//
// The bare "NN" is the control, and it is what makes the point observable. It is
// not in the table, and the initials rule would once have grouped it with "N.N."
// (both are the marked key I:nn) and resolved the pair onto one spelling. With
// the dotted forms folded away, that group holds only "NN", so it keeps its own
// record - the fold removed the variant from the evidence, it did not merge
// anything into it.
func TestCollectiveFoldIsNotEvidenceOfItsOwnSpelling(t *testing.T) {
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0NOMENNE1", title: "Anonymous Chronicle", authors: `{"name":"N.N."}`},
		libexRow{asin: "B0NOMENNE2", title: "Second Chronicle", authors: `{"name":"N. N."}`},
		libexRow{asin: "B0NOMENNE3", title: "Third Chronicle", authors: `{"name":"NN"}`},
	), false)

	if sum.NewWorks != 3 {
		t.Fatalf("NewWorks = %d, want 3", sum.NewWorks)
	}
	if !entryExists(t, dataDir, personAddr("unknown")) {
		t.Error("the N.N. spellings did not fold onto unknown")
	}
	if entryExists(t, dataDir, personAddr("n-n")) {
		t.Error("a dotted N.N. spelling minted its own record")
	}
	if !entryExists(t, dataDir, personAddr("nn")) {
		t.Error("the unlisted bare spelling was folded or merged; the table matches exact names only")
	}
}

// TestCollectiveFoldToleratesAnExistingVariantRecord is the INTERIM state this
// change deliberately leaves behind: the catalogue still holds the twins a
// separate data PR will migrate (n-n names 180 works today). A new import must
// neither crash on them nor keep feeding them - it folds onto the canonical,
// creating it if absent, and leaves the twin exactly as it found it.
func TestCollectiveFoldToleratesAnExistingVariantRecord(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/n-/n-n.json":                              `{"id":"n-n","license":"CC0-1.0","name":"N. N.","sources":[{"type":"user"}]}`,
		"works/an/an-old-work/work.json":                  `{"authors":["n-n"],"id":"an-old-work","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"An Old Work"}`,
		"people/be/bea-reader.json":                       `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"user"}]}`,
		"works/an/an-old-work/recordings/bea-reader.json": `{"asin":[{"asin":"B0OLDREC01","region":"uk"}],"id":"bea-reader","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"an-old-work"}`,
	})

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0NEWWORK1", title: "A New Work", authors: `{"name":"N.N."}`},
	))

	if sum.NewWorks != 1 {
		t.Fatalf("NewWorks = %d, want 1", sum.NewWorks)
	}
	var work struct {
		Authors []string `json:"authors"`
	}
	readEntity(t, dataDir, workAddr("a-new-work"), &work)
	if len(work.Authors) != 1 || work.Authors[0] != "unknown" {
		t.Errorf("new work authors = %v, want [unknown]", work.Authors)
	}
	// The twin is untouched, and still the author of the work that names it:
	// this change migrates no data.
	var twin OutPerson
	readEntity(t, dataDir, personAddr("n-n"), &twin)
	if twin.Name != "N. N." || len(twin.Sources) != 1 || twin.Sources[0].Type != "user" {
		t.Errorf("the existing twin record was rewritten: %+v", twin)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

// TestUserLibraryImportersFoldCollectiveCredits proves the fold is not a libex
// rule. It lives in the shared credit cleaning, so every source that states a
// credit gets it - here the two user-library shapes, whose exports carry the
// same Audible credit strings.
func TestUserLibraryImportersFoldCollectiveCredits(t *testing.T) {
	t.Run("audiosilo-books", func(t *testing.T) {
		sum, dataDir := runAudiosiloBooks(t, `{"format":"audiosilo-books","version":1,"books":[
			{"title":"An Anthology","authors":["Autori Vari"],"narrators":["Narratori Vari"],
			 "asin":"B0ASBOOKS1","language":"en","runtime_min":300,"release_date":"2020-01-01"}]}`, false)

		// The Italian author credit and the Italian narrator credit state the
		// same thing, so they are ONE record - which is the whole point.
		if sum.NewPeople != 1 {
			t.Errorf("NewPeople = %d, want 1 (both credits fold onto Various)", sum.NewPeople)
		}
		assertFoldedAnthology(t, dataDir)
	})

	t.Run("openaudible", func(t *testing.T) {
		_, dataDir := runImport(t, `[{"asin":"B0OPENAUD1","title_short":"An Anthology",
			"author":"Autori Vari","narrated_by":"Narratori Vari","language":"english",
			"seconds":18000,"release_date":"2020-01-01","region":"US"}]`, false)

		assertFoldedAnthology(t, dataDir)
	})
}

// assertFoldedAnthology is the shared assertion of the two user-library runs: a
// work authored and narrated by the canonical Various, with no language twin.
func assertFoldedAnthology(t *testing.T, dataDir string) {
	t.Helper()
	var work struct {
		Authors []string `json:"authors"`
	}
	readEntity(t, dataDir, workAddr("an-anthology"), &work)
	if len(work.Authors) != 1 || work.Authors[0] != "various" {
		t.Errorf("work authors = %v, want [various]", work.Authors)
	}
	var rec struct {
		Narrators []string `json:"narrators"`
	}
	readEntity(t, dataDir, recAddr("an-anthology", "various-2020"), &rec)
	if len(rec.Narrators) != 1 || rec.Narrators[0] != "various" {
		t.Errorf("recording narrators = %v, want [various]", rec.Narrators)
	}
	var person OutPerson
	readEntity(t, dataDir, personAddr("various"), &person)
	if person.Name != "Various" {
		t.Errorf("canonical record name = %q, want the canonical spelling %q", person.Name, "Various")
	}
	for _, slug := range []string{"autori-vari", "narratori-vari"} {
		if entryExists(t, dataDir, personAddr(slug)) {
			t.Errorf("a language twin %s was minted", slug)
		}
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}
