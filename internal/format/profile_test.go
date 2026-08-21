package format

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// profile_test.go covers metafmt under a TREE PROFILE (pack.Profile), and the
// gap it closes is exactly the one that let the profile ship half-applied: the
// structural pass was scoped to the profile while the CANONICAL pass still
// walked the whole root, so a subset profile rewrote - and, for an out-of-profile
// LEGACY family that refuseLegacy no longer reached, rewrote IN PLACE while
// exiting 0 - files the profile disclaims.
//
// The contract these pin is one sentence: under a profile, metafmt neither
// touches nor judges a file outside it. What such a file IS remains metacheck's
// unrecognized location (pkg/check's profile tests), which is the other half.

// mustWriteProfile is mustWrite under a profile.
func mustWriteProfile(t *testing.T, dir string, p pack.Profile) Report {
	t.Helper()
	rep, err := WriteProfile(dir, p)
	if err != nil {
		t.Fatalf("WriteProfile(%s): %v", p, err)
	}
	return rep
}

// mustCheckProfile is mustCheck under a profile.
func mustCheckProfile(t *testing.T, dir string, p pack.Profile) Report {
	t.Helper()
	rep, err := CheckProfile(dir, p)
	if err != nil {
		t.Fatalf("CheckProfile(%s): %v", p, err)
	}
	return rep
}

// TestProfileLeavesAnOutOfProfileLegacyFamilyAlone is the VIOLATING fixture that
// found the bug, kept as the regression: a leftover file-per-entity
// works-community/ under the core profile is neither rewritten (the canonical
// pass no longer reaches it) nor refused (refuseLegacy is scoped to the
// profile's families, and a family this tree disclaims is not this tree's
// migration to perform). Its bytes are untouched, and the run still exits
// normally.
func TestProfileLeavesAnOutOfProfileLegacyFamilyAlone(t *testing.T) {
	const body = `{"title":"Dune","id":"dune"}`
	legacyRel := filepath.Join("works-community", "du", "dune", "characters.json")

	for _, mode := range []string{"check", "write"} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(keyed("aa", 4)))
		writeFile(t, filepath.Join(dir, legacyRel), body)
		before := snapshot(t, dir)

		var rep Report
		if mode == "write" {
			rep = mustWriteProfile(t, dir, pack.ProfileCore)
		} else {
			rep = mustCheckProfile(t, dir, pack.ProfileCore)
		}

		raw, err := os.ReadFile(filepath.Join(dir, legacyRel))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != body {
			t.Errorf("%s: an out-of-profile legacy file was rewritten: %s", mode, raw)
		}
		if named(rep, filepath.Join(dir, legacyRel)) {
			t.Errorf("%s: the report names a file the profile disclaims: %v", mode, rep.Lines())
		}
		// The in-profile family was still handled, so this is scoping and not a
		// run that did nothing.
		if mode == "write" {
			if diff := treeDiff(before, snapshot(t, dir)); !strings.Contains(diff, "people/0.json") {
				t.Errorf("write did not format the in-profile family: %s", diff)
			}
			assertProfileClean(t, dir, pack.ProfileCore)
		}
	}

	// The PASSING twin: the very same tree under the default profile IS refused,
	// because there the family is this tree's - which is what makes the silence
	// above the profile's doing and not a lost rule.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(keyed("aa", 4)))
	writeFile(t, filepath.Join(dir, legacyRel), body)
	for name, run := range map[string]func(string) (Report, error){"check": Check, "write": Write} {
		if _, err := run(dir); !errors.Is(err, pack.ErrLegacyLayout) {
			t.Errorf("%s under the default profile: err = %v, want ErrLegacyLayout", name, err)
		}
	}
}

