package extract

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// recapsSidecar wraps recap texts into a valid-shaped recaps sidecar. The test
// strings are printable, so strconv.Quote's escaping is JSON-compatible.
func recapsSidecar(t *testing.T, dir, name string, texts ...string) string {
	t.Helper()
	body := `{"recaps":[`
	for i, tx := range texts {
		if i > 0 {
			body += ","
		}
		body += `{"through":{"chapter":` + strconv.Itoa(i+1) + `},"text":` + strconv.Quote(tx) + `}`
	}
	body += `]}`
	return writeFile(t, dir, name, body)
}

func TestNGramHitAtExactlyN(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "alpha beta gamma delta epsilon zeta eta theta iota")
	// Exactly eight matching words.
	sc := recapsSidecar(t, dir, "r.json", "beta gamma delta epsilon zeta eta theta iota")

	f, err := NGram(src, []string{sc}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(f), f)
	}
	if f[0].Words != 8 {
		t.Errorf("Words = %d, want 8", f[0].Words)
	}
	if f[0].Locus != "recaps[0].text" {
		t.Errorf("Locus = %q, want recaps[0].text", f[0].Locus)
	}
}

func TestNGramNoHitAtNMinusOne(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "alpha beta gamma delta epsilon zeta eta theta iota")
	// Only seven consecutive words overlap; the rest diverge.
	sc := recapsSidecar(t, dir, "r.json", "gamma delta epsilon zeta eta theta iota XX YY ZZ QQ")

	f, err := NGram(src, []string{sc}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 0 {
		t.Fatalf("findings = %d, want 0: %+v", len(f), f)
	}
}

func TestNGramGreedyExtension(t *testing.T) {
	dir := t.TempDir()
	run := "one two three four five six seven eight nine ten eleven twelve"
	src := writeFile(t, dir, "src.txt", "prefix words "+run+" suffix words")
	sc := recapsSidecar(t, dir, "r.json", "unrelated lead in "+run+" trailing off")

	f, err := NGram(src, []string{sc}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(f), f)
	}
	if f[0].Words != 12 {
		t.Errorf("Words = %d, want 12 (greedy extension to the full run)", f[0].Words)
	}
	if f[0].Text != run {
		t.Errorf("Text = %q, want %q", f[0].Text, run)
	}
}

func TestNGramPunctuationCaseCurlyQuoteInvariance(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "the quiet harbor town slept beneath a heavy grey sky")
	// Same words, different case, punctuation, curly quotes, hyphenation.
	sc := recapsSidecar(t, dir, "r.json",
		"The QUIET, harbor-town slept “beneath” a heavy... grey sky!")

	f, err := NGram(src, []string{sc}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(f), f)
	}
}

func TestNGramCharactersField(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "he was a tall man with a weathered face and cold eyes")
	sc := writeFile(t, dir, "c.json",
		`{"characters":[{"id":"x","name":"X","reveal":{"chapter":1},`+
			`"description":"He was a tall man with a weathered face and cold eyes."}]}`)

	f, err := NGram(src, []string{sc}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(f), f)
	}
	if f[0].Locus != "characters[0].description" {
		t.Errorf("Locus = %q, want characters[0].description", f[0].Locus)
	}
}

func TestNGramInShortAndEnding(t *testing.T) {
	dir := t.TempDir()
	phrase := "the whole thing came apart at the very last moment before dawn"
	src := writeFile(t, dir, "src.txt", phrase)
	sc := writeFile(t, dir, "r.json",
		`{"recaps":[],"in_short":`+strconv.Quote("Summary. "+phrase)+`,"ending":`+strconv.Quote(phrase+" indeed.")+`}`)

	f, err := NGram(src, []string{sc}, 8)
	if err != nil {
		t.Fatal(err)
	}
	loci := map[string]bool{}
	for _, x := range f {
		loci[x.Locus] = true
	}
	if !loci["in_short"] || !loci["ending"] {
		t.Fatalf("expected findings in both in_short and ending, got %+v", f)
	}
}

func TestNGramNeitherKeyIsError(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "some source text that is long enough to shingle over")
	sc := writeFile(t, dir, "bad.json", `{"work":"something","title":"nope"}`)

	_, err := NGram(src, []string{sc}, 8)
	if err == nil {
		t.Fatal("NGram succeeded, want error for a file with neither key")
	}
	// The message must name every recognized sidecar kind, so an operator knows
	// what the tool was looking for.
	for _, kind := range []string{"characters", "recaps"} {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("error %q does not name sidecar kind %q", err, kind)
		}
	}
}

func TestNGramMinimumN(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "one two three four five")
	sc := recapsSidecar(t, dir, "r.json", "one two three four five")

	if _, err := NGram(src, []string{sc}, 3); err == nil {
		t.Fatal("NGram succeeded with n=3, want error (minimum is 4)")
	}
	if _, err := NGram(src, []string{sc}, 4); err != nil {
		t.Fatalf("NGram with n=4 errored: %v", err)
	}
}

func TestNGramCleanIsEmpty(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "the source talks about entirely separate matters here")
	sc := recapsSidecar(t, dir, "r.json", "a wholly original paraphrase in the author own distinct words")

	f, err := NGram(src, []string{sc}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 0 {
		t.Fatalf("findings = %d, want 0 (clean): %+v", len(f), f)
	}
}

// TestRoundTripSplitThenNGram splits a synthetic 3-chapter epub and then runs
// ngram against the split output with a sidecar that copies one verbatim
// sentence out of chapter 2, expecting exactly one finding.
func TestRoundTripSplitThenNGram(t *testing.T) {
	verbatim := "she crossed the frozen river just before the last light failed entirely"
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Round Trip</dc:title></metadata>
  <manifest>
    <item id="c1" href="ch01.html" media-type="application/xhtml+xml"/>
    <item id="c2" href="ch02.html" media-type="application/xhtml+xml"/>
    <item id="c3" href="ch03.html" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/><itemref idref="c3"/></spine>
</package>`
	epub := buildEpub(t, map[string]string{
		"META-INF/container.xml": container,
		"OEBPS/content.opf":      opf,
		"OEBPS/ch01.html":        `<p>The story begins in a quiet unremarkable place.</p>`,
		"OEBPS/ch02.html":        `<p>Then everything changed. ` + verbatim + `. The night grew colder.</p>`,
		"OEBPS/ch03.html":        `<p>Afterwards nothing was ever quite the same again.</p>`,
	})

	out := t.TempDir()
	if _, err := Split(epub, out); err != nil {
		t.Fatalf("Split: %v", err)
	}

	scDir := t.TempDir()
	sc := recapsSidecar(t, scDir, "r.json",
		"In her own words, "+verbatim+", which is far too close to the text.")

	f, err := NGram(out, []string{sc}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 {
		t.Fatalf("findings = %d, want exactly 1: %+v", len(f), f)
	}
	if f[0].Words < 11 {
		t.Errorf("Words = %d, want at least the %d-word verbatim run", f[0].Words, 11)
	}
}
