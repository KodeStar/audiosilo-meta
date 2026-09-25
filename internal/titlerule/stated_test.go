package titlerule

import "testing"

func TestVolumeHeadOf(t *testing.T) {
	cases := []struct {
		title        string
		ok           bool
		head, marker string
		vol          float64
	}{
		{"Our Vietnam Wars, Volume 2", true, "Our Vietnam Wars", "volume", 2},
		{"Medical Mysteries Across History, Pt.2", true, "Medical Mysteries Across History", "pt", 2},
		{"I'm Sorry I Haven't A Clue Live: Volume 2", true, "I'm Sorry I Haven't A Clue Live", "volume", 2},
		{"The Shadow Weaver, Book 2", true, "The Shadow Weaver", "book", 2},
		{"Hellmervick, Book Two: The Black Forest", true, "Hellmervick", "book", 2},
		{"Sevenfold Sword: Part V", true, "Sevenfold Sword", "part", 5},
		{"Wildwood (Book One)", true, "Wildwood", "book", 1},
		// The roman arm takes a dot separator, as romanVolume does.
		{"Legends, Vol.II", true, "Legends", "vol", 2},
		{"Legends Part.II", true, "Legends", "part", 2},
		// A DIGIT marker outranks an earlier word one, as in the volume statements.
		{"Part Two, Book 5", true, "Part Two", "book", 5},
		// An unreadable word marker is passed over for a later readable one.
		{"Book One Hundred, Part Two", true, "Book One Hundred", "part", 2},
		// A composite word number states nothing; a surname is not a marker; a season is
		// a division, not a volume of the title before it.
		{"Book One Hundred", false, "", "", 0},
		{"Partone Rising", false, "", "", 0},
		{"Our Vietnam Wars", false, "", "", 0},
		{"Wildwood: Season 2", false, "", "", 0},
	}
	for _, c := range cases {
		h, ok := VolumeHeadOf(c.title)
		if ok != c.ok {
			t.Errorf("VolumeHeadOf(%q) ok = %v, want %v", c.title, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if h.Head != c.head || h.Marker != c.marker || h.Volume != c.vol {
			t.Errorf("VolumeHeadOf(%q) = %+v, want head %q marker %q vol %v", c.title, h, c.head, c.marker, c.vol)
		}
		// The head reads exactly what the volume statements read.
		if st := StatementOf(c.title, "", ReaderPolicy); !st.States || st.Volume != h.Volume {
			t.Errorf("VolumeHeadOf(%q) read %v, StatementOf read %+v", c.title, h.Volume, st)
		}
	}
}

func TestVolumeHeadIsPart(t *testing.T) {
	for _, title := range []string{"X, Volume 2", "X Vol. 2", "X, Vols II", "X, Part 2", "X, Pt.2", "X, Parts Two"} {
		if h, ok := VolumeHeadOf(title); !ok || !h.IsPart() {
			t.Errorf("VolumeHeadOf(%q).IsPart() = false, want true", title)
		}
	}
	// "Book N" is the retailer's series-position convention, and the non-English
	// markers are series positions far more often than parts.
	for _, title := range []string{"X, Book 2", "X, Bk 2", "X, Episode 2", "X, Band Two", "X, Tome Two"} {
		if h, ok := VolumeHeadOf(title); !ok || h.IsPart() {
			t.Errorf("VolumeHeadOf(%q).IsPart() = true (or unread), want false", title)
		}
	}
}

func TestVolumeHeadRestatesVolume(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"Z-Burbia 2: Parkway To Hell, Volume 2", true},
		{"Z-Burbia Two: Parkway To Hell, Volume 2", true},
		{"Rocky II: Round Two, Part 2", true},
		// A number that is not the segment's last token restates nothing.
		{"2 States, Part 2", false},
		{"Z-Burbia 3: Parkway To Hell, Volume 2", false},
		{"Our Vietnam Wars, Volume 2", false},
	}
	for _, c := range cases {
		h, ok := VolumeHeadOf(c.title)
		if !ok {
			t.Fatalf("VolumeHeadOf(%q) read nothing", c.title)
		}
		if got := h.RestatesVolume(); got != c.want {
			t.Errorf("VolumeHeadOf(%q).RestatesVolume() = %v, want %v", c.title, got, c.want)
		}
	}
}

