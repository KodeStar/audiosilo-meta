package titlerule

import "testing"

func TestVolumeHeadOf(t *testing.T) {
	cases := []struct {
		title        string
		ok           bool
		head, marker string
		vol          float64
		tail         string
	}{
		{"Our Vietnam Wars, Volume 2", true, "Our Vietnam Wars", "volume", 2, ""},
		{"Medical Mysteries Across History, Pt.2", true, "Medical Mysteries Across History", "pt", 2, ""},
		{"I'm Sorry I Haven't A Clue Live: Volume 2", true, "I'm Sorry I Haven't A Clue Live", "volume", 2, ""},
		{"The Shadow Weaver, Book 2", true, "The Shadow Weaver", "book", 2, ""},
		{"Hellmervick, Book Two: The Black Forest", true, "Hellmervick", "book", 2, "The Black Forest"},
		{"Sevenfold Sword: Part V", true, "Sevenfold Sword", "part", 5, ""},
		{"Wildwood (Book One)", true, "Wildwood", "book", 1, ""},
		// The earliest marker wins, whichever spelling it is in.
		{"Part Two, Book 5", true, "", "part", 2, "Book 5"},
		// A composite word number states nothing; a surname is not a marker.
		{"Book One Hundred", false, "", "", 0, ""},
		{"Partone Rising", false, "", "", 0, ""},
		{"Our Vietnam Wars", false, "", "", 0, ""},
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
		if h.Head != c.head || h.Marker != c.marker || h.Volume != c.vol || h.Tail != c.tail {
			t.Errorf("VolumeHeadOf(%q) = %+v, want head %q marker %q vol %v tail %q", c.title, h, c.head, c.marker, c.vol, c.tail)
		}
	}
}

func TestIsPartMarker(t *testing.T) {
	for _, m := range []string{"volume", "Vol", "vols", "part", "Pt", "pts"} {
		if !IsPartMarker(m) {
			t.Errorf("IsPartMarker(%q) = false, want true", m)
		}
	}
	// "Book N" is the retailer's series-position convention, and the non-English
	// markers are series positions far more often than parts.
	for _, m := range []string{"book", "bk", "episode", "band", "tome", "teil", "libro"} {
		if IsPartMarker(m) {
			t.Errorf("IsPartMarker(%q) = true, want false", m)
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

func TestIsSeriesTieIn(t *testing.T) {
	yes := []string{"Tangled: The Series", "Batman - The Animated Series", "Outlander (The TV Series)"}
	no := []string{
		"Tangled",
		"Endangered: Zak Bates Eco-Adventure Series, Book 2",
		"The Series of Unfortunate Events",
		"Discworld Series",
	}
	for _, s := range yes {
		if !IsSeriesTieIn(s) {
			t.Errorf("IsSeriesTieIn(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsSeriesTieIn(s) {
			t.Errorf("IsSeriesTieIn(%q) = true, want false", s)
		}
	}
}

// The "N in 1" bundle announcement carries no collection WORD, so it is a phrase arm of
// its own - and "in one" is read only after "books".
func TestIsCollectionReadsTheNInOneBundle(t *testing.T) {
	yes := []string{
		"Rapid Extreme Weight Loss Hypnosis for Women (2 in 1)",
		"Adult ADHD Toolkit: 3-in-1",
		"Cryptocurrency Bible: 3 Books in 1",
		"Stoicism: 3 Books in One",
		"Grow Your Confidence: Two Books in One",
		"Small Talk [5-in-1]",
	}
	no := []string{
		"6 In One Head and Out the Other",
		"Chapter 21 - Three in One",
		"Rapid Extreme Weight Loss Hypnosis for Women",
		"12 in 1 Night",
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