// TestProfileStillRefusesAnInProfileLegacyFamily: the scoping above removed
// nothing from the families a profile DOES hold. A legacy works/ under the core
// profile is refused before a byte is written, exactly as under the default.
func TestProfileStillRefusesAnInProfileLegacyFamily(t *testing.T) {
	const body = `{"title":"Dune","id":"dune"}`
	work := filepath.Join("works", "du", "dune", "work.json")

	for _, tc := range []struct {
		profile pack.Profile
		family  string
		rel     string
	}{
		{pack.ProfileCore, "works", work},
		{pack.ProfileCommunity, "works-community", filepath.Join("works-community", "du", "dune", "characters.json")},
	} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, tc.rel), body)

		for name, run := range map[string]func(string, pack.Profile) (Report, error){
			"check": CheckProfile, "write": WriteProfile,
		} {
			_, err := run(dir, tc.profile)
			if !errors.Is(err, pack.ErrLegacyLayout) {
				t.Errorf("%s %s/%s: err = %v, want ErrLegacyLayout", tc.profile, name, tc.family, err)
			}
			if err != nil && !strings.Contains(err.Error(), "cmd/metamigrate") {
				t.Errorf("%s %s: error does not name the fix: %v", tc.profile, name, err)
			}
		}
		raw, err := os.ReadFile(filepath.Join(dir, tc.rel))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != body {
			t.Errorf("%s: a refused run rewrote the file: %s", tc.profile, raw)
		}
	}
}

// TestProfileDoesNotFormatAnOutOfProfilePackFamily is the same rule for a
// perfectly ordinary PACK family that simply belongs to the other half of the
// split: its non-canonical file is left byte-for-byte alone under the subset
// profile, and formatted under the default. Working-tree churn in a directory
// that is about to be extracted byte-for-byte into another repository is the
// concrete harm.
func TestProfileDoesNotFormatAnOutOfProfilePackFamily(t *testing.T) {
	seed := func(dir string) {
		// Both compact, so both are non-canonical and either could be formatted.
		writeFile(t, filepath.Join(dir, "works", "0", "0.json"), packOf(keyed("dune", 4)))
		writeFile(t, filepath.Join(dir, "works-community", "0", "0.json"), packOf(keyed("dune", 4)))
	}
	const communityRel = "works-community/0/0.json"
	const worksRel = "works/0/0.json"

	// Under the default profile BOTH are formatted - the baseline the subset
	// runs are measured against.
	base := t.TempDir()
	seed(base)
	before := snapshot(t, base)
	mustWrite(t, base)
	after := snapshot(t, base)
	for _, rel := range []string{worksRel, communityRel} {
		if after[rel] == before[rel] {
			t.Fatalf("the default profile did not format %s, so the fixture proves nothing", rel)
		}
	}

	for _, tc := range []struct {
		profile     pack.Profile
		formatted   string
		leftAlone   string
		description string
	}{
		{pack.ProfileCore, worksRel, communityRel, "core leaves works-community alone"},
		{pack.ProfileCommunity, communityRel, worksRel, "community leaves works alone"},
	} {
		dir := t.TempDir()
		seed(dir)
		before := snapshot(t, dir)

		// --check must not even REPORT it: naming a file it will never fix would
		// send a contributor after work that is not theirs.
		rep := mustCheckProfile(t, dir, tc.profile)
		if named(rep, filepath.Join(dir, filepath.FromSlash(tc.leftAlone))) {
			t.Errorf("%s: --check named %s: %v", tc.description, tc.leftAlone, rep.Lines())
		}
		if !named(rep, filepath.Join(dir, filepath.FromSlash(tc.formatted))) {
			t.Errorf("%s: --check did not name %s: %v", tc.description, tc.formatted, rep.Lines())
		}
		if diff := treeDiff(before, snapshot(t, dir)); diff != "" {
			t.Errorf("%s: --check wrote something: %s", tc.description, diff)
		}

		mustWriteProfile(t, dir, tc.profile)
		got := snapshot(t, dir)
		if got[tc.leftAlone] != before[tc.leftAlone] {
			t.Errorf("%s: --write reformatted %s", tc.description, tc.leftAlone)
		}
		if got[tc.formatted] == before[tc.formatted] {
			t.Errorf("%s: --write did not format %s", tc.description, tc.formatted)
		}
	}
}

