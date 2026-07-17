package scan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// mkTree creates the given relative file paths as empty files under a temp dir
// and returns the root. Directories are created as needed.
func mkTree(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// scanNoProbe scans without ffprobe so grouping/derivation tests are deterministic
// and hermetic (empty files carry no tags).
func scanNoProbe(t *testing.T, root string) (*Result, Stats) {
	t.Helper()
	res, stats, err := Scan(root, Options{FFprobePath: ""})
	if err != nil {
		t.Fatal(err)
	}
	return res, stats
}

func TestGrouping(t *testing.T) {
	root := mkTree(t,
		// A folder-per-book under Author/Series.
		"Lee Child/Jack Reacher/01 - Killing Floor/Killing Floor.m4b",
		// A multi-file folder book (both files are parts of one book).
		"Brandon Sanderson/Mistborn/Book 2 - Well of Ascension/part1.mp3",
		"Brandon Sanderson/Mistborn/Book 2 - Well of Ascension/part2.mp3",
		// Author/Book (one ancestor).
		"Neil Gaiman/Good Omens/good-omens.m4b",
		// A loose file at the scan root -> its own single-file book.
		"Loose Standalone.mp3",
		// Non-audio files are ignored.
		"Lee Child/Jack Reacher/01 - Killing Floor/cover.jpg",
		"notes.txt",
	)

	res, stats := scanNoProbe(t, root)

	var paths []string
	for _, b := range res.Books {
		paths = append(paths, b.Path)
	}
	want := []string{
		"Brandon Sanderson/Mistborn/Book 2 - Well of Ascension",
		"Lee Child/Jack Reacher/01 - Killing Floor",
		"Loose Standalone",
		"Neil Gaiman/Good Omens",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("book paths = %v, want %v", paths, want)
	}
	// Output must be sorted by path (deterministic).
	if !sort.StringsAreSorted(paths) {
		t.Errorf("book paths not sorted: %v", paths)
	}

	byPath := map[string]Book{}
	for _, b := range res.Books {
		byPath[b.Path] = b
	}

	// The multi-file folder is ONE book with both parts.
	mistborn := byPath["Brandon Sanderson/Mistborn/Book 2 - Well of Ascension"]
	if mistborn.AudioFiles != 2 || !reflect.DeepEqual(mistborn.Files, []string{"part1.mp3", "part2.mp3"}) {
		t.Errorf("Mistborn files = %v (count %d), want the two parts", mistborn.Files, mistborn.AudioFiles)
	}
	if mistborn.Series != "Mistborn" || mistborn.SeriesPosition != "2" || mistborn.Authors[0] != "Brandon Sanderson" {
		t.Errorf("Mistborn derivation wrong: %+v", mistborn)
	}

	// Author/Book: author derived, no series.
	go2 := byPath["Neil Gaiman/Good Omens"]
	if go2.Series != "" || len(go2.Authors) != 1 || go2.Authors[0] != "Neil Gaiman" {
		t.Errorf("Good Omens: want author Neil Gaiman and no series, got %+v", go2)
	}

	// Loose root file: single-file book, title from filename, no hierarchy.
	loose := byPath["Loose Standalone"]
	if loose.Title != "Loose Standalone" || loose.Sources["title"] != "filename" || loose.Series != "" {
		t.Errorf("Loose Standalone wrong: %+v", loose)
	}

	// The untagged multi-file folder (Mistborn) was kept as one book with no tag
	// evidence either way, so it must be counted as ambiguous for the summary.
	if stats.AmbiguousDirs != 1 {
		t.Errorf("stats.AmbiguousDirs = %d, want 1 (the untagged Mistborn folder)", stats.AmbiguousDirs)
	}
}

// TestScanProgressCallback pins the OnProgress contract: (0, total) first, done
// monotonically non-decreasing, ending at (total, total), with total equal to
// the group count (here each group is one book).
func TestScanProgressCallback(t *testing.T) {
	root := mkTree(t,
		"A/Book One/x.m4b", // group A/Book One
		"A/Book Two/y.m4b", // group A/Book Two
		"loose.mp3",        // group root
	)
	var calls [][2]int
	res, _, err := Scan(root, Options{OnProgress: func(done, total int) {
		calls = append(calls, [2]int{done, total})
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) == 0 {
		t.Fatal("OnProgress never called")
	}
	const wantTotal = 3 // A/Book One, A/Book Two, and the root's loose file
	if first := calls[0]; first[0] != 0 || first[1] != wantTotal {
		t.Fatalf("first call = (%d,%d), want (0,%d)", first[0], first[1], wantTotal)
	}
	prev := -1
	for i, c := range calls {
		if c[1] != wantTotal {
			t.Errorf("call %d total = %d, want %d", i, c[1], wantTotal)
		}
		if c[0] < prev {
			t.Errorf("call %d done = %d decreased from %d", i, c[0], prev)
		}
		prev = c[0]
	}
	if last := calls[len(calls)-1]; last[0] != wantTotal || last[1] != wantTotal {
		t.Errorf("last call = (%d,%d), want (%d,%d)", last[0], last[1], wantTotal, wantTotal)
	}
	// Each group is one book here, so the group total equals the book count.
	if len(res.Books) != wantTotal {
		t.Fatalf("want %d books, got %d", wantTotal, len(res.Books))
	}
}

// TestScanBookCallback pins the OnBook contract: it fires exactly once per final
// book and the streamed paths set equals the final paths set.
func TestScanBookCallback(t *testing.T) {
	root := mkTree(t,
		"Lee Child/Jack Reacher/01 - Killing Floor/x.m4b",
		"Neil Gaiman/Good Omens/good-omens.m4b",
		"Loose Standalone.mp3",
	)
	var streamed []Book
	res, _, err := Scan(root, Options{OnBook: func(b Book) { streamed = append(streamed, b) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(streamed) != len(res.Books) {
		t.Fatalf("OnBook fired %d times, want %d (len Result.Books)", len(streamed), len(res.Books))
	}
	streamedPaths := map[string]bool{}
	for _, b := range streamed {
		if streamedPaths[b.Path] {
			t.Errorf("path %q streamed twice", b.Path)
		}
		streamedPaths[b.Path] = true
	}
	finalPaths := map[string]bool{}
	for _, b := range res.Books {
		finalPaths[b.Path] = true
	}
	if !reflect.DeepEqual(streamedPaths, finalPaths) {
		t.Errorf("streamed paths %v != final paths %v", streamedPaths, finalPaths)
	}
}

// TestScanBookCallbackProvisional shows a streamed book is PROVISIONAL: sibling
// corroboration runs only after every book exists, so the streamed
// "Jack Reacher 3 - Tripwire" is still uncorroborated (no series, whole name as
// title) while the final Result.Books carries the corrected series/position/title.
// (Same fixture shape as TestSiblingCorroboration.)
func TestScanBookCallbackProvisional(t *testing.T) {
	root := mkTree(t,
		// Solid sibling: zero-padded position under Author/Series.
		"Lee Child/Jack Reacher/01 - Killing Floor/x.m4b",
		// Tentative: unpadded same-series claim, loose at the root.
		"Jack Reacher 3 - Tripwire.m4b",
	)
	streamed := map[string]Book{}
	res, _, err := Scan(root, Options{OnBook: func(b Book) { streamed[b.Path] = b }})
	if err != nil {
		t.Fatal(err)
	}
	tw, ok := streamed["Jack Reacher 3 - Tripwire"]
	if !ok {
		t.Fatal("Tripwire never streamed")
	}
	// Streamed (provisional): the claim is not yet corroborated.
	if tw.Series != "" || tw.SeriesPosition != "" || tw.Title != "Jack Reacher 3 - Tripwire" {
		t.Errorf("streamed book should be uncorroborated, got %+v", tw)
	}
	// Final (authoritative): corroborated by the solid sibling.
	byPath := map[string]Book{}
	for _, b := range res.Books {
		byPath[b.Path] = b
	}
	final := byPath["Jack Reacher 3 - Tripwire"]
	if final.Series != "Jack Reacher" || final.SeriesPosition != "3" || final.Title != "Tripwire" {
		t.Errorf("final book should be corroborated, got %+v", final)
	}
}

// TestScanDeterministic pins that the final Result is deterministic regardless of
// the nondeterministic group load-completion order the streaming loader uses.
func TestScanDeterministic(t *testing.T) {
	root := mkTree(t,
		"Lee Child/Jack Reacher/01 - Killing Floor/x.m4b",
		"Brandon Sanderson/Mistborn/Book 2 - Well of Ascension/part1.mp3",
		"Brandon Sanderson/Mistborn/Book 2 - Well of Ascension/part2.mp3",
		"Neil Gaiman/Good Omens/good-omens.m4b",
		"Jack Reacher 3 - Tripwire.m4b",
		"Loose Standalone.mp3",
	)
	res1, _, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	res2, _, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res1, res2) {
		t.Errorf("scan not deterministic:\n%+v\n%+v", res1, res2)
	}
}

func TestScanRootMissing(t *testing.T) {
	if _, _, err := Scan(filepath.Join(t.TempDir(), "nope"), Options{}); err == nil {
		t.Fatal("expected an error for a missing root")
	}
}

func TestScanEnvelope(t *testing.T) {
	root := mkTree(t, "a.mp3")
	res, _ := scanNoProbe(t, root)
	if res.Format != Format || res.Version != Version {
		t.Errorf("envelope = %q v%d, want %q v%d", res.Format, res.Version, Format, Version)
	}
	if !filepath.IsAbs(res.Root) {
		t.Errorf("root %q should be absolute", res.Root)
	}
}

// TestAssembleDisagreement exercises the merge policy directly (no I/O): tag wins
// for title/narrator/author, path wins for series/position, ASIN is hunted.
func TestAssembleDisagreement(t *testing.T) {
	pd := derived{
		title: "Path Title", titleSrc: "path",
		author: "Path Author", authorSrc: "path",
		series: "Path Series", seriesSrc: "path",
		position: "3", posSrc: "path",
	}
	tags := tagInfo{
		album:     "Tag Title",
		authors:   []string{"Tag Author"},
		narrators: []string{"Some Narrator"},
		series:    "Tag Series",
		position:  "9",
	}

	b := assemble("Path Series/03 - Book", []string{"03 - Book.m4b"}, pd, tags, "")

	// title: tag wins.
	if b.Title != "Tag Title" || b.Sources["title"] != "tag" {
		t.Errorf("title: got %q (%s), want Tag Title (tag)", b.Title, b.Sources["title"])
	}
	// author: tag wins.
	if b.Authors[0] != "Tag Author" || b.Sources["authors"] != "tag" {
		t.Errorf("author: got %v (%s), want Tag Author (tag)", b.Authors, b.Sources["authors"])
	}
	// narrator: tag only.
	if b.Narrators[0] != "Some Narrator" || b.Sources["narrators"] != "tag" {
		t.Errorf("narrator: got %v (%s)", b.Narrators, b.Sources["narrators"])
	}
	// series: PATH wins over the tag.
	if b.Series != "Path Series" || b.Sources["series"] != "path" {
		t.Errorf("series: got %q (%s), want Path Series (path)", b.Series, b.Sources["series"])
	}
	// position: PATH wins over the tag.
	if b.SeriesPosition != "3" || b.Sources["series_position"] != "path" {
		t.Errorf("position: got %q (%s), want 3 (path)", b.SeriesPosition, b.Sources["series_position"])
	}
}

func TestAssembleFallbacks(t *testing.T) {
	// No tags at all: everything falls back to the path/filename derivation.
	pd := derived{
		title: "Killing Floor", titleSrc: "path",
		author: "Lee Child", authorSrc: "path",
		series: "Jack Reacher", seriesSrc: "path",
		position: "1", posSrc: "path",
	}
	b := assemble("Lee Child/Jack Reacher/01 - Killing Floor",
		[]string{"Killing Floor [B076HYPQLK].m4b"}, pd, tagInfo{}, "")

	if b.Title != "Killing Floor" || b.Sources["title"] != "path" {
		t.Errorf("title fallback wrong: %q (%s)", b.Title, b.Sources["title"])
	}
	if b.Sources["authors"] != "path" || b.Sources["series"] != "path" {
		t.Errorf("path provenance not recorded: %v", b.Sources)
	}
	// ASIN pulled from the file name when tags lack it.
	if b.ASIN != "B076HYPQLK" || b.Sources["asin"] != "filename" {
		t.Errorf("asin from filename: got %q (%s)", b.ASIN, b.Sources["asin"])
	}
}

func TestAssembleUntitledFallback(t *testing.T) {
	// Neither tags nor a parseable title -> derivePath guarantees the raw
	// identity segment as the title, so a book is never untitled.
	pd := derivePath("weird", srcFilename, nil)
	b := assemble("weird", []string{"weird.mp3"}, pd, tagInfo{}, "")
	if b.Title != "weird" || b.Sources["title"] != "filename" {
		t.Errorf("untitled fallback: got %q (%s)", b.Title, b.Sources["title"])
	}
}

// TestScanEmptyMarshalsBooksArray pins the wire shape for an empty scan:
// "books" must be [], never null.
func TestScanEmptyMarshalsBooksArray(t *testing.T) {
	res, _ := scanNoProbe(t, t.TempDir())
	if res.Books == nil {
		t.Fatal("Books must be non-nil so it marshals as [], not null")
	}
	if len(res.Books) != 0 {
		t.Fatalf("empty scan yielded %d books", len(res.Books))
	}
}

// TestNoPhantomSeries pins the Fahrenheit-451 guard end to end: a loose file
// whose name accidentally fits "Series NN - Title" must NOT invent a series -
// the whole name stays the title.
func TestNoPhantomSeries(t *testing.T) {
	root := mkTree(t,
		"Fahrenheit 451 - Ray Bradbury.m4b",
		"Catch 22 - HarperAudio.mp3",
	)
	res, stats := scanNoProbe(t, root)
	if len(res.Books) != 2 {
		t.Fatalf("want 2 books, got %d", len(res.Books))
	}
	for _, b := range res.Books {
		if b.Series != "" || b.SeriesPosition != "" {
			t.Errorf("%q: phantom series %q pos %q", b.Path, b.Series, b.SeriesPosition)
		}
	}
	byPath := map[string]Book{}
	for _, b := range res.Books {
		byPath[b.Path] = b
	}
	if b := byPath["Fahrenheit 451 - Ray Bradbury"]; b.Title != "Fahrenheit 451 - Ray Bradbury" {
		t.Errorf("title mangled: %q", b.Title)
	}
	if b := byPath["Catch 22 - HarperAudio"]; b.Title != "Catch 22 - HarperAudio" {
		t.Errorf("title mangled: %q", b.Title)
	}
	if stats.WithSeries != 0 {
		t.Errorf("stats.WithSeries = %d, want 0", stats.WithSeries)
	}
}

// TestSiblingCorroboration: an unpadded name-embedded series claim is accepted
// when a sibling book asserts the same series through solid evidence.
func TestSiblingCorroboration(t *testing.T) {
	root := mkTree(t,
		// Solid: folder book with a zero-padded position under Author/Series.
		"Lee Child/Jack Reacher/01 - Killing Floor/x.m4b",
		// Tentative: unpadded claim of the same series, loose at the root.
		"Jack Reacher 3 - Tripwire.m4b",
	)
	res, _ := scanNoProbe(t, root)
	byPath := map[string]Book{}
	for _, b := range res.Books {
		byPath[b.Path] = b
	}
	tw := byPath["Jack Reacher 3 - Tripwire"]
	if tw.Series != "Jack Reacher" || tw.SeriesPosition != "3" || tw.Title != "Tripwire" {
		t.Errorf("sibling-corroborated claim not applied: %+v", tw)
	}
	if tw.Sources["series"] != "filename" || tw.Sources["title"] != "filename" {
		t.Errorf("claim provenance wrong: %v", tw.Sources)
	}

	// Two accidental shapes must NOT vouch for each other.
	root2 := mkTree(t,
		"Catch 22 - HarperAudio.mp3",
		"Catch 23 - HarperAudio.mp3",
	)
	res2, _ := scanNoProbe(t, root2)
	for _, b := range res2.Books {
		if b.Series != "" {
			t.Errorf("mutual tentative claims corroborated each other: %+v", b)
		}
	}
}

// TestTagCorroboration: a book's own SERIES tag naming the claimed series
// confirms an unpadded name-embedded claim. ffmpeg-gated.
func TestTagCorroboration(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	root := t.TempDir()
	file := filepath.Join(root, "Jack Reacher 3 - Tripwire.mp3")
	cmd := exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", "1", "-c:a", "libmp3lame",
		"-metadata", "SERIES=Jack Reacher",
		file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate the fixture: %v\n%s", err, out)
	}
	res, _ := scanNoProbe(t, root)
	if len(res.Books) != 1 {
		t.Fatalf("want 1 book, got %d", len(res.Books))
	}
	b := res.Books[0]
	if b.Series != "Jack Reacher" || b.SeriesPosition != "3" || b.Title != "Tripwire" {
		t.Errorf("tag-corroborated claim not applied: %+v", b)
	}
}

// TestSymlinkedDirsFollowed: a symlink-organized library must scan, and a
// symlink cycle must not hang.
func TestSymlinkedDirsFollowed(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target", "Neil Gaiman", "Good Omens")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "x.m4b"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "scanroot")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "target"), filepath.Join(root, "library")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	// A cycle back to the scan root must be guarded, not looped.
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}

	res, stats := scanNoProbe(t, root)
	if len(res.Books) != 1 {
		t.Fatalf("symlinked library not scanned: %d books", len(res.Books))
	}
	if b := res.Books[0]; b.Path != "library/Neil Gaiman/Good Omens" || b.Authors[0] != "Neil Gaiman" {
		t.Errorf("symlinked book wrong: %+v", b)
	}
	if stats.UnreadableDirs != 0 {
		t.Errorf("UnreadableDirs = %d, want 0", stats.UnreadableDirs)
	}
}

// TestPartialProbeOmitsFacts: if ANY file's probe fails, runtime_min/chapters
// are omitted (a partial sum is an undercount asserted as fact) and the
// failure is counted.
func TestPartialProbeOmitsFacts(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "Broken Book")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", "1", "-c:a", "libmp3lame", filepath.Join(dir, "01 - real.mp3"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate the fixture: %v\n%s", err, out)
	}
	// An empty file: ffprobe fails on it.
	if err := os.WriteFile(filepath.Join(dir, "02 - broken.mp3"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	res, stats, err := Scan(root, Options{FFprobePath: "ffprobe"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Books) != 1 {
		t.Fatalf("want 1 book, got %d", len(res.Books))
	}
	b := res.Books[0]
	if b.RuntimeMin != 0 || b.Chapters != 0 {
		t.Errorf("partial probe must omit runtime/chapters, got runtime=%d chapters=%d", b.RuntimeMin, b.Chapters)
	}
	if stats.ProbeFailures != 1 {
		t.Errorf("ProbeFailures = %d, want 1", stats.ProbeFailures)
	}
}

// TestScanWithFFprobe generates a tiny tagged m4a with ffmpeg (skipping if ffmpeg
// is absent, mirroring the server's scanner tests) and checks tags + enrichment
// flow end to end.
func TestScanWithFFprobe(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}

	root := t.TempDir()
	dir := filepath.Join(root, "Lee Child", "Jack Reacher", "01 - Killing Floor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// mp3 so the ASIN rides in an ID3 TXXX frame (arbitrary MP4 atoms are dropped
	// by ffmpeg, so an .m4a fixture cannot carry a custom ASIN tag).
	file := filepath.Join(dir, "book.mp3")
	// 1s of silence, tagged with an album title, artist, and an ASIN.
	cmd := exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", "1", "-c:a", "libmp3lame",
		"-metadata", "album=Killing Floor",
		"-metadata", "artist=Lee Child",
		"-metadata", "ASIN=B076HYPQLK",
		file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate the fixture: %v\n%s", err, out)
	}

	res, stats, err := Scan(root, Options{FFprobePath: "ffprobe"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Books) != 1 {
		t.Fatalf("want 1 book, got %d", len(res.Books))
	}
	b := res.Books[0]
	if b.Title != "Killing Floor" || b.Sources["title"] != "tag" {
		t.Errorf("title from tag: got %q (%s)", b.Title, b.Sources["title"])
	}
	if len(b.Authors) != 1 || b.Authors[0] != "Lee Child" {
		t.Errorf("author from tag: got %v", b.Authors)
	}
	// ASIN should be found (tag atom via dhowden or ffprobe container tag).
	if b.ASIN != "B076HYPQLK" {
		t.Errorf("asin: got %q, want B076HYPQLK", b.ASIN)
	}
	// ffprobe enrichment: at least one chapter counted for the single file.
	if b.Chapters < 1 {
		t.Errorf("chapters: got %d, want >= 1", b.Chapters)
	}
	// Series/position still come from the path.
	if b.Series != "Jack Reacher" || b.SeriesPosition != "1" {
		t.Errorf("path series/position: got %q / %q", b.Series, b.SeriesPosition)
	}
	if stats.WithASIN != 1 {
		t.Errorf("stats.WithASIN = %d, want 1", stats.WithASIN)
	}
}

// TestCollectionSplit reproduces the flat-series-folder layout (two single-file
// books loose in one Series/ folder, like the server's Will Wight/Cradle
// fixtures) with real tagged files, and checks the tag evidence splits them into
// separate books with the folder feeding series/author and the filename the
// position. ffmpeg-gated like TestScanWithFFprobe.
func TestCollectionSplit(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}

	root := t.TempDir()
	dir := filepath.Join(root, "Will Wight", "Cradle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mkTagged := func(name, album string) {
		cmd := exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
			"-t", "1", "-c:a", "libmp3lame",
			"-metadata", "album="+album,
			"-metadata", "artist=Will Wight",
			filepath.Join(dir, name))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("ffmpeg could not generate the fixture: %v\n%s", err, out)
		}
	}
	mkTagged("01 - Unsouled.mp3", "Unsouled")
	mkTagged("02 - Soulsmith.mp3", "Soulsmith")

	res, stats := scanNoProbe(t, root)
	if len(res.Books) != 2 {
		t.Fatalf("want 2 split books, got %d: %+v", len(res.Books), res.Books)
	}
	if stats.AmbiguousDirs != 0 {
		t.Errorf("split folder must not count as ambiguous, got %d", stats.AmbiguousDirs)
	}

	first, second := res.Books[0], res.Books[1]
	if first.Path != "Will Wight/Cradle/01 - Unsouled" || second.Path != "Will Wight/Cradle/02 - Soulsmith" {
		t.Fatalf("split paths wrong: %q / %q", first.Path, second.Path)
	}
	for i, b := range []Book{first, second} {
		wantTitle := []string{"Unsouled", "Soulsmith"}[i]
		wantPos := []string{"1", "2"}[i]
		if b.Title != wantTitle || b.Sources["title"] != "tag" {
			t.Errorf("book %d title: got %q (%s), want %q (tag)", i, b.Title, b.Sources["title"], wantTitle)
		}
		if b.Series != "Cradle" || b.Sources["series"] != "path" {
			t.Errorf("book %d series: got %q (%s), want Cradle (path)", i, b.Series, b.Sources["series"])
		}
		if b.SeriesPosition != wantPos || b.Sources["series_position"] != "filename" {
			t.Errorf("book %d position: got %q (%s), want %q (filename)", i, b.SeriesPosition, b.Sources["series_position"], wantPos)
		}
		if len(b.Authors) != 1 || b.Authors[0] != "Will Wight" {
			t.Errorf("book %d authors: got %v, want Will Wight", i, b.Authors)
		}
		if b.AudioFiles != 1 || len(b.Files) != 1 {
			t.Errorf("book %d must be single-file, got %v", i, b.Files)
		}
	}
}

