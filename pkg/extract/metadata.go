package extract

import (
	"strings"
)

// Metadata is what an epub's OPF says about the book itself, as opposed to how it
// is laid out. It is the identity a consumer matches against a catalogue: the
// title and authors always, an ISBN when the publisher recorded one, and the
// series when the producer recorded that.
//
// Every field is best-effort and omitted when absent. Nothing here is inferred
// from the filename or the content - an epub that states nothing yields an empty
// Metadata rather than a guess.
type Metadata struct {
	Title       string            `json:"title,omitempty"`
	Subtitle    string            `json:"subtitle,omitempty"`
	Authors     []string          `json:"authors,omitempty"`
	Language    string            `json:"language,omitempty"`
	Publisher   string            `json:"publisher,omitempty"`
	ISBN        string            `json:"isbn,omitempty"`
	Identifiers map[string]string `json:"identifiers,omitempty"` // scheme -> value, verbatim
	Series      string            `json:"series,omitempty"`
	SeriesPos   string            `json:"series_pos,omitempty"`
}

// ReadMetadata parses only the container and the OPF of the epub at path. It reads
// no content document, so it is cheap enough to run across a whole library while
// scanning for candidates.
func ReadMetadata(epubPath string) (*Metadata, error) {
	zr, _, pkg, _, err := openEpub(epubPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return metadataOf(pkg), nil
}

// metadataOf projects a parsed OPF onto Metadata. Pure, so the per-field rules are
// unit-testable without building an epub.
func metadataOf(pkg *opfPackage) *Metadata {
	m := &Metadata{}
	md := pkg.Meta

	// EPUB 3 refines carry per-element facts ("this title is the subtitle", "this
	// creator's role is author"), keyed by the element's id.
	refines := map[string]map[string]string{}
	for _, meta := range md.Metas {
		id := strings.TrimPrefix(strings.TrimSpace(meta.Refines), "#")
		if id == "" || meta.Property == "" {
			continue
		}
		if refines[id] == nil {
			refines[id] = map[string]string{}
		}
		refines[id][strings.ToLower(strings.TrimSpace(meta.Property))] = strings.TrimSpace(meta.Value)
	}

	// EPUB 3 types each dc:title. Only "main" (or an untyped title, the EPUB 2
	// case) is the book's own title: "collection" names the SERIES and "edition"
	// the printing, and taking either as the title would attach the book to the
	// wrong catalogue work - the worst outcome available, and silent, since a
	// series name looks exactly like a plausible title.
	mainSeen := false
	for _, t := range md.Titles {
		v := normalizeLabel(t.Value)
		if v == "" {
			continue
		}
		switch strings.ToLower(refines[t.ID]["title-type"]) {
		case "subtitle":
			if m.Subtitle == "" {
				m.Subtitle = v
			}
		case "main":
			if !mainSeen {
				m.Title, mainSeen = v, true // an explicit main title always wins
			}
		case "collection", "edition":
			// Names the series or the printing, never the book.
		default:
			if m.Title == "" {
				m.Title = v
			}
		}
	}

	// Prefer creators explicitly marked as authors, but only when the file says so
	// for at least one of them - many epubs record no roles at all, and dropping
	// every creator because none is tagged "aut" would lose the author entirely.
	var authors, tagged []string
	for _, c := range md.Creators {
		v := normalizeLabel(c.Value)
		if v == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(c.Role))
		if role == "" {
			role = strings.ToLower(refines[c.ID]["role"])
		}
		if role == "aut" {
			tagged = append(tagged, v)
		}
		authors = append(authors, v)
	}
	if len(tagged) > 0 {
		m.Authors = tagged
	} else {
		m.Authors = authors
	}

	m.Language = firstNonEmpty(md.Languages)
	m.Publisher = firstNonEmpty(md.Publishers)

	for _, id := range md.Identifiers {
		v := strings.TrimSpace(id.Value)
		if v == "" {
			continue
		}
		scheme := strings.ToLower(strings.TrimSpace(id.Scheme))
		if scheme == "" {
			if before, _, found := strings.Cut(strings.ToLower(v), ":"); found && before == "urn" {
				scheme = "urn"
			}
		}
		if m.Identifiers == nil {
			m.Identifiers = map[string]string{}
		}
		key := scheme
		if key == "" {
			key = "unknown"
		}
		if _, dup := m.Identifiers[key]; !dup {
			m.Identifiers[key] = v
		}
		if m.ISBN == "" {
			if isbn, ok := isbnFrom(v, scheme); ok {
				m.ISBN = isbn
			}
		}
	}

	m.Series, m.SeriesPos = seriesOf(md.Metas, refines)
	return m
}

