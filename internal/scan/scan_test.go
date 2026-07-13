package scan

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
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
func scanNoProbe(t *testing.T, root string) *Result {
	t.Helper()
	res, _, err := Scan(root, Options{FFprobePath: ""})
	if err != nil {
		t.Fatal(err)
	}
	return res
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

	res := scanNoProbe(t, root)

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
}

func TestScanRootMissing(t *testing.T) {
	if _, _, err := Scan(filepath.Join(t.TempDir(), "nope"), Options{}); err == nil {
		t.Fatal("expected an error for a missing root")
	}
}

func TestScanEnvelope(t *testing.T) {
	root := mkTree(t, "a.mp3")
	res := scanNoProbe(t, root)
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
		title:     "Tag Title",
		authors:   []string{"Tag Author"},
		narrators: []string{"Some Narrator"},
		series:    "Tag Series",
		position:  "9",
	}

	b := assemble("Path Series/03 - Book", "03 - Book", "path", []string{"03 - Book.m4b"}, pd, tags)

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
	b := assemble("Lee Child/Jack Reacher/01 - Killing Floor", "01 - Killing Floor", "path",
		[]string{"Killing Floor [B076HYPQLK].m4b"}, pd, tagInfo{})

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
	// Neither tags nor a parseable title -> the raw identity segment is used.
	b := assemble("weird", "weird", "filename", []string{"weird.mp3"}, derived{}, tagInfo{})
	if b.Title != "weird" || b.Sources["title"] != "filename" {
		t.Errorf("untitled fallback: got %q (%s)", b.Title, b.Sources["title"])
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
