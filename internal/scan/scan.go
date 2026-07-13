// Package scan walks a directory of audiobooks and gathers whatever metadata it
// can find - embedded tags (via dhowden/tag), the folder structure, and file
// names - into a JSON document the meta.audiosilo.app import page accepts. It is
// the low-friction path for a contributor who has only files, no OpenAudible or
// Libation export.
//
// Two design rules run through the whole package:
//
//   - The folder tree is a FIRST-CLASS source, not a fallback. Tags are often
//     missing or wrong (series data especially), so Author/Series/Book layouts
//     and name patterns like "01 - Title" are parsed for author/series/position.
//   - Omit, never guess. An unknown field is absent from the output. Every field
//     that is present records where it came from ("tag" | "path" | "filename")
//     in the book's sources map.
//
// Grouping follows the workspace convention: a directory that directly contains
// audio files is ONE book (those files are its parts); loose audio files at the
// scan root are individual single-file books.
package scan

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Format and Version identify the output document. The contract is fixed - the
// site's import page parses exactly this shape.
const (
	Format  = "audiosilo-folder-scan"
	Version = 1
)

// Result is the top-level scan document.
type Result struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Root    string `json:"root"`
	Books   []Book `json:"books"`
}

// Book is one detected audiobook. Every field except Path, Title, Files, and
// AudioFiles is optional and omitted when unknown.
type Book struct {
	Path           string            `json:"path"`
	Title          string            `json:"title"`
	Subtitle       string            `json:"subtitle,omitempty"`
	Authors        []string          `json:"authors,omitempty"`
	Narrators      []string          `json:"narrators,omitempty"`
	Series         string            `json:"series,omitempty"`
	SeriesPosition string            `json:"series_position,omitempty"`
	ASIN           string            `json:"asin,omitempty"`
	ISBN           string            `json:"isbn,omitempty"`
	Publisher      string            `json:"publisher,omitempty"`
	ReleaseDate    string            `json:"release_date,omitempty"`
	Language       string            `json:"language,omitempty"`
	RuntimeMin     int               `json:"runtime_min,omitempty"`
	Chapters       int               `json:"chapters,omitempty"`
	Files          []string          `json:"files"`
	AudioFiles     int               `json:"audio_files"`
	Sources        map[string]string `json:"sources,omitempty"`
}

// Options tunes a scan.
type Options struct {
	// FFprobePath is the ffprobe binary to use for runtime/chapter enrichment.
	// Empty disables it. "ffprobe" resolves on PATH. Enrichment is best-effort:
	// a missing or failing ffprobe never fails the scan.
	FFprobePath string
}

// Stats summarizes a scan for the human-readable report.
type Stats struct {
	Books       int
	WithASIN    int
	WithSeries  int
	TagFailures int
}

// isAudio reports whether name has a recognized audiobook extension.
func isAudio(name string) bool {
	return audioExts[strings.ToLower(filepath.Ext(name))]
}

var audioExts = map[string]bool{
	".m4b": true, ".m4a": true, ".mp4": true, ".mp3": true, ".aac": true,
	".ogg": true, ".opus": true, ".flac": true, ".wma": true,
}

// Scan walks root and returns the assembled document plus summary stats. It
// errors only if root cannot be read; individual file/tag problems degrade to
// missing metadata.
func Scan(root string, opts Options) (*Result, Stats, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, Stats{}, err
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, Stats{}, err
	}
	if !info.IsDir() {
		return nil, Stats{}, &os.PathError{Op: "scan", Path: absRoot, Err: os.ErrInvalid}
	}

	probeEnabled := hasFFprobe(opts.FFprobePath)

	var books []Book
	var stats Stats
	walk(absRoot, absRoot, true, opts, probeEnabled, &books, &stats)

	sort.Slice(books, func(i, j int) bool { return books[i].Path < books[j].Path })

	stats.Books = len(books)
	for i := range books {
		if books[i].ASIN != "" {
			stats.WithASIN++
		}
		if books[i].Series != "" {
			stats.WithSeries++
		}
	}
	return &Result{Format: Format, Version: Version, Root: absRoot, Books: books}, stats, nil
}

