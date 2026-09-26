package importer

import (
	"html"
	"strings"
	"unicode"
	"unicode/utf8"
)

// entities.go reads the HTML character references a source's free text arrives
// carrying.
//
// libex returns its text with LITERAL references - the title of B0BS4HL89H is
// `The Doomsday Key &quot;International Edition&quot;` - and a reference read as
// text is a fact written wrong twice over: the stored title spells the markup,
// and the slug minted from it spells the reference's NAME as words
// ("the-doomsday-key-quot-international-edition-quot", "shopaholic-amp-sister",
// "ingenier-iacute-a-interior"). Before this rule 1,328 values in the tree
// carried one (969 work titles, 183 chapter titles, 175 publishers and a series
// name), and every one of those 969 works had minted its slug from the escaped
// spelling. The credit names were already read this way at the libex
// parse layer (the seven escaped names the list refusal measured); this is the
// same decode, moved to where EVERY source and every free-text field meets it.
//
// WHERE it runs, and why there:
//
//   - runBooks decodes every book (sourceBook.decodeText) before anything reads
//     it - before the AI gate, the credit censuses, the title pre-pass and every
//     slug the planner mints - so all four importers inherit it, and a new one
//     does by construction.
//   - libex-select composes its books without runBooks (selectLibexRow ->
//     seriesIndex.libexBook), so it decodes the same way at that one door, or a
//     selection would resolve an escaped title the import then reads decoded.
//   - a series claim is decoded where it is BUILT (makeSeriesRef, which every
//     source's parse layer and the live series lookup go through), because the
//     claim is composed at parse time and its name cleaning has to read the
//     decoded text.
//   - a genre claim is decoded where it is BUILT too (libexGenreClaims,
//     pathGenreClaims), for the same reason: the claim is composed at parse
//     time, and a name or category path read escaped misses the vocabulary.
//   - the libex CREDIT-LIST refusal still reads the escaped spelling, on purpose:
//     it has to tell the ';' that ends a reference from a list separator (see
//     htmlEntityRE). Every other credit-side refusal judges the name DECODED,
//     as the import will read it (refuseLibexCredits; creditIdentifies decodes
//     for itself).
//
// Decoding is ONE pass. A value escaped twice ("&amp;quot;") decodes to its
// once-escaped spelling rather than being chased to a fixpoint: a source that
// double-escapes is vanishingly rare, and a loop would also decode a reference a
// title genuinely spells after its first decode.

// DecodeHTMLEntities returns s with every HTML character reference decoded:
// named ("&amp;", "&iacute;"), decimal ("&#39;") and hexadecimal ("&#x2019;").
//
// Only a TERMINATED reference - one ending in ';', the shape htmlEntityRE
// matches - is read. html.UnescapeString alone also decodes the legacy
// references HTML accepts WITHOUT a semicolon, so "Rock&regular" would become
// "Rock®ular" and "Fish&notes" "Fish¬es"; a retailer title is text, not an HTML
// document, and a bare ampersand in it is an ampersand. An unknown name
// ("AT&T;" reads "&T;") is left exactly as written - INCLUDING one that merely
// begins with a legacy name, which html.UnescapeString would decode as that
// prefix ("&copyright;" -> "©right;"); see decodeEntity.
//
// A decoded WHITESPACE character - a no-break space ("&nbsp;", "&#160;",
// "&#xa0;"), a newline, a tab - becomes an ordinary space: the reference is
// layout markup, not part of the name, and a U+00A0 or a line break stored
// inside a title is invisible to every reader while breaking every exact
// comparison against the same title typed with a space.
//
// Exported because internal/issueform reads the same references out of an
// issue body - one rule for what a reference in submitted text means.
func DecodeHTMLEntities(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	return htmlEntityRE.ReplaceAllStringFunc(s, decodeEntity)
}

// decodeEntity decodes one terminated reference htmlEntityRE matched.
// (pkg/extract's normalizeText makes the same no-break-space choice for epub
// text.)
//
// A whole reference decodes to at most two runes (the longest HTML5 entities
// are two code points). A longer result is html.UnescapeString decoding only a
// legacy PREFIX of a name it does not know ("&copyright;" -> "©right;"), so the
// reference is not one and stays as written.
func decodeEntity(ref string) string {
	r := html.UnescapeString(ref)
	if utf8.RuneCountInString(r) > 2 {
		return ref
	}
	if c, size := utf8.DecodeRuneInString(r); size == len(r) && unicode.IsSpace(c) {
		return " "
	}
	return r
}

// sourceTextKeys are the raw keys every parser leaves a book's FREE TEXT under
// (the OpenAudible shape the shared pipeline reads): the titles, the joined
// credit strings of the sources that have no typed list, and the publisher.
// Identifiers, dates, URLs and codes are deliberately absent - they are not
// text, and a cover URL's query string is not ours to rewrite.
var sourceTextKeys = []string{"title", "title_short", "subtitle", "author", "narrated_by", "publisher"}

// decodeText decodes the book's free text in place: sourceTextKeys, the typed
// credit lists and the chapter titles, each trimmed again afterwards (every
// parser hands its text over trimmed, and a decoded no-break space at either
// end would otherwise survive as an edge space). Series claims are decoded where they
// are built (makeSeriesRef), so they are not revisited here. It must run
// exactly once per book - see the file comment for the two doors that call it.
func (s *sourceBook) decodeText() {
	for _, key := range sourceTextKeys {
		if v, ok := s.raw[key].(string); ok {
			s.raw[key] = decodeField(v)
		}
	}
	for _, list := range [][]string{s.authors, s.narrators} {
		for i, name := range list {
			list[i] = decodeField(name)
		}
	}
	// The rows are maps shared with the raw entry, so the decode is written
	// through them; keeping the slice on the book only spares the record builder
	// a second rawBook.chapters() walk.
	s.chapters = s.chapterRows()
	for _, ch := range s.chapters {
		if v, ok := ch["title"].(string); ok {
			ch["title"] = decodeField(v)
		}
	}
}

// decodeField is DecodeHTMLEntities plus the re-trim decodeText documents,
// and leaves a value holding no reference exactly as it was.
func decodeField(v string) string {
	if !strings.ContainsRune(v, '&') {
		return v
	}
	return strings.TrimSpace(DecodeHTMLEntities(v))
}

// decodeNames is decodeField over a credit list, returning a NEW slice (the
// caller's escaped list is still read by the list rule); a list holding no
// reference is returned as it is.
func decodeNames(names []string) []string {
	var out []string
	for i, n := range names {
		d := decodeField(n)
		if d != n && out == nil {
			out = append(make([]string, 0, len(names)), names[:i]...)
		}
		if out != nil {
			out = append(out, d)
		}
	}
	if out == nil {
		return names
	}
	return out
}
