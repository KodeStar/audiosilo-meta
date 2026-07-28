package extract

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Manifest describes the result of splitting an epub: one entry per spine
// document, in spine order, plus any toc anomalies that need an operator's eye.
type Manifest struct {
	Epub     string     `json:"epub"`               // base name of the input file
	Title    string     `json:"title"`              // dc:title, "" if absent
	Metadata *Metadata  `json:"metadata,omitempty"` // OPF identity: authors, ISBN, series, ...
	Warnings []string   `json:"warnings,omitempty"` // omitted when none
	Docs     []DocEntry `json:"docs"`
}

// DocEntry is one emitted text document, written to <outdir>/<file>.
//
// It is usually a whole spine content document, but when a document holds several
// toc entries that target fragments inside it, that document is split at those
// anchors and each section becomes its own DocEntry sharing the same Spine and
// Href. Index therefore counts EMITTED files (and always matches File), while
// Spine records where the text came from.
type DocEntry struct {
	Index   int    `json:"index"`             // 1-based position among the emitted files
	Spine   int    `json:"spine"`             // 1-based spine position this text came from
	File    string `json:"file"`              // "001.txt", ...
	Href    string `json:"href"`              // manifest href, relative to the OPF
	Anchor  string `json:"anchor,omitempty"`  // fragment id, when this is one section of a document
	Label   string `json:"label,omitempty"`   // toc label, when one targets this text
	Chapter *int   `json:"chapter,omitempty"` // chapter number, when the label states one outright
	Words   int    `json:"words"`
}

// Split parses the epub at epubPath and writes one UTF-8 text file per spine
// document (001.txt, 002.txt, ...) plus manifest.json into outDir, creating
// outDir if needed. It returns the manifest it wrote.
func Split(epubPath, outDir string) (*Manifest, error) {
	zr, err := zip.OpenReader(epubPath)
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
	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	idToItem := make(map[string]opfItem, len(pkg.Items))
	for _, it := range pkg.Items {
		idToItem[it.ID] = it
	}

	// Spine content documents, in order.
	type spineDoc struct{ href, zipPath string }
	var docs []spineDoc
	var warnings []string
	zipToDoc := map[string]int{}
	for _, ref := range pkg.Spine.Items {
		it, ok := idToItem[ref.IDRef]
		if !ok {
			return nil, fmt.Errorf("spine itemref %q has no matching manifest item", ref.IDRef)
		}
		zp, err := resolveHref(opfDir, it.Href)
		if err != nil {
			return nil, fmt.Errorf("spine item %q: %w", it.Href, err)
		}
		if files[zp] == nil {
			return nil, fmt.Errorf("spine item %q resolves to %q, which is missing from the archive", it.Href, zp)
		}
		// First occurrence wins the zipToDoc slot, so toc labels attach to the
		// first copy of a file the spine references more than once.
		if _, dup := zipToDoc[zp]; dup {
			warnings = append(warnings, fmt.Sprintf(
				"spine references %s more than once; toc labels attach to the first occurrence", it.Href))
		} else {
			zipToDoc[zp] = len(docs)
		}
		docs = append(docs, spineDoc{href: it.Href, zipPath: zp})
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("spine has no content documents")
	}

	// All toc entries that target each spine document, in toc order, each keeping
	// its #fragment so a document holding several chapters can be split at them.
	labels, tocDir := readTOC(files, pkg, opfDir)
	perDoc := make([][]tocLabel, len(docs))
	for _, lb := range labels {
		if lb.Label == "" {
			continue
		}
		tp, err := resolveHref(tocDir, lb.Src)
		if err != nil {
			continue
		}
		if di, ok := zipToDoc[tp]; ok {
			perDoc[di] = append(perDoc[di], lb)
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	meta := metadataOf(pkg)
	man := &Manifest{
		Epub:     filepath.Base(epubPath),
		Title:    meta.Title,
		Metadata: meta,
		Warnings: warnings,
	}
	emitted := 0
	for i, d := range docs {
		data, err := readZipFile(files[d.zipPath])
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", d.zipPath, err)
		}
		entries := perDoc[i]
		secs := sectionsForDoc(data, entries)
		if secs == nil {
			// One chapter (or none) in this document: emit it whole, as before.
			secs = []section{{text: htmlToText(data)}}
			if len(entries) > 0 {
				secs[0].label = entries[0].Label
			}
			if len(entries) > 1 {
				// The toc names several chapters here but we could not locate
				// their anchors in the markup, so the chapters cannot be
				// separated. Warn loudly: silently emitting one file would give
				// every later chapter the wrong number, and chapter numbers are
				// the spoiler positions downstream.
				man.Warnings = append(man.Warnings, fmt.Sprintf(
					"%03d.txt (%s): multiple toc labels target this file and its anchors could not be located, so they share one text file: %s",
					emitted+1, d.href, quoteJoin(labelTexts(entries))))
			}
		} else if len(secs) > 1 {
			man.Warnings = append(man.Warnings, fmt.Sprintf(
				"%s: split into %d sections at its toc anchors", d.href, len(secs)))
		}

		for _, sec := range secs {
			emitted++
			fileName := fmt.Sprintf("%03d.txt", emitted)
			if err := os.WriteFile(filepath.Join(outDir, fileName), []byte(sec.text), 0o644); err != nil {
				return nil, err
			}
			entry := DocEntry{
				Index:  emitted,
				Spine:  i + 1,
				File:   fileName,
				Href:   d.href,
				Anchor: sec.anchor,
				Label:  sec.label,
				Words:  wordCount(sec.text),
			}
			if n, _, conf := ChapterFromLabel(sec.label); conf == Strict {
				entry.Chapter = &n
			}
			man.Docs = append(man.Docs, entry)
		}
	}

	out, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), out, 0o644); err != nil {
		return nil, err
	}
	return man, nil
}

