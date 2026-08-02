package importer

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// wrongmerge_test.go pins the identity rules an adversarial review of seed wave
// 5 found firing the wrong way. Each test here is derived from one of that
// review's reproductions, and each one measured real damage in the tree or in a
// wave-6 simulation - none is hypothetical.
//
// They are grouped by the question they answer:
//
//	WRONG MERGES  two different books, or two different humans, became one
//	WRONG SPLITS  one book became two works, or a row found no work at all
//
// The seed literals are deliberately small and hand-written: every one of them
// is a reduction of a named pair from the real data, and the comment says which.

// personAddr composes the per-entity address of a person record, the way
// workAddr and seriesAddr do for their families.
func personAddr(slug string) string {
	return "people/" + shard(slug) + "/" + slug + ".json"
}

// personRec renders a person record.
func personRec(id, name string) string {
	return `{"id":"` + id + `","license":"CC0-1.0","name":"` + name + `","sources":[{"type":"user"}]}`
}

// workRec renders a work record. authorsJSON and creditsJSON are JSON array
// bodies ("a","b" / {"person":"a","role":"translator"}).
func workRec(id, title, lang, authorsJSON, creditsJSON string) string {
	credits := ""
	if creditsJSON != "" {
		credits = `,"credits":[` + creditsJSON + `]`
	}
	return `{"authors":[` + authorsJSON + `],"id":"` + id + `","language":"` + lang + `"` + credits +
		`,"license":"CC0-1.0","sources":[{"type":"user"}],"title":"` + title + `"}`
}

// recRec renders a recording record.
func recRec(work, id, lang, narrator, asin string, runtime int) string {
	return `{"asin":[{"asin":"` + asin + `","region":"us"}],"id":"` + id + `","language":"` + lang +
		`","license":"CC0-1.0","narrators":["` + narrator + `"],"runtime_min":` + itoa(runtime) +
		`,"sources":[{"type":"user"}],"work":"` + work + `"}`
}

// ---------------------------------------------------------------------------
// WRONG MERGE: across a language boundary

// TestTranslationDoesNotMergeIntoItsOriginal is the enchantra pair: the bare
// slug holds the English work and the author-suffixed one its German
// translation. Once the translators are excluded from identity the two reduce
// to the SAME author set, so nothing but the language tells them apart - and a
// 20,000-row wave-6 simulation left 82 more cross-language recordings in the
// tree without this test than with it.
func TestTranslationDoesNotMergeIntoItsOriginal(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/ka/kaylie-smith.json":   personRec("kaylie-smith", "Kaylie Smith"),
		"people/ju/julian-muller.json":  personRec("julian-muller", "Julian Muller"),
		"people/la/laura-horowitz.json": personRec("laura-horowitz", "Laura Horowitz"),
		"works/en/enchantra/work.json":  workRec("enchantra", "Enchantra", "en", `"kaylie-smith"`, ""),
		"works/en/enchantra/recordings/laura-horowitz-2025.json": recRec(
			"enchantra", "laura-horowitz-2025", "en", "laura-horowitz", "B0ENCHANT0", 600),
	})

	sum := runLibexInto(t, dataDir, rows(libexRow{
		asin: "B0ENCHANT2", title: "Enchantra", language: "german", minutes: 610,
		narrators: `{"name":"Greta Sprecherin"}`,
		authors:   `{"name":"Julian Muller - Ubersetzer"},{"name":"Kaylie Smith"}`,
	}))

	if entryExists(t, dataDir, recAddr("enchantra", "greta-sprecherin-2020")) {
		t.Error("a German recording was attached to the English work 'enchantra'")
	}
	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1: the translation must become a work of its own", sum.NewWorks)
	}
}

// TestUnknownLanguageStillMerges holds the other side of the guard: it fires on
// evidence only, so a work or a row that states no language never blocks.
func TestUnknownLanguageStillMerges(t *testing.T) {
	// The unknown-on-either-side cases are stated as a unit assertion: the work
	// schema requires a language, so a work with none cannot be written, and a
	// row whose language does not map is refused before it reaches a work.
	for _, c := range []struct{ have, want string }{{"", "de"}, {"en", ""}, {"", ""}} {
		if !langCompatible(c.have, c.want) {
			t.Errorf("langCompatible(%q, %q) = false: an unknown language must never block", c.have, c.want)
		}
	}
	if langCompatible("en", "de") {
		t.Error(`langCompatible("en", "de") = true`)
	}
	for _, workLang := range []string{"en"} {
		dataDir := t.TempDir()
		seedTree(t, dataDir, map[string]string{
			"people/ad/ada-mapmaker.json":  personRec("ada-mapmaker", "Ada Mapmaker"),
			"people/an/ann-reader.json":    personRec("ann-reader", "Ann Reader"),
			"works/th/the-tower/work.json": workRec("the-tower", "The Tower", workLang, `"ada-mapmaker"`, ""),
			"works/th/the-tower/recordings/ann-reader-2001.json": recRec(
				"the-tower", "ann-reader-2001", "en", "ann-reader", "B0TOWER001", 400),
		})
		sum := runLibexInto(t, dataDir, rows(libexRow{
			asin: "B0TOWER002", title: "The Tower", minutes: 400,
			authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Bo Reader"}`,
		}))
		if sum.NewWorks != 0 {
			t.Errorf("work language %q: NewWorks = %d, want 0 - an unknown language must never block a merge",
				workLang, sum.NewWorks)
		}
	}
}

// ---------------------------------------------------------------------------
// WRONG MERGE: two humans behind one honorific

// TestHonorificNeedsTheSameCreditSide is the Richard Burton pair: the catalogue
// holds Richard Burton the actor (28 narrations in the dump) and a row credits
// "Sir Richard Burton" the Victorian translator as its AUTHOR. Seven such pairs
// were measured; every one is an author meeting a narrator, or the reverse.
func TestHonorificNeedsTheSameCreditSide(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/ad/ada-mapmaker.json":   personRec("ada-mapmaker", "Ada Mapmaker"),
		"people/ri/richard-burton.json": personRec("richard-burton", "Richard Burton"),
		"works/se/seed-work/work.json":  workRec("seed-work", "Seed Work", "en", `"ada-mapmaker"`, ""),
		"works/se/seed-work/recordings/richard-burton-2001.json": recRec(
			"seed-work", "richard-burton-2001", "en", "richard-burton", "B0SEED0001", 300),
	})

	runLibexInto(t, dataDir, rows(libexRow{
		asin: "B0BURTON01", title: "The Kasidah",
		authors: `{"name":"Sir Richard Burton"}`, narrators: `{"name":"Ann Reader"}`,
	}))

	var work struct{ Authors []string }
	readEntity(t, dataDir, workAddr("the-kasidah"), &work)
	if len(work.Authors) == 1 && work.Authors[0] == "richard-burton" {
		t.Errorf("the author 'Sir Richard Burton' was written as the catalogued NARRATOR %q", work.Authors[0])
	}
	if !entryExists(t, dataDir, personAddr("sir-richard-burton")) {
		t.Errorf("the author kept no record of his own; authors = %v", work.Authors)
	}
}

