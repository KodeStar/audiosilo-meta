package importer

import (
	"html"
	"strings"
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
//   - the libex CREDIT-SIDE REFUSALS still read the escaped spelling, on purpose:
//     the list rule has to tell the ';' that ends a reference from a list
//     separator (see htmlEntityRE). The one refusal that judges a name's IDENTITY
//     rather than its punctuation, creditIdentifies, decodes first.
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
// ("AT&T;" reads "&T;") is left exactly as written, which is also
// html.UnescapeString's own behaviour.
//
// A decoded NO-BREAK SPACE ("&nbsp;", "&#160;", "&#xa0;") becomes an ordinary
// space: the reference is layout markup, not part of the name, and a U+00A0
// stored inside a title is invisible to every reader while breaking every exact
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
func decodeEntity(ref string) string {
	if r := html.UnescapeString(ref); r != "\u00a0" {
		return r
	}
	return " "
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
		if v, ok := s.raw[key].(string); ok && strings.ContainsRune(v, '&') {
			s.raw[key] = strings.TrimSpace(DecodeHTMLEntities(v))
		}
	}
	for _, list := range [][]string{s.authors, s.narrators} {
		for i, name := range list {
			list[i] = strings.TrimSpace(DecodeHTMLEntities(name))
		}
	}
	for _, ch := range s.chapterRows() {
		if v, ok := ch["title"].(string); ok && strings.ContainsRune(v, '&') {
			ch["title"] = strings.TrimSpace(DecodeHTMLEntities(v))
		}
	}
}
