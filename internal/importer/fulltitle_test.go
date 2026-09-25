package importer

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// fulltitle_test.go pins the create path's FULL-TITLE merge (resolveWork) and the
// duplicate-identity guard's agreement with it, on the shape issue #2337 measured:
// a user-library export titles a book by its short title ("Nightfall") while the
// bulk mirror minted the same production under its full retailer title, so the
// short title's slug chain cannot see the work and the row used to mint a sibling
// beside it. Every merge test has a neighbour that must NOT merge.

const (
	nightfallFull   = "Nightfall - A Fantasy Adventure (Dragon Centurion, Book 4)"
	nightfallSlug   = "nightfall-a-fantasy-adventure-dragon-centurion-book-4"
	nightfallSeries = `{"name":"Dragon Centurion","position":"4"}`
)

// runOpenAudibleInto runs the OpenAudible CREATE importer against an existing
// (seeded) data dir - runImport always starts from an empty tree.
func runOpenAudibleInto(t *testing.T, dataDir, booksJSON string) Summary {
	t.Helper()
	sum, err := Run(writeBooks(t, booksJSON), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("openaudible run: %v", err)
	}
	return sum
}

// seedNightfall imports the bulk mirror's record of the book under its FULL title,
// optionally beside another author's "Nightfall" holding the bare slug (the real
// tree's shape, which is why #2337 minted "nightfall-sarah-hawke").
func seedNightfall(t *testing.T, bareTaken bool) string {
	t.Helper()
	dataDir := t.TempDir()
	rs := []libexRow{{asin: "B0H1DTHKQK", title: nightfallFull, authors: `{"name":"Sarah Hawke"}`,
		minutes: 601, series: nightfallSeries}}
	if bareTaken {
		rs = append(rs, libexRow{asin: "B0OTHERNF1", title: "Nightfall",
			authors: `{"name":"Other Writer"}`, narrators: `{"name":"Someone Else"}`, minutes: 300})
	}
	sum := runLibexInto(t, dataDir, rows(rs...))
	if sum.NewWorks != len(rs) {
		t.Fatalf("seed: NewWorks = %d, want %d; warnings %v", sum.NewWorks, len(rs), sum.Warnings)
	}
	if !entryExists(t, dataDir, workAddr(nightfallSlug)) {
		t.Fatalf("seed left no work at %s: %v", nightfallSlug, listWorks(t, dataDir))
	}
	return dataDir
}

// nightfallAU is the OpenAudible row of #2337: the AU storefront's ASIN for the same
// production, the same narrator, no runtime in the export.
const nightfallAU = `[{"asin":"B0H1DY5QWL","title":"` + nightfallFull + `","title_short":"Nightfall",
	"author":"Sarah Hawke","narrated_by":"Ann Reader","series_name":"Dragon Centurion","series_sequence":"4",
	"language":"english","region":"AU","abridged":"false"}]`

// The fix: the short-titled row resolves to the full-titled work and its AU ASIN
// joins the matching recording (the ordinary ASIN-merge path, with its runtime,
// abridged and narrator-set guards) - no second work, nothing refused.
func TestShortTitledRowMergesIntoItsFullTitledWork(t *testing.T) {
	for _, bareTaken := range []bool{true, false} {
		name := "bare-slug-free"
		if bareTaken {
			name = "bare-slug-taken"
		}
		t.Run(name, func(t *testing.T) {
			dataDir := seedNightfall(t, bareTaken)
			sum := runOpenAudibleInto(t, dataDir, nightfallAU)

			if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.MergedASINs != 1 {
				t.Fatalf("summary = %+v, want 0 works / 0 recordings / 1 merged ASIN; warnings %v",
					sum, sum.Warnings)
			}
			if sum.SkippedDuplicateIdentity != 0 {
				t.Errorf("SkippedDuplicateIdentity = %d, want 0: the row merged", sum.SkippedDuplicateIdentity)
			}
			for _, sibling := range []string{"nightfall-sarah-hawke", "nightfall"} {
				if sibling == "nightfall" && bareTaken {
					continue // the other author's book
				}
				if entryExists(t, dataDir, workAddr(sibling)) {
					t.Errorf("a sibling work %q was minted: %v", sibling, listWorks(t, dataDir))
				}
			}
			recs := recSlugsOf(t, dataDir, nightfallSlug)
			if len(recs) != 1 {
				t.Fatalf("recordings = %v, want the one production", recs)
			}
			got := asinsOf(t, dataDir, recAddr(nightfallSlug, recs[0]))
			if got["B0H1DTHKQK"] != "us" || got["B0H1DY5QWL"] != "au" {
				t.Errorf("recording ASINs = %v, want the US and the AU storefront IDs", got)
			}
			if res := check.Load(dataDir); !res.OK() {
				t.Fatalf("tree failed validation: %v", res.Problems)
			}
		})
	}
}

