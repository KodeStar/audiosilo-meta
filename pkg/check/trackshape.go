package check

import (
	"regexp"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// trackshape.go reads what a recording's chapter list says about the BOOK's
// structure, for the position-scale advisory (checkSidecarPositionScale). A
// recording's track list is the only independent measure of a work's length the
// tree carries, but it is an audiobook's track list: besides the chapters it
// holds front and back matter (Opening Credits, Dedication, End Credits) and,
// in a book divided into parts, one divider track per part ("Part One: The
// Traveler", "Book II", or a bare "Seven: Intersections"). Counting those as
// chapters inflates the measure, and the dividers are also the one structure a
// mis-scaled sidecar is most often written against - a sidecar numbered by PART
// stops exactly at the recording's part count.
//
// pkg/extract's ChapterFromLabel is the near-twin for epub toc labels, and its
// structural rule ("part|book|volume|section|act|scene") is deliberately not
// shared: a TRACK titled "Part 1 - Chapter 3" or "Act I, Scene 2" is a chapter of
// the recording, not a divider, and a run of "Scene N" or "Section N" tracks is
// the chapter list itself.

// numberWordRe spells a number word up to ninety-nine ("Seven", "Twenty-One"),
// the form a part divider takes when a recording titles it "One: Thief at Sea".
const numberWordRe = `(?:zero|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|` +
	`fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|sixty|seventy|` +
	`eighty|ninety)(?:[- ](?:one|two|three|four|five|six|seven|eight|nine))?`

var (
	// reTrackNumber is a leading track number an encoder prefixed to the title
	// ("03 - Part Two", "12. Chapter 9"); the title proper follows it.
	reTrackNumber = regexp.MustCompile(`^\d+\s*[-.:)]\s+`)

	// reMatterTrack is a front- or back-matter track: no chapter of the work, at
	// either end of it. The vocabulary is the non-chapter titles that recur across
	// the recordings of works carrying a sidecar (2026-11-03: Opening Credits
	// 2,910, End Credits 2,508, Dedication 244, Epigraph 169, Preface 111, Closing
	// Credits 105, Author's Note 59, Foreword 47, Credits 40, Acknowledgments 39,
	// Afterword 36, Bloopers 33). Prologue, Epilogue and Interlude are story and
	// stay chapters; Introduction is left a chapter too, since a novel may open
	// with one.
	reMatterTrack = regexp.MustCompile(`(?i)^(?:(?:opening|end|closing)\s+)?credits\b|` +
		`^(?:dedication|epigraph|acknowledge?ments|foreword|preface|afterword|` +
		`(?:author|translator)['’]?s\s+note|about\s+the\s+author|copyright|bloopers)\b`)

	// reLowerUnit is a chapter-level unit named inside a title. A title naming
	// one is a chapter track whatever it opens with: "Part 1 - Chapter 3" and
	// "Book Two, Chapter 1" are chapters that state their part, never dividers.
	reLowerUnit = regexp.MustCompile(`(?i)\b(?:chapter|chap|ch|kapitel|chapitre|cap[ií]tulo|capitolo|` +
		`hoofdstuk|scene|section|canto)\b`)

	// reDividerTrack is a part divider: a title opening with a part-level unit
	// ("Part One: The Traveler", "Book II", "Act 3"), or a bare number word,
	// alone or before a colon ("Seven", "Seven: Intersections").
	reDividerTrack = regexp.MustCompile(`(?i)^(?:part|book|volume|act)\b|^` + numberWordRe + `\s*(?::|$)`)

	// rePartPrefix is the part a chapter track states ahead of its chapter
	// ("Part 2 - Chapter 7", "Bk 3: Ch 1-2"), the shape a recording with no
	// divider tracks carries its parts in.
	rePartPrefix = regexp.MustCompile(`(?i)^(?:part|book|volume|pt|bk|vol)\.?\s*(\d+|[ivxlcdm]+|` + numberWordRe + `)\b`)
)

// recordingShape is a recording's chapter list read as the book's structure:
// the tracks that are chapters of the work, and the parts they are divided into
// (0 when the recording shows fewer than two).
type recordingShape struct {
	chapters int
	parts    int
}

// shapeOf classifies a recording's tracks. A divider counts as one only when it
// divides something: a recording whose dividers are at least as many as its
// other tracks ("Book 1" .. "Book 24" for the Iliad, or an encoder's "Part 1" ..
// "Part 30" file split) is titling its chapters that way, so they are counted as
// chapters and the recording shows no parts. Parts are the divider tracks when
// there are two or more, else the distinct parts the chapter titles state.
func shapeOf(chapters []model.Chapter) recordingShape {
	var dividers, chapterTracks int
	var stated []string
	for _, c := range chapters {
		t := reTrackNumber.ReplaceAllString(strings.TrimSpace(c.Title), "")
		if reMatterTrack.MatchString(t) {
			continue
		}
		lower := reLowerUnit.MatchString(t)
		if !lower && reDividerTrack.MatchString(t) {
			dividers++
			continue
		}
		chapterTracks++
		if !lower {
			continue
		}
		if m := rePartPrefix.FindStringSubmatch(t); m != nil {
			label := strings.ToLower(m[1])
			if !slices.Contains(stated, label) {
				stated = append(stated, label)
			}
		}
	}
	if dividers >= chapterTracks {
		return recordingShape{chapters: chapterTracks + dividers}
	}
	s := recordingShape{chapters: chapterTracks}
	switch {
	case dividers >= 2:
		s.parts = dividers
	case len(stated) >= 2:
		s.parts = len(stated)
	}
	return s
}