// --- OPF / container ---

type opfPackage struct {
	Meta  opfMetadata `xml:"metadata"`
	Items []opfItem   `xml:"manifest>item"`
	Spine opfSpine    `xml:"spine"`
}

// opfMetadata is the OPF <metadata> block. encoding/xml matches on the LOCAL
// element name, so these fields pick up the dc:-prefixed Dublin Core elements
// without the package having to know the namespace bindings.
type opfMetadata struct {
	Titles      []opfText  `xml:"title"`
	Creators    []opfText  `xml:"creator"`
	Languages   []string   `xml:"language"`
	Publishers  []string   `xml:"publisher"`
	Identifiers []opfIdent `xml:"identifier"`
	Metas       []opfMeta  `xml:"meta"`
}

type opfText struct {
	Value string `xml:",chardata"`
	ID    string `xml:"id,attr"`
	Role  string `xml:"role,attr"` // EPUB 2 opf:role, e.g. "aut"
}

type opfIdent struct {
	Value  string `xml:",chardata"`
	ID     string `xml:"id,attr"`
	Scheme string `xml:"scheme,attr"` // EPUB 2 opf:scheme, e.g. "ISBN"
}

// opfMeta covers both <meta> dialects: EPUB 2's name/content pair (calibre writes
// its series there) and EPUB 3's property/refines form.
type opfMeta struct {
	Value    string `xml:",chardata"`
	ID       string `xml:"id,attr"`
	Name     string `xml:"name,attr"`
	Content  string `xml:"content,attr"`
	Property string `xml:"property,attr"`
	Refines  string `xml:"refines,attr"`
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type opfSpine struct {
	Toc   string `xml:"toc,attr"`
	Items []struct {
		IDRef string `xml:"idref,attr"`
	} `xml:"itemref"`
}

func findOPFPath(files map[string]*zip.File) (string, error) {
	f := files["META-INF/container.xml"]
	if f == nil {
		return "", fmt.Errorf("META-INF/container.xml not found (not a valid epub?)")
	}
	data, err := readZipFile(f)
	if err != nil {
		return "", err
	}
	var c struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("parse container.xml: %w", err)
	}
	for _, rf := range c.Rootfiles {
		if rf.FullPath == "" {
			continue
		}
		p, err := resolveHref("", rf.FullPath)
		if err != nil {
			return "", fmt.Errorf("container rootfile: %w", err)
		}
		if files[p] == nil {
			return "", fmt.Errorf("OPF %q referenced by container.xml is missing from the archive", p)
		}
		return p, nil
	}
	return "", fmt.Errorf("container.xml lists no rootfile")
}

func parseOPF(files map[string]*zip.File, opfPath string) (*opfPackage, error) {
	data, err := readZipFile(files[opfPath])
	if err != nil {
		return nil, err
	}
	var pkg opfPackage
	if err := xml.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("parse OPF %q: %w", opfPath, err)
	}
	return &pkg, nil
}

