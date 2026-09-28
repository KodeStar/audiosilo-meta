package titlerule

import (
	"regexp"
	"strconv"
)

// product.go is the ONE set of PRODUCT statements a title can make about which
// product of a book it is - one part of a split release, a derived edition (a
// young-readers adaptation or a "<title>: The Series" edition), a collection of
// several books - and the rule every writer applies to them: two records whose
// titles say different things here are not one book, however their titles
// otherwise reduce. pkg/check's identity predicate (the census, the intake gate,
// the importer's create guard) and the importer's attach rule all compare
// ProductOf, so none of them can call one book what another calls two.

// splitPartMarkers are the title forms a SPLIT RELEASE's parts are recorded with,
// each capturing the part number and the part count. Measured over the whole
// catalogue (GraphicAudio's part products, internal/remediate), plus the German
// "Teil N von M" and the square-bracket group a retailer uses as often as the
// round one:
//
//	(N of M)          "The Blood Mirror (2 of 2) [Dramatized Adaptation]"
//	(Part N of M)     "Morning Star (Part 1 of 2) (Dramatized Adaptation)"
//	( N of M)         "The Broken Eye ( 1 of 3) [Dramatized Adaptation]" - a stray space
//	, Vol. N of M     "The Earth Died Screaming, Vol. 1 of 2 (Dramatized Adaptation)"
//	[Teil N von M]
//
// "(Book N of M)" is deliberately NOT a part: it is the retailer's series count
// ("Blood Stained: The Legend of Andrew Rufus (Book 3 of 7)"), a volume rather
// than a piece of one production.
var splitPartMarkers = []*regexp.Regexp{
	regexp.MustCompile(`\s*[(\[]\s*(?:(?:Part|Pt\.?|Teil)\s+)?([0-9]+)\s+(?:of|von)\s+([0-9]+)\s*[)\]]`),
	regexp.MustCompile(`,?\s*Vol\.\s*([0-9]+)\s+of\s+([0-9]+)`),
}

// PartOf reads the split-release part a title states - part num of total - and
// reports whether it states one.
func PartOf(title string) (num, total int, ok bool) {
	for _, re := range splitPartMarkers {
		m := re.FindStringSubmatch(title)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		t, err := strconv.Atoi(m[2])
		if err != nil || n < 1 || t < 1 || n > t {
			continue
		}
		return n, t, true
	}
	return 0, 0, false
}

// StripPartMarker removes the first part marker PartOf reads, or returns the
// title unchanged.
func StripPartMarker(title string) string {
	for _, re := range splitPartMarkers {
		if re.MatchString(title) {
			return re.ReplaceAllString(title, "")
		}
	}
	return title
}

// IsSplitPart reports whether a title names itself one PART of a split release:
// a part count (PartOf), or a volume marker of the part words proper ("Part 2",
// "Pt. 2"). "Volume N" is not one here: it is the commonest series numbering
// there is.
func IsSplitPart(title string) bool {
	if _, _, ok := PartOf(title); ok {
		return true
	}
	h, ok := VolumeHeadOf(title)
	return ok && (h.Marker == "part" || h.Marker == "parts" || h.Marker == "pt" || h.Marker == "pts")
}

// Product is what a record's titles say about which product of the book it is.
// Two Products that differ are two books.
type Product struct {
	Part       bool // one part of a split release (IsSplitPart)
	Adapted    bool // a young-readers adaptation or a "<title>: The Series" edition
	Collection bool // several books in one product (IsCollectionIn)
}

// ProductOf is the statements any of titles makes - a record's title and its
// subtitle, or a row's short title, full title and subtitle - each read against
// series (IsCollectionIn discounts a collection word the series' own name
// carries). A statement any variant makes is the record's: a retailer puts
// "Young Readers Edition" in whichever field it likes.
func ProductOf(series string, titles ...string) Product {
	var p Product
	for _, t := range titles {
		if t == "" {
			continue
		}
		p.Part = p.Part || IsSplitPart(t)
		p.Adapted = p.Adapted || IsYoungReadersAdaptation(t) || IsSeriesEdition(t)
		p.Collection = p.Collection || IsCollectionIn(t, series)
	}
	return p
}

// ComparableKey is a title's comparison identity where retailer decoration is
// removable: StripDecoration against the series name when it proposes a title
// (the gate the add-work form applies), then CompareKeyWhole.
func ComparableKey(title, series string) string {
	if cleaned, _, ok := StripDecoration(title, series); ok {
		title = cleaned
	}
	return CompareKeyWhole(title)
}
