package importer

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestBareRoleVocabulary pins the two structural claims barerole.go rests on:
// the words are in their own comparison form, and what each one STATES comes
// from roleQualifiers rather than a second table that could drift from it. It
// also pins the exclusions, which are the measurement's real product - every
// word below fires on a dump name that is an entity, a surname or a stage name.
func TestBareRoleVocabulary(t *testing.T) {
	for word := range bareRoleTails {
		if got := foldCredit(word); got != word {
			t.Errorf("bareRoleTails key %q is not canonical (foldCredit gives %q); it can never match", word, got)
		}
		if len(strings.Fields(word)) != 1 {
			t.Errorf("bareRoleTails key %q is not a single token", word)
		}
		if _, listed := roleQualifiers[word]; !listed {
			t.Errorf("bareRoleTails key %q is not in roleQualifiers; the welded and dashed spellings of one qualifier must state the same thing", word)
		}
	}
	for _, word := range []string{
		"editora",        // Portuguese publishing houses: "Luz da Serra Editora", "On Line Editora"
		"scribe",         // a surname: Augustin Eugène Scribe, A. P. Scribe, J. U. Scribe
		"music", "musik", // entity names: "Amazon Music", "Meditative Minds Music"
		"translation",   // "New Living Translation", "Spanish Book Translation"
		"producer",      // "West End Producer", a persona
		"director",      // "Emeka the Director"
		"illustrations", // "Luna Bright Illustrations", a studio
		"compilation",   // "1 Hour Fail Compilation"
		"dramatization", // "Full Cast Dramatization", a collective statement
	} {
		if bareRoleTails[word] {
			t.Errorf("bareRoleTails holds %q; the dump's names ending in it are entities, surnames or stage names, not people with a welded qualifier", word)
		}
	}
}

// TestBareRoleTailStrips is the measured target list: every dump name the three
// gates admit, with the head attested exactly as the run's census would attest
// it. All eleven are a person carrying a qualifier the source welded on with no
// separator at all.
func TestBareRoleTailStrips(t *testing.T) {
	cases := []struct {
		in        string
		want      string
		wantRoles []string
	}{
		{"Andrew Lang Editor", "Andrew Lang", []string{model.RoleEditor}},
		{"Arthur B. Reeve Editor", "Arthur B. Reeve", []string{model.RoleEditor}},
		{"Murray Stein editor", "Murray Stein", []string{model.RoleEditor}},
		{"Basil Hall Chamberlain translator", "Basil Hall Chamberlain", []string{model.RoleTranslator}},
		{"Frank Wynne Translator", "Frank Wynne", []string{model.RoleTranslator}},
		{"Lionel Giles translator", "Lionel Giles", []string{model.RoleTranslator}},
		{"Martin McLaughlin translator", "Martin McLaughlin", []string{model.RoleTranslator}},
		{"David Coward Translator", "David Coward", []string{model.RoleTranslator}},
		{"James Martin SJ Foreword", "James Martin SJ", []string{model.RoleForeword}},
		// Narration is modeled by recording.narrators, so the qualifier states
		// nothing even though it strips.
		{"Kate Reading Narrator", "Kate Reading", nil},
		{"Alexander Scourby Narrator", "Alexander Scourby", nil},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, roles := stripBareRoleTail(c.in, seenAs(c.want))
			if got != c.want {
				t.Errorf("stripBareRoleTail(%q) = %q, want %q", c.in, got, c.want)
			}
			if !slices.Equal(roles, c.wantRoles) {
				t.Errorf("roles = %v, want %v", roles, c.wantRoles)
			}
		})
	}
}

// TestBareRoleTailNeedsTheCensus is the gate the shape cannot supply itself: the
// same string is decided by the attestation universe alone. Both names here are
// real dump names whose head nobody has been credited under, and both import as
// the source spelled them.
func TestBareRoleTailNeedsTheCensus(t *testing.T) {
	for _, name := range []string{"Benjamin Drew editor", "Los Andes Grupo Editor", "A. D. Godley Translator"} {
		t.Run(name, func(t *testing.T) {
			for _, seen := range []creditSeenFunc{nil, seenAs("Somebody Else")} {
				if got, roles := stripBareRoleTail(name, seen); got != name || roles != nil {
					t.Errorf("stripBareRoleTail(%q) = %q %v, want it untouched", name, got, roles)
				}
			}
		})
	}
}

