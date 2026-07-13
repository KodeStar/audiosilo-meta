package scan

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
)

// source identifies where a field's value came from. The values are the wire
// strings written into a book's "sources" map.
type source string

const (
	srcTag      source = "tag"
	srcPath     source = "path"
	srcFilename source = "filename"
)

// derived holds the fields a book's path (folder hierarchy + name) can yield,
// each with its provenance (srcPath for a folder name/ancestor, srcFilename for
// a loose file's name). An empty value means "not derivable" (omit, never
// guess) - except title, which derivePath guarantees non-empty for a non-empty
// name (the raw name is the fallback, so a book is never untitled).
type derived struct {
	title     string
	titleSrc  source
	author    string
	authorSrc source
	series    string
	seriesSrc source
	position  string // normalized string position ("1", "2.5")
	posSrc    source
}

// derivePath infers title/author/series/position from a book's location.
//
// name is the book's identity segment (the folder name for a folder book, the
// file stem for a loose single-file book); nameSrc is the provenance to record
// for anything read out of it (srcPath or srcFilename). ancestors are the
// folder segments above the book, nearest-last.
//
// Folder-structure mapping (the folder tree is a first-class source, not a
// fallback - tags rarely carry good series data):
//
//	Author/Book             -> author (1 ancestor, no series in the name)
//	Author/Series/Book      -> author + series (2+ ancestors)
//	Series/03 - Title        -> series (1 ancestor, name carries a position)
//	Jack Reacher 03 - Title  -> series + position from the name itself
func derivePath(name string, nameSrc source, ancestors []string) derived {
	var d derived

	nameSeries, position, title := parseName(name)
	if title == "" {
		title = name // never leave a book untitled: fall back to the raw name
	}
	d.title, d.titleSrc = title, nameSrc
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
			d.series, d.seriesSrc = ancestors[0], srcPath
		} else {
			d.author, d.authorSrc = ancestors[0], srcPath
		}
	default:
		n := len(ancestors)
		if d.series == "" {
			d.series, d.seriesSrc = ancestors[n-1], srcPath
		}
		d.author, d.authorSrc = ancestors[n-2], srcPath
	}
	return d
}

// The name-pattern grammar is composed from shared atoms so the vocabulary
// exists once. posPat is a SINGLE numeric position - omnibus ranges ("1-3.5")
// are tag-only (a leading "1-3" in a file name is indistinguishable from the
// ubiquitous "01 - Title" numbering), and final acceptance of every position
// goes through importer.NormalizeSequence either way.
const (
	posPat = `\d+(?:\.\d+)?`
	sepPat = `[-:._ ]`
)

var (
	// "Title, Book 3" / "Title - Book 3" (trailing volume marker).
	trailingVol = regexp.MustCompile(`(?i)^(.+?)[,\-]?\s+(?:book|vol|volume|part)\.?\s+(` + posPat + `)\s*$`)
	// "Book 3 - Title" / "Volume 3: Title" / "#3 Title" (leading volume marker).
	leadingVol = regexp.MustCompile(`(?i)^(?:book|vol|volume|part|#)\.?\s*(` + posPat + `)\s*` + sepPat + `+\s*(.+)$`)
	// "01 - Title" / "3. Title" / "2.5 Title" (leading bare number).
	leadingNum = regexp.MustCompile(`^(` + posPat + `)\s*` + sepPat + `+\s*(.+)$`)
	// "Jack Reacher 03 - Title" (series words, number, then title). Group 1 must
	// contain a letter so a bare-number name never matches here. The separator
	// class is deliberately NARROWER than sepPat ("-" or ":" only): the number
	// sits mid-name here, so accepting a mere space or dot after it would
	// misread ordinary titles that contain a number partway through.
	seriesNum = regexp.MustCompile(`^(.*?\p{L}.*?)\s+(` + posPat + `)\s*[-:]\s*(.+)$`)
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

// normalizePosition validates and canonicalizes a position string. Acceptance
// is importer.NormalizeSequence - the repo's ONE definition of a valid series
// position, which also admits omnibus ranges ("1-3.5", reachable from tags
// only) - with two scan-side refinements on top: leading zeros collapse
// ("03" -> "3", "2.50" -> "2.5"), and a bare integer of 4+ digits is treated
// as a year/part of the title and rejected unless it was explicitly marked as
// a volume ("Book 1984", explicit=true) - the server's year heuristic.
// Returns "" when the input is not an acceptable position.
func normalizePosition(raw string, explicit bool) string {
	pos, ok := importer.NormalizeSequence(raw)
	if !ok {
		return ""
	}
	parts := strings.Split(pos, "-")
	for i, p := range parts {
		if parts[i] = canonNumber(p); parts[i] == "" {
			return ""
		}
	}
	if !explicit && len(parts) == 1 && !strings.Contains(parts[0], ".") && len(parts[0]) >= 4 {
		return "" // "1984" is a year, not volume 1984
	}
	return strings.Join(parts, "-")
}

// canonNumber canonicalizes one numeric component: "03" -> "3", "2.50" -> "2.5".
func canonNumber(s string) string {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return ""
	}
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// asinRE matches an Audible-style ASIN in FREE TEXT (file/folder names): "B0"
// followed by 8 alphanumerics on a word boundary, so it catches both bracketed
// ("Title [B076HYPQLK]") and loose occurrences. Case-sensitive on purpose -
// real ASINs are upper-case. Explicit ASIN-keyed tag values go through
// importer.NormalizeASIN instead (whole-string, admits non-B0 ASINs).
var asinRE = regexp.MustCompile(`\bB0[0-9A-Z]{8}\b`)

// findASIN returns the first ASIN found in free text s, or "".
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
