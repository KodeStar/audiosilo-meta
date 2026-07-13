package scan

import "strings"

// verdict is the collection-split decision for a directory holding 2+ audio
// files: is it ONE book in parts, or a flat folder of separate single-file books?
type verdict int

const (
	// verdictKeep: the folder is one book (positive shared-album evidence, or a
	// single file - nothing to split).
	verdictKeep verdict = iota
	// verdictKeepAmbiguous: kept as one book, but the tags gave no signal either
	// way - surfaced in the summary so a human can check for a collection.
	verdictKeepAmbiguous
	// verdictSplit: strong tag evidence that each file is its own book.
	verdictSplit
)

// splitVerdict decides whether a directory's audio files are chapter parts of
// ONE book (the workspace one-folder-one-book convention) or separate
// single-file books that must not be silently merged (the common flat
// "Series/01 - A.m4b, 02 - B.m4b" layout).
//
// The decision is evidence-gated on the embedded tags, never on filenames alone
// (the server's phantom-book lesson):
//
//   - Every file carries an ALBUM tag and they are mutually distinct
//     (normalized) -> split: each file names a different book.
//   - Every file carries the SAME album -> keep: chapter parts of one book.
//   - No album verdict, but every file's TITLE tag is distinct, non-generic
//     ("Chapter 07" never counts), and matches its own filename-derived title
//     -> split: the files agree with their names about being different books.
//   - Anything else (no tags, partial tags, mixed albums) -> keep, flagged
//     ambiguous so the summary can point a human at it.
//
// files are the audio file names (used for the filename-derived titles); tags
// are their parallel tagInfo values (zero for unreadable files).
func splitVerdict(files []string, tags []tagInfo) verdict {
	if len(files) < 2 {
		return verdictKeep
	}

	albums := make([]string, len(tags))
	allAlbums := true
	for i, t := range tags {
		albums[i] = normalizeEvidence(t.album)
		if albums[i] == "" {
			allAlbums = false
		}
	}
	if allAlbums {
		if allDistinct(albums) {
			return verdictSplit
		}
		if allSame(albums) {
			return verdictKeep
		}
		return verdictKeepAmbiguous // mixed albums: no clean signal
	}

	// Title evidence (only when albums are inconclusive): each file's tag title
	// must be present, non-generic, distinct, and agree with the title its own
	// filename derives - e.g. "01 - Unsouled.m4b" tagged "Unsouled".
	titles := make([]string, len(tags))
	for i, t := range tags {
		titles[i] = normalizeEvidence(t.trackTitle)
		if titles[i] == "" || isGenericTitle(t.trackTitle) {
			return verdictKeepAmbiguous
		}
		_, _, fromName, _ := parseName(stem(files[i]))
		if fromName == "" {
			fromName = stem(files[i])
		}
		if normalizeEvidence(fromName) != titles[i] {
			return verdictKeepAmbiguous
		}
	}
	if allDistinct(titles) {
		return verdictSplit
	}
	return verdictKeepAmbiguous
}

// normalizeEvidence canonicalizes a tag value for comparison: trimmed and
// case-folded.
func normalizeEvidence(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func allDistinct(vals []string) bool {
	seen := make(map[string]bool, len(vals))
	for _, v := range vals {
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

func allSame(vals []string) bool {
	for _, v := range vals[1:] {
		if v != vals[0] {
			return false
		}
	}
	return true
}

// discLabels are the words that, together with a number, form a generic part
// label ("Track 01", "Disc 2", "CD1", "Chapter 07").
var discLabels = map[string]bool{"track": true, "chapter": true, "part": true, "disc": true, "disk": true, "cd": true}

// isGenericTitle reports whether a title carries no real book identity - a bare
// number or a "track/chapter/part/disc N"-style label (e.g. "Chapter 07"). It is
// token-based, so real titles that merely start with such a word ("Part of Your
// World") are NOT flagged: every token must be a number or a disc label, with at
// least one number present. Ported from audiosilo-server's metadata package -
// such titles must never count as split evidence.
func isGenericTitle(title string) bool {
	// Tokenize: lowercase, split on every run of non-alphanumerics.
	tokens := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(tokens) == 0 {
		return true
	}
	hasNumber := false
	for _, tok := range tokens {
		switch {
		case isAllDigits(tok):
			hasNumber = true
		case discLabels[tok]:
			// a bare label word; contributes no number on its own
		default:
			// a label with digits attached, e.g. "cd1" / "disc02"?
			ok := false
			for lbl := range discLabels {
				if rest, found := strings.CutPrefix(tok, lbl); found && rest != "" && isAllDigits(rest) {
					ok, hasNumber = true, true
					break
				}
			}
			if !ok {
				return false // a real word - not a generic label
			}
		}
	}
	return hasNumber
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
