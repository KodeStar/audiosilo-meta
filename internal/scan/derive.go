package scan

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// derived holds the fields a book's path (folder hierarchy + name) can yield,
// each with its provenance ("path" for a folder name/ancestor, "filename" for a
// loose file's name). An empty value means "not derivable" (omit, never guess).
type derived struct {
	title     string
	titleSrc  string
	author    string
	authorSrc string
	series    string
	seriesSrc string
	position  string // normalized string position ("1", "2.5")
	posSrc    string
}

// derivePath infers title/author/series/position from a book's location.
//
// name is the book's identity segment (the folder name for a folder book, the
// file stem for a loose single-file book); nameSrc is the provenance to record
// for anything read out of it ("path" or "filename"). ancestors are the folder
// segments above the book, nearest-last.
//
// Folder-structure mapping (the folder tree is a first-class source, not a
// fallback - tags rarely carry good series data):
//
//	Author/Book             -> author (1 ancestor, no series in the name)
//	Author/Series/Book      -> author + series (2+ ancestors)
//	Series/03 - Title        -> series (1 ancestor, name carries a position)
//	Jack Reacher 03 - Title  -> series + position from the name itself
func derivePath(name, nameSrc string, ancestors []string) derived {
	var d derived

	nameSeries, position, title := parseName(name)
	if title != "" {
		d.title, d.titleSrc = title, nameSrc
	}
	if position != "" {
		d.position, d.posSrc = position, nameSrc
	}
	if nameSeries != "" {
		// The filename itself embedded the series ("Jack Reacher 03 - Title").
		d.series, d.seriesSrc = nameSeries, nameSrc
	}

	switch len(ancestors) {
	case 0:
		// No hierarchy (a loose file at the scan root).
	case 1:
		// One ancestor is ambiguous: Author/Book vs Series/03 - Title. If the
		// name carried a position, the parent is far more likely the series
		// folder; otherwise treat it as the author.
		if d.series == "" && position != "" {
			d.series, d.seriesSrc = ancestors[0], "path"
		} else if d.author == "" {
			d.author, d.authorSrc = ancestors[0], "path"
		}
	default:
		n := len(ancestors)
		if d.series == "" {
			d.series, d.seriesSrc = ancestors[n-1], "path"
		}
		d.author, d.authorSrc = ancestors[n-2], "path"
	}
	return d
}

var (
	// "Title, Book 3" / "Title - Book 3" (trailing volume marker).
	trailingVol = regexp.MustCompile(`(?i)^(.+?)[,\-]?\s+(?:book|vol|volume|part)\.?\s+(\d+(?:\.\d+)?)\s*$`)
	// "Book 3 - Title" / "Volume 3: Title" / "#3 Title" (leading volume marker).
	leadingVol = regexp.MustCompile(`(?i)^(?:book|vol|volume|part|#)\.?\s*(\d+(?:\.\d+)?)\s*[-:._ ]+\s*(.+)$`)
	// "01 - Title" / "3. Title" / "2.5 Title" (leading bare number).
	leadingNum = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*[-:._ ]+\s*(.+)$`)
	// "Jack Reacher 03 - Title" (series words, number, then title). Group 1 must
	// contain a letter so a bare-number name never matches here.
	seriesNum = regexp.MustCompile(`^(.*?\p{L}.*?)\s+(\d+(?:\.\d+)?)\s*[-:]\s*(.+)$`)
)

// parseName pulls a series name, a position, and a clean title out of a single
// book/file name. Any of the three may be empty. Ordering matters: the more
// specific volume-marker patterns are tried before the generic ones so
// "Book 3 - Title" is not misread as a series called "Book".
func parseName(name string) (series, position, title string) {
	s := strings.TrimSpace(name)
	if s == "" {
		return "", "", ""
	}

	if m := trailingVol.FindStringSubmatch(s); m != nil {
		if pos := normalizePosition(m[2], false); pos != "" {
			return "", pos, strings.TrimSpace(m[1])
		}
	}
	if m := leadingVol.FindStringSubmatch(s); m != nil {
		if pos := normalizePosition(m[1], true); pos != "" {
			return "", pos, strings.TrimSpace(m[2])
		}
	}
	if m := leadingNum.FindStringSubmatch(s); m != nil {
		if pos := normalizePosition(m[1], false); pos != "" {
			return "", pos, strings.TrimSpace(m[2])
		}
	}
	if m := seriesNum.FindStringSubmatch(s); m != nil {
		if pos := normalizePosition(m[2], false); pos != "" {
			return strings.TrimSpace(m[1]), pos, strings.TrimSpace(m[3])
		}
	}
	return "", "", s
}

// normalizePosition validates and canonicalizes a numeric position string,
// collapsing leading zeros ("03" -> "3", "2.50" -> "2.5"). A bare 4+ digit
// number is treated as a year/part of the title and rejected unless it was
// explicitly marked as a volume (explicit=true), mirroring the server heuristic.
// Returns "" when the input is not an acceptable position.
func normalizePosition(raw string, explicit bool) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	intPart, _, _ := strings.Cut(raw, ".")
	if !explicit && len(intPart) >= 4 {
		return ""
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f < 0 {
		return ""
	}
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// asinRE matches an Audible-style ASIN: "B0" followed by 8 alphanumerics,
// on a word boundary so it catches both bracketed ("Title [B076HYPQLK]") and
// loose occurrences. Case-sensitive on purpose - real ASINs are upper-case.
var asinRE = regexp.MustCompile(`\bB0[0-9A-Z]{8}\b`)

// findASIN returns the first ASIN found in s, or "".
func findASIN(s string) string {
	return asinRE.FindString(s)
}

// isbnRE matches a bare ISBN-13 (978/979 + 10 digits). Hyphenated forms are
// normalized away by the caller before matching.
var isbnRE = regexp.MustCompile(`\b97[89]\d{10}\b`)

// findISBN returns the first ISBN-13 found in s (after stripping hyphens so
// "978-0-399-59050-4" matches), or "". Spaces are kept so they still delimit a
// word boundary ("isbn 9781401238964").
func findISBN(s string) string {
	return isbnRE.FindString(strings.ReplaceAll(s, "-", ""))
}

// stem returns a file name without its extension.
func stem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}
