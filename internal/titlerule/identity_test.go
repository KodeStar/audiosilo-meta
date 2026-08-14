package titlerule

import "testing"

// IdentityTitleKey is the rule three defences share, so what it calls one book is
// pinned here rather than at any of them: the intake gate, the bulk importer's
// create guard and metacheck's census would otherwise each be free to disagree.
func TestIdentityTitleKey(t *testing.T) {
	cases := []struct {
		name         string
		titleA, serA string
		titleB, serB string
		same         bool
	}{
		{
			name:   "a retailer's series-and-volume tail is not identity",
			titleA: "Hammered", serA: "The Iron Druid Chronicles",
			titleB: "Hammered: The Iron Druid Chronicles, Book 3", serB: "The Iron Druid Chronicles",
			same: true,
		},
		{
			name:   "an edition marker is not identity",
			titleA: "Mageling", titleB: "Mageling (Unabridged)", same: true,
		},
		{
			name:   "a genre subtitle is not identity",
			titleA: "Broken Pride", titleB: "Broken Pride: A Dark Fantasy Adventure", same: true,
		},
		{
			name:   "a leading article is not identity",
			titleA: "The Blood of Elves", titleB: "Blood of Elves", same: true,
		},
		{
			name:   "case, punctuation and diacritics are not identity",
			titleA: "Café Society", titleB: "cafe society!", same: true,
		},
		{
			name:   "two different books stay two keys",
			titleA: "Hammered", titleB: "Hounded", same: false,
		},
		{
			name:   "a real subtitle is part of the title",
			titleA: "Star Wars", titleB: "Star Wars: A New Hope", same: false,
		},
		{
			// The one shape the key CANNOT separate, which is why every caller pairs it
			// with the stated-volume test: two volumes of a serial differ only by the
			// marker the key removes.
			name:   "two volumes of one serial share the key",
			titleA: "Bravelands, Book 1", titleB: "Bravelands, Book 2", same: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b := IdentityTitleKey(c.titleA, c.serA), IdentityTitleKey(c.titleB, c.serB)
			if a == "" || b == "" {
				t.Fatalf("empty key: %q -> %q, %q -> %q", c.titleA, a, c.titleB, b)
			}
			if (a == b) != c.same {
				t.Errorf("IdentityTitleKey(%q) = %q and (%q) = %q: same = %v, want %v",
					c.titleA, a, c.titleB, b, a == b, c.same)
			}
		})
	}
}

// The stated-volume test is what keeps the serial case above from being read as a
// duplicate, and it is deliberately silent when only ONE side states a number - that
// pair ("Hammered" beside "Hammered, Book 3") is the duplicate the gates exist for.
func TestSameStatedVolume(t *testing.T) {
	cases := []struct {
		a, sa, b, sb string
		want         bool
	}{
		{"Bravelands, Book 1", "Bravelands", "Bravelands, Book 2", "Bravelands", false},
		{"Bravelands, Book 2", "Bravelands", "Bravelands, Book 2", "Bravelands", true},
		{"Hammered", "", "Hammered, Book 3", "", true},
		{"Hammered", "", "Hounded", "", true},
	}
	for _, c := range cases {
		if got := SameStatedVolume(c.a, c.sa, c.b, c.sb); got != c.want {
			t.Errorf("SameStatedVolume(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// StripDecoration's refusal CODES are what the intake gate branches on, so each one
// is reached by a fixture: the two content refusals become a needs-human verdict and
// the rest leave the submitted title alone.
func TestStripDecorationRefusals(t *testing.T) {
	cases := []struct {
		name          string
		title, series string
		want          string // the proposed title, or "" when refused
		refusal       string
	}{
		{
			name: "an edition marker strips safely",
			title: "Two Ravens (Unabridged)", want: "Two Ravens",
		},
		{
			name: "a series-and-volume tail strips safely",
			title: "Hammered: The Iron Druid Chronicles, Book 3", series: "The Iron Druid Chronicles",
			want: "Hammered",
		},
		{
			name: "an undecorated title has nothing to strip",
			title: "Hounded", refusal: RefuseNothingToStrip,
		},
		{
			name: "a residual that names no book is refused",
			title: "Omnibus: A LitRPG Adventure", refusal: RefuseNoIdentity,
		},
		{
			name: "a residual that reads as a fragment is refused",
			title: "and the Great Escape: A Dark Fantasy Adventure", refusal: RefuseFragment,
		},
		{
			name: "a title that IS its series' name is left alone",
			title: "The Iron Druid Chronicles", series: "The Iron Druid Chronicles",
			refusal: RefuseIsSeriesName,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, refusal, ok := StripDecoration(c.title, c.series)
			if c.want != "" {
				if !ok || got != c.want {
					t.Fatalf("StripDecoration(%q, %q) = (%q, %q, %v), want %q", c.title, c.series, got, refusal, ok, c.want)
				}
				return
			}
			if ok {
				t.Fatalf("StripDecoration(%q, %q) proposed %q, want the refusal %q", c.title, c.series, got, c.refusal)
			}
			if refusal != c.refusal {
				t.Errorf("StripDecoration(%q, %q) refusal = %q, want %q", c.title, c.series, refusal, c.refusal)
			}
		})
	}
}

// ProposeTitle is StripDecoration without the code, and the two must agree - it is
// the same rule with one return value dropped, and internal/audit reads it.
func TestProposeTitleAgreesWithStripDecoration(t *testing.T) {
	for _, title := range []string{
		"Two Ravens (Unabridged)", "Hounded", "Omnibus: A LitRPG Adventure",
		"Hammered: The Iron Druid Chronicles, Book 3",
	} {
		want, _, wantOK := StripDecoration(title, "The Iron Druid Chronicles")
		got, ok := ProposeTitle(title, "The Iron Druid Chronicles")
		if got != want || ok != wantOK {
			t.Errorf("ProposeTitle(%q) = (%q, %v), StripDecoration says (%q, %v)", title, got, ok, want, wantOK)
		}
	}
}

// SeriesNameFor picks the membership a title is READ against: the one the title
// spells out, else the first of the caller's (deterministically ordered) list.
func TestSeriesNameFor(t *testing.T) {
	names := []string{"Alpha Chronicles", "The Iron Druid Chronicles"}
	if got := SeriesNameFor("Hammered: The Iron Druid Chronicles, Book 3", names); got != "The Iron Druid Chronicles" {
		t.Errorf("SeriesNameFor = %q, want the series the title names", got)
	}
	if got := SeriesNameFor("Hammered", names); got != "Alpha Chronicles" {
		t.Errorf("SeriesNameFor = %q, want the first membership when the title names none", got)
	}
	if got := SeriesNameFor("Hammered", nil); got != "" {
		t.Errorf("SeriesNameFor with no memberships = %q, want empty", got)
	}
}
