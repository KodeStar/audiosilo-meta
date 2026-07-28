package extract

import "testing"

func TestChapterFromLabelStrict(t *testing.T) {
	cases := []struct {
		label string
		num   int
		title string
	}{
		// Bare and prefixed digits.
		{"7", 7, ""},
		{"007", 7, ""},
		{"Chapter 7", 7, ""},
		{"CHAPTER 7", 7, ""},
		// Digits with each observed separator.
		{"1. I Get Flushed", 1, "I Get Flushed"},
		{"1: I Battle The Cheerleading Squad", 1, "I Battle The Cheerleading Squad"},
		{"2 - Grover Gets Caffeinated", 2, "Grover Gets Caffeinated"},
		{"3 • My Girlfriend Takes Me to the Graveyard", 3, "My Girlfriend Takes Me to the Graveyard"},
		{"4) The Preparation", 4, "The Preparation"},
		{"Chapter 12: The Reckoning", 12, "The Reckoning"},
		// Spelled-out numbers, including a hyphenated compound and dot leaders.
		{"Chapter One", 1, ""},
		{"Chapter Twenty-One", 21, ""},
		{"Chapter Twenty One", 21, ""},
		{"Chapter Twenty-One: The Thing", 21, "The Thing"},
		{"Chapter Seven ..........................", 7, ""},
		// Roman numerals behind an explicit "Chapter".
		{"CHAPTER I. MR. SHERLOCK HOLMES.", 1, "MR. SHERLOCK HOLMES"},
		{"CHAPTER IV. The Preparation", 4, "The Preparation"},
		{"Chapter XIV", 14, ""},
	}
	for _, c := range cases {
		n, title, conf := ChapterFromLabel(c.label)
		if conf != Strict {
			t.Errorf("ChapterFromLabel(%q) confidence = %v, want Strict", c.label, conf)
			continue
		}
		if n != c.num || title != c.title {
			t.Errorf("ChapterFromLabel(%q) = (%d, %q), want (%d, %q)", c.label, n, title, c.num, c.title)
		}
	}
}

func TestChapterFromLabelLoose(t *testing.T) {
	cases := []struct {
		label string
		num   int
		title string
	}{
		{"I An Irate Neighbor", 1, "An Irate Neighbor"},
		{"II Selling in Haste", 2, "Selling in Haste"},
		{"I. Silver Blaze", 1, "Silver Blaze"},
		{"III.", 3, ""},
		{"1 I ACCIDENTALLY VAPORIZE", 1, "I ACCIDENTALLY VAPORIZE"},
		{"2 THREE OLD LADIES KNIT", 2, "THREE OLD LADIES KNIT"},
		{"He rode a black horse. CHAPTER III.", 3, ""},
		{"I hope Mr. Bingley will like it. CHAPTER II.", 2, ""},
	}
	for _, c := range cases {
		n, title, conf := ChapterFromLabel(c.label)
		if conf != Loose {
			t.Errorf("ChapterFromLabel(%q) confidence = %v, want Loose", c.label, conf)
			continue
		}
		if n != c.num || title != c.title {
			t.Errorf("ChapterFromLabel(%q) = (%d, %q), want (%d, %q)", c.label, n, title, c.num, c.title)
		}
	}
}

// TestChapterFromLabelRefuses pins the labels that must NOT yield a chapter
// number. Every entry here is a real failure mode: a structural divider read as a
// chapter collides with the real chapter of that number and shifts every spoiler
// position after it, and an English word read as a Roman numeral invents a chapter
// out of a title.
func TestChapterFromLabelRefuses(t *testing.T) {
	labels := []string{
		// Front and back matter.
		"Cover", "Title Page", "Contents", "Copyright", "Dedication",
		"About the Author", "By the Same Author", "Acknowledgements",
		// Unnumbered story divisions.
		"Prologue", "Epilogue", "Introduction",
		// Structural dividers: refused outright, never read as a chapter number.
		"Part I", "Part Two", "PART II. The Country of the Saints.",
		"Book the First—Recalled to Life", "Volume 1", "ACT I", "SCENE II",
		// Titled-only chapters (the short-story-collection shape).
		"The Golden Age of Cannibalism", "Perseus Wants a Hug",
		"THE ADVENTURE OF THE EMPTY HOUSE",
		// English words that parse as large Roman numerals if left unbounded.
		"MIX", "DID", "DIM", "MILD", "LID",
		// Prose that begins with the word "I". Reading the leading "I" as the
		// numeral 1 invents a chapter out of a teaser sentence - a real shape in
		// the Project Gutenberg Pride and Prejudice toc.
		"I hope Mr. Bingley will like it",
		"I could not have parted with you to anyone less worthy",
		// Non-canonical Roman spellings.
		"Chapter IIII", "Chapter VV",
		// A year in a title must not become a chapter number.
		"1984 Was Different",
		"",
	}
	for _, l := range labels {
		if n, _, conf := ChapterFromLabel(l); conf != NotAChapter {
			t.Errorf("ChapterFromLabel(%q) = (%d, %v), want NotAChapter", l, n, conf)
		}
	}
}

func TestContiguous(t *testing.T) {
	cases := []struct {
		name string
		nums []int
		want bool
	}{
		{"one-based run", []int{1, 2, 3, 4, 5}, true},
		{"zero-based run", []int{0, 1, 2, 3}, true},
		{"out of order but complete", []int{3, 1, 2, 5, 4}, true},
		{"gap", []int{1, 2, 4, 5}, false},
		{"duplicate", []int{1, 2, 2, 3}, false},
		{"does not start at 0 or 1", []int{5, 6, 7}, false},
		{"too short to trust", []int{1, 2}, false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Contiguous(c.nums); got != c.want {
				t.Errorf("Contiguous(%v) = %v, want %v", c.nums, got, c.want)
			}
		})
	}
}

func TestRomanRoundTripRejectsNonCanonical(t *testing.T) {
	for _, s := range []string{"IIII", "VV", "IL", "XXXX", "IC"} {
		if n, ok := romanToNumber(s); ok {
			t.Errorf("romanToNumber(%q) = %d, want rejected (non-canonical)", s, n)
		}
	}
	for n := 1; n <= maxRomanChapter; n++ {
		r := numberToRoman(n)
		got, ok := romanToNumber(r)
		if !ok || got != n {
			t.Errorf("round trip %d -> %q -> (%d, %v)", n, r, got, ok)
		}
	}
}

func TestLeadingNumberWords(t *testing.T) {
	cases := []struct {
		in   string
		num  int
		rest string
		ok   bool
	}{
		{"One", 1, "", true},
		{"Twenty-One: The Thing", 21, ": The Thing", true},
		{"Twenty One - The Thing", 21, " - The Thing", true},
		{"Seven ....", 7, " ....", true},
		{"The Reckoning", 0, "", false},
		{"", 0, "", false},
	}
	for _, c := range cases {
		n, rest, ok := leadingNumberWords(c.in)
		if ok != c.ok || (ok && (n != c.num || rest != c.rest)) {
			t.Errorf("leadingNumberWords(%q) = (%d, %q, %v), want (%d, %q, %v)",
				c.in, n, rest, ok, c.num, c.rest, c.ok)
		}
	}
}