// --- table of contents ---

type tocLabel struct {
	Label string
	Src   string // href as written in the toc document (may carry a #fragment)
}

// readTOC returns the toc labels and the directory the toc document lives in
// (so label hrefs can be resolved). The EPUB 3 nav document is preferred; the
// EPUB 2 NCX is the fallback. Returns nil labels when neither is present or
// parseable - split then simply emits no labels.
func readTOC(files map[string]*zip.File, pkg *opfPackage, opfDir string) ([]tocLabel, string) {
	// tryTOC resolves a toc item, reads it, and parses it; ok is false when the
	// item is absent, unresolvable, unreadable, or yields no labels.
	tryTOC := func(item *opfItem, parse func([]byte) []tocLabel) (labels []tocLabel, dir string, ok bool) {
		if item == nil {
			return nil, "", false
		}
		zp, err := resolveHref(opfDir, item.Href)
		if err != nil || files[zp] == nil {
			return nil, "", false
		}
		data, err := readZipFile(files[zp])
		if err != nil {
			return nil, "", false
		}
		if labels = parse(data); len(labels) == 0 {
			return nil, "", false
		}
		return labels, path.Dir(zp), true
	}

	if labels, dir, ok := tryTOC(findNavItem(pkg.Items), parseNav); ok {
		return labels, dir
	}
	if labels, dir, ok := tryTOC(findNCXItem(pkg.Items, pkg.Spine.Toc), parseNCX); ok {
		return labels, dir
	}
	return nil, ""
}

func findNavItem(items []opfItem) *opfItem {
	for i := range items {
		if slices.Contains(strings.Fields(items[i].Properties), "nav") {
			return &items[i]
		}
	}
	return nil
}

func findNCXItem(items []opfItem, tocID string) *opfItem {
	if tocID != "" {
		for i := range items {
			if items[i].ID == tocID {
				return &items[i]
			}
		}
	}
	for i := range items {
		if items[i].MediaType == "application/x-dtbncx+xml" {
			return &items[i]
		}
	}
	return nil
}

// parseNav reads an EPUB 3 nav document, collecting the anchors inside the
// <nav epub:type="toc"> list in document order.
func parseNav(data []byte) []tocLabel {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var labels []tocLabel
	depth := 0
	tocDepth := -1 // depth of the toc <nav>, or -1 when not inside it
	inAnchor := false
	var href string
	var text strings.Builder

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return labels
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			local := strings.ToLower(t.Name.Local)
			if local == "nav" && tocDepth < 0 && strings.EqualFold(attrValue(t.Attr, "type"), "toc") {
				tocDepth = depth
			}
			if tocDepth >= 0 && local == "a" {
				inAnchor = true
				href = attrValue(t.Attr, "href")
				text.Reset()
			}
		case xml.CharData:
			if inAnchor {
				text.Write(t)
			}
		case xml.EndElement:
			local := strings.ToLower(t.Name.Local)
			if inAnchor && local == "a" {
				inAnchor = false
				if href != "" {
					labels = append(labels, tocLabel{Label: normalizeLabel(text.String()), Src: href})
				}
			}
			if tocDepth == depth && local == "nav" {
				tocDepth = -1
			}
			depth--
		}
	}
	return labels
}