// walk recurses the tree, emitting books per the grouping convention.
func walk(dir, root string, isRoot bool, opts Options, probeEnabled bool, books *[]Book, stats *Stats) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var audioFiles, subdirs []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // skip dotfiles / dot-dirs
		}
		if e.IsDir() {
			subdirs = append(subdirs, name)
		} else if isAudio(name) {
			audioFiles = append(audioFiles, name)
		}
	}
	sort.Strings(audioFiles)
	sort.Strings(subdirs)

	switch {
	case isRoot:
		// Loose files at the scan root are individual single-file books.
		for _, f := range audioFiles {
			*books = append(*books, buildBook(dir, root, []string{f}, false, opts, probeEnabled, stats))
		}
	case len(audioFiles) > 0:
		// A directory that directly contains audio is ONE book.
		*books = append(*books, buildBook(dir, root, audioFiles, true, opts, probeEnabled, stats))
	}

	for _, sd := range subdirs {
		walk(filepath.Join(dir, sd), root, false, opts, probeEnabled, books, stats)
	}
}

// buildBook assembles one Book from a folder (isFolder, path = the dir) or a
// single loose root file (path = the file stem).
func buildBook(dir, root string, files []string, isFolder bool, opts Options, probeEnabled bool, stats *Stats) Book {
	// Identity segment (name) + provenance + the location for the book path.
	var name, nameSrc, bookPath string
	var ancestors []string
	segs := relSegments(dir, root)
	if isFolder {
		name, nameSrc = filepath.Base(dir), "path"
		ancestors = segs[:len(segs)-1] // segments above the book folder
		bookPath = strings.Join(segs, "/")
	} else {
		name, nameSrc = stem(files[0]), "filename"
		ancestors = segs // segs is the dir's ancestors; empty at root
		bookPath = name
	}

	pd := derivePath(name, nameSrc, ancestors)

	// Read tags from the first audio file (folder books share album-level tags).
	firstFile := filepath.Join(dir, files[0])
	tags, ok := readTags(firstFile)
	if !ok {
		stats.TagFailures++
	}

	b := assemble(bookPath, name, nameSrc, files, pd, tags)

	// ffprobe enrichment (best-effort): runtime, chapter count, richer container
	// tags (subtitle/publisher/language/full release date). Never fails the scan.
	if probeEnabled {
		enrich(&b, dir, files, opts.FFprobePath)
	}

	if len(b.Sources) == 0 {
		b.Sources = nil
	}
	return b
}

// assemble merges path-derived fields (pd) and embedded tags into a Book,
// applying the disagreement policy. It is pure (no I/O) so the policy is
// directly unit-testable:
//
//	title / narrator  -> tag preferred, path/filename fallback
//	series / position -> path preferred, tag fallback (tags rarely carry good
//	                     series data, so the folder structure wins)
//	author            -> tag preferred, path fallback
//
// ASIN is hunted across tag atoms, then file names, then the folder path.
func assemble(bookPath, name, nameSrc string, files []string, pd derived, tags tagInfo) Book {
	b := Book{Path: bookPath, Files: files, AudioFiles: len(files), Sources: map[string]string{}}

	switch {
	case tags.title != "":
		b.Title, b.Sources["title"] = tags.title, "tag"
	case pd.title != "":
		b.Title, b.Sources["title"] = pd.title, pd.titleSrc
	default:
		// Never leave a book untitled: fall back to the raw identity segment.
		b.Title, b.Sources["title"] = name, nameSrc
	}

	switch {
	case len(tags.authors) > 0:
		b.Authors, b.Sources["authors"] = tags.authors, "tag"
	case pd.author != "":
		b.Authors, b.Sources["authors"] = []string{pd.author}, pd.authorSrc
	}

	if len(tags.narrators) > 0 {
		b.Narrators, b.Sources["narrators"] = tags.narrators, "tag"
	}

	switch {
	case pd.series != "":
		b.Series, b.Sources["series"] = pd.series, pd.seriesSrc
	case tags.series != "":
		b.Series, b.Sources["series"] = tags.series, "tag"
	}

	switch {
	case pd.position != "":
		b.SeriesPosition, b.Sources["series_position"] = pd.position, pd.posSrc
	case tags.position != "":
		b.SeriesPosition, b.Sources["series_position"] = tags.position, "tag"
	}

	switch {
	case tags.asin != "":
		b.ASIN, b.Sources["asin"] = tags.asin, "tag"
	default:
		if asin := findASIN(strings.ToUpper(strings.Join(files, " "))); asin != "" {
			b.ASIN, b.Sources["asin"] = asin, "filename"
		} else if asin := findASIN(strings.ToUpper(bookPath)); asin != "" {
			b.ASIN, b.Sources["asin"] = asin, "path"
		}
	}

	switch {
	case tags.isbn != "":
		b.ISBN, b.Sources["isbn"] = tags.isbn, "tag"
	default:
		if isbn := findISBN(strings.Join(files, " ")); isbn != "" {
			b.ISBN, b.Sources["isbn"] = isbn, "filename"
		}
	}

	if tags.year > 0 {
		b.ReleaseDate, b.Sources["release_date"] = strconv.Itoa(tags.year), "tag"
	}
	return b
}

