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
// scan root are individual single-file books. The one evidence-gated exception
// is splitVerdict: a folder whose files' tags prove they are different books
// (the flat "Series/01 - A.m4b, 02 - B.m4b" layout) is split per file.
package scan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

// set fills a string field (and its provenance) if the field is still empty and
// v is non-empty - value and source are recorded atomically, so a sequence of
// set calls IS the preference order for that field.
func (b *Book) set(field *string, key, v string, src source) {
	if *field != "" || v == "" {
		return
	}
	*field = v
	b.Sources[key] = string(src)
}

// setList is set for list-valued fields.
func (b *Book) setList(field *[]string, key string, v []string, src source) {
	if len(*field) > 0 || len(v) == 0 {
		return
	}
	*field = v
	b.Sources[key] = string(src)
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
	// AmbiguousDirs counts multi-file folders kept as one book with NO tag
	// evidence either way - possible collections a human should check.
	AmbiguousDirs int
}

// isAudio reports whether name has a recognized audiobook extension.
func isAudio(name string) bool {
	return audioExts[strings.ToLower(filepath.Ext(name))]
}

var audioExts = map[string]bool{
	".m4b": true, ".m4a": true, ".mp4": true, ".mp3": true, ".aac": true,
	".ogg": true, ".opus": true, ".flac": true, ".wma": true,
}

// group is one directory's audio files - the unit the grouping convention (and
// splitVerdict) decides over. isRoot marks the scan root, whose loose files are
// unconditionally individual books.
type group struct {
	dir    string   // absolute directory
	files  []string // audio file names, sorted
	isRoot bool
}

// fileData is the per-file evidence gathered before grouping: the canonical
// dhowden tag read plus the optional ffprobe result.
type fileData struct {
	tags  tagInfo
	tagOK bool
	probe *probeResult // nil when ffprobe is disabled or failed for the file
}

// maxWorkers bounds the per-file worker pool. The scan is latency-bound (tag
// reads + ffprobe spawns), so a modest fan-out dominates the wall clock on
// network mounts without swamping the disk.
const maxWorkers = 8

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

	// Resolve ffprobe once: an empty path means "no enrichment" from here on.
	ffprobePath := ""
	if hasFFprobe(opts.FFprobePath) {
		ffprobePath = opts.FFprobePath
	}

	groups := collectGroups(absRoot)
	data := loadGroups(groups, ffprobePath)

	// Build books sequentially (stats stay race-free); the final sort makes the
	// output deterministic regardless of load order.
	var books []Book
	var stats Stats
	for gi, g := range groups {
		gd := data[gi]
		tags := make([]tagInfo, len(gd))
		for i, fd := range gd {
			tags[i] = fd.tags
			if !fd.tagOK {
				stats.TagFailures++
			}
		}

		// Loose root files are unconditionally individual books; elsewhere the
		// files' own tags decide (never filenames alone).
		v := verdictSplit
		if !g.isRoot {
			v = splitVerdict(g.files, tags)
		}
		switch v {
		case verdictSplit:
			for i, f := range g.files {
				books = append(books, buildBook(g.dir, absRoot, []string{f}, false, gd[i:i+1]))
			}
		case verdictKeepAmbiguous:
			stats.AmbiguousDirs++
			fallthrough
		default:
			books = append(books, buildBook(g.dir, absRoot, g.files, true, gd))
		}
	}

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

// collectGroups walks the tree (cheap, ReadDir only) and returns each directory
// that directly contains audio files, in deterministic order.
func collectGroups(root string) []group {
	var groups []group
	var walk func(dir string, isRoot bool)
	walk = func(dir string, isRoot bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		var audio, subdirs []string
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue // skip dotfiles / dot-dirs
			}
			if e.IsDir() {
				subdirs = append(subdirs, name)
			} else if isAudio(name) {
				audio = append(audio, name)
			}
		}
		sort.Strings(audio)
		sort.Strings(subdirs)
		if len(audio) > 0 {
			groups = append(groups, group{dir: dir, files: audio, isRoot: isRoot})
		}
		for _, sd := range subdirs {
			walk(filepath.Join(dir, sd), false)
		}
	}
	walk(root, true)
	return groups
}

// loadGroups gathers every file's tag read + optional ffprobe result through a
// bounded worker pool (the scan's latency-bound part). Each goroutine writes
// only its own fileData slot, so the result is race-free by construction.
func loadGroups(groups []group, ffprobePath string) [][]fileData {
	data := make([][]fileData, len(groups))
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	for gi, g := range groups {
		data[gi] = make([]fileData, len(g.files))
		for fi, f := range g.files {
			wg.Add(1)
			go func(slot *fileData, path string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				slot.tags, slot.tagOK = readTags(path)
				if ffprobePath != "" {
					if p, err := probe(path, ffprobePath); err == nil {
						slot.probe = p
					}
				}
			}(&data[gi][fi], filepath.Join(g.dir, f))
		}
	}
	wg.Wait()
	return data
}

