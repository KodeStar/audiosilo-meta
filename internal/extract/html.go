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
func htmlToText(src []byte) string {
	s := string(src)
	var b strings.Builder
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

		name, isEnd, next := parseTag(s, i)
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
		if !isEnd && (name == "script" || name == "style") {
			i = skipRawElement(s, next, name)
			continue
		}

		switch {
		case name == "head":
			if isEnd {
				if inHead > 0 {
					inHead--
				}
			} else {
				inHead++
			}
		case blockElements[name] && inHead == 0:
			b.WriteByte('\n')
		}
		i = next
	}

	return normalizeText(b.String())
}

// parseTag parses the tag starting at s[i] (which must be '<'). It returns the
// lowercased local element name (namespace prefix stripped), whether it is an
// end tag, and the index just past the closing '>'. next<=i signals "not a
// tag" (a bare '<' or an unterminated tag).
func parseTag(s string, i int) (name string, isEnd bool, next int) {
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
		return "", false, i
	}
	// Scan to the closing '>', honoring quoted attribute values.
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
			return name, isEnd, j + 1
		}
		j++
	}
	return "", false, i // unterminated
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
