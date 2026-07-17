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
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

	// OnProgress, if non-nil, reports scan progress at GROUP granularity - a
	// group is one directory's audio files (the unit collectGroups yields, and
	// splitVerdict decides over). total is the number of groups and is fixed for
	// the whole scan; done rises from 0 to total as each group finishes loading
	// its files (the tag reads + optional ffprobe, the scan's latency-bound
	// part). It is called once with (0, total) as soon as the total is known
	// (before any group has loaded), then once more per completed group, ending
	// at (total, total); done is monotonically non-decreasing. On an empty tree
	// the single (0, 0) call is both the first and the last.
	//
	// Concurrency: OnProgress is always invoked from the single goroutine that
	// runs Scan - never concurrently, and serialized with OnBook - so the
	// callback needs no locking of its own. It must not block for long: it runs
	// inline with assembly and stalls the scan. A panic in either callback
	// propagates out of Scan.
	OnProgress func(done, total int)

	// OnBook, if non-nil, is invoked once per assembled book, as soon as that
	// book is assembled. It receives an independent copy and is called exactly
	// len(Result.Books) times over a whole scan.
	//
	// The streamed book is PROVISIONAL. Two finalization steps run only AFTER
	// every book exists, so they are NOT yet reflected in a streamed book:
	//   - sibling corroboration - a tentative series claim parsed out of a
	//     book's name is accepted only once a sibling book vouches for the
	//     series, which can change a streamed book's series/position/title;
	//   - the final deterministic sort by path.
	// So a streamed book's Title/Series/SeriesPosition/Authors may differ from
	// its final form, and books arrive in load-completion order, which is
	// NONDETERMINISTIC - the caller must not rely on ordering. Result.Books
	// (returned by Scan) remains the sole authoritative output; use OnBook for
	// progressive display, not as the result. The book's Path is stable (never
	// touched by corroboration), so streamed and final paths always agree.
	//
	// Concurrency: like OnProgress, always invoked from the single Scan
	// goroutine and serialized with it.
	OnBook func(Book)

	// OnWalk, if non-nil, reports directory-walk progress WHILE the tree is being
	// enumerated (before OnProgress/OnBook, which only fire afterwards during the
	// load phase). dirsScanned is directories read so far; groupsFound is
	// audio-containing directories found so far. Called periodically (throttled),
	// never per-directory; the final counts are not guaranteed (OnProgress(0,total)
	// gives the authoritative group total). Invoked from a SINGLE dedicated
	// goroutine during the walk only, so it never overlaps OnProgress/OnBook and
	// needs no locking; it must not block.
	OnWalk func(dirsScanned, groupsFound int)
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

