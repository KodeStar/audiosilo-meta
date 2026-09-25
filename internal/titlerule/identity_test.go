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

// A residual that names no book is NO IDENTITY, not a shared one. The measured
// hazard: a one-word series name was stripped out of its own titles, leaving the bare
// volume number, so "Cars 2" and "Hawk 2" keyed alike - 199 numeric keys over 3,287
// real works in the tree, plus the packaging residuals.
//
// The bare-number half of that hazard cannot arise any more: the series strip is
// boundary-anchored, so a one-word name in front of a number is not excised at all and
// the two sequels below keep their own keys (asserted in the test that follows). The
// packaging residuals still need the guard.
func TestIdentityTitleKeyRefusesDegenerateResiduals(t *testing.T) {
	for _, c := range []struct{ title, series string }{
		{"Omnibus", ""},
		{"Book One", ""},
		{"The Complete Boxed Set", ""},
		{"- Band 5", ""},
	} {
		if got := IdentityTitleKey(c.title, c.series); got != "" {
			t.Errorf("IdentityTitleKey(%q, %q) = %q, want no identity", c.title, c.series, got)
		}
	}
	// And the titles it must still key, so the guard is not simply off. The numeric
	// ones are the exception numbersAreIdentity carves: a year or a date range names a
	// book where a bare volume number does not.
	for _, c := range []struct{ title, series string }{
		{"Hammered", "The Iron Druid Chronicles"},
		{"Cars and Trucks", "Cars"},
		{"1984", ""},
		{"Without a Trace: 1881-1968", ""},
	} {
		if IdentityTitleKey(c.title, c.series) == "" {
			t.Errorf("IdentityTitleKey(%q, %q) refused a title that names a book", c.title, c.series)
		}
	}
	// A title whose residual is the SERIES NAME (the clean falls back rather than
	// emptying) does key - and is kept apart by the stated-volume test instead, which
	// is the division of labour every caller relies on.
	a := IdentityTitleKey("Unintended Cultivator: Volume 9", "Unintended Cultivator")
	b := IdentityTitleKey("Unintended Cultivator: Volume 3", "Unintended Cultivator")
	if a == "" || a != b {
		t.Fatalf("keys = %q and %q, want one shared key", a, b)
	}
	if SameStatedVolume("Unintended Cultivator: Volume 9", "Unintended Cultivator",
		"Unintended Cultivator: Volume 3", "Unintended Cultivator", ReaderPolicy) {
		t.Error("volumes 9 and 3 must state different volumes, which is what keeps them apart")
	}
}

// THE SERIES STRIP IS BOUNDARY-ANCHORED, and this is the live failure that made it so:
// a repair wave merged two different travel guides, non-advisory. Both titles named a
// catalogued series ("New Orleans", "New York") and the mid-title excision cut EVERY
// occurrence out, so both reduced to "The Best of for Short Stay Travel" - one key, two
// books, two narrators, two runtimes.
func TestIdentityTitleKeyStripsASeriesNameAtBoundariesOnly(t *testing.T) {
	orleans := IdentityTitleKey("New Orleans: The Best of New Orleans for Short Stay Travel", "New Orleans")
	york := IdentityTitleKey("New York: The Best of New York for Short Stay Travel", "New York")
	if orleans == "" || york == "" {
		t.Fatalf("keys = %q and %q, want both books keyed", orleans, york)
	}
	if orleans == york {
		t.Errorf("two different travel guides share the key %q", orleans)
	}
	// The LEADING series segment still comes off both, which is what makes each of them
	// meet its own undecorated twin - the anchoring removes the excision, not the strip.
	if want := IdentityTitleKey("The Best of New Orleans for Short Stay Travel", ""); orleans != want {
		t.Errorf("key = %q, want the leading series segment stripped (%q)", orleans, want)
	}
	// And the shapes that MUST still meet, each an anchored boundary: a trailing
	// "<Series>, Book N" segment, a leading "<Series>: " segment, and a bracketed group.
	for _, c := range []struct{ decorated, plain, series string }{
		{"Hammered: The Iron Druid Chronicles, Book 3", "Hammered", "The Iron Druid Chronicles"},
		{"The Iron Druid Chronicles: Hammered", "Hammered", "The Iron Druid Chronicles"},
		{"Hammered (The Iron Druid Chronicles, Book 3)", "Hammered", "The Iron Druid Chronicles"},
	} {
		a, b := IdentityTitleKey(c.decorated, c.series), IdentityTitleKey(c.plain, c.series)
		if a == "" || a != b {
			t.Errorf("IdentityTitleKey(%q) = %q, want the key of %q (%q)", c.decorated, a, c.plain, b)
		}
	}
	// A one-word series name in front of a number is no longer excised, so the sequels
	// that used to collide on the bare "2" now key by what they say.
	cars, hawk := IdentityTitleKey("Cars 2", "Cars"), IdentityTitleKey("Hawk 2", "Hawk")
	if cars == "" || hawk == "" || cars == hawk {
		t.Errorf("keys = %q and %q, want two distinct keys", cars, hawk)
	}
	if IdentityTitleKey("Cars 3", "Cars") == cars {
		t.Error("two volumes of one one-word series must not share a key")
	}
}