// A full title naming a work the SERIES places at a different position is a
// different volume: the series claim's position veto (seriesClaim.compatible)
// applies to the full-title chain exactly as to the short one, so nothing merges.
func TestShortTitledRowDoesNotMergeIntoAnotherVolume(t *testing.T) {
	dataDir := t.TempDir()
	seed := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0LOSTROAD", title: "Wayfarer: The Lost Road", authors: `{"name":"Sarah Hawke"}`,
			minutes: 500, series: `{"name":"Wayfarer Saga","position":"3"}`},
	))
	if seed.NewWorks != 1 {
		t.Fatalf("seed: %+v", seed)
	}

	sum := runOpenAudibleInto(t, dataDir, `[{"asin":"B0WAYFAR02","title":"Wayfarer: The Lost Road",
		"title_short":"Wayfarer","author":"Sarah Hawke","narrated_by":"Ann Reader",
		"series_name":"Wayfarer Saga","series_sequence":"2","language":"english","region":"AU"}]`)

	if sum.MergedASINs != 0 {
		t.Errorf("MergedASINs = %d, want 0: the series puts that work at position 3, not 2", sum.MergedASINs)
	}
	if got := asinsOf(t, dataDir, recAddr("wayfarer-the-lost-road", recSlugsOf(t, dataDir, "wayfarer-the-lost-road")[0])); len(got) != 1 {
		t.Errorf("the other volume's recording gained an ASIN: %v", got)
	}
	if sum.NewWorks != 1 || !entryExists(t, dataDir, workAddr("wayfarer")) {
		t.Errorf("want the row minted as its own work at wayfarer: %+v, works %v", sum, listWorks(t, dataDir))
	}
}

// Nor does a full title held by ANOTHER AUTHOR's book: the identity author set is
// asked of the full-title chain exactly as of the short one.
func TestShortTitledRowDoesNotMergeIntoAnotherAuthorsFullTitle(t *testing.T) {
	dataDir := t.TempDir()
	seed := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0OTHERAU2", title: "Nightfall - A Fantasy Adventure",
			authors: `{"name":"Other Writer"}`, minutes: 601},
	))
	if seed.NewWorks != 1 {
		t.Fatalf("seed: %+v", seed)
	}

	sum := runOpenAudibleInto(t, dataDir, `[{"asin":"B0HAWKE001","title":"Nightfall - A Fantasy Adventure",
		"title_short":"Nightfall","author":"Sarah Hawke","narrated_by":"Ann Reader",
		"language":"english","region":"AU"}]`)

	if sum.MergedASINs != 0 || sum.NewWorks != 1 {
		t.Errorf("summary = %+v, want the row minted as its own work; warnings %v", sum, sum.Warnings)
	}
	if !entryExists(t, dataDir, workAddr("nightfall")) {
		t.Errorf("want the row at nightfall: %v", listWorks(t, dataDir))
	}
	if got := recSlugsOf(t, dataDir, "nightfall-a-fantasy-adventure"); len(got) != 1 {
		t.Errorf("the other author's work gained a recording: %v", got)
	}
}

// longTitle is a retail title whose slug runs past MaxSlugLen, so the volume
// tail is exactly what Slugify cuts.
func longTitle(volume string) string {
	return "The Extraordinary and Unbelievably Lengthy Chronicle of the Wandering Cartographer Who Mapped the Whole World, Book " + volume
}