// pendingBook is a sibling-corroboration claim tied to a book by its index in
// the accumulating books slice (see derived.pending and Scan's post-pass). The
// index is stable because groups are only ever appended to books, never
// reordered before the corroboration pass.
type pendingBook struct {
	idx   int
	claim *nameClaim
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
	groups := collectGroups(absRoot, &stats, opts.OnWalk)

	// Report the total up front so a caller with a progress bar can size it
	// before any group has loaded (Options.OnProgress).
	if opts.OnProgress != nil {
		opts.OnProgress(0, len(groups))
	}

	// Load every file's evidence through the maxWorkers pool, but receive each
	// group's index the moment ITS files finish so assembly can begin
	// immediately - that streaming is what makes an otherwise-opaque Scan
	// observable via the callbacks. Assembly runs here, on the single Scan
	// goroutine, so stats stays race-free and OnProgress/OnBook are serialized.
	// The completion ORDER is nondeterministic, but the final sort below makes
	// the output deterministic regardless, so a nil-callback Scan is identical to
	// loading everything first and assembling in slice order.
	// books starts non-nil so an empty scan marshals as "books": [].
	a := assembler{absRoot: absRoot, ffprobePath: ffprobePath, stats: &stats, onBook: opts.OnBook, books: []Book{}}
	processed := 0
	for gi := range loadGroups(groups, ffprobePath) {
		a.group(groups[gi])
		processed++
		if opts.OnProgress != nil {
			opts.OnProgress(processed, len(groups))
		}
	}
	books, pendings := a.books, a.pendings

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

	// Path alone is not a total order: a folder book and a loose single-file
	// book can share one (dir "Foo" vs "Foo.m4b", whose path is its stem), and
	// assembly runs in nondeterministic load-completion order, so ties break on
	// Files to keep the output deterministic.
	sort.Slice(books, func(i, j int) bool {
		if books[i].Path != books[j].Path {
			return books[i].Path < books[j].Path
		}
		return slices.Compare(books[i].Files, books[j].Files) < 0
	})

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

// walkWorkers bounds the concurrent directory walk. Unlike the per-file load
// (maxWorkers=8, which competes for disk+CPU on tag reads and ffprobe spawns),
// the walk is purely LATENCY-bound: each directory costs one EvalSymlinks +
// ReadDir (plus a Stat per symlink entry) round-trip, dominated by round-trip
// time on a high-latency network mount (SMB/NFS). Keeping more of those requests
// in flight than the file-load pool hides that latency, so a higher concurrency
// is warranted here; the bounded semaphore still caps total in-flight work so a
// large share is never swamped.
const walkWorkers = 16

// walkThrottle is how often the dedicated OnWalk reporter samples the atomic
// walk counters. Reporting is coarse "Scanning folders..." progress, not
// per-directory, so a few hundred ms keeps the UI live without churn.
const walkThrottle = 250 * time.Millisecond

// collectGroups walks the tree (cheap, ReadDir only) and returns each directory
// that directly contains audio files. Directory symlinks are followed
// (symlink-organized libraries are common), with a visited set over resolved
// paths guarding cycles; unreadable or unresolvable directories are counted in
// stats and skipped, never silently dropped.
//
// The walk is CONCURRENT: a bounded pool (walkWorkers) keeps multiple
// EvalSymlinks/ReadDir round-trips in flight at once, which is the win on a
// latency-bound network mount. The returned group ORDER is nondeterministic, but
// Scan re-sorts the resulting books by path (with a Files tie-break), so the
// final Result.Books is byte-identical to a serial walk's - INCLUDING that a
// directory reachable via multiple in-tree aliases (a sibling symlink and the
// real dir) is represented by its lexicographically-SMALLEST alias path, matching
// a serial DFS in sorted-subdir order. The shared-state invariant that survives
// concurrency is the visited check-and-set (atomic under mu): a symlink cycle or
// duplicate entry point can never double-append a directory's group, and the
// smallest claiming alias is chosen deterministically in every interleaving (a
// smaller alias arriving before the append is picked up from visited; one
// arriving after relabels the already-appended group in place - never re-reading
// the directory, so a losing alias costs no extra ReadDir on a network mount).
// Each directory's own audio files are still numeric-sorted (NaturalLess), which
// fixes the parts' track order and so is load-bearing.
//
// If onWalk is non-nil, a SINGLE dedicated ticker goroutine samples the atomic
// dirsScanned/groupsFound counters and calls onWalk (throttled), so the callback
// is never invoked from a walk worker and needs no locking; it is stopped and
// joined before this returns, then fired once more with the finished counts.
func collectGroups(root string, stats *Stats, onWalk func(dirsScanned, groupsFound int)) []groupData {
	var (
		mu     sync.Mutex // guards groups, visited, groupOf, and the stats counters
		groups []groupData
		// visited maps a resolved directory to the lexicographically-SMALLEST
		// in-tree alias (unresolved dir) seen for it so far; groupOf maps a
		// resolved directory to its index in groups (set once that dir has audio).
		// Together they make the recorded alias deterministic: when several in-tree
		// paths (e.g. a sibling symlink and the real dir) resolve to the same
		// audio-bearing directory, the group's dir converges on the smallest alias,
		// matching a serial DFS in sorted-subdir order - without any losing alias
		// re-reading the directory.
		visited = map[string]string{}
		groupOf = map[string]int{}
		wg      sync.WaitGroup
		sem     = make(chan struct{}, walkWorkers)
	)
	var dirsScanned, groupsFound int64 // atomic walk counters for onWalk

	// Optional progress reporter: one dedicated goroutine so the onWalk contract
	// (single goroutine, never overlapping OnProgress/OnBook) holds.
	var reporterWG sync.WaitGroup
	stopReporter := make(chan struct{})
	if onWalk != nil {
		reporterWG.Add(1)
		go func() {
			defer reporterWG.Done()
			t := time.NewTicker(walkThrottle)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					onWalk(int(atomic.LoadInt64(&dirsScanned)), int(atomic.LoadInt64(&groupsFound)))
				case <-stopReporter:
					return
				}
			}
		}()
	}

	// scanDir does one directory's latency-bound work (resolve + read + classify)
	// holding a semaphore slot ONLY for its own duration, and returns the child
	// directory paths to recurse into. The slot is released (deferred) BEFORE the
	// caller spawns child goroutines - holding a slot while waiting on children
	// would deadlock once the tree is deeper than walkWorkers.
	scanDir := func(dir string, isRoot bool) []string {
		sem <- struct{}{}
		defer func() { <-sem }()

		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			mu.Lock()
			stats.UnreadableDirs++
			mu.Unlock()
			return nil
		}
		// The visited check-and-set MUST be atomic so two aliasing paths (a
		// symlink cycle / duplicate entry point) can never both proceed and
		// double-append the same directory's group. When an alias loses the race
		// but is lexicographically smaller than the recorded one, it relabels the
		// group in place (contents are identical - same resolved dir - so only the
		// label changes) and returns WITHOUT re-reading or recursing: this
		// preserves the cycle guard, keeps the smallest-alias guarantee, and never
		// costs a second ReadDir on a network mount.
		mu.Lock()
		if prev, ok := visited[resolved]; ok {
			if dir < prev {
				visited[resolved] = dir
				if gi, ok := groupOf[resolved]; ok {
					groups[gi].dir = dir
				}
			}
			mu.Unlock()
			return nil
		}
		visited[resolved] = dir
		mu.Unlock()

		entries, err := os.ReadDir(dir)
		if err != nil {
			mu.Lock()
			stats.UnreadableDirs++
			mu.Unlock()
			return nil
		}
		// dirsScanned counts directories SUCCESSFULLY READ (visited-skipped and
		// unreadable dirs are not counted), so it is coarse walk progress for
		// OnWalk, not a precise directory total.
		atomic.AddInt64(&dirsScanned, 1)

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
					mu.Lock()
					stats.UnreadableDirs++ // dangling symlink
					mu.Unlock()
				}
			case isAudio(name):
				audio = append(audio, name)
			}
		}
		// audio orders a folder book's parts (they become Book.Files, i.e. the
		// track order), so use numeric-aware sorting: unpadded "Chapter 2.mp3"
		// must precede "Chapter 10.mp3". subdirs only drives walk order (books
		// are re-sorted by path in Scan), so a plain sort is fine there.
		sort.SliceStable(audio, func(i, j int) bool { return NaturalLess(audio[i], audio[j]) })
		sort.Strings(subdirs)
		if len(audio) > 0 {
			mu.Lock()
			// Read the current smallest alias rather than this goroutine's own dir:
			// a smaller sibling alias may have updated visited[resolved] while we
			// were reading. (A smaller alias arriving AFTER this append relabels the
			// group via groupOf, above - so either interleaving yields the smallest.)
			d := visited[resolved]
			groups = append(groups, groupData{group: group{dir: d, files: audio, isRoot: isRoot}})
			groupOf[resolved] = len(groups) - 1
			mu.Unlock()
			atomic.AddInt64(&groupsFound, 1)
		}
		children := make([]string, len(subdirs))
		for i, sd := range subdirs {
			children[i] = filepath.Join(dir, sd)
		}
		return children
	}

	var walk func(dir string, isRoot bool)
	walk = func(dir string, isRoot bool) {
		defer wg.Done()
		for _, child := range scanDir(dir, isRoot) {
			wg.Add(1)
			// One goroutine per directory: the WORK is bounded by the walkWorkers
			// semaphore, but the spawned goroutines are not. At audiobook-library
			// scale this is fine; a pathological tree with hundreds of thousands of
			// dirs could pile up blocked goroutines - an acceptable tradeoff versus
			// the complexity of a fixed worker-queue.
			go walk(child, false)
		}
	}
	wg.Add(1)
	go walk(root, true)
	wg.Wait()

	if onWalk != nil {
		close(stopReporter)
		reporterWG.Wait()
		// One final authoritative sample so a walk that finished between ticks
		// still reports its totals (OnProgress(0,total) remains the authoritative
		// group total per OnWalk's doc). Safe unsynchronized read: every walker
		// has completed (wg.Wait) and the reporter has stopped.
		onWalk(int(atomic.LoadInt64(&dirsScanned)), int(atomic.LoadInt64(&groupsFound)))
	}

	return groups
}