// TestHonorificSameSideStillMerges is the rule doing its job: the bare twin is
// credited on the same side, so the courtesy title resolves onto it and no
// second record is minted. This is the shape of all 41 correct merges.
func TestHonorificSameSideStillMerges(t *testing.T) {
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0DOYLE001", title: "A Study In Scarlet",
			authors: `{"name":"Arthur Conan Doyle"}`, narrators: `{"name":"Ann Reader"}`},
		libexRow{asin: "B0DOYLE002", title: "The Sign Of Four",
			authors: `{"name":"Sir Arthur Conan Doyle"}`, narrators: `{"name":"Ann Reader"}`},
	), false)

	if entryExists(t, dataDir, personAddr("sir-arthur-conan-doyle")) {
		t.Error("the honorific spelling minted a second person record")
	}
	if !entryExists(t, dataDir, personAddr("arthur-conan-doyle")) {
		t.Fatal("no arthur-conan-doyle record")
	}
	if want := "Sir Arthur Conan Doyle -> Arthur Conan Doyle"; !containsLine(sum.HonorificMerges, want) {
		t.Errorf("HonorificMerges = %v, want it to report %q", sum.HonorificMerges, want)
	}
}

// TestHonorificMergesAreEmptyWhenTheRuleNeverFires keeps the report honest: a
// wave that merged nobody says so by carrying nothing.
func TestHonorificMergesAreEmptyWhenTheRuleNeverFires(t *testing.T) {
	sum, _ := runLibex(t, rows(libexRow{
		asin: "B0PLAIN001", title: "Plain Book",
		authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Ann Reader"}`,
	}), false)
	if len(sum.HonorificMerges) != 0 {
		t.Errorf("HonorificMerges = %v, want none", sum.HonorificMerges)
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// WRONG MERGE: the serial-position suffix meeting a work that is not a volume

// TestSerialSuffixDoesNotAbsorbAnUnrelatedWork is the live landmine: 258 works
// in the tree already sit on a "<something>-book-<N>" slug because their TITLE
// ends that way. The pre-pass mints exactly those slugs, so a suffixed row must
// never treat one as its own volume.
func TestSerialSuffixDoesNotAbsorbAnUnrelatedWork(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/er/erin-hunter.json": personRec("erin-hunter", "Erin Hunter"),
		"people/an/ann-reader.json":  personRec("ann-reader", "Ann Reader"),
		// A DIFFERENT book by the same author, whose own title is "Bravelands
		// Book 1" - not volume 1 of the serial the rows below claim.
		"works/br/bravelands-book-1/work.json": workRec(
			"bravelands-book-1", "Bravelands Book 1", "en", `"erin-hunter"`, ""),
		"works/br/bravelands-book-1/recordings/ann-reader-2001.json": recRec(
			"bravelands-book-1", "ann-reader-2001", "en", "ann-reader", "B0SEEDBK10", 400),
	})

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0COLL0001", title: "Bravelands", authors: `{"name":"Erin Hunter"}`, minutes: 505,
			series: `{"name":"Bravelands","position":"1"}`},
		libexRow{asin: "B0COLL0002", title: "Bravelands", authors: `{"name":"Erin Hunter"}`, minutes: 510,
			series: `{"name":"Bravelands","position":"2"}`},
	))

	var seeded struct {
		Recordings map[string]struct {
			ASIN []struct{ ASIN string }
		}
	}
	readEntity(t, dataDir, workAddr("bravelands-book-1"), &seeded)
	for _, rec := range seeded.Recordings {
		for _, a := range rec.ASIN {
			if a.ASIN == "B0COLL0001" {
				t.Errorf("the suffixed row's ASIN %s landed on the unrelated work bravelands-book-1", a.ASIN)
			}
		}
	}
	if sum.NewWorks != 2 {
		t.Errorf("NewWorks = %d, want 2: both volumes are new books", sum.NewWorks)
	}
}

// TestSerialSuffixNamespacesTwoSeries is the two-series-one-group case: one
// title, one author, volumes in two different serials. "Book 1 of Alpha" and
// "Book 1 of Beta" are different books, so the tail has to name the series too.
func TestSerialSuffixNamespacesTwoSeries(t *testing.T) {
	const author = `{"name":"Erin Hunter"}`
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0ALPHA001", title: "Bravelands", authors: author, minutes: 500,
			series: `{"name":"Alpha","position":"1"}`},
		libexRow{asin: "B0ALPHA002", title: "Bravelands", authors: author, minutes: 505,
			series: `{"name":"Alpha","position":"2"}`},
		libexRow{asin: "B0BETA0001", title: "Bravelands", authors: author, minutes: 502,
			series: `{"name":"Beta","position":"1"}`},
	), false)

	if sum.NewWorks != 3 {
		t.Errorf("NewWorks = %d, want 3: three different books", sum.NewWorks)
	}
	if sum.MergedASINs != 0 {
		t.Errorf("MergedASINs = %d, want 0: an ASIN crossed between two books", sum.MergedASINs)
	}
	for _, slug := range []string{"bravelands-alpha-book-1", "bravelands-alpha-book-2", "bravelands-beta-book-1"} {
		if !entryExists(t, dataDir, workAddr(slug)) {
			t.Errorf("no work at %q", slug)
		}
	}
}

// TestSerialSuffixStaysBareInOneSeries keeps the namespacing to the case that
// needs it: the ordinary single-series serial must not grow a redundant tail.
func TestSerialSuffixStaysBareInOneSeries(t *testing.T) {
	const author = `{"name":"Erin Hunter"}`
	_, dataDir := runLibex(t, rows(
		libexRow{asin: "B0BRAVE001", title: "Bravelands", authors: author, minutes: 500,
			series: `{"name":"Bravelands","position":"1"}`},
		libexRow{asin: "B0BRAVE002", title: "Bravelands", authors: author, minutes: 505,
			series: `{"name":"Bravelands","position":"2"}`},
	), false)
	for _, slug := range []string{"bravelands-book-1", "bravelands-book-2"} {
		if !entryExists(t, dataDir, workAddr(slug)) {
			t.Errorf("no work at %q", slug)
		}
	}
}

// TestSerialPositionSuffixSeparatesDecimalFromRange: a slug carries no '.', so
// slugifying alone maps the decimal position "2.5" and the omnibus range "2-5"
// onto one tail - and one slug for two volumes is the collapse the whole
// pre-pass exists to prevent.
func TestSerialPositionSuffixSeparatesDecimalFromRange(t *testing.T) {
	cases := map[string]string{
		"1":     "book-1",
		"2.5":   "book-2-5",
		"2-5":   "book-2-to-5",
		"1-3.5": "book-1-to-3-5",
		"10":    "book-10",
		"0":     "book-0",
	}
	seen := map[string]string{}
	for pos, want := range cases {
		got := serialPositionSuffix(pos)
		if got != want {
			t.Errorf("serialPositionSuffix(%q) = %q, want %q", pos, got, want)
		}
		if other, clash := seen[got]; clash {
			t.Errorf("positions %q and %q share the suffix %q", other, pos, got)
		}
		seen[got] = pos
	}
}

