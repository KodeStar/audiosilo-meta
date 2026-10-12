package titlerule

import (
	"strings"
	"testing"
)

// Every case is a series name from the tree (2026-10-11) or the importer's own
// narrator-qualifier fixtures, plus the shapes the reader must leave alone.
func TestStripSeriesRecordingQualifiers(t *testing.T) {
	cases := []struct{ in, want string }{
		// Narrator credits, bracketed and as a trailing segment.
		{"Harry Potter (Narrated by Stephen Fry)", "Harry Potter"},
		{"Harry Potter - Gesprochen von Rufus Beck", "Harry Potter"},
		{"Der Seher (gelesen von Gerrit Petersen)", "Der Seher"},
		{"Kalle Blomquist (Hörspiele von Kurt Vethake)", "Kalle Blomquist"},
		{"Audible Exclusive - The Pallisers, read by Timothy West", "Audible Exclusive - The Pallisers"},
		{"Just So Stories (Read by Tony Robinson)", "Just So Stories"},
		{"Foo [narrated by Bob]", "Foo"},
		// Production statements.
		{"Harry Potter (Full-Cast Editions)", "Harry Potter"},
		{"Harry Potter (Full Cast Edition)", "Harry Potter"},
		{"Discworld: Tiffany Aching (abridged)", "Discworld: Tiffany Aching"},
		{"Chrestomanci [Abridged]", "Chrestomanci"},
		{"Jack Reacher (Gekürzt)", "Jack Reacher"},
		{"Die HULDA-Reihe (ungekürzt)", "Die HULDA-Reihe"},
		{"The Sword of Shannara (Unabridged)", "The Sword of Shannara"},
		{"Mistborn [Dramatized Adaptation]", "Mistborn"},
		{"Corvis Rebaine [Dramatized]", "Corvis Rebaine"},
		{"The Innocence of Father Brown [Dramatization]", "The Innocence of Father Brown"},
		{"The New Adventures of Sherlock Holmes [Dramatized Version]", "The New Adventures of Sherlock Holmes"},
		// Several groups: every recording group goes, an ordering or edition group
		// stays where it was, and a group that is neither stops the reading.
		{"Horatio Hornblower (publication order) (abridged)", "Horatio Hornblower (publication order)"},
		{"Harry Potter [German Edition] (gekürzt)", "Harry Potter [German Edition]"},
		{"Harry Potter (Full-Cast Editions) [Spanish Edition]", "Harry Potter [Spanish Edition]"},
		{"Foo (a note) (narrated by B)", "Foo (a note)"},
		{"Foo (abridged) (Narrated by Ann Reader)", "Foo"},
		// A narrator segment exposes the group before it, which is read in the same
		// call - the strip is idempotent, so one call is the whole reading.
		{"Foo (abridged) - Narrated by Ann Reader", "Foo"},
		{"Foo [German Edition] (gekürzt) - Gesprochen von Ann Reader", "Foo [German Edition]"},
		// The separator a narrator segment follows goes with it, whatever it is: an en
		// dash or a period is not left dangling on the name.
		{"Harry Potter – Gesprochen von Rufus Beck", "Harry Potter"},
		{"Die Bibel. Gelesen von Rufus Beck", "Die Bibel"},

		// Left alone: not a recording qualifier.
		{"Harry Potter [German Edition]", "Harry Potter [German Edition]"},
		{"Harry Potter (Deutsche Ausgabe)", "Harry Potter (Deutsche Ausgabe)"},
		{"Vorkosigan Saga (chronological)", "Vorkosigan Saga (chronological)"},
		{"Die Chroniken von Narnia [Hörspiel]", "Die Chroniken von Narnia [Hörspiel]"},
		{"Amanecer Rojo [Red Rising] (Narración en Castellano)", "Amanecer Rojo [Red Rising] (Narración en Castellano)"},
		{"Discworld (Box Set)", "Discworld (Box Set)"},
		{"Charles Paris Dramatisations", "Charles Paris Dramatisations"},
		{"Classic Stories and Tales Read by Celebrities", "Classic Stories and Tales Read by Celebrities"},
		{"Neil Gaiman Full-Cast Radio Adaptations", "Neil Gaiman Full-Cast Radio Adaptations"},
		{"Der König der purpurnen Stadt - Das ungekürzte Hörspiel", "Der König der purpurnen Stadt - Das ungekürzte Hörspiel"},
		{"Sherlock Holmes gelesen von Rupert Pichler", "Sherlock Holmes gelesen von Rupert Pichler"},
		// A narrator segment must be a NAME: prose after a lead-in is not a credit.
		{"Murder: Read by Candlelight", "Murder: Read by Candlelight"},
		{"Books - Narrated by the Author", "Books - Narrated by the Author"},
		// A qualifier naming nobody, a doubled opener, and a name that is nothing but
		// its qualifier.
		{"Sherlock Holmes (gelesen von)", "Sherlock Holmes (gelesen von)"},
		{"Foo ((gelesen von Peter)", "Foo ((gelesen von Peter)"},
		{"(gelesen von Peter Bocek)", "(gelesen von Peter Bocek)"},
		{"(abridged)", "(abridged)"},
		{"Discworld", "Discworld"},
	}
	for _, c := range cases {
		got, ok := StripSeriesRecordingQualifiers(c.in)
		if got != c.want || ok != (c.want != c.in) {
			t.Errorf("StripSeriesRecordingQualifiers(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.want != c.in)
		}
		if again, ok := StripSeriesRecordingQualifiers(got); again != got || ok {
			t.Errorf("not idempotent on %q: %q -> %q", c.in, got, again)
		}
	}
}

// The fast path - no bracket, no lead-in, which is nearly every series name - must
// cost nothing, and the case-folding guard must find exactly what lowering would.
func TestStripSeriesRecordingQualifiersFastPath(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { StripSeriesRecordingQualifiers("The Stormlight Archive") }); n != 0 {
		t.Errorf("a plain series name allocates %v times, want 0", n)
	}
	for _, s := range []string{
		"Harry Potter - Gesprochen von Rufus Beck", "X, READ BY Y", "Narrated By", "gelesen vo",
		"Gelesen\u00a0von", "Über alles", "", "by", "Discworld",
	} {
		if got, want := mentionsNarratorLeadInFold(s), mentionsNarratorLeadIn(strings.ToLower(s)); got != want {
			t.Errorf("mentionsNarratorLeadInFold(%q) = %v, lowering says %v", s, got, want)
		}
	}
}

