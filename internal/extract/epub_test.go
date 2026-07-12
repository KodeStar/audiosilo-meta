package extract

import (
	"archive/zip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// buildEpub writes an epub (zip) into a temp dir from the given member map and
// returns its path. Book text is synthesized here and never checked into the
// repo (workspace rule: real book text never enters the repo).
func buildEpub(t *testing.T, members map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "book.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	zw := zip.NewWriter(f)
	// Deterministic order for reproducibility.
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(members[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

const container = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`

func docByFile(m *Manifest, file string) *DocEntry {
	for i := range m.Docs {
		if m.Docs[i].File == file {
			return &m.Docs[i]
		}
	}
	return nil
}

func TestSplitEPUB2(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Killing Floor</dc:title></metadata>
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="ch1" href="ch01.html" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch02.html" media-type="application/xhtml+xml"/>
    <item id="cover" href="cover.html" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="cover"/>
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`
	ncx := `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <navMap>
    <navPoint><navLabel><text>Chapter 1</text></navLabel><content src="ch01.html"/></navPoint>
    <navPoint><navLabel><text>Chapter 2</text></navLabel><content src="ch02.html#start"/></navPoint>
  </navMap>
</ncx>`
	cover := `<html><head><title>Cover</title></head><body><p>Cover Page</p></body></html>`
	// ch01 exercises head/style/script dropping and entity unescape.
	ch01 := `<html><head><style>.x{color:red}</style></head>` +
		`<body><h2>Chapter 1</h2><p>Reacher stepped off the bus &amp; walked north.</p>` +
		`<script>var x = 1 < 2;</script></body></html>`
	ch02 := `<html><body><p>The second chapter opens.</p></body></html>`

	epub := buildEpub(t, map[string]string{
		"META-INF/container.xml": container,
		"OEBPS/content.opf":      opf,
		"OEBPS/toc.ncx":          ncx,
		"OEBPS/cover.html":       cover,
		"OEBPS/ch01.html":        ch01,
		"OEBPS/ch02.html":        ch02,
	})

	out := t.TempDir()
	man, err := Split(epub, out)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if man.Title != "Killing Floor" {
		t.Errorf("Title = %q, want %q", man.Title, "Killing Floor")
	}
	if len(man.Docs) != 3 {
		t.Fatalf("len(Docs) = %d, want 3", len(man.Docs))
	}
	if len(man.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", man.Warnings)
	}

	// Spine order (cover, ch1, ch2) drives numbering, not manifest order.
	if got := man.Docs[0].Href; got != "cover.html" {
		t.Errorf("Docs[0].Href = %q, want cover.html", got)
	}
	if got := man.Docs[1].Href; got != "ch01.html" {
		t.Errorf("Docs[1].Href = %q, want ch01.html", got)
	}

	if d := docByFile(man, "001.txt"); d.Label != "" || d.Chapter != nil {
		t.Errorf("cover doc got label=%q chapter=%v, want none", d.Label, d.Chapter)
	}
	d2 := docByFile(man, "002.txt")
	if d2.Label != "Chapter 1" || d2.Chapter == nil || *d2.Chapter != 1 {
		t.Errorf("002 label=%q chapter=%v, want Chapter 1 / 1", d2.Label, d2.Chapter)
	}
	d3 := docByFile(man, "003.txt")
	if d3.Label != "Chapter 2" || d3.Chapter == nil || *d3.Chapter != 2 {
		t.Errorf("003 label=%q chapter=%v, want Chapter 2 / 2", d3.Label, d3.Chapter)
	}

	body, err := os.ReadFile(filepath.Join(out, "002.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "Reacher stepped off the bus & walked north.") {
		t.Errorf("002.txt missing expected body text; got:\n%s", text)
	}
	if strings.Contains(text, "color:red") || strings.Contains(text, "var x") {
		t.Errorf("002.txt leaked style/script content:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(out, "manifest.json")); err != nil {
		t.Errorf("manifest.json not written: %v", err)
	}
}

func TestSplitEPUB3Nav(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Nav Book</dc:title></metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="ch01.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="ch02.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="c1"/>
    <itemref idref="c2"/>
  </spine>
</package>`
	nav := `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
  <body>
    <nav epub:type="landmarks"><ol><li><a href="ch01.xhtml">Start</a></li></ol></nav>
    <nav epub:type="toc">
      <ol>
        <li><a href="ch01.xhtml">Chapter 1</a></li>
        <li><a href="ch02.xhtml">Epilogue</a></li>
      </ol>
    </nav>
  </body>
</html>`
	epub := buildEpub(t, map[string]string{
		"META-INF/container.xml": container,
		"OEBPS/content.opf":      opf,
		"OEBPS/nav.xhtml":        nav,
		"OEBPS/ch01.xhtml":       `<html><body><p>Opening.</p></body></html>`,
		"OEBPS/ch02.xhtml":       `<html><body><p>Closing.</p></body></html>`,
	})

	man, err := Split(epub, t.TempDir())
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if len(man.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none (landmarks nav must be ignored)", man.Warnings)
	}
	d1 := docByFile(man, "001.txt")
	if d1.Label != "Chapter 1" || d1.Chapter == nil || *d1.Chapter != 1 {
		t.Errorf("001 label=%q chapter=%v, want Chapter 1 / 1", d1.Label, d1.Chapter)
	}
	d2 := docByFile(man, "002.txt")
	if d2.Label != "Epilogue" || d2.Chapter != nil {
		t.Errorf("002 label=%q chapter=%v, want Epilogue / nil", d2.Label, d2.Chapter)
	}
}

func TestSplitMultipleLabelsWarning(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>T</dc:title></metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="ch01.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="ch02.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`
	// Both "Chapter 2" and "Chapter 3" point at ch02.xhtml.
	nav := `<html xmlns:epub="http://www.idpf.org/2007/ops"><body>
    <nav epub:type="toc"><ol>
      <li><a href="ch01.xhtml">Chapter 1</a></li>
      <li><a href="ch02.xhtml">Chapter 2</a></li>
      <li><a href="ch02.xhtml#s2">Chapter 3</a></li>
    </ol></nav></body></html>`
	epub := buildEpub(t, map[string]string{
		"META-INF/container.xml": container,
		"OEBPS/content.opf":      opf,
		"OEBPS/nav.xhtml":        nav,
		"OEBPS/ch01.xhtml":       `<p>a</p>`,
		"OEBPS/ch02.xhtml":       `<p>b</p>`,
	})

	man, err := Split(epub, t.TempDir())
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if len(man.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly one", man.Warnings)
	}
	w := man.Warnings[0]
	if !strings.Contains(w, "002.txt (ch02.xhtml): multiple toc labels target this file") ||
		!strings.Contains(w, `"Chapter 2"`) || !strings.Contains(w, `"Chapter 3"`) {
		t.Errorf("unexpected warning: %q", w)
	}
	// First label still wins.
	if d := docByFile(man, "002.txt"); d.Label != "Chapter 2" {
		t.Errorf("002 label = %q, want Chapter 2", d.Label)
	}
}

func TestSplitMissingSpineFile(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <manifest><item id="c1" href="ch01.html" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`
	epub := buildEpub(t, map[string]string{
		"META-INF/container.xml": container,
		"OEBPS/content.opf":      opf,
		// ch01.html deliberately absent.
	})
	if _, err := Split(epub, t.TempDir()); err == nil {
		t.Fatal("Split succeeded, want error for missing spine file")
	}
}

func TestSplitRejectsEscapingHref(t *testing.T) {
	// OPF at the archive root, so a "../" href escapes the archive entirely.
	rootContainer := `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <manifest><item id="c1" href="../secret.html" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`
	epub := buildEpub(t, map[string]string{
		"META-INF/container.xml": rootContainer,
		"content.opf":            opf,
		"secret.html":            `<p>secret</p>`,
	})
	if _, err := Split(epub, t.TempDir()); err == nil {
		t.Fatal("Split succeeded, want error for escaping href")
	}
}