// ---------------------------------------------------------------------------
// WRONG SPLIT: a later run meeting a work the serial pre-pass suffixed
//
// The pre-pass fires on a BATCH holding two same-titled volumes. Seed wave 6
// minted the first suffixed works in the tree, so from now on a run can bring
// ONE volume of a serial whose siblings are already catalogued - and a lone row
// composes the bare base, which is not where that work sits. Finding it again is
// the row's own series claim: the tail is a function of (series, position), both
// of which the row states.

// seedBravelandsVolumes is the two-volume serial as an earlier run left it: the
// pre-pass suffixed both works and the series records which volume each is.
// Everything below starts from this tree and brings ONE more row.
func seedBravelandsVolumes(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0SEEDVOL1", title: "Bravelands", authors: `{"name":"Erin Hunter"}`, minutes: 500,
			series: `{"name":"Bravelands","position":"1"}`},
		libexRow{asin: "B0SEEDVOL2", title: "Bravelands", authors: `{"name":"Erin Hunter"}`, minutes: 505,
			series: `{"name":"Bravelands","position":"2"}`},
	))
	if sum.NewWorks != 2 {
		t.Fatalf("seed run: NewWorks = %d, want 2", sum.NewWorks)
	}
	for _, slug := range []string{"bravelands-book-1", "bravelands-book-2"} {
		if !entryExists(t, dataDir, workAddr(slug)) {
			t.Fatalf("seed run left no work at %q", slug)
		}
	}
	return dataDir
}

// workHasASIN reports whether any recording of a work carries the ASIN.
func workHasASIN(t *testing.T, dataDir, slug, asin string) bool {
	t.Helper()
	for _, rec := range recSlugsOf(t, dataDir, slug) {
		var r struct {
			ASIN []struct{ ASIN string }
		}
		readEntity(t, dataDir, recAddr(slug, rec), &r)
		for _, a := range r.ASIN {
			if a.ASIN == asin {
				return true
			}
		}
	}
	return false
}

// TestLoneVolumeFindsItsSuffixedWork is the reachability half of the rule, and
// the defect the real-data slug test caught: a single row for volume 1 of a
// serial whose work already sits at "bravelands-book-1" must land there, not
// mint a second work at the bare slug.
func TestLoneVolumeFindsItsSuffixedWork(t *testing.T) {
	dataDir := seedBravelandsVolumes(t)

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0LONEVOL1", title: "Bravelands", authors: `{"name":"Erin Hunter"}`, minutes: 500,
			series: `{"name":"Bravelands","position":"1"}`},
	))

	if sum.NewWorks != 0 {
		t.Errorf("NewWorks = %d, want 0: volume 1 is already in the catalogue", sum.NewWorks)
	}
	if entryExists(t, dataDir, workAddr("bravelands")) {
		t.Error("a duplicate work was minted at the bare slug beside bravelands-book-1")
	}
	if !workHasASIN(t, dataDir, "bravelands-book-1", "B0LONEVOL1") {
		t.Error("the row's ASIN did not land on bravelands-book-1")
	}
}

// TestLoneVolumeFindsTheSeriesScopedSuffixedWork is the same reachability for
// the other tail the pre-pass mints: a group spanning two serials namespaces the
// suffix with the series, so the probe has to address that form too.
func TestLoneVolumeFindsTheSeriesScopedSuffixedWork(t *testing.T) {
	const author = `{"name":"Erin Hunter"}`
	dataDir := t.TempDir()
	runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0SCOPEDA1", title: "Bravelands", authors: author, minutes: 500,
			series: `{"name":"Alpha","position":"1"}`},
		libexRow{asin: "B0SCOPEDA2", title: "Bravelands", authors: author, minutes: 505,
			series: `{"name":"Alpha","position":"2"}`},
		libexRow{asin: "B0SCOPEDB1", title: "Bravelands", authors: author, minutes: 502,
			series: `{"name":"Beta","position":"1"}`},
	))
	if !entryExists(t, dataDir, workAddr("bravelands-alpha-book-1")) {
		t.Fatal("seed run left no work at bravelands-alpha-book-1")
	}

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0SCOPELN1", title: "Bravelands", authors: author, minutes: 500,
			series: `{"name":"Alpha","position":"1"}`},
	))

	if sum.NewWorks != 0 {
		t.Errorf("NewWorks = %d, want 0: Alpha volume 1 is already in the catalogue", sum.NewWorks)
	}
	if !workHasASIN(t, dataDir, "bravelands-alpha-book-1", "B0SCOPELN1") {
		t.Error("the row's ASIN did not land on bravelands-alpha-book-1")
	}
}

// TestLoneVolumeAtAnotherPositionKeepsItsOwnWork is the refusal the reachability
// must not cost: a row claiming a position the suffixed work does not hold is a
// DIFFERENT volume, and the series says so.
func TestLoneVolumeAtAnotherPositionKeepsItsOwnWork(t *testing.T) {
	dataDir := seedBravelandsVolumes(t)

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0LONEVOL3", title: "Bravelands", authors: `{"name":"Erin Hunter"}`, minutes: 500,
			series: `{"name":"Bravelands","position":"3"}`},
	))

	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1: volume 3 is a book the catalogue does not hold", sum.NewWorks)
	}
	for _, slug := range []string{"bravelands-book-1", "bravelands-book-2"} {
		if workHasASIN(t, dataDir, slug, "B0LONEVOL3") {
			t.Errorf("volume 3's ASIN landed on %q", slug)
		}
	}
}

// TestClaimlessRowNeverReachesASuffixedWork is the other refusal, and the one
// that keeps the 258 title-suffixed works safe: a row that states no position
// probes no suffixed slug and can be placed by no series, so it keeps its own
// work exactly as it did before the probe existed.
func TestClaimlessRowNeverReachesASuffixedWork(t *testing.T) {
	dataDir := seedBravelandsVolumes(t)

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0NOCLAIM1", title: "Bravelands", authors: `{"name":"Erin Hunter"}`, minutes: 500},
	))

	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1: a claim-less row cannot claim a volume's work", sum.NewWorks)
	}
	if !entryExists(t, dataDir, workAddr("bravelands")) {
		t.Error("the claim-less row must mint its own work at the bare slug")
	}
	for _, slug := range []string{"bravelands-book-1", "bravelands-book-2"} {
		if workHasASIN(t, dataDir, slug, "B0NOCLAIM1") {
			t.Errorf("the claim-less row's ASIN landed on %q", slug)
		}
	}
}

