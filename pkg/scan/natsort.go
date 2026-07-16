package scan

import "strings"

// NaturalLess compares two strings with numeric-aware ("natural") ordering:
// maximal runs of digits are compared by value (leading zeros ignored, so "02"
// == "2" in value; a shorter significant-digit run therefore sorts before a
// longer one, e.g. "Chapter 2" < "Chapter 10"), and non-digit runs compare
// case-insensitively. When two names are otherwise equal under that compare it
// falls back to a raw byte compare (so "02" and "2" keep a deterministic order
// and case differences break ties), making the ordering total and stable.
//
// This is why a folder book's Files array orders "Chapter 2.mp3" before
// "Chapter 10.mp3" even though a plain byte sort would not - very common for
// unpadded chapter names. Folder-book file/chapter enumeration order is built
// directly on it.
//
// NaturalLess is part of pkg/scan's PUBLIC contract precisely so consumers - in
// particular the sibling audiosilo-sidecars module's audio split - share this
// ONE implementation instead of copying it. Downstream, that enumeration order
// determines chapter numbers, which spoiler-gate community sidecars
// (position.chapter), so a tie-break divergence between two copies would
// silently misalign spoiler positions with no test catching it. Treat any
// change to the ordering as a breaking change to chapter numbering: never make
// one casually, and keep every consumer on this exported function.
func NaturalLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	ia, ib := 0, 0
	for ia < len(la) && ib < len(lb) {
		da, db := isDigit(la[ia]), isDigit(lb[ib])
		if da && db {
			ra, ea := digitRun(la, ia)
			rb, eb := digitRun(lb, ib)
			if len(ra) != len(rb) {
				return len(ra) < len(rb) // fewer significant digits = smaller value
			}
			if ra != rb {
				return ra < rb
			}
			ia, ib = ea, eb
			continue
		}
		if la[ia] != lb[ib] {
			return la[ia] < lb[ib]
		}
		ia++
		ib++
	}
	switch {
	case ia == len(la) && ib < len(lb):
		return true // a is a prefix of b
	case ib == len(lb) && ia < len(la):
		return false // b is a prefix of a
	default:
		return a < b // equal under the natural compare: raw tiebreak (e.g. case)
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// digitRun returns the significant digits (leading zeros stripped) of the digit
// run starting at i, and the index just past it. An all-zero run yields "".
func digitRun(s string, i int) (significant string, end int) {
	start := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return strings.TrimLeft(s[start:i], "0"), i
}