// TestCommunityProfileLeavesTheTombstoneTableAlone: the redirects file is core
// glue, so a community root neither formats it nor reports it. (pkg/check is
// where it becomes an unrecognized location.) The core profile, which does carry
// the table, formats it exactly as the default does - the passing twin, so this
// is scoping rather than a rule that stopped running.
func TestCommunityProfileLeavesTheTombstoneTableAlone(t *testing.T) {
	// Deliberately non-canonical: compact, and with the namespaces out of order.
	const table = `{"works":{"old":"new"},"people":{},"series":{}}`

	community := t.TempDir()
	writeFile(t, filepath.Join(community, "works-community", "0", "0.json"), packOf(keyed("dune", 4)))
	writeFile(t, filepath.Join(community, pack.RedirectsFile), table)

	rep := mustCheckProfile(t, community, pack.ProfileCommunity)
	if named(rep, filepath.Join(community, pack.RedirectsFile)) {
		t.Errorf("the community profile reported the tombstone table: %v", rep.Lines())
	}
	mustWriteProfile(t, community, pack.ProfileCommunity)
	raw, err := os.ReadFile(filepath.Join(community, pack.RedirectsFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != table {
		t.Errorf("the community profile rewrote the tombstone table: %s", raw)
	}

	core := t.TempDir()
	writeFile(t, filepath.Join(core, "works", "0", "0.json"), packOf(keyed("dune", 4)))
	writeFile(t, filepath.Join(core, pack.RedirectsFile), table)
	mustWriteProfile(t, core, pack.ProfileCore)
	raw, err = os.ReadFile(filepath.Join(core, pack.RedirectsFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == table {
		t.Error("the core profile did not format the tombstone table it carries")
	}
}

// TestProfileStillFormatsStrayJSONAtTheDataRoot pins the SUBTRACTIVE choice: a
// profile removes the paths it DISCLAIMS, not "everything outside its families".
// A stray file at the data root belongs to no family and has always been
// formatted; a profile is not a reason to stop.
func TestProfileStillFormatsStrayJSONAtTheDataRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "works-community", "0", "0.json"), packOf(keyed("dune", 4)))
	writeFile(t, filepath.Join(dir, "notes.json"), `{"b":1,"a":2}`)

	mustWriteProfile(t, dir, pack.ProfileCommunity)
	raw, err := os.ReadFile(filepath.Join(dir, "notes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == `{"b":1,"a":2}` {
		t.Errorf("a stray root file was not formatted under a profile: %s", raw)
	}
}

// TestProfileWriteConverges: one pass leaves a subset root clean, and a second
// is a byte-level no-op - the property the whole-tree runs already have, held
// under a profile too.
func TestProfileWriteConverges(t *testing.T) {
	for _, p := range []pack.Profile{pack.ProfileCore, pack.ProfileCommunity} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "works", "0", "0.json"), packOf(keyed("dune", 4)))
		writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(keyed("aa", 4), keyed("zz", 4)))
		writeFile(t, filepath.Join(dir, "people", "mm.json"), packOf(keyed("mm", 4)))
		writeFile(t, filepath.Join(dir, "works-community", "0", "0.json"), packOf(keyed("dune", 4)))

		mustWriteProfile(t, dir, p)
		assertProfileClean(t, dir, p)

		before := snapshot(t, dir)
		rep := mustWriteProfile(t, dir, p)
		if !rep.Clean() {
			t.Errorf("%s: second run reported work: %v", p, rep.Lines())
		}
		if diff := treeDiff(before, snapshot(t, dir)); diff != "" {
			t.Errorf("%s: second run changed the tree: %s", p, diff)
		}
	}
}

// assertProfileClean fails the test when the tree does not check clean under p.
// assertClean is this at ProfileAll.
func assertProfileClean(t *testing.T, dir string, p pack.Profile) {
	t.Helper()
	rep := mustCheckProfile(t, dir, p)
	if !rep.Clean() {
		t.Fatalf("%s: tree is not clean after --write:\n  %s", p, strings.Join(rep.Lines(), "\n  "))
	}
}

// named reports whether the report mentions path anywhere a contributor reads -
// unlike its siblings hasLine/hasPrefix (format_test.go) it matches a substring
// of ANY line and additionally consults rep.Invalid, which Lines() omits on the
// Write path.
func named(rep Report, path string) bool {
	for _, line := range rep.Lines() {
		if strings.Contains(line, path) {
			return true
		}
	}
	for _, f := range rep.Invalid {
		if f == path {
			return true
		}
	}
	return false
}