func TestEditionOrdinal(t *testing.T) {
	cases := []struct {
		title string
		n     int
		ok    bool
	}{
		{"Security Analysis (Sixth Edition)", 6, true},
		{"Security Analysis (Seventh Edition)", 7, true},
		{"Security Analysis, Seventh ed.", 7, true},
		{"Building Wealth One House at a Time (Revised and Expanded Third Edition)", 3, true},
		{"Building Wealth One House at a Time (Updated and Expanded, Second Edition)", 2, true},
		{"Principles of Economics, 7th ed.", 7, true},
		{"Linear Algebra, 2nd Revised Edition", 2, true},
		// An anniversary re-release is not a numbered revision.
		{"Fifteen Dogs (Tenth Anniversary Edition)", 0, false},
		{"Emma (AmazonClassics Edition)", 0, false},
		{"The Second Coming", 0, false},
	}
	for _, c := range cases {
		n, ok := EditionOrdinal(c.title)
		if n != c.n || ok != c.ok {
			t.Errorf("EditionOrdinal(%q) = %d, %v; want %d, %v", c.title, n, ok, c.n, c.ok)
		}
	}
}

func TestIsYoungReadersAdaptation(t *testing.T) {
	yes := []string{
		"Notes from a Young Black Chef (Adapted for Young Adults)",
		"The Boys in the Boat (Young Readers Adaptation)",
		"The Da Vinci Code (The Young Adult Adaptation)",
		"The First Conspiracy (Young Reader's Edition)",
		"The Lemon Tree (Young Readers' Edition)",
		"Alice’s Adventures in Wonderland (Adapted for Children)",
		"Sapiens (Children's Edition)",
	}
	no := []string{
		"Notes from a Young Black Chef",
		"Stoicism for Kids",
		"Letters to a Young Poet",
		"The Woman in White (Dramatized)",
		"A Young Adult Dystopian Novel",
	}
	for _, s := range yes {
		if !IsYoungReadersAdaptation(s) {
			t.Errorf("IsYoungReadersAdaptation(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsYoungReadersAdaptation(s) {
			t.Errorf("IsYoungReadersAdaptation(%q) = true, want false", s)
		}
	}
}

func TestIsSeriesEdition(t *testing.T) {
	yes := []string{"Tangled: The Series", "Batman - The Animated Series", "Outlander (The TV Series)"}
	no := []string{
		"Tangled",
		"Endangered: Zak Bates Eco-Adventure Series, Book 2",
		"The Series of Unfortunate Events",
		"Discworld Series",
		// A whole-series omnibus spelled the same way is IsCollection's shape.
		"Last Light - The Complete Series",
		"Mortal Engines Omnibus - The Series",
	}
	for _, s := range yes {
		if !IsSeriesEdition(s) {
			t.Errorf("IsSeriesEdition(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsSeriesEdition(s) {
			t.Errorf("IsSeriesEdition(%q) = true, want false", s)
		}
	}
}

// The "N in 1" bundle announcement carries no collection WORD, so it is read on its own -
// but only where it announces the product, never as a phrase anywhere in a title.
func TestIsCollectionReadsTheNInOneBundle(t *testing.T) {
	yes := []string{
		"Rapid Extreme Weight Loss Hypnosis for Women (2 in 1)",
		"Adult ADHD Toolkit: 3-in-1",
		"Cryptocurrency Bible: 3 Books in 1",
		"Stoicism: 3 Books in One",
		"Grow Your Confidence: Two Books in One",
		"Small Talk [5-in-1]",
		"Witchcraft: 4 in 1",
		"3 IN 1: Fun Math for Kids",
		"The Social Skills Blueprint 2 in 1",
		"Lean Mastery: 12 Books in 1",
		"ADHD Toolkit 10-in-1",
	}
	no := []string{
		"6 In One Head and Out the Other",
		"Chapter 21 - Three in One",
		"Rapid Extreme Weight Loss Hypnosis for Women",
		"Lose 5 in 1 Month",
		"Chapter 3 in 1 Corinthians",
		"1 in 1 Million",
	}
	for _, s := range yes {
		if !IsCollection(s) {
			t.Errorf("IsCollection(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsCollection(s) {
			t.Errorf("IsCollection(%q) = true, want false", s)
		}
	}
}