// buildBook assembles one Book from a folder (isFolder, path = the dir) or a
// single file (a loose root file, or one file of a split collection folder;
// path = <dir-relative>/<file stem>, the containing folder feeding the path
// heuristics). data is per-file evidence parallel to files.
func buildBook(dir, root string, files []string, isFolder bool, data []fileData) Book {
	// Identity segment (name) + provenance + the location for the book path.
	var name, bookPath string
	var nameSrc source
	var ancestors []string
	segs := relSegments(dir, root)
	if isFolder {
		name, nameSrc = filepath.Base(dir), srcPath
		ancestors = segs[:len(segs)-1] // segments above the book folder
		bookPath = strings.Join(segs, "/")
	} else {
		name, nameSrc = stem(files[0]), srcFilename
		ancestors = segs // the containing dir's segments; empty at the root
		bookPath = name
		if len(segs) > 0 {
			bookPath = strings.Join(segs, "/") + "/" + name
		}
	}

	pd := derivePath(name, nameSrc, ancestors)

	// One merged tag view: the first file's dhowden read (folder books share
	// album-level tags), with its ffprobe container tags filling the gaps.
	tags := data[0].tags
	if data[0].probe != nil {
		tags = mergeTags(tags, probeTagInfo(data[0].probe.tags))
	}

	b := assemble(bookPath, files, pd, tags)

	// Duration/chapter enrichment sums the per-file probes. A file with no
	// embedded chapters counts as one chapter; a failed probe contributes
	// nothing (degrade, never fail).
	var totalSec float64
	var chapters int
	for _, fd := range data {
		if fd.probe == nil {
			continue
		}
		totalSec += fd.probe.duration
		if fd.probe.chapters > 0 {
			chapters += fd.probe.chapters
		} else {
			chapters++
		}
	}
	if totalSec > 0 {
		b.RuntimeMin = int(totalSec/60 + 0.5)
	}
	b.Chapters = chapters

	if len(b.Sources) == 0 {
		b.Sources = nil
	}
	return b
}

// assemble merges path-derived fields (pd) and the (already merged) tags into a
// Book. It is pure (no I/O), and the disagreement policy is simply the call
// order of the set() lines per field:
//
//	title / narrator  -> tag preferred, path/filename fallback
//	series / position -> path preferred, tag fallback (tags rarely carry good
//	                     series data, so the folder structure wins)
//	author            -> tag preferred, path fallback
//	asin              -> tag atoms, then file names, then the folder path
//
// pd.title is guaranteed non-empty (derivePath), so a book is never untitled.
func assemble(bookPath string, files []string, pd derived, tags tagInfo) Book {
	b := Book{Path: bookPath, Files: files, AudioFiles: len(files), Sources: map[string]string{}}
	fileText := strings.Join(files, " ")

	b.set(&b.Title, "title", tags.bookTitle(), srcTag)
	b.set(&b.Title, "title", pd.title, pd.titleSrc)

	b.setList(&b.Authors, "authors", tags.authors, srcTag)
	if pd.author != "" {
		b.setList(&b.Authors, "authors", []string{pd.author}, pd.authorSrc)
	}

	b.setList(&b.Narrators, "narrators", tags.narrators, srcTag)

	b.set(&b.Series, "series", pd.series, pd.seriesSrc)
	b.set(&b.Series, "series", tags.series, srcTag)

	b.set(&b.SeriesPosition, "series_position", pd.position, pd.posSrc)
	b.set(&b.SeriesPosition, "series_position", tags.position, srcTag)

	b.set(&b.ASIN, "asin", tags.asin, srcTag)
	b.set(&b.ASIN, "asin", findASIN(strings.ToUpper(fileText)), srcFilename)
	b.set(&b.ASIN, "asin", findASIN(strings.ToUpper(bookPath)), srcPath)

	b.set(&b.ISBN, "isbn", tags.isbn, srcTag)
	b.set(&b.ISBN, "isbn", findISBN(fileText), srcFilename)

	b.set(&b.Subtitle, "subtitle", tags.subtitle, srcTag)
	b.set(&b.Publisher, "publisher", tags.publisher, srcTag)
	b.set(&b.Language, "language", tags.language, srcTag)
	b.set(&b.ReleaseDate, "release_date", tags.releaseDate, srcTag)
	return b
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
