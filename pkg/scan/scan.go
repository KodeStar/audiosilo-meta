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
//
// This package is PUBLIC API: it is consumed by the sibling audiosilo-sidecars
// tool as an ordinary module dependency, so its exported surface is a contract.
package scan

import (
	"io/fs"
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
	TagFailures int // real tag-read failures (untagged files are not failures)
	// AmbiguousDirs counts multi-file folders kept as one book with NO tag
	// evidence either way - possible collections a human should check.
	AmbiguousDirs int
	// UnreadableDirs counts directories that could not be read or resolved
	// (permissions, dangling symlinks) and were skipped.
	UnreadableDirs int
	// ProbeFailures counts files ffprobe was asked about but could not read;
	// a book with any probe failure omits runtime_min/chapters rather than
	// assert undercounted facts.
	ProbeFailures int
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

// fileData is the per-file evidence gathered before grouping.
type fileData struct {
	tags      tagInfo      // canonical dhowden read
	merged    tagInfo      // tags + probe container fill - the ONE view both splitVerdict and buildBook use
	probeASIN string       // container-tag ASIN (lowest ASIN precedence; see mergeTags)
	hasTags   bool         // dhowden parsed a tag block
	failed    bool         // real tag-read failure (not "no tags")
	probe     *probeResult // nil when ffprobe is disabled or failed for the file
}

// groupData binds a group to its per-file evidence, so the two can never be
// re-ordered independently.
type groupData struct {
	group
	data []fileData // parallel to group.files
}

// maxWorkers bounds the per-file fan-out. The scan is latency-bound (tag reads
// + ffprobe spawns), so a modest concurrency dominates the wall clock on
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

	var stats Stats
	groups := collectGroups(absRoot, &stats)
	loadGroups(groups, ffprobePath)

	// Build books sequentially (stats stay race-free); the final sort makes the
	// output deterministic regardless of load order. books is non-nil so an
	// empty scan marshals as "books": [].
	books := []Book{}
	// pending holds sibling-corroboration claims by book index (see
	// derived.pending); resolved after all books exist, before the sort.
	type pendingBook struct {
		idx   int
		claim *nameClaim
	}
	var pendings []pendingBook

	for _, g := range groups {
		for _, fd := range g.data {
			if fd.failed {
				stats.TagFailures++
			}
			if ffprobePath != "" && fd.probe == nil {
				stats.ProbeFailures++
			}
		}

		// Loose root files are unconditionally individual books; elsewhere the
		// files' own tags decide (never filenames alone). The verdict sees the
		// SAME merged tag view the books are built from, so probe-visible
		// distinct albums also prevent a wrong merge.
		v := verdictSplit
		if !g.isRoot {
			tags := make([]tagInfo, len(g.data))
			for i, fd := range g.data {
				tags[i] = fd.merged
			}
			v = splitVerdict(g.files, tags)
		}
		switch v {
		case verdictSplit:
			for i, f := range g.files {
				b, claim := buildBook(g.dir, absRoot, []string{f}, false, g.data[i:i+1])
				if claim != nil {
					pendings = append(pendings, pendingBook{idx: len(books), claim: claim})
				}
				books = append(books, b)
			}
		case verdictKeepAmbiguous:
			stats.AmbiguousDirs++
			fallthrough
		default:
			b, claim := buildBook(g.dir, absRoot, g.files, true, g.data)
			if claim != nil {
				pendings = append(pendings, pendingBook{idx: len(books), claim: claim})
			}
			books = append(books, b)
		}
	}

	// Sibling corroboration: a pending seriesNum claim is accepted when another
	// book asserts the same series name through solid evidence (its own claims
	// don't count, so two accidental "Catch NN - X" shapes can't vouch for each
	// other).
	known := map[string]bool{}
	for i := range books {
		if s := books[i].Series; s != "" {
			known[strings.ToLower(s)] = true
		}
	}
	for _, p := range pendings {
		if !known[strings.ToLower(p.claim.series)] {
			continue
		}
		b := &books[p.idx]
		if b.Series == "" {
			b.set(&b.Series, "series", p.claim.series, p.claim.src)
			b.set(&b.SeriesPosition, "series_position", p.claim.position, p.claim.src)
		}
		if b.Sources["title"] != string(srcTag) && p.claim.title != "" {
			b.Title = p.claim.title
			b.Sources["title"] = string(p.claim.src)
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
// that directly contains audio files, in deterministic order. Directory
// symlinks are followed (symlink-organized libraries are common), with a
// visited set over resolved paths guarding cycles; unreadable or unresolvable
// directories are counted in stats and skipped, never silently dropped.
func collectGroups(root string, stats *Stats) []groupData {
	var groups []groupData
	visited := map[string]bool{}
	var walk func(dir string, isRoot bool)
	walk = func(dir string, isRoot bool) {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			stats.UnreadableDirs++
			return
		}
		if visited[resolved] {
			return // symlink cycle / duplicate entry point
		}
		visited[resolved] = true

		entries, err := os.ReadDir(dir)
		if err != nil {
			stats.UnreadableDirs++
			return
		}
		var audio, subdirs []string
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue // skip dotfiles / dot-dirs
			}
			switch {
			case e.IsDir():
				subdirs = append(subdirs, name)
			case e.Type()&fs.ModeSymlink != 0:
				// Follow the symlink: a directory joins the walk, an audio
				// file joins the group (os.Open follows it transparently).
				if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && fi.IsDir() {
					subdirs = append(subdirs, name)
				} else if err == nil && isAudio(name) {
					audio = append(audio, name)
				} else if err != nil {
					stats.UnreadableDirs++ // dangling symlink
				}
			case isAudio(name):
				audio = append(audio, name)
			}
		}
		// audio orders a folder book's parts (they become Book.Files, i.e. the
		// track order), so use numeric-aware sorting: unpadded "Chapter 2.mp3"
		// must precede "Chapter 10.mp3". subdirs only drives walk order (books
		// are re-sorted by path below), so a plain sort is fine there.
		sort.SliceStable(audio, func(i, j int) bool { return naturalLess(audio[i], audio[j]) })
		sort.Strings(subdirs)
		if len(audio) > 0 {
			groups = append(groups, groupData{group: group{dir: dir, files: audio, isRoot: isRoot}})
		}
		for _, sd := range subdirs {
			walk(filepath.Join(dir, sd), false)
		}
	}
	walk(root, true)
	return groups
}