// TestRepeatedSerialBatchDoesNotDuplicate is the same gap on the BATCH path: a
// second run bringing both volumes again composes the same suffixes, and without
// the placement test would refuse the works it minted itself last time and mint
// "bravelands-book-1-2" beside them.
func TestRepeatedSerialBatchDoesNotDuplicate(t *testing.T) {
	dataDir := seedBravelandsVolumes(t)

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0REPEATV1", title: "Bravelands", authors: `{"name":"Erin Hunter"}`, minutes: 500,
			series: `{"name":"Bravelands","position":"1"}`},
		libexRow{asin: "B0REPEATV2", title: "Bravelands", authors: `{"name":"Erin Hunter"}`, minutes: 505,
			series: `{"name":"Bravelands","position":"2"}`},
	))

	if sum.NewWorks != 0 {
		t.Errorf("NewWorks = %d, want 0: both volumes are already in the catalogue", sum.NewWorks)
	}
	if entryExists(t, dataDir, workAddr("bravelands-book-1-2")) {
		t.Error("the second run minted a numbered duplicate of its own suffixed work")
	}
	if !workHasASIN(t, dataDir, "bravelands-book-1", "B0REPEATV1") ||
		!workHasASIN(t, dataDir, "bravelands-book-2", "B0REPEATV2") {
		t.Error("a re-imported volume's ASIN did not land on its own work")
	}
}

// TestPositionProbesAreLookOnly pins what the probe is allowed to be: a place to
// LOOK. Nothing may ever be minted at a position-suffixed slug outside the
// pre-pass, or a claim-bearing row would start creating works at addresses the
// batch path owns.
func TestPositionProbesAreLookOnly(t *testing.T) {
	authors := workAuthors{all: []string{"erin-hunter"}, identity: []string{"erin-hunter"}}
	cands, primary := workCandidates("bravelands", authors, positionClaim{series: "Alpha", pos: "1"})

	var probes []string
	for i, c := range cands {
		if !c.posSuffixed {
			continue
		}
		if !c.probeOnly {
			t.Errorf("candidate %q carries a position suffix but is mintable", c.slug)
		}
		if i >= primary {
			t.Errorf("candidate %q sits past the primary count, where the walk may stop early", c.slug)
		}
		probes = append(probes, c.slug)
	}
	want := []string{"bravelands-book-1", "bravelands-alpha-book-1"}
	if !slices.Equal(probes, want) {
		t.Errorf("position probes = %v, want %v", probes, want)
	}

	// And a row that states no claim probes nothing at all.
	bare, _ := workCandidates("bravelands", authors, positionClaim{})
	for _, c := range bare {
		if c.posSuffixed {
			t.Errorf("a claim-less row probed the suffixed slug %q", c.slug)
		}
	}
}

// ---------------------------------------------------------------------------
// WRONG SPLIT: a contributor spelled bare on one row and role-qualified on
// another

// TestBareAndQualifiedContributorAreOneWork is the French Witcher shape, in
// both orders and against the catalogue. Wave 5 measured 28 same-language
// duplicate work pairs of exactly this kind, and 9,907 catalogued works record
// no credits[] at all - so the disk side's identity is its FULL author list
// while a role-qualified row's is not.
func TestBareAndQualifiedContributorAreOneWork(t *testing.T) {
	const bare = `{"name":"Andrzej Sapkowski"},{"name":"Marie Traductrice"}`
	const qualified = `{"name":"Andrzej Sapkowski"},{"name":"Marie Traductrice - Traducteur"}`
	cases := []struct{ name, first, second string }{
		{"bare then qualified", bare, qualified},
		{"qualified then bare", qualified, bare},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sum, _ := runLibex(t, rows(
				libexRow{asin: "B0WITCHR01", title: "Le Sang Des Elfes", language: "french", minutes: 500,
					authors: c.first, narrators: `{"name":"Ann Reader"}`},
				libexRow{asin: "B0WITCHR02", title: "Le Sang Des Elfes", language: "french", minutes: 505,
					authors: c.second, narrators: `{"name":"Bo Lecteur"}`},
			), false)
			if sum.NewWorks != 1 {
				t.Errorf("NewWorks = %d, want 1: one book forked on a translator credit", sum.NewWorks)
			}
		})
	}
}

// TestQualifiedRowFindsACataloguedWorkWithNoCredits is the same defect across
// runs, which is the shape the tree actually holds: the catalogued work records
// its translator in authors[] and nothing in credits[].
func TestQualifiedRowFindsACataloguedWorkWithNoCredits(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/an/andrzej-sapkowski.json": personRec("andrzej-sapkowski", "Andrzej Sapkowski"),
		"people/ma/marie-traductrice.json": personRec("marie-traductrice", "Marie Traductrice"),
		"people/an/ann-reader.json":        personRec("ann-reader", "Ann Reader"),
		"works/le/le-sang-des-elfes/work.json": workRec("le-sang-des-elfes", "Le Sang Des Elfes", "fr",
			`"andrzej-sapkowski","marie-traductrice"`, ""),
		"works/le/le-sang-des-elfes/recordings/ann-reader-2019.json": recRec(
			"le-sang-des-elfes", "ann-reader-2019", "fr", "ann-reader", "B0WITCHR01", 500),
	})
	sum := runLibexInto(t, dataDir, rows(libexRow{
		asin: "B0WITCHR03", title: "Le Sang Des Elfes", language: "french", minutes: 505,
		authors:   `{"name":"Andrzej Sapkowski"},{"name":"Marie Traductrice - Traducteur"}`,
		narrators: `{"name":"Bo Lecteur"}`,
	}))
	if sum.NewWorks != 0 {
		t.Errorf("NewWorks = %d, want 0: the row minted a duplicate of a catalogued work", sum.NewWorks)
	}
	if !entryExists(t, dataDir, recAddr("le-sang-des-elfes", "bo-lecteur-2020")) {
		t.Error("the new narration did not land on the catalogued work")
	}
}

// TestMutualTranslationStaysTwoWorks bounds the subsumption rule. Two different
// books, each one's author role-credited on the other's row: both carry the same
// two people, so each identity IS contained in the other's whole credit list -
// and only the fact that the two identities are DISJOINT keeps them apart.
func TestMutualTranslationStaysTwoWorks(t *testing.T) {
	sum, _ := runLibex(t, rows(
		libexRow{asin: "B0CROSSC01", title: "The Tower",
			authors: `{"name":"Ada One"},{"name":"Bea Two - Translator"}`, narrators: `{"name":"Ann Reader"}`},
		libexRow{asin: "B0CROSSC02", title: "The Tower",
			authors: `{"name":"Bea Two"},{"name":"Ada One - Translator"}`, narrators: `{"name":"Ann Reader"}`},
	), false)
	if sum.NewWorks != 2 {
		t.Errorf("NewWorks = %d, want 2: two different books were conflated", sum.NewWorks)
	}
}

// TestTranslatorOnlyRowKeepsItsOwnWork holds the empty-identity fallback: a row
// whose author column is entirely role-credited falls back to its whole list
// rather than matching every authorless work in the catalogue.
func TestTranslatorOnlyRowKeepsItsOwnWork(t *testing.T) {
	sum, _ := runLibex(t, rows(
		libexRow{asin: "B0TRANSL01", title: "The Odyssey",
			authors: `{"name":"Homer"}`, narrators: `{"name":"Ann Reader"}`},
		libexRow{asin: "B0TRANSL02", title: "The Odyssey",
			authors: `{"name":"Emily Wilson - Translator"}`, narrators: `{"name":"Bo Reader"}`},
	), false)
	if sum.NewWorks != 2 {
		t.Errorf("NewWorks = %d, want 2: a translator-only row matched the author's work", sum.NewWorks)
	}
}

