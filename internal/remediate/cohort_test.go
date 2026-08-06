package remediate

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/build"
)

// TestPartMarkerForms pins the four spellings measured in the live tree, and
// the forms that are deliberately NOT part markers.
func TestPartMarkerForms(t *testing.T) {
	cases := []struct {
		title string
		num   int
		total int
		base  string
		ok    bool
	}{
		{"The Blood Mirror (2 of 2) [Dramatized Adaptation]", 2, 2, "The Blood Mirror", true},
		{"Morning Star (Part 1 of 2) (Dramatized Adaptation)", 1, 2, "Morning Star", true},
		{"The Broken Eye ( 3 of 3) [Dramatized Adaptation]", 3, 3, "The Broken Eye", true},
		{"The Earth Died Screaming, Vol. 1 of 2 (Dramatized Adaptation)", 1, 2, "The Earth Died Screaming", true},
		{"Oathbringer (1 of 6)", 1, 6, "Oathbringer", true},
		{"Heroes Road: Volume Two (3 of 3) [Dramatized]", 3, 3, "Heroes Road: Volume Two", true},

		// Not part markers.
		{"Black Prism [Dramatized Adaptation]", 0, 0, "Black Prism", false},
		{"Blood Stained: The Legend of Andrew Rufus (Book 3 of 7)", 0, 0,
			"Blood Stained: The Legend of Andrew Rufus (Book 3 of 7)", false},
		{"The Riftborn Chronicles, Book 1 of 10: Awakening the Rift", 0, 0,
			"The Riftborn Chronicles, Book 1 of 10: Awakening the Rift", false},
		{"1984", 0, 0, "1984", false},
	}
	for _, c := range cases {
		ref, ok := partOf(c.title)
		if ok != c.ok || (ok && (ref.Num != c.num || ref.Total != c.total)) {
			t.Errorf("partOf(%q) = %+v,%v; want %d of %d,%v", c.title, ref, ok, c.num, c.total, c.ok)
		}
		if got := baseTitle(c.title); got != c.base {
			t.Errorf("baseTitle(%q) = %q, want %q", c.title, got, c.base)
		}
	}
}

// TestTitleKeyIsArticleTolerant pins the matching key two spellings of one book
// have to meet on.
func TestTitleKeyIsArticleTolerant(t *testing.T) {
	same := [][2]string{
		{"The Black Prism", "Black Prism"},
		{"Against The Odds", "Against the Odds"},
		{"A Court of Thorns and Roses", "Court of Thorns and Roses"},
		{"Stone of Farewell", "The Stone of Farewell"},
	}
	for _, p := range same {
		if titleKey(p[0]) != titleKey(p[1]) {
			t.Errorf("titleKey(%q)=%q and titleKey(%q)=%q must match", p[0], titleKey(p[0]), p[1], titleKey(p[1]))
		}
	}
	if titleKey("The Blinding Knife") == titleKey("The Broken Eye") {
		t.Error("different books must not share a title key")
	}
}

func TestEarlierDatePrefersTheMoreSpecificSpelling(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"", "2020", "2020"},
		{"2020", "", "2020"},
		{"2020", "2020-06-01", "2020-06-01"},
		{"2020-06-01", "2020", "2020-06-01"},
		{"2019-11-05", "2020-06-01", "2019-11-05"},
		{"2021", "2020-06-01", "2020-06-01"},
	}
	for _, c := range cases {
		if got := earlierDate(c.a, c.b); got != c.want {
			t.Errorf("earlierDate(%q,%q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// TestEarlierStampAgreesWithTheArtifactOrdering pins this package's added_at
// comparison against internal/build's, which derives the artifact's added_at
// from the same values. Two orderings of one pair would mean the merged record
// and the artifact disagreed about which date is earlier.
func TestEarlierStampAgreesWithTheArtifactOrdering(t *testing.T) {
	values := []string{
		"2026-07-25",
		"2026-07-25T17:12:12+01:00", // the same DAY, an hour ahead of UTC
		"2026-07-25T23:30:00-05:00", // the NEXT day in UTC
		"2026-07-26T00:00:00Z",
		"2026-07-26",
		"2020-01-01T00:00:00Z",
	}
	for _, a := range values {
		for _, b := range values {
			got := earlierStamp(a, b)
			want := a
			if build.TimeKey(b) < build.TimeKey(a) {
				want = b
			}
			if got != want {
				t.Errorf("earlierStamp(%q,%q) = %q; the artifact orders them %q first", a, b, got, want)
			}
		}
	}
	if got := earlierStamp("", "2026-07-25"); got != "2026-07-25" {
		t.Errorf("earlierStamp(\"\", x) = %q, want x", got)
	}
}

func TestModalIsTieBrokenByValue(t *testing.T) {
	if got := modal([]string{"GraphicAudio", "Graphic Audio LLC", "GraphicAudio"}); got != "GraphicAudio" {
		t.Errorf("modal = %q, want the commonest", got)
	}
	if got := modal([]string{"b", "a"}); got != "a" {
		t.Errorf("modal = %q, want a tie broken by the value so the answer never depends on input order", got)
	}
	if got := modal(nil); got != "" {
		t.Errorf("modal(nil) = %q, want empty", got)
	}
}