// THE KEY DOES NOT INHERIT THE PROPOSAL'S FRAGMENT TEST, in either of its arms - a
// residual that reads as a fragment is a poor title to WRITE and a perfectly
// discriminating key. Every title here would lose its key to one arm or the other, and
// each of them names a book; the measurement behind both refusals is on
// IdentityTitleKey.
func TestIdentityTitleKeyDoesNotInheritTheFragmentTest(t *testing.T) {
	for _, c := range []struct{ title, series string }{
		// The EDGE arms: a leading joining word, a trailing stopword. Each of these was
		// a correct merge proposal that applying them cost.
		{"Of Mice and Men", ""},
		{"To Kill a Mockingbird", ""},
		{"At the Mountains of Madness [Blackstone Edition]", ""},
		{"By Royal Command", "Young Bond"},
		{"All In, Book 3", ""},
		// The INTERIOR arm: real titles that simply ABUT two function words. Nothing was
		// cut out of the middle of any of them, and there are ~198 more in the tree.
		{"To Have and to Hold", ""},
		{"In Sickness and in Health", ""},
		{"For Better or for Worse", ""},
		{"Snowed in with the Tycoon", ""},
		{"Murder in E Minor", ""},
		{"Girls from da Hood 10", ""},
		{"The Spy Who Came in from the Cold (Dramatised)", ""},
		// And the diacritic case: the raw ASCII split cut "astronomía" in half and read
		// the "a" as a stranded article, which refused the whole Spanish catalogue.
		{"Breve historia de la astronomía [Brief History of Astronomy]", ""},
	} {
		if IdentityTitleKey(c.title, c.series) == "" {
			t.Errorf("IdentityTitleKey(%q, %q) refused a title that names a book", c.title, c.series)
		}
	}
	// The pairs those lines exist for: the decorated spelling still meets its twin, which
	// is the merge each refusal would have withheld.
	for _, c := range []struct{ a, sa, b, sb string }{
		{"At the Mountains of Madness [Blackstone Edition]", "", "At the Mountains of Madness", ""},
		{"Young Bond: By Royal Command", "Young Bond", "By Royal Command", "Young Bond"},
		{"All In, Book 3", "", "All In", ""},
		{"The Spy Who Came in from the Cold (Dramatised)", "", "The Spy Who Came in from the Cold", ""},
		{"Breve historia de la astronomía [Brief History of Astronomy]", "", "Breve historia de la astronomía", ""},
	} {
		x, y := IdentityTitleKey(c.a, c.sa), IdentityTitleKey(c.b, c.sb)
		if x == "" || x != y {
			t.Errorf("IdentityTitleKey(%q) = %q, want the key of %q (%q)", c.a, x, c.b, y)
		}
	}
}