// loadGroups gathers every file's evidence - the dhowden tag read, the optional
// ffprobe result, and the pre-computed merged view - fanning the per-file work
// (the scan's latency-bound part) across goroutines bounded by a semaphore to
// maxWorkers in flight, exactly as a load-everything-first pass would. What it
// adds for a streaming caller is per-GROUP completion: it returns a channel that
// yields a group's index the moment ALL of that group's files have loaded, and
// closes it once every group is done. Each goroutine writes only its own
// fileData slot; the atomic remaining-count plus the channel send/receive
// publish those writes, so a received group's data is race-free to read. Every
// group has at least one file (collectGroups drops audioless dirs), so exactly
// len(groups) indices are sent. The channel is buffered to len(groups) so a
// send never blocks on a busy consumer.
func loadGroups(groups []groupData, ffprobePath string) <-chan int {
	ready := make(chan int, len(groups))
	sem := make(chan struct{}, maxWorkers)
	remaining := make([]int32, len(groups))
	var wg sync.WaitGroup
	for gi := range groups {
		g := &groups[gi]
		g.data = make([]fileData, len(g.files))
		remaining[gi] = int32(len(g.files))
		if len(g.files) == 0 {
			// collectGroups never emits an audioless group, but a dropped one
			// would silently lose its book AND strand the progress count below
			// (total, total) - yield it like any other group (the buffered
			// channel makes this send non-blocking).
			ready <- gi
			continue
		}
		for fi := range groups[gi].files {
			wg.Add(1)
			go func(gi, fi int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				g := &groups[gi]
				slot := &g.data[fi]
				path := filepath.Join(g.dir, g.files[fi])
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
				if atomic.AddInt32(&remaining[gi], -1) == 0 {
					ready <- gi
				}
			}(gi, fi)
		}
	}
	go func() {
		wg.Wait()
		close(ready)
	}()
	return ready
}

