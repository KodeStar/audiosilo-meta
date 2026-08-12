package importer

import "testing"

// TestLeadingConjunctionStrips is the measured target list: the eight dump names
// the two guards admit, each the tail of a co-credit list the source split one
// element too late.
func TestLeadingConjunctionStrips(t *testing.T) {
	cases := []struct{ in, want string }{
		{"and Melanie Mendez", "Melanie Mendez"},
		{"and Ava Erickson", "Ava Erickson"},
		{"and Erin Mallon", "Erin Mallon"},
		{"and Lili Valente", "Lili Valente"},
		{"and Soneela Nankani", "Soneela Nankani"},
		{"and Tim Sample", "Tim Sample"},
		{"and David Meerman Scott", "David Meerman Scott"},
		{"and various narrators", "various narrators"},
		// The dump spells the conjunction both ways.
		{"And Ava Erickson", "Ava Erickson"},
		{"AND Ava Erickson", "Ava Erickson"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := stripLeadingConjunction(c.in, seenAs(c.want)); got != c.want {
				t.Errorf("stripLeadingConjunction(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestLeadingConjunctionNeedsTwoWords is the word floor, and it is load-bearing
// rather than defensive: "more", "others" and "Friends" are all attested credits
// in the dump, so the census alone would have stripped these onto a person
// called "more".
func TestLeadingConjunctionNeedsTwoWords(t *testing.T) {
	seen := func(string) bool { return true } // the most permissive census there is
	for _, name := range []string{"and more", "and others", "and Others", "and Friends", "And Arndt."} {
		if got := stripLeadingConjunction(name, seen); got != name {
			t.Errorf("stripLeadingConjunction(%q) = %q: a one-word remainder must never strip", name, got)
		}
	}
}

// TestLeadingConjunctionNeedsTheCensus is the other guard. "And We Know" is a
// channel whose name begins with the word; the two real names are ones nobody
// credited bare in the dump, so they import as spelled - a missed cleanup, which
// is the only thing a census smaller than the truth can cost.
func TestLeadingConjunctionNeedsTheCensus(t *testing.T) {
	for _, name := range []string{"And We Know", "and Christopher Wall", "and the Axis Team with Sarah Miller"} {
		t.Run(name, func(t *testing.T) {
			for _, seen := range []creditSeenFunc{nil, seenAs("Somebody Else")} {
				if got := stripLeadingConjunction(name, seen); got != name {
					t.Errorf("stripLeadingConjunction(%q) = %q, want it untouched", name, got)
				}
			}
		})
	}
}

// TestLeadingConjunctionNeedsTheWholeWord keeps the rule off every name that
// merely begins with the letters.
func TestLeadingConjunctionNeedsTheWholeWord(t *testing.T) {
	seen := func(string) bool { return true }
	for _, name := range []string{"Andrea Lodge", "Andi Arndt", "Anderson Cooper", "Andrew Lang"} {
		if got := stripLeadingConjunction(name, seen); got != name {
			t.Errorf("stripLeadingConjunction(%q) = %q, want it untouched", name, got)
		}
	}
}

// TestLeadingConjunctionIsSilentWithoutACensus keeps the public door honest.
func TestLeadingConjunctionIsSilentWithoutACensus(t *testing.T) {
	const name = "and Melanie Mendez"
	if got := CleanCreditName(name); got != name {
		t.Errorf("CleanCreditName(%q) = %q, want the name unchanged", name, got)
	}
}

// TestLeadingConjunctionMergesOntoTheCreditedPerson is the run-level proof: one
// row credits the narrator properly and the next carries the dangling
// conjunction, which without the rule minted and-erin-mallon beside her.
func TestLeadingConjunctionMergesOntoTheCreditedPerson(t *testing.T) {
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0CONJUNC1", title: "A Book", authors: `{"name":"Ada One"}`, narrators: `{"name":"Erin Mallon"}`},
		libexRow{asin: "B0CONJUNC2", title: "Another Book", authors: `{"name":"Ada One"}`, narrators: `{"name":"and Erin Mallon"}`},
	), false)

	if sum.NewPeople != 2 { // the shared author plus the one narrator
		t.Fatalf("NewPeople = %d, want 2: the dangling conjunction forked the narrator", sum.NewPeople)
	}
	if entryExists(t, dataDir, personAddr("and-erin-mallon")) {
		t.Error("the conjunction-prefixed person record was created")
	}
	if !entryExists(t, dataDir, personAddr("erin-mallon")) {
		t.Error("the bare person record is missing")
	}
}