// parseNCX reads an EPUB 2 NCX, flattening the (possibly nested) navMap in
// document order.
func parseNCX(data []byte) []tocLabel {
	var doc struct {
		Points []ncxPoint `xml:"navMap>navPoint"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	var out []tocLabel
	flattenNCX(doc.Points, &out)
	return out
}

type ncxPoint struct {
	Label   string `xml:"navLabel>text"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Children []ncxPoint `xml:"navPoint"`
}

func flattenNCX(points []ncxPoint, out *[]tocLabel) {
	for _, p := range points {
		if p.Content.Src != "" {
			*out = append(*out, tocLabel{Label: normalizeLabel(p.Label), Src: p.Content.Src})
		}
		flattenNCX(p.Children, out)
	}
}

// --- splitting a document at its toc anchors ---

// section is one emitted text document: a whole spine document, or one anchored
// slice of it.
type section struct {
	label  string
	anchor string
	text   string
}

// sectionsForDoc splits a spine content document at the fragment anchors its toc
// entries point at, returning one section per chapter in document order. It
// returns nil when the document should be emitted whole - either the toc names at
// most one chapter in it, or the anchors could not be found in the markup.
//
// This matters more than it looks. Many epubs put several chapters in one spine
// document and distinguish them only by fragment ("ch07.xhtml#c8"). Emitting one
// file per spine document then silently merges those chapters, and since chapter
// numbers ARE the spoiler positions for the sidecars built from this text, the
// damage is invisible: every later chapter is numbered too low, so every reveal
// and recap is gated one or more chapters early. In a 34-book validation corpus,
// 12 books had this shape.
//
// Anchors that the toc names but the markup does not define are skipped rather
// than guessed, so a mislabelled toc degrades to today's whole-document behaviour
// (with a warning) instead of cutting the text at an invented boundary.
func sectionsForDoc(data []byte, entries []tocLabel) []section {
	if len(entries) < 2 {
		return nil
	}
	want := make(map[string]bool, len(entries))
	for _, e := range entries {
		if f := fragmentOf(e.Src); f != "" {
			want[f] = true
		}
	}
	if len(want) == 0 {
		return nil
	}

	raw, offsets := renderHTML(data, want)
	if len(offsets) == 0 {
		return nil
	}

	// Cut points in toc order, de-duplicated, then sorted by where they actually
	// occur - a toc's order should match the document's, but nothing guarantees it.
	type cut struct {
		label, anchor string
		off           int
	}
	var cuts []cut
	seen := make(map[string]bool, len(offsets))
	for _, e := range entries {
		f := fragmentOf(e.Src)
		if f == "" || seen[f] {
			continue
		}
		off, ok := offsets[f]
		if !ok {
			continue
		}
		seen[f] = true
		cuts = append(cuts, cut{label: e.Label, anchor: f, off: off})
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].off < cuts[j].off })

	// The text before the first anchor belongs to whichever toc entry targeted the
	// document without a fragment; if there is none and the lead is blank, there is
	// no lead section at all.
	bounds := cuts
	if len(cuts) > 0 && cuts[0].off > 0 {
		lead := cut{off: 0}
		for _, e := range entries {
			if fragmentOf(e.Src) == "" {
				lead.label = e.Label
				break
			}
		}
		if lead.label != "" || strings.TrimSpace(raw[:cuts[0].off]) != "" {
			bounds = append([]cut{lead}, cuts...)
		}
	}
	if len(bounds) < 2 {
		return nil // nothing to separate
	}

	out := make([]section, 0, len(bounds))
	for i, b := range bounds {
		end := len(raw)
		if i+1 < len(bounds) {
			end = bounds[i+1].off
		}
		out = append(out, section{
			label:  b.label,
			anchor: b.anchor,
			text:   normalizeText(raw[b.off:end]),
		})
	}
	return out
}

// fragmentOf returns the #fragment of an href, or "".
func fragmentOf(href string) string {
	i := strings.IndexByte(href, '#')
	if i < 0 {
		return ""
	}
	frag := strings.TrimSpace(href[i+1:])
	if dec, err := url.PathUnescape(frag); err == nil {
		return dec
	}
	return frag
}

func labelTexts(entries []tocLabel) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Label
	}
	return out
}

// --- helpers ---

func attrValue(attrs []xml.Attr, local string) string {
	for _, a := range attrs {
		if strings.EqualFold(a.Name.Local, local) {
			return a.Value
		}
	}
	return ""
}

func normalizeLabel(s string) string { return strings.Join(strings.Fields(s), " ") }

func quoteJoin(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
}

// resolveHref resolves a (possibly percent-encoded, possibly fragment-carrying)
// epub-internal href against baseDir and returns the cleaned archive path. It
// rejects absolute paths and any href that escapes the archive root, so a
// hostile OPF/toc cannot make split read outside the zip.
func resolveHref(baseDir, href string) (string, error) {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	href = strings.TrimSpace(href)
	if href == "" {
		return "", fmt.Errorf("empty href")
	}
	if dec, err := url.PathUnescape(href); err == nil {
		href = dec
	}
	if path.IsAbs(href) {
		return "", fmt.Errorf("absolute href %q not allowed", href)
	}
	cleaned := path.Clean(path.Join(baseDir, href))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("href %q escapes the archive root", href)
	}
	return cleaned, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}
