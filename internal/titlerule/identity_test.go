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
		"Unintended Cultivator: Volume 3", "Unintended Cultivator") {
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
		got, ok := StatedVolume(c.title, c.series)
		if !ok || got != c.want {
			t.Errorf("StatedVolume(%q) = (%v, %v), want %v", c.title, got, ok, c.want)
		}
	}
	// The pair that must therefore disagree - the whole point of reading them.
	if SameStatedVolume("The Inn: Season 1", "The Inn", "The Inn: Season 2", "The Inn") {
		t.Error("two seasons must state different volumes")
	}
	if SameStatedVolume("Paladin: Volume I", "Paladin", "Paladin: Volume II", "Paladin") {
		t.Error("two roman-numbered volumes must state different volumes")
	}
	// And an ordinary title states nothing, so it is not accidentally a volume.
	if _, ok := StatedVolume("A Season for Ravens", ""); ok {
		t.Error("an ordinary title must not read as a stated volume")
	}
}

// The third spelling BareSeq cannot read: a WORD volume number. The key loses it in
// exactly two shapes - inside a decorative bracket group, and left dangling by the
// series strip - and both collapsed a serial onto a sibling until StatedVolume read
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
		got, ok := StatedVolume(c.title, c.series)
		if c.want == 0 {
			if ok {
				t.Errorf("StatedVolume(%q) = (%v, true), want no statement", c.title, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("StatedVolume(%q) = (%v, %v), want %v", c.title, got, ok, c.want)
		}
	}
	// The pair that must therefore disagree - the whole point of reading them.
	if SameStatedVolume("Wildwood (Book One)", "", "Wildwood (Book Two)", "") {
		t.Error("two word-numbered volumes must state different volumes")
	}
	// One spelling never contradicts the other: they are one vocabulary.
	if !SameStatedVolume("Wildwood (Book Two)", "", "Wildwood (Book 2)", "") {
		t.Error("the same volume spelled two ways must not read as a contradiction")
	}
	// A word that merely FOLLOWS a marker is not a number, and a marker welded into a
	// word is not a marker: the whitespace in wordVolume is what says so.
	for _, title := range []string{
		"Parts Unknown", "The Book Thief", "Book of One Thousand Nights",
		"The Chosen One", "The Partone Affair", "Book Thirteen",
	} {
		if v, ok := StatedVolume(title, ""); ok {
			t.Errorf("StatedVolume(%q) = (%v, true), want no statement", title, v)
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
		if got, ok := StatedVolume(c.title, c.series); !ok || got != c.want {
			t.Errorf("StatedVolume(%q) = (%v, %v), want %v", c.title, got, ok, c.want)
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
		if got := SameStatedVolume(c.a, "", c.b, ""); got != c.same {
			t.Errorf("SameStatedVolume(%q, %q) = %v, want %v", c.a, c.b, got, c.same)
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
