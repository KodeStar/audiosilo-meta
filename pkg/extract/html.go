package extract

import (
	"html"
	"regexp"
	"strings"
)

// blockElements turn into a newline (on both their open and close tags) so text
// that was laid out as separate blocks stays on separate lines.
var blockElements = map[string]bool{
	"p": true, "div": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "li": true, "br": true, "tr": true, "blockquote": true,
}

var (
	reSpaces   = regexp.MustCompile(`[ \t]+`)
	reLineTrim = regexp.MustCompile(`[ \t]*\n[ \t]*`)
	reBlankRun = regexp.MustCompile(`\n{3,}`)
)

// htmlToText renders an XHTML content document to plain text. It is a
// hand-rolled tokenizer (dependency-free) rather than a strict XML parse
// because real-world epub content is not always well-formed. It drops the
// CONTENT of head/style/script entirely (a naive tag-strip leaks inline CSS and
// script bodies), turns block elements into newlines, unescapes HTML entities,
// and collapses runs of whitespace.
//
// Inline elements (span, em, a, ...) deliberately emit NO separator - that is
// HTML-faithful (a drop cap or other intra-word markup must not split its
// word). The accepted consequence: when a source uses whitespace-less inline
// boundaries between words, ngram shingle runs can shorten there.
func htmlToText(src []byte) string {
	raw, _ := renderHTML(src, nil)
	return normalizeText(raw)
}

// renderHTML is htmlToText's engine. It returns the UN-normalized rendered text
// plus, for each id in wantIDs that it encountered, the offset in that text where
// the element carrying the id begins.
//
// The offsets are into the raw output rather than the normalized one because a
// caller splitting a document at those points must slice BEFORE normalizing -
// normalizeText collapses whitespace and would invalidate any offset taken across
// it. Each slice is then normalized on its own.
//
// wantIDs may be nil, which skips the id bookkeeping entirely so the common
// whole-document path pays nothing for it.
func renderHTML(src []byte, wantIDs map[string]bool) (string, map[string]int) {
	s := string(src)
	var b strings.Builder
	var offsets map[string]int
	if len(wantIDs) > 0 {
		offsets = make(map[string]int, len(wantIDs))
	}
	inHead := 0 // >0 while inside a <head> element; its text is dropped
	i, n := 0, len(s)

	for i < n {
		if s[i] != '<' {
			// A run of text up to the next tag.
			j := strings.IndexByte(s[i:], '<')
			var chunk string
			if j < 0 {
				chunk, i = s[i:], n
			} else {
				chunk, i = s[i:i+j], i+j
			}
			if inHead == 0 {
				b.WriteString(html.UnescapeString(chunk))
			}
			continue
		}

		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				i = n
			} else {
				i += 4 + end + 3
			}
			continue
		case strings.HasPrefix(s[i:], "<![CDATA["):
			end := strings.Index(s[i+9:], "]]>")
			if end < 0 {
				if inHead == 0 {
					b.WriteString(s[i+9:])
				}
				i = n
			} else {
				if inHead == 0 {
					b.WriteString(s[i+9 : i+9+end])
				}
				i += 9 + end + 3
			}
			continue
		case strings.HasPrefix(s[i:], "<!"), strings.HasPrefix(s[i:], "<?"):
			// DOCTYPE / declarations / processing instructions: skip to '>'.
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				i = n
			} else {
				i += end + 1
			}
			continue
		}

		name, isEnd, selfClosing, next := parseTag(s, i)
		if next <= i {
			// A lone '<' that does not begin a tag: keep it as text.
			if inHead == 0 {
				b.WriteByte('<')
			}
			i++
			continue
		}

		// script/style are raw-text elements: their bodies may contain '<'
		// that is not markup, so skip everything up to the matching close tag.
		// A self-closed <script/> / <style/> has no body - skipping to a close
		// tag that does not exist would swallow the rest of the document.
		if !isEnd && !selfClosing && (name == "script" || name == "style") {
			i = skipRawElement(s, next, name)
			continue
		}

		switch {
		case name == "head":
			// A self-closed <head/> opens nothing; counting it would suppress
			// every character after it.
			if isEnd {
				if inHead > 0 {
					inHead--
				}
			} else if !selfClosing {
				inHead++
			}
		case blockElements[name] && inHead == 0:
			b.WriteByte('\n')
		}

		// Record a wanted anchor AFTER the block newline above, so the separator
		// stays with the preceding section and the new one starts on content.
		if offsets != nil && !isEnd && inHead == 0 {
			id, name := tagAnchorIDs(s[i:next])
			for _, cand := range [2]string{id, name} {
				if cand == "" || !wantIDs[cand] {
					continue
				}
				if _, seen := offsets[cand]; !seen {
					offsets[cand] = b.Len()
				}
			}
		}
		i = next
	}

	return b.String(), offsets
}

