package importer

import (
	"strings"
	"testing"
)

// fulltitle_cut_test.go pins how a walk judges a candidate the slug cap CUT, and
// the full-title walk's own claim and warning.

// A cut slug is judged by the WHOLE title on every walk: the same long title
// again is the same book, so the full-title merge and the recordings-only matcher
// both still reach a work whose slug the cap cut.
func TestCutSlugStillMatchesTheSameWholeTitle(t *testing.T) {
	seedBook3 := func(t *testing.T) string {
		dataDir := t.TempDir()
		if seed := runLibexInto(t, dataDir, rows(
			libexRow{asin: "B0LONGVOL3", title: longTitle("3"), authors: `{"name":"Sarah Hawke"}`, minutes: 500},
		)); seed.NewWorks != 1 {
			t.Fatalf("seed: %+v", seed)
		}
		return dataDir
	}
	book3 := Slugify(longTitle("3"))

	t.Run("full-title merge", func(t *testing.T) {
		dataDir := seedBook3(t)
		sum := runOpenAudibleInto(t, dataDir, `[{"asin":"B0LONGAU03","title":"`+longTitle("3")+`",
			"title_short":"The Extraordinary Chronicle","author":"Sarah Hawke","narrated_by":"Ann Reader",
			"language":"english","region":"AU"}]`)
		if sum.NewWorks != 0 || sum.MergedASINs != 1 {
			t.Errorf("summary = %+v, want the AU ASIN merged into Book 3; warnings %v", sum, sum.Warnings)
		}
		if got := asinsOf(t, dataDir, recAddr(book3, recSlugsOf(t, dataDir, book3)[0])); got["B0LONGAU03"] != "au" {
			t.Errorf("Book 3's ASINs = %v, want the AU one merged", got)
		}
	})
	t.Run("recordings-only", func(t *testing.T) {
		dataDir := seedBook3(t)
		sum := runRecordingsOnly(t, dataDir, rows(
			libexRow{asin: "B0LONGALT3", title: longTitle("3"), authors: `{"name":"Sarah Hawke"}`,
				narrators: `{"name":"Other Voice"}`, minutes: 520},
		), false)
		if sum.NewRecordings != 1 || sum.SkippedNoWork != 0 {
			t.Errorf("summary = %+v, want the second narration attached to Book 3", sum)
		}
		if got := recSlugsOf(t, dataDir, book3); len(got) != 2 {
			t.Errorf("Book 3's recordings = %v, want the two narrations", got)
		}
	})
}

// The full-title walk is judged by a claim read off the FULL title: a row titled
// "Towerbound" / "Towerbound, Book 6" at Audible's position 8 is volume 6, which
// only the full title says - so it meets the catalogued volume 6 (the same
// production corroborates the title) instead of creating a duplicate.
func TestFullTitleWalkReadsTheFullTitlesVolume(t *testing.T) {
	dataDir := t.TempDir()
	if seed := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0TOWER006", title: "Towerbound, Book 6", authors: `{"name":"A Writer"}`,
			narrators: `{"name":"A Narrator"}`, minutes: 500, series: `{"name":"Towerbound","position":"6"}`},
	)); seed.NewWorks != 1 {
		t.Fatalf("seed: %+v", seed)
	}

	sum := runOpenAudibleInto(t, dataDir, `[{"asin":"B0TOWERAU6","title":"Towerbound, Book 6","title_short":"Towerbound",
		"author":"A Writer","narrated_by":"A Narrator","series_name":"Towerbound","series_sequence":"8",
		"language":"english","region":"AU","abridged":"false","seconds":30000}]`)

	if sum.NewWorks != 0 || sum.MergedASINs != 1 {
		t.Errorf("summary = %+v, want the AU ASIN merged into volume 6; warnings %v", sum, sum.Warnings)
	}
	if entryExists(t, dataDir, workAddr("towerbound")) {
		t.Errorf("a duplicate work was created at towerbound: %v", listWorks(t, dataDir))
	}
}

// The empty-slug warning belongs to the SHORT title's walk: a row whose short
// title slugs to nothing but whose full title resolves to a work merges there
// without being told its title "produced an empty slug".
func TestUntitledWarningOnlyForTheShortWalk(t *testing.T) {
	dataDir := seedNightfall(t, false)
	sum := runOpenAudibleInto(t, dataDir, `[{"asin":"B0H1DY5QWL","title":"`+nightfallFull+`","title_short":"夜",
		"author":"Sarah Hawke","narrated_by":"Ann Reader","series_name":"Dragon Centurion","series_sequence":"4",
		"language":"english","region":"AU","abridged":"false"}]`)
	if sum.MergedASINs != 1 {
		t.Fatalf("summary = %+v, want the merge; warnings %v", sum, sum.Warnings)
	}
	if hasWarning(sum.Warnings, "produced an empty slug") {
		t.Errorf("the full-title merge was blamed on the short title: %v", sum.Warnings)
	}
}

// A position probe that had to cut the base to fit its "-book-<n>" tail reports
// the cut like every other candidate.
func TestPositionProbesReportTheirCut(t *testing.T) {
	base := strings.Repeat("abcd-", 19) + "abcd" // 99 chars: any tail cuts it
	cands, _ := workCandidates(base, workAuthors{all: []string{"a"}, identity: []string{"a"}},
		positionClaim{series: "Saga", pos: "3"})
	seen := 0
	for _, c := range cands {
		if !c.posSuffixed {
			continue
		}
		seen++
		if !c.shortened {
			t.Errorf("position probe %q cut the base but is not marked shortened", c.slug)
		}
	}
	if seen == 0 {
		t.Fatal("no position probe composed")
	}
}