// The two volume spellings BareSeq cannot read, both of them residuals the key throws
// away: a DIVISION-class ordinal (the key drops "Season 2" as packaging) and a ROMAN
// numeral (the key drops "Volume II" as a marker). Unread, each collapsed a serial
// onto its first volume.
func TestStatedVolumeReadsOrdinalsAndRomanNumerals(t *testing.T) {
	for _, c := range []struct {
		title, series string
		want          float64
	}{
		{"The Wandering Inn: Season 2", "The Wandering Inn", 2},
		{"Pimsleur Spanish: Level 3", "Pimsleur Spanish", 3},
		{"Die Akademie - Staffel 4", "Die Akademie", 4},
		{"Faraway Paladin: Volume II", "Faraway Paladin", 2},
		{"Faraway Paladin: Volume I", "Faraway Paladin", 1},
		{"Discworld, Book IX", "Discworld", 9},
	} {
		got, ok := ReaderPolicy.Volume(c.title, c.series)
		if !ok || got != c.want {
			t.Errorf("ReaderPolicy.Volume(%q) = (%v, %v), want %v", c.title, got, ok, c.want)
		}
	}
	// The pair that must therefore disagree - the whole point of reading them.
	if SameStatedVolume("The Inn: Season 1", "The Inn", "The Inn: Season 2", "The Inn", ReaderPolicy) {
		t.Error("two seasons must state different volumes")
	}
	if SameStatedVolume("Paladin: Volume I", "Paladin", "Paladin: Volume II", "Paladin", ReaderPolicy) {
		t.Error("two roman-numbered volumes must state different volumes")
	}
	// And an ordinary title states nothing, so it is not accidentally a volume.
	if _, ok := ReaderPolicy.Volume("A Season for Ravens", ""); ok {
		t.Error("an ordinary title must not read as a stated volume")
	}
}