// tagAnchorIDs returns the fragment identifiers a raw start tag declares: the
// modern `id` attribute and the legacy `<a name="...">` form older epubs still use
// as a link target. Either may be "".
//
// BOTH are returned rather than one being preferred, because only the caller knows
// which of them the toc actually points at, and a converter can emit a tag whose
// `id` and `name` differ.
//
// It parses the attribute list rather than searching for `id=`, so `data-id` and
// an `id` appearing inside another attribute's quoted value cannot be mistaken for
// the real thing. Whitespace around the equals sign (`id = "c2"`, legal XML) and a
// valueless attribute (`<p hidden id="c2">`, legal HTML) are both tolerated
// mid-list: bailing out on either would silently lose the anchor, and a lost anchor
// merges two chapters and mis-numbers every chapter after them.
func tagAnchorIDs(raw string) (id, name string) {
	i := 0
	// Skip '<' and the element name.
	if i < len(raw) && raw[i] == '<' {
		i++
	}
	for i < len(raw) && isNameChar(raw[i]) {
		i++
	}
	for i < len(raw) {
		i = skipTagSpace(raw, i)
		start := i
		for i < len(raw) && isNameChar(raw[i]) {
			i++
		}
		if i == start {
			break // '>', '/' or malformed input: no more attributes
		}
		attr := strings.ToLower(raw[start:i])

		// A valueless attribute simply has no '=' before the next attribute name.
		eq := skipTagSpace(raw, i)
		if eq >= len(raw) || raw[eq] != '=' {
			i = eq
			continue
		}
		i = skipTagSpace(raw, eq+1)

		var value string
		if i < len(raw) && (raw[i] == '"' || raw[i] == '\'') {
			quote := raw[i]
			i++
			vs := i
			for i < len(raw) && raw[i] != quote {
				i++
			}
			value = raw[vs:i]
			if i < len(raw) {
				i++
			}
		} else {
			vs := i
			for i < len(raw) && !isTagSpace(raw[i]) && raw[i] != '>' && raw[i] != '/' {
				i++
			}
			value = raw[vs:i]
		}
		switch {
		case attr == "id" && id == "":
			id = value
		case attr == "name" && name == "":
			name = value
		}
	}
	return id, name
}

func isTagSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func skipTagSpace(s string, i int) int {
	for i < len(s) && isTagSpace(s[i]) {
		i++
	}
	return i
}

// parseTag parses the tag starting at s[i] (which must be '<'). It returns the
// lowercased local element name (namespace prefix stripped), whether it is an
// end tag, whether it uses the XHTML self-closing syntax (<br/>, <head/>), and
// the index just past the closing '>'. next<=i signals "not a tag" (a bare '<'
// or an unterminated tag).
func parseTag(s string, i int) (name string, isEnd, selfClosing bool, next int) {
	n := len(s)
	j := i + 1
	if j < n && s[j] == '/' {
		isEnd = true
		j++
	}
	start := j
	for j < n && isNameChar(s[j]) {
		j++
	}
	name = strings.ToLower(s[start:j])
	if idx := strings.IndexByte(name, ':'); idx >= 0 {
		name = name[idx+1:] // drop a namespace prefix, e.g. svg:rect
	}
	if name == "" {
		return "", false, false, i
	}
	// Scan to the closing '>', honoring quoted attribute values. A '/'
	// immediately before the '>' (outside quotes) marks a self-closing tag.
	var quote byte
	for j < n {
		c := s[j]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return name, isEnd, s[j-1] == '/', j + 1
		}
		j++
	}
	return "", false, false, i // unterminated
}

func isNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '-' || c == ':' || c == '.'
}

// skipRawElement returns the index just past the matching </name> close tag,
// starting the search at from (just past the open tag). If no close is found it
// returns len(s), swallowing the rest of the document.
func skipRawElement(s string, from int, name string) int {
	closeTag := "</" + name
	idx := strings.Index(strings.ToLower(s[from:]), closeTag)
	if idx < 0 {
		return len(s)
	}
	k := from + idx
	gt := strings.IndexByte(s[k:], '>')
	if gt < 0 {
		return len(s)
	}
	return k + gt + 1
}

// normalizeText collapses whitespace: non-breaking spaces become spaces, runs
// of spaces/tabs collapse to one, each line is trimmed, and 3+ blank lines
// collapse to a single blank line.
func normalizeText(t string) string {
	t = strings.ReplaceAll(t, " ", " ")
	t = reSpaces.ReplaceAllString(t, " ")
	t = reLineTrim.ReplaceAllString(t, "\n")
	t = reBlankRun.ReplaceAllString(t, "\n\n")
	return strings.TrimSpace(t)
}

// wordCount returns the number of whitespace-separated tokens in t.
func wordCount(t string) int { return len(strings.Fields(t)) }