// A full title cut at MaxSlugLen has lost its tail, and a long retail title's tail
// is its volume number: Book 4 and Book 3 slug identically. With no series claim
// to veto it (compatible() is vacuously true), a merge through the cut title would
// fold one volume into the other silently. It must not merge at all.
func TestShortTitledRowDoesNotMergeThroughACutFullTitle(t *testing.T) {
	dataDir := t.TempDir()
	seed := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0LONGVOL3", title: longTitle("3"), authors: `{"name":"Sarah Hawke"}`, minutes: 500},
	))
	if seed.NewWorks != 1 {
		t.Fatalf("seed: %+v", seed)
	}
	if Slugify(longTitle("3")) != Slugify(longTitle("4")) {
		t.Fatalf("fixture no longer exercises the cut: %q vs %q", Slugify(longTitle("3")), Slugify(longTitle("4")))
	}

	sum := runOpenAudibleInto(t, dataDir, `[{"asin":"B0LONGVOL4","title":"`+longTitle("4")+`",
		"title_short":"The Extraordinary Chronicle","author":"Sarah Hawke","narrated_by":"Ann Reader",
		"language":"english","region":"AU"}]`)

	if sum.MergedASINs != 0 || sum.NewWorks != 1 {
		t.Errorf("summary = %+v, want Book 4 minted as its own work; warnings %v", sum, sum.Warnings)
	}
	book3 := Slugify(longTitle("3"))
	if got := asinsOf(t, dataDir, recAddr(book3, recSlugsOf(t, dataDir, book3)[0])); len(got) != 1 {
		t.Errorf("Book 3's recording gained Book 4's ASIN: %v", got)
	}
}

// Nor does a full title held by a TRANSLATION: the language test applies to the
// full-title chain exactly as to the short one.
func TestShortTitledRowDoesNotMergeIntoAnotherLanguage(t *testing.T) {
	dataDir := t.TempDir()
	seed := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0GERMAN01", title: "Nightfall - A Fantasy Adventure", language: "german",
			authors: `{"name":"Sarah Hawke"}`, minutes: 601},
	))
	if seed.NewWorks != 1 {
		t.Fatalf("seed: %+v", seed)
	}

	sum := runOpenAudibleInto(t, dataDir, `[{"asin":"B0ENGLISH1","title":"Nightfall - A Fantasy Adventure",
		"title_short":"Nightfall","author":"Sarah Hawke","narrated_by":"Ann Reader",
		"language":"english","region":"AU"}]`)

	if sum.MergedASINs != 0 || sum.NewWorks != 1 {
		t.Errorf("summary = %+v, want the English row minted as its own work; warnings %v", sum, sum.Warnings)
	}
	if got := recSlugsOf(t, dataDir, "nightfall-a-fantasy-adventure"); len(got) != 1 {
		t.Errorf("the German work gained a recording: %v", got)
	}
}

// The long-standing BLOCKED retry, both halves: a same-author "Wayfarer" the
// series places at 2 blocks the short chain for a row claiming 3, so the row's
// work lives on its full title's chain - merged into when it is there, minted there
// (and titled by the full title) when it is not.
func TestBlockedShortTitleRetriesTheFullTitle(t *testing.T) {
	const row = `[{"asin":"B0WAYFAR03","title":"Wayfarer: The Lost Road","title_short":"Wayfarer",
		"author":"Sarah Hawke","narrated_by":"Ann Reader","series_name":"Wayfarer Saga","series_sequence":"3",
		"language":"english","region":"AU"}]`
	volume2 := libexRow{asin: "B0WAYFARE2", title: "Wayfarer", authors: `{"name":"Sarah Hawke"}`,
		minutes: 480, series: `{"name":"Wayfarer Saga","position":"2"}`}

	t.Run("full-title work held", func(t *testing.T) {
		dataDir := t.TempDir()
		seed := runLibexInto(t, dataDir, rows(volume2,
			libexRow{asin: "B0WAYFARE3", title: "Wayfarer: The Lost Road", authors: `{"name":"Sarah Hawke"}`,
				minutes: 500, series: `{"name":"Wayfarer Saga","position":"3"}`}))
		if seed.NewWorks != 2 {
			t.Fatalf("seed: %+v", seed)
		}
		sum := runOpenAudibleInto(t, dataDir, row)
		if sum.NewWorks != 0 || sum.MergedASINs != 1 {
			t.Errorf("summary = %+v, want the AU ASIN merged into volume 3; warnings %v", sum, sum.Warnings)
		}
		got := asinsOf(t, dataDir, recAddr("wayfarer-the-lost-road", recSlugsOf(t, dataDir, "wayfarer-the-lost-road")[0]))
		if got["B0WAYFAR03"] != "au" {
			t.Errorf("volume 3's ASINs = %v, want the AU one merged", got)
		}
	})
	t.Run("full-title work absent", func(t *testing.T) {
		dataDir := t.TempDir()
		if seed := runLibexInto(t, dataDir, rows(volume2)); seed.NewWorks != 1 {
			t.Fatalf("seed: %+v", seed)
		}
		sum := runOpenAudibleInto(t, dataDir, row)
		if sum.NewWorks != 1 || sum.MergedASINs != 0 {
			t.Errorf("summary = %+v, want volume 3 minted; warnings %v", sum, sum.Warnings)
		}
		var work struct {
			Title string `json:"title"`
		}
		readEntity(t, dataDir, workAddr("wayfarer-the-lost-road"), &work)
		if work.Title != "Wayfarer: The Lost Road" {
			t.Errorf("minted work title = %q, want the full title", work.Title)
		}
		if got := asinsOf(t, dataDir, recAddr("wayfarer", recSlugsOf(t, dataDir, "wayfarer")[0])); len(got) != 1 {
			t.Errorf("volume 2 gained an ASIN: %v", got)
		}
	})
}