// TestBareRoleTailRefusesStructurally is the half that does NOT depend on the
// census: every name here is judged under the most generous census possible and
// must still come back whole. Between them they exercise all four head guards.
func TestBareRoleTailRefusesStructurally(t *testing.T) {
	intact := []string{
		// The stage-name shape: the role word is the name, and an article in
		// front of it is what says so (6 dump names).
		"Cricket the Narrator",
		"Erikka The Narrator",
		"KM the Narrator",
		// A one-token head is no evidence at all (minPersonTokens).
		"Lang Editor",
		// A head ending in an entity word means the cut landed inside the
		// entity's own name (personHalfOK).
		"Blackstone Audio Editor",
		// A head ending in a co-credit conjunction, likewise one token too late.
		"Bob Carter & Editor",
		// A head ending in a stray separator: the census would still recognize
		// it (Slugify drops the punctuation) and the name would keep the pipe.
		"Jane Doe | Editor",
		// The vocabulary's own boundary: these tails strip after a DASH and
		// never on their own.
		"Augustin Eugène Scribe",
		"Public Play Editora",
		"Meditative Minds Music",
		"New Living Translation",
		"West End Producer",
		"Luna Bright Illustrations",
		"Full Cast Dramatization",
	}
	for _, name := range intact {
		t.Run(name, func(t *testing.T) {
			if got, _ := stripBareRoleTail(name, everyFragmentSeen(name)); got != name {
				t.Errorf("stripBareRoleTail(%q) = %q, want it untouched", name, got)
			}
		})
	}
}

// TestBareRoleTailComposesWithTheDashRule is the two names the full-dump replay
// turned up beyond the eleven the shape alone predicts, and the reason the rule
// sits INSIDE the fixpoint rather than beside it. "editor foreword" is not a
// listed combined qualifier, so the dash rule cannot take it whole and both
// names minted otto-penzler-editor-foreword; the welded rule takes the foreword
// off (the head "<name> - editor" is attested), and the next pass takes the
// dash-separated editor off what is left. Two rules, both roles, no new table
// entry.
func TestBareRoleTailComposesWithTheDashRule(t *testing.T) {
	for _, name := range []string{"Otto Penzler - editor foreword", "Mark Jones - editor foreword"} {
		want := strings.TrimSuffix(name, " - editor foreword")
		c := creditCensus{anySide: seenAs(want + " - editor"), sameSide: seenAs(want)}
		gotName, gotRoles := creditWithRolesSided(name, c)
		if gotName != want {
			t.Errorf("cleaned %q = %q, want %q", name, gotName, want)
		}
		if wantRoles := []string{model.RoleEditor, model.RoleForeword}; !slices.Equal(gotRoles, wantRoles) {
			t.Errorf("roles = %v, want %v", gotRoles, wantRoles)
		}
	}
}

// TestBareRoleTailIsSilentWithoutACensus keeps the public door honest: the shape
// alone is not evidence, so a tag or a typed name never triggers the rule.
func TestBareRoleTailIsSilentWithoutACensus(t *testing.T) {
	const name = "Andrew Lang Editor"
	if got := CleanCreditName(name); got != name {
		t.Errorf("CleanCreditName(%q) = %q, want the name unchanged", name, got)
	}
}

// TestBareRoleTailMergesOntoTheCreditedPerson is the run-level proof: the dump
// credits Andrew Lang both ways, and without the rule the welded spelling minted
// andrew-lang-editor beside him.
func TestBareRoleTailMergesOntoTheCreditedPerson(t *testing.T) {
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0BAREROL1", title: "The Blue Fairy Book", authors: `{"name":"Andrew Lang"}`},
		libexRow{asin: "B0BAREROL2", title: "Cinderella, or The Glass Slipper", authors: `{"name":"Andrew Lang Editor"}`},
	), false)

	if sum.NewPeople != 2 { // the one author plus the shared narrator
		t.Fatalf("NewPeople = %d, want 2: the welded qualifier forked the author", sum.NewPeople)
	}
	if entryExists(t, dataDir, personAddr("andrew-lang-editor")) {
		t.Error("the welded-qualifier person record was created")
	}
	if !entryExists(t, dataDir, personAddr("andrew-lang")) {
		t.Error("the bare person record is missing")
	}
}
