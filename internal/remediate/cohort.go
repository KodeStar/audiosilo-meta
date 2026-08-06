package remediate

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// cohort.go decides what this remediation is ABOUT: the GraphicAudio
// multi-part dramatized-adaptation products a user-library import seeded as
// separate works, one per part.
//
// Every rule here is measured against the live tree rather than assumed. The
// numbers in the comments come from the 133,706-work catalogue as of
// 2026-08-06 and are what the regexes and vocabularies are shaped to.

// graphicAudioPublishers is the closed set of spellings the catalogue records
// GraphicAudio's imprint under. Measured over every work whose title carries a
// part marker: 284 recordings say "GraphicAudio", 95 say "Graphic Audio LLC",
// 1 says "Graphic Audio". Nothing else publishes a part-marked GraphicAudio
// product, and the gate is what keeps the rules off the OTHER publishers whose
// titles happen to spell a volume as "N of M" (Bradford M. Smith's Riftborn
// Chronicles, J.V. Nolan's Xyania Project, Craig Halloran's Dragon Wars - real,
// separate books that must never be merged).
var graphicAudioPublishers = map[string]bool{
	"GraphicAudio":      true,
	"Graphic Audio LLC": true,
	"Graphic Audio":     true,
}

// partMarkers are the title forms GraphicAudio's part products are recorded
// with. Measured over the whole catalogue, the parenthesized marker has four
// spellings and there is exactly one unparenthesized one:
//
//	(N of M)        290 works   "The Blood Mirror (2 of 2) [Dramatized Adaptation]"
//	(Part N of M)    93 works   "Morning Star (Part 1 of 2) (Dramatized Adaptation)"
//	( N of M)         3 works   "The Broken Eye ( 1 of 3) [Dramatized Adaptation]" - a stray space
//	, Vol. N of M     2 works   "The Earth Died Screaming, Vol. 1 of 2 (Dramatized Adaptation)"
//
// The "(Book N of M)" form is deliberately NOT here: its single occurrence is
// Lone Ghost Publishing's "Blood Stained: The Legend of Andrew Rufus (Book 3 of
// 7)", a series volume rather than a part of one production. The publisher gate
// would reject it anyway; leaving the form out says why.
var partMarkers = []*regexp.Regexp{
	regexp.MustCompile(`\s*\(\s*(?:Part\s+)?([0-9]+)\s+of\s+([0-9]+)\s*\)`),
	regexp.MustCompile(`,?\s*Vol\.\s*([0-9]+)\s+of\s+([0-9]+)`),
}

// dramatizedMarker is the edition marker a dramatization's title carries, in
// both bracket styles and both spellings, with or without the word
// "Adaptation". It is stripped from a title before the title is used as an
// identity, so "Black Prism (1 of 3) [Dramatized Adaptation]" and the
// complete-set product "Black Prism [Dramatized Adaptation]" resolve to one
// base title.
//
// NOT every part product carries it - "Oathbringer (1 of 6)" and "The Way of
// Kings (3 of 5)" do not - which is exactly why the publisher, and not the
// marker, is the cohort gate.
var dramatizedMarker = regexp.MustCompile(`\s*[\[(]Dramati[sz]ed(?:\s+Adaptation)?[\])]`)

// whitespaceRun collapses the gap a stripped marker leaves mid-title.
var whitespaceRun = regexp.MustCompile(`\s+`)

// partRef is the part a title states: "(2 of 5)" is part 2 of a 5-part set.
type partRef struct {
	Num   int
	Total int
}

// partOf reads the part marker out of a title. ok is false for a title that
// states none, which is every whole-book product.
func partOf(title string) (partRef, bool) {
	for _, re := range partMarkers {
		m := re.FindStringSubmatch(title)
		if m == nil {
			continue
		}
		num, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		total, err := strconv.Atoi(m[2])
		if err != nil || num < 1 || total < 1 || num > total {
			continue
		}
		return partRef{Num: num, Total: total}, true
	}
	return partRef{}, false
}

// baseTitle strips the part marker and the dramatized edition marker, leaving
// the title of the BOOK. It is the identity half of a part group's key (the
// author set is the other half).
func baseTitle(title string) string {
	for _, re := range partMarkers {
		if re.MatchString(title) {
			title = re.ReplaceAllString(title, "")
			break
		}
	}
	title = dramatizedMarker.ReplaceAllString(title, "")
	return strings.TrimSpace(whitespaceRun.ReplaceAllString(title, " "))
}

// isDramatized reports whether a title carries the edition marker.
func isDramatized(title string) bool { return dramatizedMarker.MatchString(title) }

// leadingArticles are the article prefixes a title-derived slug is compared
// modulo, so "Stone of Farewell" and "The Stone of Farewell" meet. Audible
// records the same book both ways and the plain edition is routinely the one
// carrying the article the dramatization drops.
var leadingArticles = []string{"the-", "an-", "a-"}

// titleKey is the article-tolerant identity a title is matched by: its slug
// with a leading article removed. It is deliberately NOT an identity anything
// is STORED under - only a matching key - so shortening it cannot mint an
// address.
func titleKey(title string) string {
	slug := model.Slugify(title)
	for _, art := range leadingArticles {
		if strings.HasPrefix(slug, art) {
			return slug[len(art):]
		}
	}
	return slug
}

// authorsKey renders an author set as one comparable string. The set, not the
// order: two records of one book may list its authors either way round.
func authorsKey(authors []string) string {
	sorted := append([]string(nil), authors...)
	sortStrings(sorted)
	return strings.Join(sorted, "\x00")
}

// dramatizedSeries reports whether a series NAME says it is the dramatized
// edition's series. Such a series parks a book's parts at decimal positions
// ("1.1".."1.5"); a plain-named series has had its numeric slots taken by them.
func dramatizedSeries(name string) bool { return isDramatized(name) }
