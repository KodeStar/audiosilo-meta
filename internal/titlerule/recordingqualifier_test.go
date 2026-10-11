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
