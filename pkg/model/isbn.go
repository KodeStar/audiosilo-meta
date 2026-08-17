package model

import "strings"

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
	sum := 0
	for i := range 13 {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return sum%10 == 0
}