// ---------------------------------------------------------------------------
// WRONG SPLIT: the legacy author-suffix chain

// legacyFirstbornTree seeds the firstborn pair: the bare slug is a DIFFERENT
// book, and the book we want sits at the suffix built from its TRANSLATOR -
// which is where every work minted before the exclusion rule sits.
func legacyFirstbornTree(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/pa/paul-b-thompson.json": personRec("paul-b-thompson", "Paul B. Thompson"),
		"people/to/tonya-c-cook.json":    personRec("tonya-c-cook", "Tonya C. Cook"),
		"people/ju/julia-schwenk.json":   personRec("julia-schwenk", "Julia Schwenk"),
		"people/m-/m-j-hastings.json":    personRec("m-j-hastings", "M. J. Hastings"),
		"people/an/ann-reader.json":      personRec("ann-reader", "Ann Reader"),
		"works/fi/firstborn/work.json": workRec("firstborn", "Firstborn", "en",
			`"paul-b-thompson","tonya-c-cook"`, ""),
		"works/fi/firstborn/recordings/ann-reader-2001.json": recRec(
			"firstborn", "ann-reader-2001", "en", "ann-reader", "B0FIRSTBN0", 400),
		"works/fi/firstborn-julia-schwenk/work.json": workRec("firstborn-julia-schwenk", "Firstborn", "de",
			`"julia-schwenk","m-j-hastings"`, `{"person":"julia-schwenk","role":"translator"}`),
		"works/fi/firstborn-julia-schwenk/recordings/ann-reader-2020.json": recRec(
			"firstborn-julia-schwenk", "ann-reader-2020", "de", "ann-reader", "B0FIRSTBN1", 500),
	})
	return dataDir
}

// legacyFirstbornRow is the row for that book: its credit list leads with the
// translator, which is why the legacy slug was built from her.
const legacyFirstbornRow = `{"name":"Julia Schwenk - Ubersetzer"},{"name":"M. J. Hastings"}`

// TestLegacySuffixedWorkStaysReachable: today's chain builds its suffix from
// the IDENTITY author, so a work stored under the whole list's first author is
// invisible to it - and the create path mints a duplicate of a book we hold.
func TestLegacySuffixedWorkStaysReachable(t *testing.T) {
	dataDir := legacyFirstbornTree(t)
	sum := runLibexInto(t, dataDir, rows(libexRow{
		asin: "B0FIRSTBN2", title: "Firstborn", language: "german",
		authors: legacyFirstbornRow, narrators: `{"name":"Bo Lecteur"}`,
	}))
	if sum.NewWorks != 0 {
		t.Errorf("NewWorks = %d, want 0: the legacy work firstborn-julia-schwenk was unreachable", sum.NewWorks)
	}
	if !entryExists(t, dataDir, recAddr("firstborn-julia-schwenk", "bo-lecteur-2020")) {
		t.Error("the new narration did not land on the legacy-suffixed work")
	}
}

// TestRecordingsOnlyReachesTheLegacySuffixedWork is the same break in the mode
// where it costs a silently dropped row rather than a duplicate.
func TestRecordingsOnlyReachesTheLegacySuffixedWork(t *testing.T) {
	dataDir := legacyFirstbornTree(t)
	sum := runRecordingsOnly(t, dataDir, rows(libexRow{
		asin: "B0FIRSTBN3", title: "Firstborn", language: "german",
		authors: legacyFirstbornRow, narrators: `{"name":"Bo Lecteur"}`,
	}), false)
	if sum.NewRecordings != 1 {
		t.Errorf("NewRecordings = %d (SkippedNoWork=%d), want 1: the alternate narration was dropped",
			sum.NewRecordings, sum.SkippedNoWork)
	}
}

// TestNothingIsEverMintedAtTheLegacySlug: the legacy form is a place to LOOK,
// never a place to create, so the locations do not grow. Here the legacy slug is
// free and the identity slug is the one that must be claimed.
func TestNothingIsEverMintedAtTheLegacySlug(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/pa/paul-b-thompson.json": personRec("paul-b-thompson", "Paul B. Thompson"),
		"people/an/ann-reader.json":      personRec("ann-reader", "Ann Reader"),
		"works/fi/firstborn/work.json":   workRec("firstborn", "Firstborn", "en", `"paul-b-thompson"`, ""),
		"works/fi/firstborn/recordings/ann-reader-2001.json": recRec(
			"firstborn", "ann-reader-2001", "en", "ann-reader", "B0FIRSTBN0", 400),
	})
	runLibexInto(t, dataDir, rows(libexRow{
		asin: "B0FIRSTBN4", title: "Firstborn", language: "german",
		authors: legacyFirstbornRow, narrators: `{"name":"Bo Lecteur"}`,
	}))
	if entryExists(t, dataDir, workAddr("firstborn-julia-schwenk")) {
		t.Error("a work was minted at the legacy (whole-credit-list) slug")
	}
	if !entryExists(t, dataDir, workAddr("firstborn-m-j-hastings")) {
		t.Error("the new work was not minted at the identity-author slug")
	}
}

// TestExactCreditListWinsOverAReducedMatch is the iliad pair: the bare slug
// holds Homer alone and the suffixed one the Fitzgerald translation, and both
// reduce to the SAME identity set. Only the whole credit list says which of the
// two a Fitzgerald row is.
func TestExactCreditListWinsOverAReducedMatch(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/ho/homer.json":             personRec("homer", "Homer"),
		"people/ro/robert-fitzgerald.json": personRec("robert-fitzgerald", "Robert Fitzgerald"),
		"people/an/ann-reader.json":        personRec("ann-reader", "Ann Reader"),
		"works/th/the-iliad/work.json":     workRec("the-iliad", "The Iliad", "en", `"homer"`, ""),
		"works/th/the-iliad/recordings/ann-reader-2001.json": recRec(
			"the-iliad", "ann-reader-2001", "en", "ann-reader", "B0ILIADX00", 900),
		"works/th/the-iliad-robert-fitzgerald/work.json": workRec("the-iliad-robert-fitzgerald",
			"The Iliad", "en", `"robert-fitzgerald","homer"`, `{"person":"robert-fitzgerald","role":"translator"}`),
		"works/th/the-iliad-robert-fitzgerald/recordings/ann-reader-2004.json": recRec(
			"the-iliad-robert-fitzgerald", "ann-reader-2004", "en", "ann-reader", "B0ILIADX01", 950),
	})
	runLibexInto(t, dataDir, rows(libexRow{
		asin: "B0ILIADX02", title: "The Iliad", minutes: 950,
		authors:   `{"name":"Robert Fitzgerald - Translator"},{"name":"Homer"}`,
		narrators: `{"name":"Bo Lecteur"}`,
	}))
	if !entryExists(t, dataDir, recAddr("the-iliad-robert-fitzgerald", "bo-lecteur-2020")) {
		t.Error("the Fitzgerald row did not land on the-iliad-robert-fitzgerald")
	}
	if entryExists(t, dataDir, recAddr("the-iliad", "bo-lecteur-2020")) {
		t.Error("the Fitzgerald row was absorbed by the bare slug")
	}
}