// seriesOf reads the series name and position, covering both conventions in the
// wild: EPUB 2 producers (overwhelmingly calibre) write <meta name="calibre:series"
// content="..."/>, while EPUB 3 uses a belongs-to-collection element refined by a
// group-position.
func seriesOf(metas []opfMeta, refines map[string]map[string]string) (name, pos string) {
	for _, meta := range metas {
		switch strings.ToLower(strings.TrimSpace(meta.Name)) {
		case "calibre:series":
			name = normalizeLabel(meta.Content)
		case "calibre:series_index":
			pos = strings.TrimSpace(meta.Content)
		}
	}
	if name != "" {
		return name, trimSeriesIndex(pos)
	}
	for _, meta := range metas {
		if strings.ToLower(strings.TrimSpace(meta.Property)) != "belongs-to-collection" {
			continue
		}
		if v := normalizeLabel(meta.Value); v != "" {
			return v, trimSeriesIndex(refines[strings.TrimSpace(meta.ID)]["group-position"])
		}
	}
	return "", ""
}

// trimSeriesIndex renders a series index the way the catalogue writes positions.
// calibre formats the index as a fixed-point number, so book 1 arrives as "1.00"
// (older versions: "1.0") and a 1.5 novella as "1.50", where the schema's position
// strings are "1" and "1.5". Anything that is not a plain decimal number is left
// exactly as written rather than mangled.
func trimSeriesIndex(pos string) string {
	pos = strings.TrimSpace(pos)
	if !strings.Contains(pos, ".") {
		return pos // "10" must not lose its zero
	}
	for _, r := range pos {
		if (r < '0' || r > '9') && r != '.' {
			return pos
		}
	}
	return strings.TrimSuffix(strings.TrimRight(pos, "0"), ".")
}

// isbnFrom extracts a VALIDATED ISBN from a dc:identifier value.
//
// Validation is not fussiness. A dc:identifier is far more often a UUID or a
// publisher-internal key than an ISBN, and a consumer uses the ISBN to decide
// which catalogue work this book IS. Attaching a book to the wrong work is the
// worst outcome available, so a value has to pass its check digit before it is
// called an ISBN.
func isbnFrom(value, scheme string) (string, bool) {
	v := strings.TrimSpace(value)
	lower := strings.ToLower(v)
	switch {
	case strings.HasPrefix(lower, "urn:isbn:"):
		v = v[len("urn:isbn:"):]
	case scheme == "isbn":
		// value is the bare ISBN
	case strings.HasPrefix(lower, "isbn:"):
		v = v[len("isbn:"):]
	default:
		// An untagged identifier may still be a bare ISBN; the check digit
		// decides. A UUID contains hyphens too, so digits alone are not enough.
	}
	digits := strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.TrimSpace(v))
	if validISBN13(digits) || validISBN10(digits) {
		return digits, true
	}
	return "", false
}

// validISBN13 checks the mod-10 weighted checksum of a 13-digit ISBN.
func validISBN13(s string) bool {
	if len(s) != 13 || (!strings.HasPrefix(s, "978") && !strings.HasPrefix(s, "979")) {
		return false
	}
	sum := 0
	for i := 0; i < 13; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return sum%10 == 0
}

// validISBN10 checks the mod-11 checksum of a 10-character ISBN, whose final
// character may be 'X' for the value 10.
func validISBN10(s string) bool {
	if len(s) != 10 {
		return false
	}
	sum := 0
	for i := 0; i < 10; i++ {
		c := s[i]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case (c == 'X' || c == 'x') && i == 9:
			d = 10
		default:
			return false
		}
		sum += d * (10 - i)
	}
	return sum%11 == 0
}

func firstNonEmpty(vals []string) string {
	for _, v := range vals {
		if t := normalizeLabel(v); t != "" {
			return t
		}
	}
	return ""
}