// A folder book and a loose single-file book can share a Path ("Foo" the dir vs
// "Foo.m4b", whose path is its stem), and assembly order is nondeterministic -
// the Files tie-breaker must keep the sorted output stable across runs.
func TestScanDeterministicOnPathCollision(t *testing.T) {
	root := mkTree(t,
		"Foo/part1.mp3",
		"Foo/part2.mp3",
		"Foo.m4b",
	)
	res1, _ := scanNoProbe(t, root)
	if len(res1.Books) != 2 {
		t.Fatalf("want 2 books, got %+v", res1.Books)
	}
	if res1.Books[0].Path != "Foo" || res1.Books[1].Path != "Foo" {
		t.Fatalf("want both paths %q, got %q and %q", "Foo", res1.Books[0].Path, res1.Books[1].Path)
	}
	for range 10 {
		res2, _ := scanNoProbe(t, root)
		if !reflect.DeepEqual(res1, res2) {
			t.Fatalf("scan not deterministic on a path collision:\n%+v\n%+v", res1, res2)
		}
	}
}

// TestParallelWalkDeepWide builds a deep AND wide tree with many audio-bearing
// directories plus a symlink cycle back to the root, and asserts the parallel
// walk finds exactly the audio directories once each (no double-append from the
// cycle), deterministically across repeated runs. Run under -race by the gate,
// this exercises the concurrent walk's shared-state guards.
func TestParallelWalkDeepWide(t *testing.T) {
	root := t.TempDir()
	var want []string
	// Wide: 20 top-level author dirs; deep: each has a 6-level nested chain, and
	// several intermediate levels ALSO carry audio (so a group is found at
	// multiple depths, not just leaves). More directories than walkWorkers, so
	// the semaphore genuinely gates.
	for a := range 20 {
		author := fmt.Sprintf("Author %02d", a)
		dir := author
		for depth := range 6 {
			dir = filepath.Join(dir, fmt.Sprintf("level%d", depth))
			// Put audio at every even depth to spread groups through the tree.
			if depth%2 == 0 {
				rel := filepath.ToSlash(dir)
				p := filepath.Join(root, filepath.FromSlash(dir), "book.m4b")
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				want = append(want, rel)
			} else {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	sort.Strings(want)

	// A cycle back to the scan root must be guarded, not looped or double-counted.
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	res1, stats1 := scanNoProbe(t, root)
	var got1 []string
	for _, b := range res1.Books {
		got1 = append(got1, b.Path)
	}
	if !reflect.DeepEqual(got1, want) {
		t.Fatalf("parallel walk book paths mismatch\n got %v\nwant %v", got1, want)
	}
	// The cycle back to root must not have produced any group (or a double-append):
	// exactly one book per audio dir, no duplicates.
	seen := map[string]bool{}
	for _, p := range got1 {
		if seen[p] {
			t.Fatalf("path %q appears twice - visited guard failed under concurrency", p)
		}
		seen[p] = true
	}
	// The loop symlink resolves to the already-visited root, so it is skipped
	// cleanly (not counted as unreadable).
	if stats1.UnreadableDirs != 0 {
		t.Errorf("UnreadableDirs = %d, want 0 (the root cycle is skipped, not unreadable)", stats1.UnreadableDirs)
	}

	// Determinism: the full Result must be byte-identical across many runs despite
	// the nondeterministic walk/load ordering.
	for range 15 {
		res2, _ := scanNoProbe(t, root)
		if !reflect.DeepEqual(res1, res2) {
			t.Fatalf("parallel scan not deterministic:\n%+v\n%+v", res1.Books, res2.Books)
		}
	}
}

// TestParallelWalkMatchesSerial pins that the parallel walk produces the same
// final books as a reference serial walk of the same tree, for a nontrivial
// tree - the core "byte-identical to the serial version" guarantee.
func TestParallelWalkMatchesSerial(t *testing.T) {
	files := []string{
		"Lee Child/Jack Reacher/01 - Killing Floor/x.m4b",
		"Lee Child/Jack Reacher/02 - Die Trying/y.m4b",
		"Brandon Sanderson/Mistborn/Book 1/part1.mp3",
		"Brandon Sanderson/Mistborn/Book 1/part2.mp3",
		"Brandon Sanderson/Mistborn/Book 2 - Well of Ascension/a.mp3",
		"Brandon Sanderson/Stormlight/01 - The Way of Kings/wok.m4b",
		"Neil Gaiman/Good Omens/good-omens.m4b",
		"Loose Standalone.mp3",
		"Another Loose.m4b",
	}
	root := mkTree(t, files...)

	// Reference: the serial walk logic (visited-guarded DFS in sorted subdir
	// order), reproduced here independently of the production concurrent walk.
	serial := serialGroups(t, root)

	res, _ := scanNoProbe(t, root)
	var got []string
	for _, b := range res.Books {
		got = append(got, b.Path)
	}
	if !reflect.DeepEqual(got, serial) {
		t.Fatalf("parallel books != serial reference\n got %v\nwant %v", got, serial)
	}
}

// serialGroups is an independent, single-threaded reference walk that returns the
// sorted book-path set a tree yields (each audio-bearing dir = one book here,
// since the fixtures are untagged so folders stay whole). It deliberately does
// NOT share code with the production collectGroups.
func serialGroups(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	visited := map[string]bool{}
	var walk func(dir string, isRoot bool)
	walk = func(dir string, isRoot bool) {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return
		}
		if visited[resolved] {
			return
		}
		visited[resolved] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		var hasAudio bool
		var subdirs, audio []string
		for _, e := range entries {
			name := e.Name()
			if name != "" && name[0] == '.' {
				continue
			}
			if e.IsDir() {
				subdirs = append(subdirs, name)
			} else if isAudio(name) {
				hasAudio = true
				audio = append(audio, name)
			}
		}
		sort.Strings(subdirs)
		if hasAudio {
			rel, _ := filepath.Rel(root, dir)
			if isRoot {
				// Loose root files are individual single-file books.
				for _, f := range audio {
					paths = append(paths, stem(f))
				}
			} else {
				paths = append(paths, filepath.ToSlash(rel))
			}
		}
		for _, sd := range subdirs {
			walk(filepath.Join(dir, sd), false)
		}
	}
	walk(root, true)
	sort.Strings(paths)
	return paths
}

// TestParallelWalkSiblingAliasDeterministic pins that when two in-tree siblings
// resolve to the SAME audio-bearing directory (a real subdir and a symlink to it),
// the concurrent walk deterministically represents the book by its
// lexicographically-SMALLEST alias path, matching a serial DFS. Here "Link" < "Real",
// so every run must yield exactly one book at "Parent/Link". Under the pre-fix code
// the recorded alias was whichever goroutine won the visited race, so the Path
// flipped run-to-run between "Parent/Link" and "Parent/Real".
func TestParallelWalkSiblingAliasDeterministic(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "Parent", "Real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "book.m4b"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A sibling symlink Parent/Link -> Real: both resolve to the same directory.
	if err := os.Symlink(real, filepath.Join(root, "Parent", "Link")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	const want = "Parent/Link" // "Link" < "Real"
	for i := range 100 {
		res, stats := scanNoProbe(t, root)
		if len(res.Books) != 1 {
			t.Fatalf("run %d: want exactly 1 book (aliases dedup to one), got %d: %+v", i, len(res.Books), res.Books)
		}
		if got := res.Books[0].Path; got != want {
			t.Fatalf("run %d: aliased dir must use the smallest alias %q, got %q", i, want, got)
		}
		if stats.UnreadableDirs != 0 {
			t.Fatalf("run %d: UnreadableDirs = %d, want 0", i, stats.UnreadableDirs)
		}
	}
}

// TestOnWalkCallback pins the OnWalk contract: called at least once for a
// multi-directory tree, its final groupsFound matches the real group count, and
// it is invoked from a single goroutine (no concurrent overlap).
func TestOnWalkCallback(t *testing.T) {
	root := mkTree(t,
		"A/Book One/x.m4b",
		"A/Book Two/y.m4b",
		"B/C/D/deep.m4b",
		"loose.mp3",
	)
	var (
		mu            sync.Mutex
		calls         int
		lastGroups    int
		maxConcurrent int
		inFlight      int
	)
	res, _, err := Scan(root, Options{OnWalk: func(dirsScanned, groupsFound int) {
		mu.Lock()
		inFlight++
		if inFlight > maxConcurrent {
			maxConcurrent = inFlight
		}
		calls++
		lastGroups = groupsFound
		inFlight--
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("OnWalk never called")
	}
	if maxConcurrent > 1 {
		t.Errorf("OnWalk invoked concurrently (max in-flight %d) - must be one goroutine", maxConcurrent)
	}
	// The final OnWalk fires with the finished counts, which must equal the real
	// group total (here 4 audio-bearing directories).
	const wantGroups = 4
	if lastGroups != wantGroups {
		t.Errorf("final OnWalk groupsFound = %d, want %d", lastGroups, wantGroups)
	}
	if len(res.Books) != wantGroups {
		t.Fatalf("want %d books, got %d", wantGroups, len(res.Books))
	}
}

// TestOnWalkNilNoPanic pins that a nil OnWalk skips the reporter entirely with no
// panic (and the scan still works).
func TestOnWalkNilNoPanic(t *testing.T) {
	root := mkTree(t, "A/Book/x.m4b", "loose.mp3")
	res, _, err := Scan(root, Options{OnWalk: nil})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Books) != 2 {
		t.Fatalf("want 2 books, got %d", len(res.Books))
	}
}

// cloneBook must deep-copy every reference-typed field of Book: a streamed
// provisional book is promised to be independent of the retained one, which the
// corroboration pass mutates in place. This reflection sweep fails when a new
// slice/map field is added to Book without updating cloneBook.
func TestCloneBookCoversAllReferenceFields(t *testing.T) {
	populate := func(v reflect.Value) {
		for i := range v.NumField() {
			f := v.Field(i)
			switch f.Kind() {
			case reflect.Slice:
				f.Set(reflect.MakeSlice(f.Type(), 1, 1))
			case reflect.Map:
				m := reflect.MakeMapWithSize(f.Type(), 1)
				m.SetMapIndex(reflect.Zero(f.Type().Key()), reflect.Zero(f.Type().Elem()))
				f.Set(m)
			case reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer:
				t.Fatalf("Book field %s has unsupported reference kind %s - extend cloneBook and this test", v.Type().Field(i).Name, f.Kind())
			}
		}
	}
	var b Book
	populate(reflect.ValueOf(&b).Elem())
	c := cloneBook(b)
	bv, cv := reflect.ValueOf(b), reflect.ValueOf(c)
	for i := range bv.NumField() {
		name := bv.Type().Field(i).Name
		switch bv.Field(i).Kind() {
		case reflect.Slice, reflect.Map:
			if bv.Field(i).Pointer() == cv.Field(i).Pointer() {
				t.Errorf("cloneBook aliases Book.%s - add it to the deep copy", name)
			}
		}
	}
}