// The third spelling BareSeq cannot read: a WORD volume number. The key loses it in
// exactly two shapes - inside a decorative bracket group, and left dangling by the
// series strip - and both collapsed a serial onto a sibling until the volume reading read
// the words back.
func TestStatedVolumeReadsWordVolumes(t *testing.T) {
	for _, c := range []struct {
		title, series string
		want          float64
	}{
		// The bracketed group: the key drops it whole, so the pair meets on "Wildwood".
		{"Wildwood (Book Two)", "", 2},
		{"Wildwood [Part One]", "", 1},
		// The issue's own pair: the series strip takes ": The Black Forest" and the
		// dangling-tail peel takes ", Book Two" with it, leaving "Hellmervick".
		{"Hellmervick, Book Two: The Black Forest", "The Black Forest", 2},
		{"Faraway Paladin: Volume Twelve", "Faraway Paladin", 12},
		{"Die Akademie, Teil Drei", "Die Akademie", 0}, // German number words are not in the vocabulary
		// A COMPOSITE number is refused, not read as its first word: "Book One
		// Hundred" states volume 100, which the vocabulary cannot read, and stating
		// 1 instead would be a fabricated fact.
		{"The Saga, Book One Hundred", "The Saga", 0},
		{"Chronicle, Volume Two Thousand", "Chronicle", 0},
	} {
		got, ok := ReaderPolicy.Volume(c.title, c.series)
		if c.want == 0 {
			if ok {
				t.Errorf("ReaderPolicy.Volume(%q) = (%v, true), want no statement", c.title, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("ReaderPolicy.Volume(%q) = (%v, %v), want %v", c.title, got, ok, c.want)
		}
	}
	// The pair that must therefore disagree - the whole point of reading them.
	if SameStatedVolume("Wildwood (Book One)", "", "Wildwood (Book Two)", "", ReaderPolicy) {
		t.Error("two word-numbered volumes must state different volumes")
	}
	// One spelling never contradicts the other: they are one vocabulary.
	if !SameStatedVolume("Wildwood (Book Two)", "", "Wildwood (Book 2)", "", ReaderPolicy) {
		t.Error("the same volume spelled two ways must not read as a contradiction")
	}
	// A word that merely FOLLOWS a marker is not a number, and a marker welded into a
	// word is not a marker: the whitespace in wordVolume is what says so.
	for _, title := range []string{
		"Parts Unknown", "The Book Thief", "Book of One Thousand Nights",
		"The Chosen One", "The Partone Affair", "Book Thirteen",
	} {
		if v, ok := ReaderPolicy.Volume(title, ""); ok {
			t.Errorf("ReaderPolicy.Volume(%q) = (%v, true), want no statement", title, v)
		}
	}
	// The digit, ordinal and roman readings are untouched by the widening.
	for _, c := range []struct {
		title, series string
		want          float64
	}{
		{"Bravelands, Book 2", "Bravelands", 2},
		{"The Wandering Inn: Season 2", "The Wandering Inn", 2},
		{"Faraway Paladin: Volume II", "Faraway Paladin", 2},
	} {
		if got, ok := ReaderPolicy.Volume(c.title, c.series); !ok || got != c.want {
			t.Errorf("ReaderPolicy.Volume(%q) = (%v, %v), want %v", c.title, got, ok, c.want)
		}
	}
}

// Issue #2258, finding 1: a DIVISION marker numbered in WORDS. divisionSequence read
// it all along while the volume reading stated nothing, so a volume-conflict cluster of
// such titles was filed with a note naming no volume at all - and every gate that
// asks for a volume was blind to it.
//
// It is read only in MARKER POSITION: the division words are ordinary title words,
// and a reading is not free - the writers' positive test turns a stated volume into
// a CREATE (see TestStatedVolumeReadsNoOrdinaryTitleWordAsAVolume).
func TestStatedVolumeReadsWordNumberedDivisions(t *testing.T) {
	for _, c := range []struct {
		title, series string
		want          float64
	}{
		{"Wildwood (Season One)", "", 1},
		{"Wildwood (Season Two)", "", 2},
		{"Wildwood, Level Three", "", 3},
		{"Powder River - Season Four", "Powder River", 4},
		{"Jago & Litefoot: Series Seven", "Jago & Litefoot", 7},
		{"Stranger Things, Season One: The Junior Novelization", "", 1},
		{"The Lucid - Season One: The Beginning", "The Lucid", 1},
		{"Supermind: Season One - The Brain Drain", "Supermind", 1},
		{"Nameless: Season One (Unabridged)", "", 1},
		{"Pimsleur Albanian, Unit Twelve", "", 12},
		{"Wildwood \u2013 Season Two \u2013 The Return", "", 2}, // an en dash is a separator too
		// The removed series name opens the segment: stripSeries leaves its space.
		{"Junkers Season Two", "Junkers", 2},
	} {
		got, ok := ReaderPolicy.Volume(c.title, c.series)
		if !ok || got != c.want {
			t.Errorf("ReaderPolicy.Volume(%q) = (%v, %v), want %v", c.title, got, ok, c.want)
		}
		// The invariant divisionWords states: a title whose markers are all division
		// markers, read in marker position, states the sequence's first element.
		if seq := divisionSequence(c.title, c.series); len(seq) == 0 || seq[0] != got {
			t.Errorf("%q: volume %v, divisionSequence %v - one division read two ways", c.title, got, seq)
		}
	}
	if SameStatedVolume("Wildwood (Season One)", "", "Wildwood (Season Two)", "", ReaderPolicy) {
		t.Error("two word-numbered seasons must state different volumes")
	}
	if !SameStatedVolume("Wildwood (Season Two)", "", "Wildwood (Season 2)", "", ReaderPolicy) {
		t.Error("one season spelled two ways must not read as a contradiction")
	}
}

// The review of issue #2258's first fix: the division words are ordinary title
// words, and reading "Level One Dropout" as volume 1 is not merely extra caution. The
// writers' POSITIVE test (a title stating a volume must be positively placed at it)
// turns a reading into a CREATE, so a decorated duplicate of the catalogued "Level One
// Dropout" was minted as a sibling work where main refused it. Only the WORD spelling
// is bounded this way - the digit spelling reads as it always did.
func TestStatedVolumeReadsNoOrdinaryTitleWordAsAVolume(t *testing.T) {
	for _, c := range []struct {
		title, series string
		want          float64 // 0 = no statement
	}{
		{"Level One Dropout", "", 0},    // the title begins with it
		{"Level One God", "", 0},        // ... and runs on after it
		{"A Series Two-Step", "", 0},    // mid-phrase on both sides
		{"Foo: Level One God", "", 0},   // opens a segment, runs on into the title
		{"Foo: Series Two-Step", "", 0}, // a hyphen welded on is no separator
		{"Kingdom of Ara: Season Four Complete", "", 0},
		{"Dad's Army: Complete Radio Series Two", "Dad's Army", 0},
		{"Series One Collection, Season 3", "", 3}, // the digit marker still reads
		{"Level 1 Dropout", "", 1},                 // main's digit reading, unchanged
	} {
		got, ok := ReaderPolicy.Volume(c.title, c.series)
		switch {
		case c.want == 0 && ok:
			t.Errorf("ReaderPolicy.Volume(%q) = (%v, true), want no statement", c.title, got)
		case c.want != 0 && (!ok || got != c.want):
			t.Errorf("ReaderPolicy.Volume(%q) = (%v, %v), want %v", c.title, got, ok, c.want)
		}
	}
}

// Issue #2258, finding 2, and the tier order it was fixed inside. Within a tier the
// EARLIEST marker answers whatever spells it - the roman arm used to be tried before
// the word arm, so "Book Two, Part V" read volume 5 - while ACROSS tiers a volume
// marker in digits outranks one in words or roman numerals, which outranks a
// division marker. Every cross-tier case below is a recorded series position the
// flat "earliest marker wins" order got wrong (see VOLUME STATEMENTS), or a reading the
// tier order CHANGES from main's arm order, pinned so the change is deliberate.
func TestStatedVolumeTierOrder(t *testing.T) {
	for _, c := range []struct {
		title, series string
		want          float64
	}{
		// Finding 2: one tier, two spellings - position decides.
		{"Book Two, Part V", "", 2},
		{"Volume One, Part II", "", 1},
		{"Part II, Book Three", "", 2},
		{"Season II, Level Three", "", 2},
		// A volume marker outranks a division marker, in any spelling.
		{"Yesterday's Gone: Season 1 - Ep. 3", "Yesterday's Gone", 3},
		{"Criminal Intentions: Season One, Episode Three", "", 3},
		{"Level One Dropout, Book Two", "", 2},
		{"Season 2, Book Three", "", 3},
		// CHANGED from main, which tried the division ordinal before the word arm
		// and read 3. No title on the tree carries this combination.
		{"Book Two, Season 3", "", 2},
		// A digit volume marker outranks a word one: the retailer's own trailing
		// "(Series, Book N)" behind a leading subseries part.
		{"Ghosts: Adrian's March, Part Five (Adrian's Undead Diary, Book 13)", "Adrian's Undead Diary", 13},
		// An unreadable match is passed over for the arm's NEXT match, never ending
		// the arm: a composite word number cannot hide a readable marker after it.
		{"The Saga, Book One Hundred, Part 2", "The Saga", 2},
		{"The Saga, Book One Hundred, Part Two", "The Saga", 2},
		{"Chronicles: Unit One Hundred and Level 4", "", 4},
		{"Season One Hundred, Season 3", "", 3},
		// A roman numeral needs a separator: "Parti" is a word, not "Part i".
		{"Parti Animals: Season 3", "", 3},
		{"Vol.II", "", 2},
	} {
		if got, ok := ReaderPolicy.Volume(c.title, c.series); !ok || got != c.want {
			t.Errorf("ReaderPolicy.Volume(%q) = (%v, %v), want %v", c.title, got, ok, c.want)
		}
	}
	for _, title := range []string{"Parti Animals", "Chronicle, Season One Hundred", "The Saga, Book One Hundred"} {
		if v, ok := ReaderPolicy.Volume(title, ""); ok {
			t.Errorf("ReaderPolicy.Volume(%q) = (%v, true), want no statement", title, v)
		}
	}
}

// WriterPolicy is the writers' reading: ReaderPolicy minus the word-numbered
// division, and otherwise identical - every other spelling, the tier order and the
// unreadable-match rule included.
func TestWriterPolicyLeavesOutOnlyTheWordNumberedDivision(t *testing.T) {
	for _, c := range []struct {
		title, series string
		want          float64 // 0 = no claim
	}{
		{"Locked In: Season One", "", 0},
		{"Powder River - Season Four", "Powder River", 0},
		{"Wildwood, Level Three", "", 0},
		{"Locked In: Season 1", "", 1},
		{"Locked In: Season II", "", 2},
		{"Hammered, Book 7", "", 7},
		{"Wildwood (Book Two)", "", 2},
		{"Book Two, Part V", "", 2},
		{"Criminal Intentions: Season One, Episode Three", "", 3},
		{"Chronicles: Unit One Hundred and Level 4", "", 4},
	} {
		got, ok := WriterPolicy.Volume(c.title, c.series)
		switch {
		case c.want == 0 && ok:
			t.Errorf("WriterPolicy.Volume(%q) = (%v, true), want no claim", c.title, got)
		case c.want != 0 && (!ok || got != c.want):
			t.Errorf("WriterPolicy.Volume(%q) = (%v, %v), want %v", c.title, got, ok, c.want)
		}
		if c.want != 0 {
			if sv, sok := ReaderPolicy.Volume(c.title, c.series); !sok || sv != got {
				t.Errorf("%q: WriterPolicy %v but ReaderPolicy (%v, %v) - they may differ only on a word-numbered division", c.title, got, sv, sok)
			}
		}
	}
}

// The CONTRADICTION test follows the policy as the positive test does. A writer turns a
// contradiction into a create (the match is vetoed, the row is minted), so a volume only
// ReaderPolicy reads - a word-numbered division - may not contradict anything for a
// writer: "Nameless (Season One)" against "Nameless (Volume II)" (one key) disagrees 1 vs
// 2 for the audit and agrees for the writers, whose reading of the first is silent.
func TestContradictionFollowsThePolicy(t *testing.T) {
	a, b := "Nameless (Season One)", "Nameless (Volume II)"
	if IdentityTitleKey(a, "") == "" || IdentityTitleKey(a, "") != IdentityTitleKey(b, "") {
		t.Fatalf("the pair must share a key: %q, %q", IdentityTitleKey(a, ""), IdentityTitleKey(b, ""))
	}
	if SameStatedVolume(a, "", b, "", ReaderPolicy) {
		t.Error("ReaderPolicy: season 1 and volume 2 must contradict")
	}
	if !SameStatedVolume(a, "", b, "", WriterPolicy) {
		t.Error("WriterPolicy: a word-numbered season states nothing, so nothing contradicts")
	}
	if got := StatementOf(a, "", WriterPolicy).Agrees(StatementOf(b, "", WriterPolicy)); !got {
		t.Error("Agrees must answer as SameStatedVolume does")
	}
}

// Label is injective over what Agrees compares: statements that disagree never share a
// label.
func TestLabelSeparatesWhatAgreesSeparates(t *testing.T) {
	for _, c := range []struct {
		title string
		want  string // "" = no label
	}{
		{"Wildwood (Season One)", "1"},
		{"Wildwood (Part One, Episode 2)", "2 [1/2]"},
		{"Wildwood (Part 1, Episode 2)", "1 [1/2]"},
		{"Wildwood (Books 1-3)", "[1]"},
		{"Wildwood", ""},
	} {
		got, ok := StatementOf(c.title, "", ReaderPolicy).Label()
		if got != c.want || ok != (c.want != "") {
			t.Errorf("Label(%q) = (%q, %v), want %q", c.title, got, ok, c.want)
		}
	}
}

// StatementOf/Agrees is SameStatedVolume split in two, so a caller comparing many
// pairs derives each title once; the two spellings must answer alike.
func TestStatementAgreesIsSameStatedVolume(t *testing.T) {
	titles := []string{
		"Wildwood (Season One)", "Wildwood (Season 2)", "Wildwood (Part One, Episode 2)",
		"Wildwood (Part 1, Episode 2)", "Wildwood (Books 1-3)", "Wildwood", "Level 1 Lessons 1-5",
		"Level 1 Lessons 6-10", "Book Two, Part V",
	}
	for _, a := range titles {
		for _, b := range titles {
			for _, p := range []VolumePolicy{ReaderPolicy, WriterPolicy} {
				if got, want := StatementOf(a, "", p).Agrees(StatementOf(b, "", p)), SameStatedVolume(a, "", b, "", p); got != want {
					t.Errorf("%q vs %q: Agrees %v, SameStatedVolume %v", a, b, got, want)
				}
			}
		}
	}
}

// A title can NEST division markers, and then the first number is not the whole
// statement. The measured population is the Pimsleur courses: 36 units of one course
// agreed on "Level 1" and differed only in their lessons, so a single-number
// comparison read them as 36 records of one book.
func TestSameStatedVolumeComparesNestedDivisions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"Pimsleur Albanian: Level 1 Lessons 1-5", "Pimsleur Albanian: Level 1 Lessons 6-10", false},
		{"Pimsleur Albanian: Level 1 Lessons 1-5", "Pimsleur Albanian: Level 2 Lessons 1-5", false},
		{"Pimsleur Albanian: Level 1 Lessons 1-5", "Pimsleur Albanian: Level 1 Lessons 1-5", true},
		// One side stating nothing is never a disagreement - the duplicate the gates
		// exist for.
		{"Hammered: The Iron Druid Chronicles, Book 3", "Hammered", true},
		// A nested sequence numbered in WORDS is read like any other, and the two
		// spellings of one sequence agree.
		{"Ranger's Apprentice: Part One, Episode 2", "Ranger's Apprentice: Part One, Episode 3", false},
		{"Wildwood, Part One", "Wildwood, Part 1", true},
		{"Ranger's Apprentice: Part Two, Episode 2", "Ranger's Apprentice: Part One, Episode 2", false},
	} {
		if got := SameStatedVolume(c.a, "", c.b, "", ReaderPolicy); got != c.same {
			t.Errorf("SameStatedVolume(%q, %q, ReaderPolicy) = %v, want %v", c.a, c.b, got, c.same)
		}
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
		if got := SameStatedVolume(c.a, c.sa, c.b, c.sb, ReaderPolicy); got != c.want {
			t.Errorf("SameStatedVolume(%q, %q, ReaderPolicy) = %v, want %v", c.a, c.b, got, c.want)
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
			name:  "an edition marker strips safely",
			title: "Two Ravens (Unabridged)", want: "Two Ravens",
		},
		{
			name:  "a series-and-volume tail strips safely",
			title: "Hammered: The Iron Druid Chronicles, Book 3", series: "The Iron Druid Chronicles",
			want: "Hammered",
		},
		{
			name:  "an undecorated title has nothing to strip",
			title: "Hounded", refusal: RefuseNothingToStrip,
		},
		{
			name:  "a residual that names no book is refused",
			title: "Omnibus: A LitRPG Adventure", refusal: RefuseNoIdentity,
		},
		{
			name:  "a residual that reads as a fragment is refused",
			title: "and the Great Escape: A Dark Fantasy Adventure", refusal: RefuseFragment,
		},
		{
			name:  "a title that IS its series' name is left alone",
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

// SameTitleUnderCommonSeries is the soundness condition on a key equality reached by
// shedding a DIFFERENT series name on each side. Every FALSE case here is a merge a
// live wave proposed non-advisory; every TRUE case is a merge that must keep working,
// which is what makes the rule the weakest one that separates them.
func TestSameTitleUnderCommonSeries(t *testing.T) {
	cases := []struct {
		name         string
		titleA, serA string
		titleB, serB string
		same         bool
	}{
		{
			// The two wrong merges that made this rule. Both shed their LEADING segment -
			// the subject the title is about - and meet on the publisher's template.
			name:   "a template residual left by two different subjects",
			titleA: "Cold War: A History from Beginning to End", serA: "Cold War",
			titleB: "The Hundred Years War: A History from Beginning to End", serB: "The Hundred Years War",
		},
		{
			name:   "the same template in German",
			titleA: "Edgar Allan Poe - Kurzbiografie kompakt", serA: "Edgar Allan Poe",
			titleB: "George Washington - Kurzbiografie kompakt", serB: "George Washington",
		},
		{
			// And the TRAILING half of the shape: the volume is what was shed, so what is
			// left is the imprint two different books share.
			name:   "a shared imprint left by two different volumes",
			titleA: "Ladybird Audio Adventures: Outer Space", serA: "Outer Space",
			titleB: "Ladybird Audio Adventures: The Frozen World", serB: "The Frozen World",
		},
		{
			name:   "two different novels under one detective's name",
			titleA: "Sherlock Holmes: Gods of War", serA: "Gods of War",
			titleB: "Sherlock Holmes: The Devil's Dust", serB: "Devil's Dust",
		},
		{
			// THE CALIBRATION PAIR. One side sheds a series-and-volume tail and the other
			// sheds nothing, and they agree under the decorated side's own name - which is
			// the whole duplicate class this key exists to find.
			name:   "a decorated title and its plain twin",
			titleA: "Hammered: The Iron Druid Chronicles, Book 3", serA: "The Iron Druid Chronicles",
			titleB: "Hammered", serB: "",
			same: true,
		},
		{
			// The LEADING strip working correctly: the residual is the book's own title.
			name:   "a leading series segment over a title that carries identity",
			titleA: "The Last Apprentice: Curse of the Bane", serA: "The Last Apprentice",
			titleB: "Curse of the Bane", serB: "The Last Apprentice",
			same: true,
		},
		{
			// Equal names are the short circuit: one strip applied to both, so the
			// caller's own key equality has already said everything this could - including
			// for a residual that carries no identity of its own, which internal/audit
			// keys by its series as well.
			name:   "one series name, an identity-less residual",
			titleA: "La Guerra de los Cielos: Volumen 2 [The War of the Skies]", serA: "La Guerra de los Cielos",
			titleB: "La Guerra de los Cielos: Volumen 2 [War in the Heavens, Vol. 2]", serB: "La Guerra de los Cielos",
			same: true,
		},
		{
			// Neither side was read against a name at all, which is the same short
			// circuit: there was no strip to disagree about.
			name:   "no series name on either side",
			titleA: "Inferno", serA: "", titleB: "Inferno (Unabridged)", serB: "",
			same: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SameTitleUnderCommonSeries(c.titleA, c.serA, c.titleB, c.serB); got != c.same {
				t.Errorf("SameTitleUnderCommonSeries(%q/%q, %q/%q) = %v, want %v",
					c.titleA, c.serA, c.titleB, c.serB, got, c.same)
			}
			// Symmetric: which side a caller passes first is not evidence.
			if got := SameTitleUnderCommonSeries(c.titleB, c.serB, c.titleA, c.serA); got != c.same {
				t.Errorf("reversed = %v, want %v", got, c.same)
			}
			// And the pair really does meet on the key, or the rule would be judging a
			// comparison nobody makes.
			ka, kb := CompareKey(Clean(c.titleA, c.serA)), CompareKey(Clean(c.titleB, c.serB))
			if ka == "" || ka != kb {
				t.Fatalf("keys %q and %q do not meet: this case tests nothing", ka, kb)
			}
		})
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
