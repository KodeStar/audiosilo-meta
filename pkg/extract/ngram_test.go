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

// sidecarEnvelope is what every BARE sidecar record carries besides its own
// prose: the schema's other required keys. collectExprs discriminates a bare
// record by exactly that required set, so a fixture missing them is not a
// partial sidecar, it is the wrong file - which is the point.
const sidecarEnvelope = `"work":"the-book","license":"CC-BY-SA-4.0","sources":[{"type":"community"}]`

// recapsSidecar wraps recap texts into a valid-shaped recaps sidecar. The test
// strings are printable, so strconv.Quote's escaping is JSON-compatible.
func recapsSidecar(t *testing.T, dir, name string, texts ...string) string {
	t.Helper()
	body := `{` + sidecarEnvelope + `,"recaps":[`
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
		`{`+sidecarEnvelope+`,"characters":[{"id":"x","name":"X","reveal":{"chapter":1},`+
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
		`{`+sidecarEnvelope+`,"recaps":[],"in_short":`+strconv.Quote("Summary. "+phrase)+
			`,"ending":`+strconv.Quote(phrase+" indeed.")+`}`)

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

// The description member is one flat document - no array to discriminate it -
// so it is recognized by its own `text`. Spoiler-free says what it may SAY;
// verbatim source phrasing is refused here exactly as in every other member, and
// a description that borrows nothing passes.
func TestNGramDescriptionField(t *testing.T) {
	dir := t.TempDir()
	phrase := "the lighthouse had stood empty for nine winters before she came"
	src := writeFile(t, dir, "src.txt", phrase)

	lifted := writeFile(t, dir, "d.json",
		`{"work":"the-book","text":`+strconv.Quote("A quiet novel. "+phrase+".")+
			`,"license":"CC-BY-SA-4.0","sources":[{"type":"community"}]}`)
	f, err := NGram(src, []string{lifted}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 || f[0].Locus != "text" {
		t.Fatalf("findings = %+v, want one at locus %q", f, "text")
	}

	clean := writeFile(t, dir, "clean.json",
		`{"work":"the-book","text":"An entirely fresh account of a keeper, a coast and a long argument with the sea."`+
			`,"license":"CC-BY-SA-4.0","sources":[{"type":"community"}]}`)
	if f, err := NGram(src, []string{clean}, 8); err != nil || len(f) != 0 {
		t.Fatalf("clean description: findings = %+v, err = %v; want none", f, err)
	}
}

// And inside a pack, where the member sits beside its siblings - the shape the
// community tree actually holds, and the one a generation wave's QA scans.
func TestNGramDescriptionInPack(t *testing.T) {
	dir := t.TempDir()
	phrase := "she had never once looked back at the house on the hill"
	src := writeFile(t, dir, "src.txt", phrase)
	pack := writeFile(t, dir, "0.json", `{"entries":{"the-book":{`+
		`"characters":{"work":"the-book","characters":[{"id":"x","name":"X","reveal":{"chapter":1},"description":"Fresh words entirely."}]},`+
		`"description":{"work":"the-book","text":`+strconv.Quote("Setup: "+phrase+".")+
		`,"license":"CC-BY-SA-4.0","sources":[{"type":"community"}]}}}}`)

	f, err := NGram(src, []string{pack}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 || f[0].Locus != "the-book.description.text" {
		t.Fatalf("findings = %+v, want one at locus %q", f, "the-book.description.text")
	}
}

// The sidecars live in works-community PACK files, so pointing the check at the
// pack a work sits in is the normal usage: every entry's members are scanned,
// and a finding names the work it came from.
func TestNGramCommunityPack(t *testing.T) {
	dir := t.TempDir()
	phrase := "he crossed the frozen river before the moon had risen at all"
	src := writeFile(t, dir, "src.txt", phrase)
	// The real shape: each entry holds the work's sidecar RECORDS, one per
	// member, and each record carries its own array.
	pack := writeFile(t, dir, "0.json", `{"entries":{`+
		`"other-book":{"characters":{"work":"other-book","characters":[`+
		`{"id":"y","name":"Y","reveal":{"chapter":1},"description":"Fresh words entirely."}]}},`+
		`"the-book":{"characters":{"work":"the-book","characters":[`+
		`{"id":"x","name":"X","reveal":{"chapter":1},"description":`+strconv.Quote(phrase)+`}]},`+
		`"recaps":{"work":"the-book","recaps":[{"through":{"chapter":1},"text":`+strconv.Quote("Then "+phrase)+`}]}}`+
		`}}`)

	f, err := NGram(src, []string{pack}, 8)
	if err != nil {
		t.Fatal(err)
	}
	loci := map[string]bool{}
	for _, x := range f {
		loci[x.Locus] = true
	}
	for _, want := range []string{"the-book.characters.characters[0].description", "the-book.recaps.recaps[0].text"} {
		if !loci[want] {
			t.Errorf("no finding at %q; got %+v", want, f)
		}
	}
	if len(f) != 2 {
		t.Errorf("findings = %d, want 2 (the clean entry must not be flagged): %+v", len(f), f)
	}
}

// A pack holding no sidecar entries at all is the wrong file, not a clean bill
// of health: silently reporting zero findings would pass the no-verbatim gate
// for text nobody checked. A pack whose sidecars are simply free of prose IS
// nothing to check, and must not be reported as the wrong file - the same
// distinction a bare sidecar record gets.
func TestNGramPackWithoutSidecarsIsError(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "some source text that is long enough to shingle over")

	wrongFile := writeFile(t, dir, "works.json", `{"entries":{"the-book":{"id":"the-book","title":"The Book"}}}`)
	if _, err := NGram(src, []string{wrongFile}, 8); err == nil {
		t.Error("NGram succeeded on a pack with no sidecars")
	}

	// Sidecars that carry no checkable prose: a character with no description
	// and a recaps member with no entries.
	empty := writeFile(t, dir, "community.json", `{"entries":{"the-book":{`+
		`"characters":{"work":"the-book","characters":[{"id":"x","name":"X","reveal":{"chapter":1}}]},`+
		`"recaps":{"work":"the-book","recaps":[]}}}}`)
	found, err := NGram(src, []string{empty}, 8)
	if err != nil {
		t.Fatalf("a pack whose sidecars hold no prose was rejected as the wrong file: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("findings = %+v, want none", found)
	}
}

// A whisper-style transcript is `{"text": [...]}`, which is the exact shape a
// one-key discriminator read as "a description whose prose is absent" - zero
// findings, gate passed, nobody's prose scanned. It must be LOUD both ways: the
// bare transcript is not a sidecar at all, and a record that IS one whose `text`
// is an array is a malformed sidecar rather than an empty one.
func TestNGramWhisperShapedFileIsError(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.txt", "some source text that is long enough to shingle over")

	transcript := writeFile(t, dir, "whisper.json",
		`{"text":[{"start":0,"end":3,"speaker":"A"}],"language":"en"}`)
	if _, err := NGram(src, []string{transcript}, 8); err == nil {
		t.Error("NGram succeeded on a whisper transcript: a file whose prose was never scanned reported clean")
	}

	// The same hazard one step in: the required keys are all there, so it IS a
	// description record - and its `text` is still not prose.
	malformed := writeFile(t, dir, "d.json",
		`{`+sidecarEnvelope+`,"text":[{"start":0,"end":3}]}`)
	_, err := NGram(src, []string{malformed}, 8)
	if err == nil {
		t.Fatal("NGram succeeded on a description whose text is an array")
	}
	if !strings.Contains(err.Error(), "expected a string") {
		t.Errorf("error %q does not say what was wrong with the field", err)
	}

	// And its sibling on the array side: a characters record whose `characters`
	// is not an array contributes nothing, so it must not report clean either.
	badArray := writeFile(t, dir, "c.json",
		`{`+sidecarEnvelope+`,"characters":{"x":{"description":"words"}}}`)
	if _, err := NGram(src, []string{badArray}, 8); err == nil {
		t.Error("NGram succeeded on a characters record whose characters is not an array")
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
	for _, kind := range []string{"characters", "recaps", "description"} {
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