// kikiTree seeds the Kiki's Delivery Service pair from seed wave 7. Both works
// lead their author list with the TRANSLATOR, so neither one's slug is built
// from its authors[0]; the bare slug holds the two-credit edition and the
// suffixed one the edition that also credits the illustrator as an author.
//
// The pair is not a defect. The illustrator is a plain author on one side and
// absent from the other, so the identity sets are {eiko-kadono} against
// {eiko-kadono, joe-todd-stanton} and matchWork's subsumption refuses them:
// the larger identity names a person the smaller side does not credit at ALL,
// which is precisely the clause that keeps two genuinely different credit lists
// apart. The suffixed work is therefore correctly minted, at the suffix today's
// chain composes - the first IDENTITY author, not the first listed one.
func kikiTree(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/em/emily-balistrieri.json": personRec("emily-balistrieri", "Emily Balistrieri"),
		"people/ei/eiko-kadono.json":       personRec("eiko-kadono", "Eiko Kadono"),
		"people/jo/joe-todd-stanton.json":  personRec("joe-todd-stanton", "Joe Todd-Stanton"),
		"people/ki/kim-mai-guest.json":     personRec("kim-mai-guest", "Kim Mai Guest"),
		"works/ki/kikis-delivery-service/work.json": workRec("kikis-delivery-service",
			"Kiki's Delivery Service", "en", `"emily-balistrieri","eiko-kadono"`,
			`{"person":"emily-balistrieri","role":"translator"}`),
		"works/ki/kikis-delivery-service/recordings/kim-mai-guest-2020.json": recRec(
			"kikis-delivery-service", "kim-mai-guest-2020", "en", "kim-mai-guest", "B0KIKI0001", 240),
		"works/ki/kikis-delivery-service-eiko-kadono/work.json": workRec("kikis-delivery-service-eiko-kadono",
			"Kiki's Delivery Service", "en", `"emily-balistrieri","eiko-kadono","joe-todd-stanton"`,
			`{"person":"emily-balistrieri","role":"translator"}`),
		"works/ki/kikis-delivery-service-eiko-kadono/recordings/kim-mai-guest-2025.json": recRec(
			"kikis-delivery-service-eiko-kadono", "kim-mai-guest-2025", "en", "kim-mai-guest", "B0KIKI0002", 245),
	})
	return dataDir
}

// kikiRow is the row for the illustrated edition, spelled the way the source
// spells it: the translator first, then the two people identity is decided on.
const kikiRow = `{"name":"Emily Balistrieri - Translator"},{"name":"Eiko Kadono"},{"name":"Joe Todd-Stanton"}`

// TestIdentitySuffixedWorkIsFoundWhenTheRowLeadsWithACredit is the Kiki case:
// the work sits at its first IDENTITY author's suffix while its authors[0] is a
// role credit, so a chain rooted at the first LISTED author reaches neither the
// work nor anything near it. Production composes the right root - this pins that
// it does, because the real-data slug test's recomputation now relies on it.
func TestIdentitySuffixedWorkIsFoundWhenTheRowLeadsWithACredit(t *testing.T) {
	dataDir := kikiTree(t)
	sum := runLibexInto(t, dataDir, rows(libexRow{
		asin: "B0KIKI0003", title: "Kiki's Delivery Service", minutes: 245,
		authors: kikiRow, narrators: `{"name":"Bo Lecteur"}`,
	}))
	if sum.NewWorks != 0 {
		t.Errorf("NewWorks = %d, want 0: kikis-delivery-service-eiko-kadono was unreachable", sum.NewWorks)
	}
	if !entryExists(t, dataDir, recAddr("kikis-delivery-service-eiko-kadono", "bo-lecteur-2020")) {
		t.Error("the new narration did not land on the identity-suffixed work")
	}
	if entryExists(t, dataDir, recAddr("kikis-delivery-service", "bo-lecteur-2020")) {
		t.Error("the illustrated edition was absorbed by the bare slug, whose identity omits the illustrator")
	}
}

// TestChainIsRootedAtTheIdentityAuthor states the Kiki round-trip's mechanism as
// a shape: the MINTABLE suffix and the numeric tail both hang off the first
// IDENTITY author, and the role-credited people the full list adds are reachable
// as probes. Rooting them at the first LISTED author instead - which is what a
// reader of authors[0] alone assumes - puts the whole chain on a translator.
func TestChainIsRootedAtTheIdentityAuthor(t *testing.T) {
	authors := workAuthors{
		all:      []string{"emily-balistrieri", "eiko-kadono", "joe-todd-stanton"},
		identity: []string{"eiko-kadono", "joe-todd-stanton"},
	}
	cands, primary := workCandidates("kikis-delivery-service", authors, positionClaim{})

	var mintable, probes []string
	for _, c := range cands[:primary] {
		if c.probeOnly {
			probes = append(probes, c.slug)
		} else {
			mintable = append(mintable, c.slug)
		}
	}
	wantMintable := []string{"kikis-delivery-service", "kikis-delivery-service-eiko-kadono"}
	if !slices.Equal(mintable, wantMintable) {
		t.Errorf("mintable candidates = %v, want %v", mintable, wantMintable)
	}
	// Every other credited person is a place to LOOK, in identity-then-full order.
	wantProbes := []string{"kikis-delivery-service-joe-todd-stanton", "kikis-delivery-service-emily-balistrieri"}
	if !slices.Equal(probes, wantProbes) {
		t.Errorf("author probes = %v, want %v", probes, wantProbes)
	}
	if got := cands[primary].slug; got != "kikis-delivery-service-eiko-kadono-2" {
		t.Errorf("first numeric candidate = %q, want the identity author's", got)
	}
}

// TestAnUncreditedCoAuthorKeepsTheWorksApart is the other half of the same pair:
// the bare work must stay reachable by ITS own row, and the two must not
// collapse. Without the second assertion the first is satisfiable by merging
// everything, which is the failure mode subsumption exists to refuse.
func TestAnUncreditedCoAuthorKeepsTheWorksApart(t *testing.T) {
	dataDir := kikiTree(t)
	sum := runLibexInto(t, dataDir, rows(libexRow{
		asin: "B0KIKI0004", title: "Kiki's Delivery Service", minutes: 240,
		authors:   `{"name":"Emily Balistrieri - Translator"},{"name":"Eiko Kadono"}`,
		narrators: `{"name":"Bo Lecteur"}`,
	}))
	if sum.NewWorks != 0 {
		t.Errorf("NewWorks = %d, want 0: the bare work was unreachable by its own row", sum.NewWorks)
	}
	if !entryExists(t, dataDir, recAddr("kikis-delivery-service", "bo-lecteur-2020")) {
		t.Error("the two-credit row did not land on the bare work")
	}
	if entryExists(t, dataDir, recAddr("kikis-delivery-service-eiko-kadono", "bo-lecteur-2020")) {
		t.Error("the two-credit row was absorbed by the work that also credits the illustrator")
	}
}