// enrich adds ffprobe-derived fields to b in place.
func enrich(b *Book, dir string, files []string, ffprobePath string) {
	var totalSec float64
	var chapters int
	var firstTags map[string]string
	for i, f := range files {
		p, err := probe(filepath.Join(dir, f), ffprobePath)
		if err != nil {
			continue
		}
		totalSec += p.duration
		if p.chapters > 0 {
			chapters += p.chapters
		} else {
			chapters++ // a file with no embedded chapters counts as one chapter
		}
		if i == 0 {
			firstTags = p.tags
		}
	}
	if totalSec > 0 {
		b.RuntimeMin = int(totalSec/60 + 0.5)
	}
	if chapters > 0 {
		b.Chapters = chapters
	}

	// Container tags fill fields dhowden does not expose. Only set when the tag
	// is genuinely present (omit, never guess).
	if firstTags == nil {
		return
	}
	if v := firstNonEmpty(firstTags["subtitle"], firstTags["subtitle-0"]); v != "" && b.Subtitle == "" {
		b.Subtitle, b.Sources["subtitle"] = v, "tag"
	}
	if v := firstNonEmpty(firstTags["publisher"], firstTags["label"], firstTags["©pub"]); v != "" && b.Publisher == "" {
		b.Publisher, b.Sources["publisher"] = v, "tag"
	}
	if v := firstTags["language"]; v != "" && b.Language == "" {
		b.Language, b.Sources["language"] = v, "tag"
	}
	// A full ISO date from the container is better than a bare tag year.
	if v := firstNonEmpty(firstTags["date"], firstTags["releasedate"], firstTags["year"]); v != "" {
		if len(v) >= len(b.ReleaseDate) {
			b.ReleaseDate, b.Sources["release_date"] = v, "tag"
		}
	}
	if b.ASIN == "" {
		if asin := findASIN(strings.ToUpper(firstNonEmpty(firstTags["asin"], firstTags["cdek"], firstTags["audible_asin"]))); asin != "" {
			b.ASIN, b.Sources["asin"] = asin, "tag"
		}
	}
	if b.Series == "" {
		if v := firstNonEmpty(firstTags["series"], firstTags["show"], firstTags["grouping"]); v != "" {
			b.Series, b.Sources["series"] = v, "tag"
		}
	}
}

// relSegments returns dir's path segments relative to root, as a slice. For the
// root itself it returns nil. It is used both as a book's location and, for a
// loose root file, as its (empty) ancestor list.
func relSegments(dir, root string) []string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || rel == "" {
		return nil
	}
	return strings.Split(filepath.ToSlash(rel), "/")
}