// loadGroups gathers every file's evidence - the dhowden tag read, the optional
// ffprobe result, and the pre-computed merged view - fanning the per-file work
// (the scan's latency-bound part) across goroutines bounded by a semaphore to
// maxWorkers in flight. Each goroutine writes only its own fileData slot, so
// the result is race-free by construction.
func loadGroups(groups []groupData, ffprobePath string) {
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	for gi := range groups {
		g := &groups[gi]
		g.data = make([]fileData, len(g.files))
		for fi, f := range g.files {
			wg.Add(1)
			go func(slot *fileData, path string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				slot.tags, slot.hasTags, slot.failed = readTags(path)
				slot.merged = slot.tags
				if ffprobePath != "" {
					if p, err := probe(path, ffprobePath); err == nil {
						slot.probe = p
						pi := probeTagInfo(p.tags)
						slot.probeASIN = pi.asin
						slot.merged = mergeTags(slot.tags, pi)
					}
				}
			}(&g.data[fi], filepath.Join(g.dir, f))
		}
	}
	wg.Wait()
}

// buildBook assembles one Book from a folder (isFolder, path = the dir) or a
// single file (a loose root file, or one file of a split collection folder;
// path = <dir-relative>/<file stem>, the containing folder feeding the path
// heuristics). data is per-file evidence parallel to files. The returned claim,
// if non-nil, is an uncorroborated seriesNum reading for Scan's sibling pass.
func buildBook(dir, root string, files []string, isFolder bool, data []fileData) (Book, *nameClaim) {
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

	// Tag evidence comes from the first file whose tag read parsed - a corrupt
	// or untagged first part ("00 - Opening Credits.mp3") must not blank the
	// whole book's tags when the rest are fine.
	base := 0
	for i := range data {
		if data[i].hasTags {
			base = i
			break
		}
	}
	tags := data[base].merged

	pd := derivePath(name, nameSrc, ancestors)
	// Corroborate a tentative name-embedded series claim against the book's
	// own tags: an album or series tag naming the claimed series confirms it.
	if c := pd.pending; c != nil && tagsCorroborate(tags, c.series) {
		pd.promote()
	}

	b := assemble(bookPath, files, pd, tags, data[base].probeASIN)

	// Duration/chapter facts are asserted only when EVERY file's probe
	// succeeded - a partial sum would be an undercount stated as fact (a file
	// with no embedded chapters counts as one chapter).
	probedAll := true
	var totalSec float64
	var chapters int
	for _, fd := range data {
		if fd.probe == nil {
			probedAll = false
			break
		}
		totalSec += fd.probe.duration
		if fd.probe.chapters > 0 {
			chapters += fd.probe.chapters
		} else {
			chapters++
		}
	}
	if probedAll {
		// Record the source only when the field is actually emitted (a
		// sub-minute runtime rounds to 0 and is omitted by omitempty).
		if m := int(totalSec/60 + 0.5); m > 0 {
			b.RuntimeMin = m
			b.Sources["runtime_min"] = string(srcTag)
		}
		if chapters > 0 {
			b.Chapters = chapters
			b.Sources["chapters"] = string(srcTag)
		}
	}

	if len(b.Sources) == 0 {
		b.Sources = nil
	}
	return b, pd.pending
}