// ---------------------------------------------------------------------------
// The recordings-only mode's own reporting

// TestRecordingsOnlyReportsTitleHitsSeparately: "we do not hold this book" is
// the mode's expected answer on an unfiltered dump and says nothing, while "we
// hold this title and could not agree on who wrote it" is where a matching
// defect surfaces. One counter hid the second inside the first.
func TestRecordingsOnlyReportsTitleHitsSeparately(t *testing.T) {
	dataDir := seedRecordingsTree(t)
	sum := runRecordingsOnly(t, dataDir, rows(
		// A title we hold, credited to somebody else entirely.
		libexRow{asin: "B0TITLEHT1", title: "Harry Potter and the Chamber of Secrets",
			authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Bo Reader"}`, minutes: 583},
		// A book we simply do not have.
		libexRow{asin: "B0NOWORK01", title: "A Book Nobody Catalogued",
			authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Bo Reader"}`},
	), false)

	if sum.SkippedNoWork != 2 {
		t.Fatalf("SkippedNoWork = %d, want 2", sum.SkippedNoWork)
	}
	if sum.SkippedTitleNoMatch != 1 {
		t.Errorf("SkippedTitleNoMatch = %d, want 1", sum.SkippedTitleNoMatch)
	}
	var titleLine, noWorkLine bool
	for _, w := range sum.Warnings {
		if strings.Contains(w, "matched a catalogued TITLE") && strings.Contains(w, "B0TITLEHT1") {
			titleLine = true
		}
		if strings.Contains(w, "matched no catalogued work") && strings.Contains(w, "B0NOWORK01") {
			noWorkLine = true
		}
	}
	if !titleLine || !noWorkLine {
		t.Errorf("both buckets must be reported with their own examples: %v", sum.Warnings)
	}
}

// ---------------------------------------------------------------------------
// The series guards, end to end

// TestDiskRecordingCarriesItsSeriesPosition is the cross-RUN half of the
// same-title serial guard. A recording's volume is not lost when the row that
// made it is: the work's membership in the series IS that volume number, and it
// is on disk. Without this, a second run's volume 2 merged its ASIN onto run
// one's volume 1 recording - the original Bravelands defect, one run later.
func TestDiskRecordingCarriesItsSeriesPosition(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/er/erin-hunter.json": personRec("erin-hunter", "Erin Hunter"),
		"people/an/ann-reader.json":  personRec("ann-reader", "Ann Reader"),
		"works/br/bravelands-book-1/work.json": workRec(
			"bravelands-book-1", "Bravelands", "en", `"erin-hunter"`, ""),
		"works/br/bravelands-book-1/recordings/ann-reader-2019.json": recRec(
			"bravelands-book-1", "ann-reader-2019", "en", "ann-reader", "B0VOL00001", 500),
		"series/br/bravelands.json": `{"id":"bravelands","license":"CC0-1.0","name":"Bravelands",` +
			`"sources":[{"type":"user"}],"works":[{"position":"1","work":"bravelands-book-1"}]}`,
	})

	// Volume 2, arriving as an alternate narration of the same title: the mode
	// resolves the work by title and author alone, so only the recording-level
	// guard can tell the two volumes apart.
	sum := runRecordingsOnly(t, dataDir, rows(libexRow{
		asin: "B0VOL00002", title: "Bravelands", authors: `{"name":"Erin Hunter"}`, minutes: 502,
		narrators: `{"name":"Ann Reader"}`, series: `{"name":"Bravelands","position":"2"}`,
	}), false)

	if sum.MergedASINs != 0 {
		t.Errorf("MergedASINs = %d, want 0: a different volume's ASIN was merged onto a disk recording", sum.MergedASINs)
	}
}

// TestSeriesClaimAndRecordingGuardAreLoadBearing is the end-to-end test the
// review found missing: disabling EITHER of the two same-title serial guards
// broke no test at all. It runs the shape both were written for - six rows of
// one serial published under the bare series name - and asserts the outcome
// neither guard alone produces.
//
//   - seriesState.claimed is what makes a DROPPED placement still block a later
//     volume: without it, a work whose position was already taken looks absent
//     from the series, which trivially satisfies the same-position test.
//   - recInfo.seriesPos is what stops two volumes' ASINs merging onto one
//     recording once they are under one work: identical narrators and
//     compatible runtimes leave nothing else to tell them apart.
func TestSeriesClaimAndRecordingGuardAreLoadBearing(t *testing.T) {
	const author = `{"name":"Erin Hunter"}`
	sum, dataDir := runLibex(t, rows(
		// The properly-titled volume takes position 1 first, so the bare-titled
		// row's own claim to position 1 is DROPPED.
		libexRow{asin: "B0GUARD001", title: "Bravelands", subtitle: "Broken Pride", authors: author,
			minutes: 500, series: `{"name":"Bravelands","position":"1"}`},
		libexRow{asin: "B0GUARD002", title: "Bravelands", authors: author, minutes: 501,
			series: `{"name":"Bravelands","position":"1"}`},
		// ...and the next volumes arrive under the same bare title.
		libexRow{asin: "B0GUARD003", title: "Bravelands", authors: author, minutes: 502,
			series: `{"name":"Bravelands","position":"2"}`},
		libexRow{asin: "B0GUARD004", title: "Bravelands", authors: author, minutes: 503,
			series: `{"name":"Bravelands","position":"3"}`},
	), false)

	if sum.MergedASINs != 0 {
		t.Errorf("MergedASINs = %d, want 0: sibling volumes' ASINs were folded onto one recording", sum.MergedASINs)
	}
	if sum.NewWorks != 4 {
		t.Errorf("NewWorks = %d, want 4: four rows, four volumes", sum.NewWorks)
	}
	// Every recording in the tree carries exactly one ASIN: no two volumes share
	// a production.
	var series struct {
		Works []struct{ Work, Position string }
	}
	readEntity(t, dataDir, seriesAddr("bravelands"), &series)
	if len(series.Works) != 3 {
		t.Errorf("series membership = %v, want the three distinct positions the rows claimed", series.Works)
	}
	seenPos := map[string]bool{}
	for _, w := range series.Works {
		if seenPos[w.Position] {
			t.Errorf("position %q claimed twice", w.Position)
		}
		seenPos[w.Position] = true
	}
}

// ---------------------------------------------------------------------------
// Minor: names that reached the source HTML-escaped

