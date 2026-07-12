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
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Manifest describes the result of splitting an epub: one entry per spine
// document, in spine order, plus any toc anomalies that need an operator's eye.
type Manifest struct {
	Epub     string     `json:"epub"`               // base name of the input file
	Title    string     `json:"title"`              // dc:title, "" if absent
	Warnings []string   `json:"warnings,omitempty"` // omitted when none
	Docs     []DocEntry `json:"docs"`
}

// DocEntry is one spine content document written to <outdir>/<file>.
type DocEntry struct {
	Index   int    `json:"index"`             // 1-based spine position
	File    string `json:"file"`              // "001.txt", ...
	Href    string `json:"href"`              // manifest href, relative to the OPF
	Label   string `json:"label,omitempty"`   // toc label, when one targets this file
	Chapter *int   `json:"chapter,omitempty"` // inferred chapter number, when unambiguous
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
		zipToDoc[zp] = len(docs)
		docs = append(docs, spineDoc{href: it.Href, zipPath: zp})
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("spine has no content documents")
	}

	// All toc labels that target each spine document, in toc order. The first
	// wins; extras mean the toc is not file-aligned and the operator is warned.
	labels, tocDir := readTOC(files, pkg, opfDir)
	perDoc := make([][]string, len(docs))
	for _, lb := range labels {
		if lb.Label == "" {
			continue
		}
		tp, err := resolveHref(tocDir, lb.Src)
		if err != nil {
			continue
		}
		if di, ok := zipToDoc[tp]; ok {
			perDoc[di] = append(perDoc[di], lb.Label)
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	man := &Manifest{Epub: filepath.Base(epubPath), Title: strings.TrimSpace(pkg.Title)}
	for i, d := range docs {
		data, err := readZipFile(files[d.zipPath])
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", d.zipPath, err)
		}
		text := htmlToText(data)
		fileName := fmt.Sprintf("%03d.txt", i+1)
		if err := os.WriteFile(filepath.Join(outDir, fileName), []byte(text), 0o644); err != nil {
			return nil, err
		}
		entry := DocEntry{Index: i + 1, File: fileName, Href: d.href, Words: wordCount(text)}
		if names := perDoc[i]; len(names) > 0 {
			entry.Label = names[0]
			entry.Chapter = inferChapter(names[0])
			if len(names) > 1 {
				man.Warnings = append(man.Warnings, fmt.Sprintf(
					"%s (%s): multiple toc labels target this file: %s",
					fileName, d.href, quoteJoin(names)))
			}
		}
		man.Docs = append(man.Docs, entry)
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
	Title string    `xml:"metadata>title"`
	Items []opfItem `xml:"manifest>item"`
	Spine opfSpine  `xml:"spine"`
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

// --- helpers ---

// reChapterLabel matches the only two label shapes we infer a chapter number
// from: "Chapter 7" (any case) or a bare "7".
var reChapterLabel = regexp.MustCompile(`(?i)^(?:chapter\s+)?(\d+)$`)

// inferChapter conservatively extracts a chapter number from a toc label:
// "Chapter 7" (any case) or a bare "7". Anything else yields nil - we never
// guess.
func inferChapter(label string) *int {
	if m := reChapterLabel.FindStringSubmatch(strings.TrimSpace(label)); m != nil {
		if v, err := strconv.Atoi(m[1]); err == nil {
			return &v
		}
	}
	return nil
}

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