// The guard still REFUSES when neither chain resolves the row: a short title and
// a full title that both slug onto nothing the catalogue holds, naming a book it
// holds under a plain spelling. resolveWork misses, so the create path would
// mint, so the guard refuses - the two agree by construction.
func TestGuardRefusesWhenNeitherTitleResolves(t *testing.T) {
	dataDir := seedPlainWork(t)

	sum := runOpenAudibleInto(t, dataDir, `[{"asin":"B0HAMMERAU","title":"Hammered: The Iron Druid Chronicles, Book 3",
		"title_short":"Hammered: Book 3","author":"Kevin Hearne","narrated_by":"Christopher Ragland",
		"series_name":"The Iron Druid Chronicles","series_sequence":"3","language":"english","region":"AU"}]`)

	if sum.SkippedDuplicateIdentity != 1 || sum.NewWorks != 0 || sum.MergedASINs != 0 {
		t.Errorf("summary = %+v, want the row refused as a duplicate of hammered; warnings %v", sum, sum.Warnings)
	}
	for _, slug := range []string{"hammered-book-3", "hammered-the-iron-druid-chronicles-book-3"} {
		if entryExists(t, dataDir, workAddr(slug)) {
			t.Errorf("a sibling work %q was minted", slug)
		}
	}
}

// workCandidates is a whole chain as a plain list - the primary candidates, then
// every numbered one - and how many are primary, for the tests that assert on
// the chain's shape.
func workCandidates(base string, authors workAuthors, pos positionClaim) ([]workCandidate, int) {
	c := newWorkChain(titleSlug{slug: base}, "", authors, pos)
	var out []workCandidate
	for i := 0; ; i++ {
		cand, ok := c.at(i)
		if !ok {
			return out, len(c.primary)
		}
		out = append(out, cand)
	}
}

// shortened is the second half of the cut-title refusal: a title base under the
// cap can still be SHORTENED inside an author-suffixed candidate (workSlugAt cuts
// the title, never the credit), and two volumes' bases then meet on one
// candidate. The flag is set where the cut happens, and only there.
func TestCandidatesReportTheirOwnCut(t *testing.T) {
	base := "the-long-and-winding-chronicle-of-the-cartographer-who-mapped-the-whole-known-world-book-4"
	cands, _ := workCandidates(base, workAuthors{all: []string{"sarah-hawke"}, identity: []string{"sarah-hawke"}}, positionClaim{})
	if cands[0].shortened {
		t.Errorf("the bare base %q was not cut but is marked shortened", cands[0].slug)
	}
	if strings.HasPrefix(cands[1].slug, base) || !cands[1].shortened {
		t.Errorf("the author-suffixed candidate %q (shortened=%v) must cut the title and say so", cands[1].slug, cands[1].shortened)
	}

	// A base under the cap by a word keeps every candidate whole up to the point
	// its suffix no longer fits - and says so exactly there.
	short, _ := workCandidates("a-short-title", workAuthors{all: []string{"sarah-hawke"}, identity: []string{"sarah-hawke"}}, positionClaim{})
	for _, c := range short {
		if c.shortened {
			t.Errorf("candidate %q of a short title is marked shortened", c.slug)
		}
	}

	// A base the cap itself cut marks EVERY candidate built on it.
	cut := newWorkChain(titleSlug{slug: "x", cut: true}, "", workAuthors{all: []string{"a"}, identity: []string{"a"}}, positionClaim{})
	for i := 0; i < 3; i++ {
		if c, _ := cut.at(i); !c.shortened {
			t.Errorf("candidate %d %q over a cut base is not marked shortened", i, c.slug)
		}
	}
}