// The abridgement a stripped qualifier states is readable, so a writer cleaning it off
// a claim can carry it onto the recording; two qualifiers that disagree state nothing.
func TestSeriesAbridgement(t *testing.T) {
	for _, tc := range []struct {
		name             string
		abridged, stated bool
	}{
		{"Discworld: Tiffany Aching (abridged)", true, true},
		{"Jack Reacher (Gekürzt)", true, true},
		{"The Sword of Shannara (Unabridged)", false, true},
		{"Die HULDA-Reihe (ungekürzt)", false, true},
		{"Harry Potter [German Edition] (gekürzt)", true, true},
		{"Foo (abridged) - Narrated by Ann Reader", true, true},
		{"Harry Potter (Full-Cast Editions)", false, false},
		{"Harry Potter (Narrated by Stephen Fry)", false, false},
		{"Foo (abridged) (unabridged)", false, false},
		{"Abridged History", false, false},
		{"Foo (abridged) (Box Set)", false, false},
	} {
		if a, s := SeriesAbridgement(tc.name); a != tc.abridged || s != tc.stated {
			t.Errorf("SeriesAbridgement(%q) = %v, %v; want %v, %v", tc.name, a, s, tc.abridged, tc.stated)
		}
	}
}

// The trailing narrator strip removes exactly the separator the opener accepted,
// including its spaces, without taking any of the title with it.
func TestStripTitleQualifiersTakesTheSeparator(t *testing.T) {
	for _, tc := range []struct {
		name, sep string
		accepted  bool
	}{
		{"colon", ": ", true},
		{"comma", ", ", true},
		{"period", ". ", true},
		{"semicolon", "; ", true},
		{"pipe", "| ", true},
		{"hyphen", " - ", true},
		{"en dash", " \u2013 ", true},
		{"en dash and hyphen", " \u2013- ", true},
		{"spaces around colon", "   :   ", true},
		{"spaces around comma", "   ,   ", true},
		{"spaces around period", "   .   ", true},
		{"spaces around semicolon", "   ;   ", true},
		{"spaces around pipe", "   |   ", true},
		{"spaces around hyphen", "   -   ", true},
		{"spaces around en dash", "   \u2013   ", true},
		{"spaces around en dash and hyphen", "   \u2013-   ", true},
		{"bare space", " ", false},
		{"no separator", "", false},
		{"em dash", " \u2014 ", false},
		{"hyphen without leading space", "- ", false},
		{"en dash without leading space", "\u2013 ", false},
		{"colon without trailing space", ":", false},
		{"hyphen without trailing space", " -", false},
		{"hyphen and en dash", " -\u2013 ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, title := range []string{"Foo", "Café", "A"} {
				head := title + tc.sep
				in := head + "Narrated by Ann Reader"
				want := in
				if tc.accepted {
					want = title
				}
				if got := opensTrailingSegment(head); got != tc.accepted {
					t.Errorf("opensTrailingSegment(%q) = %v, want %v", head, got, tc.accepted)
				}
				if got := dropNarratorQualifier(in, strings.ToLower(in)); got != want {
					t.Errorf("dropNarratorQualifier(%q) = %q, want %q", in, got, want)
				}
				if got := StripTitleQualifiers(in); got != want {
					t.Errorf("StripTitleQualifiers(%q) = %q, want %q", in, got, want)
				}
				if got, ok := StripSeriesRecordingQualifiers(in); got != want || ok != tc.accepted {
					t.Errorf("StripSeriesRecordingQualifiers(%q) = %q, %v; want %q, %v", in, got, ok, want, tc.accepted)
				}
			}
		})
	}
}