// tagsCorroborate reports whether the tags name the claimed series (in the
// series tag or as the album), case-folded.
func tagsCorroborate(tags tagInfo, series string) bool {
	s := strings.ToLower(series)
	return strings.ToLower(tags.series) == s || strings.ToLower(tags.album) == s
}

// assemble merges path-derived fields (pd) and the (already merged) tags into a
// Book. It is pure (no I/O), and the disagreement policy is simply the call
// order of the set() lines per field:
//
//	title             -> tag preferred (album; a per-file track title only for
//	                     single-file books, and never a generic part label),
//	                     path/filename fallback
//	author            -> tag preferred, path fallback
//	narrator          -> tag only (the path never names a narrator)
//	series / position -> path preferred, tag fallback (tags rarely carry good
//	                     series data, so the folder structure wins)
//	asin              -> dhowden tag > filename > path > probe container
//	                     (probeASIN last: a deliberate folder rename must be
//	                     able to override a stale embedded ASIN)
//
// pd.title is guaranteed non-empty (derivePath), so a book is never untitled.
func assemble(bookPath string, files []string, pd derived, tags tagInfo, probeASIN string) Book {
	b := Book{Path: bookPath, Files: files, AudioFiles: len(files), Sources: map[string]string{}}
	fileText := strings.Join(files, " ")

	// A multi-file book never takes a per-file track title as the book title
	// (a folder of parts tagged "Chapter 01" must not title the book);
	// single-file books may, unless the title is a generic part label.
	tagTitle := tags.album
	if tagTitle == "" && len(files) == 1 && !isGenericTitle(tags.trackTitle) {
		tagTitle = tags.trackTitle
	}
	b.set(&b.Title, "title", tagTitle, srcTag)
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
	b.set(&b.ASIN, "asin", findASIN(fileText), srcFilename)
	b.set(&b.ASIN, "asin", findASIN(bookPath), srcPath)
	b.set(&b.ASIN, "asin", probeASIN, srcTag)

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
