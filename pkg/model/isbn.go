package model

import (
	"strconv"
	"strings"
)

// ValidISBN13 reports whether s is a checksum-valid 13-digit Bookland ISBN: a
// 978/979 prefix and a mod-10 weighted check digit. It is the module's one
// spelling of that rule - pkg/extract's epub metadata normalization and
// internal/serve's purchase-link derivation both judge by it. Deliberately a
// different question from importer.NormalizeISBN, which accepts any 10- or
// 13-character shape a source states without verifying the checksum: acceptance
// records what a source said, this rule says whether the value can be a real
// ISBN-13 at all.
func ValidISBN13(s string) bool {
	if len(s) != 13 || (!strings.HasPrefix(s, "978") && !strings.HasPrefix(s, "979")) {
		return false
	}
	sum, ok := isbn13Sum(s)
	return ok && sum%10 == 0
}

// ValidISBN10 reports whether s is a checksum-valid ISBN-10, whose final character
// may be X or x for the value 10.
func ValidISBN10(s string) bool {
	if len(s) != 10 {
		return false
	}
	sum, ok := isbn10Sum(s)
	return ok && sum%11 == 0
}

// isbn13Sum is the alternating 1 and 3 weighted sum of s, which must be digits. A
// full ISBN-13's is a multiple of 10; its first twelve digits' give the check digit.
func isbn13Sum(s string) (sum int, ok bool) {
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		d := int(c - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return sum, true
}

// isbn10Sum is the 10-down-to-1 weighted sum of s, digits with X or x allowed only
// as a tenth character. A full ISBN-10's is a multiple of 11; its first nine
// characters' give the check digit.
func isbn10Sum(s string) (sum int, ok bool) {
	for i := range len(s) {
		c := s[i]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case (c == 'X' || c == 'x') && i == 9:
			d = 10
		default:
			return 0, false
		}
		sum += d * (10 - i)
	}
	return sum, true
}

// ISBNSpellings returns the upper-cased spellings of one ISBN: itself, plus the
// same number in the other form when there is one. Only a 978 ISBN-13 has an
// ISBN-10. The uniqueness rule compares values, so this is for the one question
// that must not turn on the spelling: is this a work's print ISBN? A value that is
// not an ISBN's shape has one spelling, itself.
func ISBNSpellings(isbn string) []string {
	isbn = strings.ToUpper(isbn)
	out := []string{isbn}
	switch {
	case len(isbn) == 10:
		if sum, ok := isbn13Sum("978" + isbn[:9]); ok {
			out = append(out, "978"+isbn[:9]+strconv.Itoa((10-sum%10)%10))
		}
	case len(isbn) == 13 && strings.HasPrefix(isbn, "978"):
		if sum, ok := isbn10Sum(isbn[3:12]); ok {
			check := "X"
			if d := (11 - sum%11) % 11; d != 10 {
				check = strconv.Itoa(d)
			}
			out = append(out, isbn[3:12]+check)
		}
	}
	return out
}

// PrintISBNKeys returns every spelling of the work's print ISBNs (xref.isbn), the
// values no recording may carry: lookup?isbn= asks recording_isbns first, so a
// recording holding one would answer a print ISBN as an audiobook edition. The
// importer and the issue forms refuse by it, and pkg/check enforces it on the tree.
func (w *Work) PrintISBNKeys() []string {
	if w.Xref == nil {
		return nil
	}
	var keys []string
	for _, isbn := range w.Xref.ISBN {
		keys = append(keys, ISBNSpellings(isbn)...)
	}
	return keys
}
