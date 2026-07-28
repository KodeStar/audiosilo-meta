package extract

import (
	"archive/zip"
	"os"
	gopath "path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestCorpusChapterRecognition measures the chapter-label parser against a real
// epub library instead of hand-written fixtures.
//
// It is env-gated because the corpus is the maintainer's own copyrighted books,
// which can never enter this repo - the same arrangement audiosilo-sidecars uses
// for its historical-extraction golden tests. Point it at a directory tree:
//
//	AUDIOSILO_EPUB_DIR=~/Books go test ./pkg/extract/ -run Corpus -v
//
// It reads only each epub's container/OPF/toc, never a content document, writes
// nothing, and asserts only a floor - so it cannot break when the corpus changes,
// but it does fail if a parser change regresses recognition below the floor.
func TestCorpusChapterRecognition(t *testing.T) {
	dir := os.Getenv("AUDIOSILO_EPUB_DIR")
	if dir == "" {
		t.Skip("AUDIOSILO_EPUB_DIR not set; skipping the real-corpus measurement")
	}
	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("resolve ~: %v", err)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}

	var epubs []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subtree must not fail the sweep
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".epub") {
			epubs = append(epubs, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	if len(epubs) == 0 {
		t.Skipf("no .epub files under %s", dir)
	}
	sort.Strings(epubs)

	var strictOK, looseOK, readable int
	for _, p := range epubs {
		labels, err := tocLabelsOf(p)
		if err != nil {
			t.Logf("%-52s UNREADABLE: %v", trimName(p), err)
			continue
		}
		readable++

		var strictNums, looseNums []int
		for _, l := range labels {
			switch n, _, conf := ChapterFromLabel(l); conf {
			case Strict:
				strictNums = append(strictNums, n)
				looseNums = append(looseNums, n)
			case Loose:
				looseNums = append(looseNums, n)
			}
		}
		sc, lc := Contiguous(strictNums), Contiguous(looseNums)
		if sc {
			strictOK++
		}
		if lc {
			looseOK++
		}
		t.Logf("%-52s labels=%3d strict=%3d%s loose=%3d%s",
			trimName(p), len(labels), len(strictNums), mark(sc), len(looseNums), mark(lc))
	}

	t.Logf("")
	t.Logf("CORPUS: %d epubs, %d readable | contiguous: strict %d (%.0f%%), loose %d (%.0f%%)",
		len(epubs), readable, strictOK, pct(strictOK, readable), looseOK, pct(looseOK, readable))

	// A floor, not the observed value: this catches a vocabulary regression while
	// leaving headroom, because the rate is a property of the CORPUS as much as of
	// the parser - a library heavy in short-story collections or plays legitimately
	// scores lower.
	//
	// Measured on the 34-book validation corpus: 59% strict, 71% loose. The 10
	// remaining books are not random failures; they are two describable classes,
	// both of which a chapter_mapping agent round handles:
	//   - titled-only tocs, with no chapter numbers at all (short-story
	//     collections, plays) - 4 books;
	//   - Part-structured books whose chapter numbers RESTART in each part, so the
	//     numbers parse fine but a flat contiguity test correctly rejects them - 4
	//     books. Mapping "Part II, Chapter 1" onto a logical chapter needs the
	//     book's own structure, which is exactly the agent's job.
	const floorPct = 50
	if got := pct(looseOK, readable); got < floorPct {
		t.Errorf("contiguous recognition %.0f%% is below the %d%% floor - the label "+
			"vocabulary regressed, or this corpus needs a different floor", got, floorPct)
	}
}

// TestCorpusSplit runs the real Split over the corpus and measures the chapter
// universe it produces - the end-to-end number that anchor splitting and the label
// vocabulary combine to deliver. It writes only into t.TempDir().
//
// Gated on AUDIOSILO_EPUB_DIR, like TestCorpusChapterRecognition.
func TestCorpusSplit(t *testing.T) {
	epubs := corpusEpubs(t)

	var contiguous, split, readable int
	for _, p := range epubs {
		man, err := Split(p, t.TempDir())
		if err != nil {
			t.Logf("%-52s SPLIT FAILED: %v", trimName(p), err)
			continue
		}
		readable++

		spines := map[int]bool{}
		var nums []int
		anchored := 0
		for _, d := range man.Docs {
			spines[d.Spine] = true
			if d.Anchor != "" {
				anchored++
			}
			if n, _, conf := ChapterFromLabel(d.Label); conf != NotAChapter {
				nums = append(nums, n)
			}
		}
		ok := Contiguous(nums)
		if ok {
			contiguous++
		}
		if anchored > 0 {
			split++
		}
		t.Logf("%-52s spine=%3d emitted=%3d anchored=%3d chapters=%3d%s",
			trimName(p), len(spines), len(man.Docs), anchored, len(nums), mark(ok))
	}

	t.Logf("")
	t.Logf("CORPUS SPLIT: %d readable | %d (%.0f%%) contiguous | %d used anchor splitting",
		readable, contiguous, pct(contiguous, readable), split)

	if readable == 0 {
		t.Fatal("no epub in the corpus could be split")
	}
	// Measured end to end on the 34-book validation corpus: 64% contiguous, with
	// 24 books relying on anchor splitting to get there. The floor leaves headroom
	// for a differently-composed library; the remainder is what the consumer's
	// chapter-mapping agent stage exists to handle.
	//
	// Two corpus books point their toc at fragments that do not exist in the
	// markup at all (a pdftohtml/calibre conversion that dropped the anchors).
	// Split refuses to invent cut points there and warns instead, which is why
	// they show up as non-contiguous rather than as plausible-looking nonsense.
	const floorPct = 55
	if got := pct(contiguous, readable); got < floorPct {
		t.Errorf("end-to-end contiguous chapter recognition %.0f%% is below the %d%% floor", got, floorPct)
	}
}

// TestCorpusMetadata measures how much identity the corpus actually carries.
// Identity decides which catalogue work a book attaches to, and attaching to the
// wrong work is the worst outcome available - so it matters both how often a field
// is present and that a present value is trustworthy.
func TestCorpusMetadata(t *testing.T) {
	epubs := corpusEpubs(t)

	var readable, withTitle, withAuthors, withLang, withISBN, withSeries int
	for _, p := range epubs {
		md, err := ReadMetadata(p)
		if err != nil {
			t.Logf("%-52s UNREADABLE: %v", trimName(p), err)
			continue
		}
		readable++
		if md.Title != "" {
			withTitle++
		}
		if len(md.Authors) > 0 {
			withAuthors++
		}
		if md.Language != "" {
			withLang++
		}
		if md.ISBN != "" {
			withISBN++
		}
		if md.Series != "" {
			withSeries++
		}
		t.Logf("%-52s title=%-3v authors=%-2d lang=%-5s isbn=%-14s series=%s",
			trimName(p), md.Title != "", len(md.Authors), md.Language, md.ISBN, md.Series)
	}

	t.Logf("")
	t.Logf("CORPUS METADATA: %d readable | title %d | authors %d | language %d | ISBN %d | series %d",
		readable, withTitle, withAuthors, withLang, withISBN, withSeries)

	// Title AND authors together are what the catalogue's title-search rung needs,
	// and they are the floor that makes ebook-only books matchable at all - an ISBN
	// is a bonus (only a minority of epubs carry a real one, and a UUID in
	// dc:identifier is not one).
	if readable == 0 {
		t.Fatal("no epub in the corpus yielded metadata")
	}
	if got := pct(withTitle, readable); got < 90 {
		t.Errorf("title present in only %.0f%% of the corpus, want >=90%%", got)
	}
	if got := pct(withAuthors, readable); got < 90 {
		t.Errorf("authors present in only %.0f%% of the corpus, want >=90%%", got)
	}
}

// corpusEpubs resolves AUDIOSILO_EPUB_DIR and returns every .epub beneath it,
// skipping the test when the corpus is not configured.
func corpusEpubs(t *testing.T) []string {
	t.Helper()
	dir := os.Getenv("AUDIOSILO_EPUB_DIR")
	if dir == "" {
		t.Skip("AUDIOSILO_EPUB_DIR not set; skipping the real-corpus measurement")
	}
	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("resolve ~: %v", err)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	var epubs []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subtree must not fail the sweep
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".epub") {
			epubs = append(epubs, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	if len(epubs) == 0 {
		t.Skipf("no .epub files under %s", dir)
	}
	sort.Strings(epubs)
	return epubs
}

// tocLabelsOf returns an epub's table-of-contents labels in document order,
// reading only the container, the OPF and the toc document - never a content
// document. It reuses the package's own resolution path, so the measurement sees
// exactly what Split sees.
func tocLabelsOf(path string) ([]string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()

	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[f.Name] = f
	}
	opfPath, err := findOPFPath(files)
	if err != nil {
		return nil, err
	}
	pkg, err := parseOPF(files, opfPath)
	if err != nil {
		return nil, err
	}
	opfDir := path2Dir(opfPath)
	labels, _ := readTOC(files, pkg, opfDir)
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if l.Label != "" {
			out = append(out, l.Label)
		}
	}
	return out, nil
}

// path2Dir mirrors Split's opfDir derivation (a top-level OPF has no directory).
func path2Dir(opfPath string) string {
	d := gopath.Dir(opfPath)
	if d == "." {
		return ""
	}
	return d
}

func mark(ok bool) string {
	if ok {
		return " CONTIGUOUS"
	}
	return "           "
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) * 100 / float64(total)
}

func trimName(p string) string {
	b := filepath.Base(p)
	if len(b) > 52 {
		return b[:52]
	}
	return b
}