// assembler accumulates Scan's assembly state - the growing books slice, the
// sibling-corroboration claims, and the stats counters - so the per-group work
// is a method rather than a parameter thicket, and a future PER-BOOK
// observation hook lands as a field, not another signature change (loop-level
// hooks like OnProgress, which need the group count rather than assembly
// state, stay in Scan's driving loop). Used only from Scan's single goroutine,
// so the stats writes are race-free.
type assembler struct {
	absRoot     string
	ffprobePath string
	stats       *Stats
	onBook      func(Book)
	books       []Book
	pendings    []pendingBook
}

// group builds the book(s) for one fully-loaded group, appending them to
// a.books, collecting any sibling-corroboration claims into a.pendings, and
// folding the group's per-file and split-verdict counters into a.stats. It is
// the body of Scan's assembly loop, factored out so the loop can drive groups
// in load-completion order. a.onBook, if non-nil, receives an independent copy
// of each assembled book.
func (a *assembler) group(g groupData) {
	for _, fd := range g.data {
		if fd.failed {
			a.stats.TagFailures++
		}
		if a.ffprobePath != "" && fd.probe == nil {
			a.stats.ProbeFailures++
		}
	}

	// Loose root files are unconditionally individual books; elsewhere the
	// files' own tags decide (never filenames alone). The verdict sees the
	// SAME merged tag view the books are built from, so probe-visible distinct
	// albums also prevent a wrong merge.
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
			b, claim := buildBook(g.dir, a.absRoot, []string{f}, false, g.data[i:i+1])
			a.emit(b, claim)
		}
	case verdictKeepAmbiguous:
		a.stats.AmbiguousDirs++
		fallthrough
	default:
		b, claim := buildBook(g.dir, a.absRoot, g.files, true, g.data)
		a.emit(b, claim)
	}
}

// emit appends one assembled book (and its corroboration claim, if any) and
// streams a provisional copy to the OnBook callback.
func (a *assembler) emit(b Book, claim *nameClaim) {
	if claim != nil {
		a.pendings = append(a.pendings, pendingBook{idx: len(a.books), claim: claim})
	}
	a.books = append(a.books, b)
	if a.onBook != nil {
		// A shallow struct copy would still alias the slices/map the
		// corroboration pass mutates in place, so clone deeply: the streamed
		// book must be a stable provisional snapshot.
		a.onBook(cloneBook(b))
	}
}

// cloneBook returns a copy of b whose slices and sources map are duplicated, so a
// streamed provisional book handed to OnBook is independent of the retained book
// (which the later sibling-corroboration pass mutates in place) and of the
// caller. Nil-ness of the optional slices/map is preserved so a re-marshal keeps
// the same omitempty shape.
func cloneBook(b Book) Book {
	c := b
	c.Authors = slices.Clone(b.Authors)
	c.Narrators = slices.Clone(b.Narrators)
	c.Files = slices.Clone(b.Files)
	c.Sources = maps.Clone(b.Sources)
	return c
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
