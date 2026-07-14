package issueform

import (
	neturl "net/url"
	"regexp"
	"strings"
)

// sections is the parsed issue-form body: each `### <label>` heading maps to
// the trimmed text that follows it, up to the next heading. A field the
// submitter left blank renders as GitHub's "_No response_" sentinel and is
// stored as "".
type sections map[string]string

// headingRE matches a GitHub issue-form field heading line. GitHub renders each
// form field's label as a level-3 heading ("### <label>") exactly, so the
// boundary is restricted to h3: a value line that itself starts with a Markdown
// heading (e.g. a Sources value like "# 1 New York Times bestseller") is then
// part of the field's value, not a new section that orphans the rest of the
// field. (A legitimate multi-line value containing a literal "### " line would
// still be mis-split as a new section - rare and accepted.)
var headingRE = regexp.MustCompile(`^###\s+(.+?)\s*$`)

// parseBody splits a rendered issue-form markdown body into its field sections.
// GitHub renders each form field (input/textarea/dropdown/checkboxes with an
// id) as an `### <label>` heading followed by the value; type:markdown display
// blocks carry no id and never render, so only real fields appear. Values are
// trimmed; the "_No response_" sentinel becomes "".
func parseBody(body string) sections {
	out := sections{}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")

	label := ""
	var buf []string
	flush := func() {
		if label == "" {
			return
		}
		val := strings.TrimSpace(strings.Join(buf, "\n"))
		if val == "_No response_" {
			val = ""
		}
		// A heading that appears twice keeps the first (forms have unique labels).
		if _, seen := out[label]; !seen {
			out[label] = val
		}
	}

	for _, line := range lines {
		if m := headingRE.FindStringSubmatch(line); m != nil {
			flush()
			label = strings.TrimSpace(m[1])
			buf = buf[:0]
			continue
		}
		if label != "" {
			buf = append(buf, line)
		}
	}
	flush()
	return out
}

// get returns the trimmed value for a field label, or "".
func (s sections) get(label string) string { return strings.TrimSpace(s[label]) }

// checkboxRE matches a rendered checkbox list item, capturing its checked state.
var checkboxRE = regexp.MustCompile(`(?m)^\s*-\s*\[([ xX])\]`)

// checked reports whether every checkbox under a checkboxes field is ticked. A
// field with no checkbox items (or an absent field) reports false, so a required
// confirmation that GitHub did not render is treated as unconfirmed.
func (s sections) checked(label string) bool {
	block, ok := s[label]
	if !ok {
		return false
	}
	matches := checkboxRE.FindAllStringSubmatch(block, -1)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		if m[1] == " " {
			return false
		}
	}
	return true
}

// attachmentURLRE captures the first http(s) URL inside a markdown link/image,
// e.g. [file.json](https://github.com/user-attachments/files/1/file.json) or
// ![alt](https://user-images.githubusercontent.com/...).
var attachmentURLRE = regexp.MustCompile(`\]\((https?://[^\s)]+)\)`)

// extractAttachment pulls a file attachment out of a textarea field's value.
// GitHub inserts an uploaded file as a markdown link; a submitter may instead
// paste the raw JSON. It returns the attachment URL (to be fetched) OR inline
// bytes, never both. ok is false when neither is present.
//
// A field can carry a prose link (a Goodreads reference, etc.) ABOVE the real
// attachment, so it prefers the first link whose host is on the same allowlist
// defaultFetch (fetch.go) enforces, and only falls back to the first link when
// none qualifies - which preserves defaultFetch's clear "not an allowed host"
// failure rather than silently succeeding on the wrong URL.
func extractAttachment(block string) (url string, inline []byte, ok bool) {
	if links := attachmentURLRE.FindAllStringSubmatch(block, -1); links != nil {
		for _, m := range links {
			if u, err := neturl.Parse(m[1]); err == nil && allowedAttachmentHost(u.Hostname()) {
				return m[1], nil, true
			}
		}
		return links[0][1], nil, true
	}
	trimmed := strings.TrimSpace(block)
	// Tolerate a fenced code block around pasted JSON.
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimSpace(stripFence(trimmed))
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return "", []byte(trimmed), true
	}
	return "", nil, false
}

// stripFence removes a leading ```lang line and a trailing ``` line from a
// fenced code block.
func stripFence(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "```") {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