// TestHTMLEntityCreditNamesAreUnescaped: seven of the dump's credit names are
// escaped, and every one is a real person whose record was minted under a slug
// spelling the ENTITY ("erika-b-aacute-lint").
func TestHTMLEntityCreditNamesAreUnescaped(t *testing.T) {
	_, dataDir := runLibex(t, rows(libexRow{
		asin: "B0ENTITY01", title: "Entity Book",
		authors: `{"name":"Erika B&aacute;lint"}`, narrators: `{"name":"Leo Kni&#382;ka"}`,
	}), false)
	for _, slug := range []string{"erika-balint", "leo-knizka"} {
		if !entryExists(t, dataDir, personAddr(slug)) {
			t.Errorf("no person record at %q; the escape was imported verbatim", slug)
		}
	}
	if entryExists(t, dataDir, personAddr("erika-b-aacute-lint")) {
		t.Error("the entity reference was minted as part of the slug")
	}
}

// TestListCreditRefusalStillReadsTheEscapedName holds the ordering the unescape
// depends on: the semicolon that ends an entity reference must not be read as a
// list separator, and a real list must still be refused.
func TestListCreditRefusalStillReadsTheEscapedName(t *testing.T) {
	if _, isList := firstListCredit([]string{"Erika B&aacute;lint"}, nil); isList {
		t.Error("an entity-bearing name was refused as a list")
	}
	if _, isList := firstListCredit([]string{"Erika B&aacute;lint; Tor Thom"}, nil); !isList {
		t.Error("a real two-person list behind an entity was not refused")
	}
}

// TestCleanSeriesNameRefusesAnUnbalancedRemainder: the qualifier regex matches
// the LAST parenthetical, so a doubled opener would leave a series named after a
// dangling bracket - whose slug is the bare name's, so it collides with it.
func TestCleanSeriesNameRefusesAnUnbalancedRemainder(t *testing.T) {
	cases := map[string]string{
		"Foo ((gelesen von Peter)":     "Foo ((gelesen von Peter)",
		"Foo (gelesen von Peter)":      "Foo",
		"Foo [narrated by Bob]":        "Foo",
		"Foo (a note) (narrated by B)": "Foo (a note)",
	}
	for in, want := range cases {
		if got := cleanSeriesName(in); got != want {
			t.Errorf("cleanSeriesName(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// The advisory's restatement of the identity rule

// TestAdvisoryIdentityRuleAgreesWithTheImporter is the drift guard for
// pkg/check's IdentityEqualWorks. metacheck must report exactly the pairs the
// importer would merge, and it cannot call the importer's rule (the dependency
// runs the other way), so it restates it - and a restatement nobody compares is
// a copy that diverges.
//
// The table is the identity model's whole surface: equal sets, one side
// reduced, the fallback, disjoint identities, and a name one side never
// credits.
func TestAdvisoryIdentityRuleAgreesWithTheImporter(t *testing.T) {
	cases := []struct {
		name               string
		aAuthors, bAuthors []string
		aCredits, bCredits []model.Credit
	}{
		{"equal", []string{"homer"}, []string{"homer"}, nil, nil},
		{"disjoint", []string{"ada"}, []string{"bea"}, nil, nil},
		{"one side reduced", []string{"sap", "marie"}, []string{"sap", "marie"},
			nil, []model.Credit{{Person: "marie", Role: model.RoleTranslator}}},
		{"reduced the other way", []string{"sap", "marie"}, []string{"sap", "marie"},
			[]model.Credit{{Person: "marie", Role: model.RoleTranslator}}, nil},
		{"fork drops the translator", []string{"sap", "marie"}, []string{"sap"}, nil, nil},
		{"mutual translation", []string{"ada", "bea"}, []string{"ada", "bea"},
			[]model.Credit{{Person: "bea", Role: model.RoleTranslator}},
			[]model.Credit{{Person: "ada", Role: model.RoleTranslator}}},
		{"whole list credited (fallback)", []string{"marie"}, []string{"sap"},
			[]model.Credit{{Person: "marie", Role: model.RoleTranslator}}, nil},
		{"extra uncredited author", []string{"ada"}, []string{"ada", "bea"}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &model.Work{ID: "a", Title: "T", Authors: c.aAuthors, Credits: c.aCredits}
			b := &model.Work{ID: "b", Title: "T", Authors: c.bAuthors, Credits: c.bCredits}
			// The importer's side: a workState built as loadExisting builds one,
			// against a row's resolved authors.
			ws := &workState{
				slug:    a.ID,
				authors: diskIdentityAuthors(a.Authors, a.Credits),
				all:     ToSet(a.Authors),
			}
			row := workAuthors{all: b.Authors, identity: identityAuthorsOf(b)}
			want := matchWork(ws, row) != matchNone
			if got := check.IdentityEqualWorks(a, b); got != want {
				t.Errorf("check.IdentityEqualWorks = %v, importer matchWork = %v", got, want)
			}
		})
	}
}

// identityAuthorsOf is the row-side identity list for a work fixture, derived
// the way splitWorkAuthors derives it from a row's credits.
func identityAuthorsOf(w *model.Work) []string {
	credited := map[string]bool{}
	for _, c := range w.Credits {
		credited[c.Person] = true
	}
	return identityAuthors(w.Authors, credited)
}

// TestAuthorOrderDoesNotForkAWork: the collision suffix is built from the FIRST
// author, and two editions of one book routinely disagree about who that is. A
// German Sherlock Holmes audio drama credited "Arthur Conan Doyle, S. Pomej" on
// one release and "S. Pomej, Arthur Conan Doyle" on the next produced two works
// with byte-equal author SETS, because each row looked only where its own first
// author would have put it. Ten such pairs came out of a single wave.
func TestAuthorOrderDoesNotForkAWork(t *testing.T) {
	sum, dataDir := runLibex(t, rows(
		// The bare slug goes to a different book by another author, which is what
		// pushes both rows onto the author-suffixed chain.
		libexRow{asin: "B0ORDER001", title: "Mord Im Ewigen Eis", language: "german",
			authors: `{"name":"Olga Nova"}`, narrators: `{"name":"Ann Reader"}`},
		libexRow{asin: "B0ORDER002", title: "Mord Im Ewigen Eis", language: "german", minutes: 300,
			authors: `{"name":"Arthur Conan Doyle"},{"name":"S. Pomej"}`, narrators: `{"name":"Peter Bocek"}`},
		libexRow{asin: "B0ORDER003", title: "Mord Im Ewigen Eis", language: "german", minutes: 305,
			authors: `{"name":"S. Pomej"},{"name":"Arthur Conan Doyle"}`, narrators: `{"name":"Andreas Lange"}`},
	), false)

	if sum.NewWorks != 2 {
		t.Errorf("NewWorks = %d, want 2: the credit ORDER forked one book", sum.NewWorks)
	}
	if entryExists(t, dataDir, workAddr("mord-im-ewigen-eis-s-pomej")) {
		t.Error("a second work was minted from the same author set in the other order")
	}
	if !entryExists(t, dataDir, recAddr("mord-im-ewigen-eis-arthur-conan-doyle", "andreas-lange-2020")) {
		t.Error("the second narration did not land on the work the first row created")
	}
}
